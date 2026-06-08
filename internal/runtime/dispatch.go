package runtime

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DotBlood/ioc/internal/core"
)

// handle authenticates and dispatches one request, returning its response and
// whether the request authenticated with the FULL (owner) token. A panic in
// dispatch is recovered into an internal error so one bad request cannot crash the
// daemon. fullAuthed is the signal serveConn uses to lift the per-connection frame
// cap: ONLY a genuine full-token request unlocks data-size frames — never an error
// response, never a read-only token (reads always fit the control-size cap). This
// closes the V6 frame-guard bypass where any non-codeAuth response (e.g. a pre-auth
// protocol-version mismatch) used to flip the tier.
func (s *Server) handle(ctx context.Context, req request) (resp response, fullAuthed bool) {
	defer func() {
		if r := recover(); r != nil {
			// fullAuthed is a named return set BEFORE dispatch, so a recovered panic
			// on a valid full-token request still reports the connection as authed.
			resp = response{ID: req.ID, Error: &wireError{Code: codeInternal, Msg: fmt.Sprintf("runtime: panic: %v", r)}}
		}
	}()
	s.reqCount.Add(1)

	if req.V != 0 && req.V != ProtoVersion {
		// Pre-auth early return: token not yet checked, so the frame tier must NOT
		// unlock here (this was the bypass).
		return response{ID: req.ID, Error: &wireError{Code: codeInvalid, Msg: fmt.Sprintf("runtime: protocol version mismatch (client %d, server %d)", req.V, ProtoVersion)}}, false
	}
	// Tiered ACL (V3 + 2-principal model). Run BOTH constant-time compares
	// unconditionally so timing doesn't reveal which token matched: control & write
	// require the full token; reads also accept the read-only token.
	ts := s.tokens.Load()
	full := subtle.ConstantTimeCompare([]byte(req.Token), []byte(ts.full)) == 1
	readOK := ts.read != "" && subtle.ConstantTimeCompare([]byte(req.Token), []byte(ts.read)) == 1
	fullAuthed = full // frame-tier signal: only the owner token lifts the cap
	authed := false
	switch tierOf(req.Method) {
	case tierControl, tierWrite:
		authed = full
	default: // tierRead
		authed = full || readOK
	}
	if !authed {
		return response{ID: req.ID, Error: &wireError{Code: codeAuth, Msg: "runtime: bad or missing token"}}, fullAuthed
	}

	switch req.Method {
	case mShutdown:
		// Acknowledge here; serveConn triggers the actual Stop after the reply
		// is flushed (avoids closing the conn before the client reads OK).
		return response{ID: req.ID}, fullAuthed
	case mStats:
		raw, _ := marshalRaw(serverStats{
			ProtoVersion: ProtoVersion, Conns: s.connCount(), Requests: s.reqCount.Load(),
			StartedAt: s.startedAt, EmbedModel: s.eng.EmbModel(),
		})
		return response{ID: req.ID, Result: raw}, fullAuthed
	case mRotateToken:
		ns, err := s.rotateTokens()
		if err != nil {
			return response{ID: req.ID, Error: errToWire(err)}, fullAuthed
		}
		raw, _ := marshalRaw(rotateTokenResult{Full: ns.full, Read: ns.read})
		return response{ID: req.ID, Result: raw}, fullAuthed
	case mMintReadToken:
		read, err := s.mintReadToken()
		if err != nil {
			return response{ID: req.ID, Error: errToWire(err)}, fullAuthed
		}
		raw, _ := marshalRaw(mintReadTokenResult{Read: read})
		return response{ID: req.ID, Result: raw}, fullAuthed
	}
	result, err := s.invoke(ctx, req.Method, req.Params)
	return response{ID: req.ID, Result: result, Error: errToWire(err)}, fullAuthed
}

// invoke runs the engine call under the guard: writes take an exclusive Lock
// (then flush embeddings), reads take a shared RLock so they run concurrently.
// defer-unlock keeps the lock released even if call panics (recovered above).
func (s *Server) invoke(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if writeMethods[method] {
		s.mu.Lock()
		defer s.mu.Unlock()
		raw, err := s.call(ctx, method, params)
		if err != nil {
			return nil, err
		}
		if err := s.eng.Sync(); err != nil {
			return nil, err
		}
		return raw, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.call(ctx, method, params)
}

// maxJSONDepth bounds nesting in request params (V7). IOC's params are flat
// (PushRequest/Query etc.), so 64 is far beyond any real payload; it only stops a
// pathologically deep object/array from burning CPU/stack in json.Unmarshal.
// (Params SIZE is already bounded by the frame cap in proto.go — V6 — so no extra
// byte cap here, which would otherwise break a legitimately large Push.Content.)
const maxJSONDepth = 64

func decode(params json.RawMessage, v any) error {
	if err := checkJSONDepth(params, maxJSONDepth); err != nil {
		return err
	}
	if err := json.Unmarshal(params, v); err != nil {
		return fmt.Errorf("%w: bad params: %v", core.ErrInvalidInput, err)
	}
	return nil
}

// checkJSONDepth rejects params whose object/array nesting exceeds max. It walks
// tokens (string-aware via encoding/json), so it counts real structural depth, not
// braces inside strings.
func checkJSONDepth(raw json.RawMessage, max int) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: malformed params json: %v", core.ErrInvalidInput, err)
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				depth++
				if depth > max {
					return fmt.Errorf("%w: params nested too deep (> %d)", core.ErrInvalidInput, max)
				}
			case '}', ']':
				depth--
			}
		}
	}
}

