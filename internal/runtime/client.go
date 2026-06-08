package runtime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"

	"github.com/DotBlood/ioc/internal/core"
)

// compile-time check: the remote client satisfies Service.
var _ Service = (*Client)(nil)

// Client speaks the framed-JSON protocol to a daemon and satisfies Service, so
// callers (CLI/MCP) use it interchangeably with an embedded *engine.Engine.
// One Client owns one connection; calls are serialized by c.mu.
type Client struct {
	conn net.Conn
	tok  string

	mu sync.Mutex
	id uint64
}

// Dial connects to the daemon that owns dir (reads <dir>/runtime.json), using the
// published full token. The caller treats any error as "no daemon" and falls back
// to an embedded engine.
func Dial(dir string) (*Client, error) {
	info, err := readRuntimeInfo(dir)
	if err != nil {
		return nil, err
	}
	conn, err := dialConn(info)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, tok: info.Token}, nil
}

// DialWithToken connects like Dial but authenticates with the given token (e.g. a
// read-only token from MintReadToken instead of the runtime.json full token).
func DialWithToken(dir, token string) (*Client, error) {
	info, err := readRuntimeInfo(dir)
	if err != nil {
		return nil, err
	}
	conn, err := dialConn(info)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, tok: token}, nil
}

// dialRefused reports whether err is a failure of the TCP connect leg itself (the
// daemon's socket is dead — stale runtime.json / daemon gone), as opposed to a
// connection that succeeded but failed later (e.g. a TLS handshake). The portable
// discriminator is the error STRUCTURE: a refused/unreachable connect produces a
// *net.OpError{Op:"dial"} on both POSIX and Windows. We do NOT key on
// syscall.ECONNREFUSED alone: on Windows syscall.ECONNREFUSED is an invented
// constant (APPLICATION_ERROR range) that never equals the real Winsock
// WSAECONNREFUSED (10061) a refused dial carries, so errors.Is would miss it there.
// The errors.Is is kept only as a POSIX belt-and-suspenders.
func dialRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

// dialConn opens the transport to the daemon: plain TCP, or mTLS when the daemon
// advertises it (RuntimeInfo.TLS), using the client cert/CA from the environment.
func dialConn(info RuntimeInfo) (net.Conn, error) {
	if info.TLS {
		cfg, err := clientTLSConfig(info.TLSServerName)
		if err != nil {
			return nil, err
		}
		conn, err := tls.Dial(info.Net, info.Addr, cfg)
		if err != nil {
			return nil, fmt.Errorf("runtime: tls dial %s: %w", info.Addr, err)
		}
		return conn, nil
	}
	conn, err := net.Dial(info.Net, info.Addr)
	if err != nil {
		return nil, fmt.Errorf("runtime: dial %s: %w", info.Addr, err)
	}
	return conn, nil
}

// RotateToken regenerates the daemon's token(s) (control op, full token only). It
// updates this client's token in-place to the new full token so the same client
// keeps working; OTHER live clients holding the old token are now invalid and must
// re-Dial (reading the updated runtime.json). Returns the new full + read tokens.
func (c *Client) RotateToken() (full, read string, err error) {
	var res rotateTokenResult
	if err = c.call(mRotateToken, nil, &res); err != nil {
		return "", "", err
	}
	c.mu.Lock()
	c.tok = res.Full
	c.mu.Unlock()
	return res.Full, res.Read, nil
}

// MintReadToken mints a fresh read-only token (control op, full token only),
// revoking any previously minted read token.
func (c *Client) MintReadToken() (string, error) {
	var res mintReadTokenResult
	err := c.call(mMintReadToken, nil, &res)
	return res.Read, err
}

// call sends one request and decodes the result into out (out may be nil).
func (c *Client) call(method string, params, out any) error {
	var praw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		praw = b
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id++
	reqBytes, err := json.Marshal(request{ID: c.id, V: ProtoVersion, Method: method, Token: c.tok, Params: praw})
	if err != nil {
		return err
	}
	if err := writeFrame(c.conn, reqBytes); err != nil {
		return err
	}
	respBytes, err := readFrame(c.conn, maxDataFrame) // responses come from the trusted daemon
	if err != nil {
		return err
	}
	var resp response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error.toError()
	}
	if out != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

// Close disconnects from the daemon (it does NOT stop the daemon).
func (c *Client) Close() error { return c.conn.Close() }

// Shutdown asks the daemon to stop gracefully. It is a control op, not part of
// Service.
func (c *Client) Shutdown() error { return c.call(mShutdown, nil, nil) }

// Stats returns the daemon's live self-report (control op, not part of Service).
func (c *Client) Stats() (serverStats, error) {
	var st serverStats
	err := c.call(mStats, nil, &st)
	return st, err
}

func (c *Client) CreateScope(_ context.Context, parent core.ID, role core.Role, title string) (core.Scope, error) {
	var sc core.Scope
	err := c.call(mCreateScope, createScopeParams{Parent: parent, Role: role, Title: title}, &sc)
	return sc, err
}

func (c *Client) Push(_ context.Context, r core.PushRequest) (core.Artifact, error) {
	var a core.Artifact
	err := c.call(mPush, pushParams{Req: r}, &a)
	return a, err
}

func (c *Client) Query(_ context.Context, q core.Query) (core.ID, []core.Hit, error) {
	var res queryResult
	err := c.call(mQuery, queryParams{Query: q}, &res)
	return res.QueryID, res.Hits, err
}

