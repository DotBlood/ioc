package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/ingest"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// parseIDList parses a comma-separated list of artifact IDs (empty => nil).
func parseIDList(s string) ([]core.ID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.ID
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			id, err := core.ParseID(part)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		}
	}
	return out, nil
}

// parseRelations parses "kind:targetID,kind:targetID" into edge specs (empty => nil).
func parseRelations(s string) ([]core.EdgeSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.EdgeSpec
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("bad relation %q (want kind:targetID)", part)
		}
		id, err := core.ParseID(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, err
		}
		out = append(out, core.EdgeSpec{Kind: core.RelationKind(strings.TrimSpace(kv[0])), Target: id})
	}
	return out, nil
}

// parseKindList parses a comma-separated relation-kind list (empty => nil = all).
func parseKindList(s string) []core.RelationKind {
	var out []core.RelationKind
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, core.RelationKind(k))
		}
	}
	return out
}

// parseEdgeDir maps "out|in|both" to core.EdgeDir (default out).
func parseEdgeDir(s string) core.EdgeDir {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in":
		return core.DirIn
	case "both":
		return core.DirBoth
	default:
		return core.DirOut
	}
}

func (a *ioc) createScope(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	parent, err := iocfmt.ParseScopeID(r.GetString("parent", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := a.svc.CreateScope(ctx, parent, iocfmt.ParseRole(r.GetString("role", "session")), r.GetString("title", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ScopeOut(s))
}

func (a *ioc) push(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	summary, err := r.RequireString("summary")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var content []byte
	if c := r.GetString("content", ""); c != "" {
		content = []byte(c)
	}
	sup, err := parseIDList(r.GetString("supersedes", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rels, err := parseRelations(r.GetString("relations", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	art, err := a.svc.Push(ctx, core.PushRequest{
		Scope:      id,
		Kind:       iocfmt.ParseKind(r.GetString("kind", "insight")),
		Summary:    summary,
		Content:    content,
		Publish:    r.GetBool("publish", false),
		Supersedes: sup,
		Relations:  rels,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ArtifactOut(art))
}

func (a *ioc) supersede(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	old, err := requireID(r, "old")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	by, err := requireID(r, "by")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := a.svc.Supersede(ctx, old, by); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"superseded": old.String(), "by": by.String()})
}

func (a *ioc) relate(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	from, err := requireID(r, "from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := requireID(r, "to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kind := r.GetString("kind", "relates_to")
	if err := a.svc.Relate(ctx, from, to, core.RelationKind(kind)); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"from": from.String(), "to": to.String(), "kind": kind})
}

func (a *ioc) related(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "artifact")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	hits, err := a.svc.Related(ctx, id, parseKindList(r.GetString("kind", "")), parseEdgeDir(r.GetString("dir", "out")), r.GetInt("depth", 1))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.HitsOut(hits))
}

func (a *ioc) ingest(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	path, err := r.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	given, err := iocfmt.ParseScopeID(r.GetString("scope", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rootScope, err := ingest.RootScope(ctx, a.svc, path, given, r.GetString("title", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	st, err := ingest.Ingest(ctx, a.svc, path, rootScope, ingest.Options{
		MaxChars:  r.GetInt("maxchars", ingest.DefaultMaxChars),
		Overlap:   r.GetInt("overlap", ingest.DefaultOverlap),
		MaxFiles:  r.GetInt("max_files", 0),
		MaxChunks: r.GetInt("max_chunks", 0),
		MaxDepth:  r.GetInt("max_depth", 0),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := st.JSON()
	out["root_scope"] = rootScope.String()
	return jsonResult(out)
}

func (a *ioc) query(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Hierarchical uses a HYBRID fine stage (vector+BM25) — the configuration that
	// holds up at scale; pure-vector hierarchy does not help.
	hier := r.GetBool("hierarchical", false)
	// Collapsed (visible ∪ all descendants, flat) is the DEFAULT: it dominates plain
	// flat retrieval, which is blind to a viewpoint's own descendant scopes (the normal
	// nested case → recall 0.00). Pass collapsed=false for flat, or hierarchical=true.
	coll := r.GetBool("collapsed", true)
	mode := core.ModeVector
	if hier {
		mode = core.ModeHybrid
	}
	qid, hits, err := a.svc.Query(ctx, core.Query{
		Scope:               id,
		Text:                text,
		Detail:              iocfmt.ParseDetail(r.GetString("detail", "overview")),
		TopK:                r.GetInt("topk", 5),
		Tier:                iocfmt.ParseTier(r.GetString("tier", "")),
		Kinds:               iocfmt.ParseKinds(r.GetString("kind", "")),
		MinScore:            r.GetFloat("min_score", 0),
		Mode:                mode,
		Hierarchical:        hier,
		Collapsed:           coll,
		CoarseK:             r.GetInt("coarsek", 0),
		Rerank:              r.GetBool("rerank", false),
		RerankN:             r.GetInt("rerank_n", 0),
		// Borderline auto-rerank ON by default (R5): when the cosine result is weak or
		// near-tied, the cross-encoder (if attached) re-ranks and gates abstention; pass
		// auto_rerank=false for pure cosine. No-op without a reranker.
		AutoRerank:          r.GetBool("auto_rerank", true),
		IncludeSuperseded:   r.GetBool("include_superseded", false),
		RecencyHalfLifeDays: r.GetFloat("recency_halflife_days", 0),
		GraphBoost:          r.GetFloat("graph_boost", 0),
		ImportanceWeight:    r.GetFloat("importance_weight", 0),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.QueryOut(qid, hits, core.ResolveConfidence(a.svc.EmbModel(), a.svc.Config)))
}

func (a *ioc) neighbors(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	hits, err := a.svc.Neighbors(ctx, id, text, r.GetInt("k", 5))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.HitsOut(hits))
}

func (a *ioc) rollup(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	summary, err := r.RequireString("summary")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := a.svc.RollupScope(ctx, id, summary); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"rolled_up": id.String()})
}

func (a *ioc) listTraces(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	trs, err := a.svc.RecentTraces(r.GetInt("n", 10))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]map[string]any, len(trs))
	for i, t := range trs {
		out[i] = map[string]any{
			"query_id": t.QueryID.String(),
			"scope":    t.Scope.String(),
			"text":     t.Text,
			"hits":     len(t.Hits),
		}
	}
	return jsonResult(out)
}

func (a *ioc) drill(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "artifact")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	h, err := a.svc.Drill(ctx, id, iocfmt.ParseDetail(r.GetString("detail", "raw")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.HitOut(h))
}

func (a *ioc) publish(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "artifact")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := a.svc.Publish(ctx, id); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"published": id.String()})
}

func (a *ioc) siblings(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	hits, err := a.svc.SiblingOverview(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.HitsOut(hits))
}

func (a *ioc) ancestors(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	scs, err := a.svc.Ancestors(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out := make([]map[string]any, len(scs))
	for i, s := range scs {
		out[i] = iocfmt.ScopeOut(s)
	}
	return jsonResult(out)
}

func (a *ioc) fork(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := a.svc.Fork(ctx, id, r.GetString("title", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ScopeOut(s))
}

func (a *ioc) consolidate(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	summary, err := r.RequireString("summary")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sup, err := parseIDList(r.GetString("supersedes", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	art, err := a.svc.Consolidate(ctx, id, summary, sup)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ArtifactOut(art))
}

func (a *ioc) crossversion(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := a.svc.CrossVersion(ctx, id, core.Seed{
		Constraints: r.GetString("constraints", ""),
		Lessons:     r.GetString("lessons", ""),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ScopeOut(s))
}

func (a *ioc) trace(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := requireID(r, "query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tr, err := a.svc.Trace(ctx, id)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(tr)
}

// --- mcp-specific helpers ---

func requireID(r mcp.CallToolRequest, name string) (core.ID, error) {
	s, err := r.RequireString(name)
	if err != nil {
		return core.NilID, err
	}
	return core.ParseID(s)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
