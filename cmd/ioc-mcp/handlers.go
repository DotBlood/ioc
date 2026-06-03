package main

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

func (a *ioc) createScope(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	parent, err := iocfmt.ParseScopeID(r.GetString("parent", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := a.e.CreateScope(ctx, parent, iocfmt.ParseRole(r.GetString("role", "session")), r.GetString("title", ""))
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
	art, err := a.e.Push(ctx, core.PushRequest{
		Scope:   id,
		Kind:    iocfmt.ParseKind(r.GetString("kind", "insight")),
		Summary: summary,
		Content: content,
		Publish: r.GetBool("publish", false),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.ArtifactOut(art))
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
	qid, hits, err := a.e.Query(ctx, core.Query{
		Scope:        id,
		Text:         text,
		Detail:       iocfmt.ParseDetail(r.GetString("detail", "overview")),
		TopK:         r.GetInt("topk", 5),
		Tier:         iocfmt.ParseTier(r.GetString("tier", "")),
		Kinds:        iocfmt.ParseKinds(r.GetString("kind", "")),
		MinScore:     r.GetFloat("min_score", 0),
		Hierarchical: r.GetBool("hierarchical", false),
		CoarseK:      r.GetInt("coarsek", 0),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(iocfmt.QueryOut(qid, hits, a.e.EmbModel()))
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
	if err := a.e.RollupScope(ctx, id, summary); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(map[string]any{"rolled_up": id.String()})
}

func (a *ioc) listTraces(_ context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	trs, err := a.e.RecentTraces(r.GetInt("n", 10))
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
	h, err := a.e.Drill(ctx, id, iocfmt.ParseDetail(r.GetString("detail", "raw")))
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
	if err := a.e.Publish(ctx, id); err != nil {
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
	hits, err := a.e.SiblingOverview(ctx, id)
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
	scs, err := a.e.Ancestors(ctx, id)
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
	s, err := a.e.Fork(ctx, id, r.GetString("title", ""))
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
	art, err := a.e.Consolidate(ctx, id, summary)
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
	s, err := a.e.CrossVersion(ctx, id, core.Seed{
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
	tr, err := a.e.Trace(ctx, id)
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