// call routes one method to the engine. Must be invoked under s.mu.
func (s *Server) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case mCreateScope:
		var p createScopeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sc, err := s.eng.CreateScope(ctx, p.Parent, p.Role, p.Title)
		if err != nil {
			return nil, err
		}
		return marshalRaw(sc)
	case mPush:
		var p pushParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		a, err := s.eng.Push(ctx, p.Req)
		if err != nil {
			return nil, err
		}
		return marshalRaw(a)
	case mQuery:
		var p queryParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		qid, hits, err := s.eng.Query(ctx, p.Query)
		if err != nil {
			return nil, err
		}
		return marshalRaw(queryResult{QueryID: qid, Hits: hits})
	case mNeighbors:
		var p neighborsParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		hits, err := s.eng.Neighbors(ctx, p.Scope, p.Text, p.K)
		if err != nil {
			return nil, err
		}
		return marshalRaw(hits)
	case mDrill:
		var p drillParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		h, err := s.eng.Drill(ctx, p.Artifact, p.Detail)
		if err != nil {
			return nil, err
		}
		return marshalRaw(h)
	case mPublish:
		var p idParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.Publish(ctx, p.ID)
	case mSiblingOverview:
		var p scopeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		hits, err := s.eng.SiblingOverview(ctx, p.Scope)
		if err != nil {
			return nil, err
		}
		return marshalRaw(hits)
	case mAncestors:
		var p scopeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		scs, err := s.eng.Ancestors(ctx, p.Scope)
		if err != nil {
			return nil, err
		}
		return marshalRaw(scs)
	case mFork:
		var p forkParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sc, err := s.eng.Fork(ctx, p.Source, p.Title)
		if err != nil {
			return nil, err
		}
		return marshalRaw(sc)
	case mConsolidate:
		var p consolidateParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		a, err := s.eng.Consolidate(ctx, p.Scope, p.Summary, p.Supersedes)
		if err != nil {
			return nil, err
		}
		return marshalRaw(a)
	case mCrossVersion:
		var p crossVersionParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sc, err := s.eng.CrossVersion(ctx, p.Scope, p.Seed)
		if err != nil {
			return nil, err
		}
		return marshalRaw(sc)
	case mRollupScope:
		var p scopeSummaryParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.RollupScope(ctx, p.Scope, p.Summary)
	case mTrace:
		var p idParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		tr, err := s.eng.Trace(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return marshalRaw(tr)
	case mRecentTraces:
		var p recentTracesParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		trs, err := s.eng.RecentTraces(p.N)
		if err != nil {
			return nil, err
		}
		return marshalRaw(trs)
	case mListScopes:
		scs, err := s.eng.ListScopes(ctx)
		if err != nil {
			return nil, err
		}
		return marshalRaw(scs)
	case mGetScope:
		var p idParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		sc, err := s.eng.GetScope(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return marshalRaw(sc)
	case mListArtifacts:
		arts, err := s.eng.ListArtifacts(ctx)
		if err != nil {
			return nil, err
		}
		return marshalRaw(arts)
	case mDeleteArtifact:
		var p idParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.DeleteArtifact(ctx, p.ID)
	case mDeleteScope:
		var p idParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.DeleteScope(ctx, p.ID)
	case mSupersede:
		var p supersedeParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.Supersede(ctx, p.Old, p.Replacement)
	case mRelate:
		var p relateParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.Relate(ctx, p.From, p.To, p.Kind)
	case mRelated:
		var p relatedParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		hits, err := s.eng.Related(ctx, p.Artifact, p.Kinds, p.Dir, p.Depth)
		if err != nil {
			return nil, err
		}
		return marshalRaw(hits)
	case mConfig:
		var p configParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		val, ok := s.eng.Config(p.Key)
		return marshalRaw(configResult{Val: val, OK: ok})
	case mSetConfig:
		var p setConfigParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		return nil, s.eng.SetConfig(p.Key, p.Val)
	case mEmbModel:
		return marshalRaw(embModelResult{Model: s.eng.EmbModel()})
	default:
		return nil, fmt.Errorf("%w: unknown method %q", core.ErrInvalidInput, method)
	}
}
