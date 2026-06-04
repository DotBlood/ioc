package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DotBlood/ioc/internal/core"
)

// handle authenticates and dispatches one request, returning its response.
func (s *Server) handle(ctx context.Context, req request) response {
	if req.Token != s.token {
		return response{ID: req.ID, Error: &wireError{Code: codeAuth, Msg: "runtime: bad or missing token"}}
	}
	if req.Method == mShutdown {
		// Acknowledge here; serveConn triggers the actual Stop after the reply
		// is flushed (avoids closing the conn before the client reads OK).
		return response{ID: req.ID}
	}
	result, err := s.invoke(ctx, req.Method, req.Params)
	return response{ID: req.ID, Result: result, Error: errToWire(err)}
}

// invoke serializes the engine call and flushes embeddings after writes.
func (s *Server) invoke(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := s.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	if writeMethods[method] {
		if err := s.eng.Sync(); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func decode(params json.RawMessage, v any) error {
	if err := json.Unmarshal(params, v); err != nil {
		return fmt.Errorf("%w: bad params: %v", core.ErrInvalidInput, err)
	}
	return nil
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
		var p scopeSummaryParams
		if err := decode(params, &p); err != nil {
			return nil, err
		}
		a, err := s.eng.Consolidate(ctx, p.Scope, p.Summary)
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