func (c *Client) Neighbors(_ context.Context, scope core.ID, text string, k int) ([]core.Hit, error) {
	var hits []core.Hit
	err := c.call(mNeighbors, neighborsParams{Scope: scope, Text: text, K: k}, &hits)
	return hits, err
}

func (c *Client) Drill(_ context.Context, artifactID core.ID, to core.Detail) (core.Hit, error) {
	var h core.Hit
	err := c.call(mDrill, drillParams{Artifact: artifactID, Detail: to}, &h)
	return h, err
}

func (c *Client) Publish(_ context.Context, artifactID core.ID) error {
	return c.call(mPublish, idParams{ID: artifactID}, nil)
}

func (c *Client) SiblingOverview(_ context.Context, scope core.ID) ([]core.Hit, error) {
	var hits []core.Hit
	err := c.call(mSiblingOverview, scopeParams{Scope: scope}, &hits)
	return hits, err
}

func (c *Client) Ancestors(_ context.Context, scope core.ID) ([]core.Scope, error) {
	var scs []core.Scope
	err := c.call(mAncestors, scopeParams{Scope: scope}, &scs)
	return scs, err
}

func (c *Client) Fork(_ context.Context, source core.ID, title string) (core.Scope, error) {
	var sc core.Scope
	err := c.call(mFork, forkParams{Source: source, Title: title}, &sc)
	return sc, err
}

func (c *Client) Consolidate(_ context.Context, scope core.ID, summary string, supersedes []core.ID) (core.Artifact, error) {
	var a core.Artifact
	err := c.call(mConsolidate, consolidateParams{Scope: scope, Summary: summary, Supersedes: supersedes}, &a)
	return a, err
}

func (c *Client) CrossVersion(_ context.Context, scope core.ID, seed core.Seed) (core.Scope, error) {
	var sc core.Scope
	err := c.call(mCrossVersion, crossVersionParams{Scope: scope, Seed: seed}, &sc)
	return sc, err
}

func (c *Client) Supersede(_ context.Context, old, replacement core.ID) error {
	return c.call(mSupersede, supersedeParams{Old: old, Replacement: replacement}, nil)
}

func (c *Client) Relate(_ context.Context, from, to core.ID, kind core.RelationKind) error {
	return c.call(mRelate, relateParams{From: from, To: to, Kind: kind}, nil)
}

func (c *Client) Related(_ context.Context, artifact core.ID, kinds []core.RelationKind, dir core.EdgeDir, depth int) ([]core.Hit, error) {
	var hits []core.Hit
	err := c.call(mRelated, relatedParams{Artifact: artifact, Kinds: kinds, Dir: dir, Depth: depth}, &hits)
	return hits, err
}

func (c *Client) RollupScope(_ context.Context, scope core.ID, summary string) error {
	return c.call(mRollupScope, scopeSummaryParams{Scope: scope, Summary: summary}, nil)
}

func (c *Client) Trace(_ context.Context, queryID core.ID) (core.TraceRecord, error) {
	var tr core.TraceRecord
	err := c.call(mTrace, idParams{ID: queryID}, &tr)
	return tr, err
}

func (c *Client) RecentTraces(n int) ([]core.TraceRecord, error) {
	var trs []core.TraceRecord
	err := c.call(mRecentTraces, recentTracesParams{N: n}, &trs)
	return trs, err
}

func (c *Client) ListScopes(_ context.Context) ([]core.Scope, error) {
	var scs []core.Scope
	err := c.call(mListScopes, nil, &scs)
	return scs, err
}

func (c *Client) GetScope(_ context.Context, id core.ID) (core.Scope, error) {
	var sc core.Scope
	err := c.call(mGetScope, idParams{ID: id}, &sc)
	return sc, err
}

func (c *Client) ListArtifacts(_ context.Context) ([]core.Artifact, error) {
	var arts []core.Artifact
	err := c.call(mListArtifacts, nil, &arts)
	return arts, err
}

func (c *Client) DeleteArtifact(_ context.Context, id core.ID) error {
	return c.call(mDeleteArtifact, idParams{ID: id}, nil)
}

func (c *Client) DeleteScope(_ context.Context, id core.ID) error {
	return c.call(mDeleteScope, idParams{ID: id}, nil)
}

func (c *Client) Config(key string) (string, bool) {
	var res configResult
	if err := c.call(mConfig, configParams{Key: key}, &res); err != nil {
		return "", false
	}
	return res.Val, res.OK
}

func (c *Client) SetConfig(key, val string) error {
	return c.call(mSetConfig, setConfigParams{Key: key, Val: val}, nil)
}

func (c *Client) EmbModel() string {
	var res embModelResult
	_ = c.call(mEmbModel, nil, &res)
	return res.Model
}

func (c *Client) Compact(_ context.Context) (core.CompactStats, error) {
	var st core.CompactStats
	err := c.call(mCompact, nil, &st)
	return st, err
}

func (c *Client) ScopeStats(_ context.Context, scope core.ID, tau float64, minArtifacts int) (core.ScopeStats, error) {
	var st core.ScopeStats
	err := c.call(mScopeStats, scopeStatsParams{Scope: scope, Tau: tau, MinArtifacts: minArtifacts}, &st)
	return st, err
}
