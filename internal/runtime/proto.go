package runtime

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DotBlood/ioc/internal/core"
)

// ProtoVersion is the wire-protocol version (handshake/compat is hardened in S3).
const ProtoVersion = 1

// Frame size caps (V6). An UNAUTHENTICATED connection (before it presents a valid
// token) may send at most maxControlFrame — enough for the request envelope + token
// + a normal request, but far too small for amplification. After the first frame
// authenticates, the connection may send maxDataFrame (a large Push rides inside).
// This closes the previous 64 MiB pre-auth allocation. NOTE: a connection's FIRST
// request is therefore capped at maxControlFrame — authenticate with a small op
// before pushing >1 MiB content (the client makes small calls first in practice).
const (
	maxControlFrame = 1 << 20  // 1 MiB — pre-auth / first frame
	maxDataFrame    = 64 << 20 // 64 MiB — post-auth
)

// Method names (string-keyed dispatch).
const (
	mCreateScope     = "create_scope"
	mPush            = "push"
	mQuery           = "query"
	mNeighbors       = "neighbors"
	mDrill           = "drill"
	mPublish         = "publish"
	mSiblingOverview = "sibling_overview"
	mAncestors       = "ancestors"
	mFork            = "fork"
	mConsolidate     = "consolidate"
	mCrossVersion    = "crossversion"
	mRollupScope     = "rollup"
	mTrace           = "trace"
	mRecentTraces    = "recent_traces"
	mListScopes      = "list_scopes"
	mGetScope        = "get_scope"
	mListArtifacts   = "list_artifacts"
	mDeleteArtifact  = "delete_artifact"
	mDeleteScope     = "delete_scope"
	mSupersede       = "supersede"
	mRelate          = "relate"
	mRelated         = "related"
	mConfig          = "config"
	mSetConfig       = "set_config"
	mEmbModel        = "emb_model"

	// mShutdown is a control op (not part of Service): it asks the daemon to
	// stop gracefully. Handled specially, never reaches the engine.
	mShutdown = "shutdown"
	// mStats is a control op returning live daemon stats (conns, uptime, ...).
	mStats = "stats"
	// mRotateToken regenerates the daemon's token(s) and republishes runtime.json.
	mRotateToken = "rotate_token"
	// mMintReadToken mints (replacing any prior) a read-only token.
	mMintReadToken = "mint_read_token"
)

// controlMethods are full-token-only ops handled before the engine switch.
var controlMethods = map[string]bool{
	mShutdown: true, mStats: true, mRotateToken: true, mMintReadToken: true,
}

// tier classifies a method for ACL: control & write require the full token; read
// also accepts the read-only token.
type tier int

const (
	tierRead tier = iota
	tierWrite
	tierControl
)

func tierOf(method string) tier {
	switch {
	case controlMethods[method]:
		return tierControl
	case writeMethods[method]:
		return tierWrite
	default:
		return tierRead
	}
}

// serverStats is the daemon's self-report (control op mStats).
type serverStats struct {
	ProtoVersion int    `json:"proto_version"`
	Conns        int    `json:"conns"`
	Requests     uint64 `json:"requests"`
	StartedAt    string `json:"started_at"`
	EmbedModel   string `json:"embed_model"`
}

// writeMethods mutate the store; the daemon flushes embeddings (eng.Sync) after
// each so a crash does not lose in-memory vectors.
var writeMethods = map[string]bool{
	mCreateScope: true, mPush: true, mPublish: true, mFork: true,
	mConsolidate: true, mCrossVersion: true, mRollupScope: true,
	mDeleteArtifact: true, mDeleteScope: true, mSetConfig: true,
	mSupersede: true, mRelate: true,
}

// --- envelopes ---

