package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"

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

// Dial connects to the daemon that owns dir (reads <dir>/runtime.json). The
// caller treats any error as "no daemon" and falls back to an embedded engine.
func Dial(dir string) (*Client, error) {
	info, err := readRuntimeInfo(dir)
	if err != nil {
		return nil, err
	}
	conn, err := net.Dial(info.Net, info.Addr)
	if err != nil {
		return nil, fmt.Errorf("runtime: dial %s: %w", info.Addr, err)
	}
	return &Client{conn: conn, tok: info.Token}, nil
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
	reqBytes, err := json.Marshal(request{ID: c.id, Method: method, Token: c.tok, Params: praw})
	if err != nil {
		return err
	}
	if err := writeFrame(c.conn, reqBytes); err != nil {
		return err
	}
	respBytes, err := readFrame(c.conn)
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

func (c *Client) Consolidate(_ context.Context, scope core.ID, summary string) (core.Artifact, error) {
	var a core.Artifact
	err := c.call(mConsolidate, scopeSummaryParams{Scope: scope, Summary: summary}, &a)
	return a, err
}

func (c *Client) CrossVersion(_ context.Context, scope core.ID, seed core.Seed) (core.Scope, error) {
	var sc core.Scope
	err := c.call(mCrossVersion, crossVersionParams{Scope: scope, Seed: seed}, &sc)
	return sc, err
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