type request struct {
	ID     uint64          `json:"id"`
	V      int             `json:"v"` // protocol version
	Method string          `json:"method"`
	Token  string          `json:"token,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
}

type wireError struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

const (
	codeNotFound = "not_found"
	codeInvalid  = "invalid"
	codeInternal = "internal"
	codeAuth     = "auth"
)

// errToWire maps an engine error onto a transport error, preserving the sentinel
// class so the client can re-expose errors.Is(core.ErrNotFound/ErrInvalidInput).
func errToWire(err error) *wireError {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, core.ErrNotFound):
		return &wireError{Code: codeNotFound, Msg: err.Error()}
	case errors.Is(err, core.ErrInvalidInput):
		return &wireError{Code: codeInvalid, Msg: err.Error()}
	default:
		return &wireError{Code: codeInternal, Msg: err.Error()}
	}
}

// toError reconstructs a Go error from the wire, re-wrapping known sentinels.
func (e *wireError) toError() error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case codeNotFound:
		return fmt.Errorf("%s: %w", e.Msg, core.ErrNotFound)
	case codeInvalid:
		return fmt.Errorf("%s: %w", e.Msg, core.ErrInvalidInput)
	default:
		return errors.New(e.Msg)
	}
}

// --- framing: 4-byte big-endian length prefix + JSON payload ---

func writeFrame(w io.Writer, b []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func readFrame(r io.Reader, max uint32) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > max {
		return nil, fmt.Errorf("runtime: frame too large (%d > %d)", n, max)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// --- per-method params/results (core.* types ride as fields; same JSON the
// store already round-trips through bbolt, so Go<->Go is faithful) ---

type createScopeParams struct {
	Parent core.ID   `json:"parent"`
	Role   core.Role `json:"role"`
	Title  string    `json:"title"`
}

type pushParams struct {
	Req core.PushRequest `json:"req"`
}

type queryParams struct {
	Query core.Query `json:"query"`
}

type queryResult struct {
	QueryID core.ID    `json:"query_id"`
	Hits    []core.Hit `json:"hits"`
}

type neighborsParams struct {
	Scope core.ID `json:"scope"`
	Text  string  `json:"text"`
	K     int     `json:"k"`
}

type drillParams struct {
	Artifact core.ID     `json:"artifact"`
	Detail   core.Detail `json:"detail"`
}

type idParams struct {
	ID core.ID `json:"id"`
}

type scopeParams struct {
	Scope core.ID `json:"scope"`
}

type forkParams struct {
	Source core.ID `json:"source"`
	Title  string  `json:"title"`
}

type scopeSummaryParams struct {
	Scope   core.ID `json:"scope"`
	Summary string  `json:"summary"`
}

type consolidateParams struct {
	Scope      core.ID   `json:"scope"`
	Summary    string    `json:"summary"`
	Supersedes []core.ID `json:"supersedes,omitempty"`
}

type crossVersionParams struct {
	Scope core.ID   `json:"scope"`
	Seed  core.Seed `json:"seed"`
}

type recentTracesParams struct {
	N int `json:"n"`
}

type configParams struct {
	Key string `json:"key"`
}

type configResult struct {
	Val string `json:"val"`
	OK  bool   `json:"ok"`
}

type setConfigParams struct {
	Key string `json:"key"`
	Val string `json:"val"`
}

type supersedeParams struct {
	Old         core.ID `json:"old"`
	Replacement core.ID `json:"replacement"`
}

type relateParams struct {
	From core.ID           `json:"from"`
	To   core.ID           `json:"to"`
	Kind core.RelationKind `json:"kind"`
}

type relatedParams struct {
	Artifact core.ID             `json:"artifact"`
	Kinds    []core.RelationKind `json:"kinds,omitempty"`
	Dir      core.EdgeDir        `json:"dir"`
	Depth    int                 `json:"depth"`
}

type embModelResult struct {
	Model string `json:"model"`
}

type rotateTokenResult struct {
	Full string `json:"full_token"`
	Read string `json:"read_token,omitempty"`
}

type mintReadTokenResult struct {
	Read string `json:"read_token"`
}

// marshalRaw is a small helper for building result payloads.
func marshalRaw(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
