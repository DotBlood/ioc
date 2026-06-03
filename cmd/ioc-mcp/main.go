// Command ioc-mcp exposes the IOC engine as a stdio MCP server, so an
// MCP-capable agent can use IOC as memory natively.
//
// Config via environment:
//
//	IOC_DIR    persistent data directory (default ".ioc-data")
//	IOC_EMBED  embedder endpoint (empty=mock; http://host:port or unix:/path)
//
// A single engine instance is guarded by a mutex (single-writer).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// instructions is sent to the client on initialize so the LLM understands what
// IOC is and how to drive it.
const instructions = `IOC is your external memory. Store distilled insights here instead of
keeping everything in context; retrieve them later instead of re-deriving or re-reading.

Mental model:
- A SCOPE is a unit of work (a project, an area, or a line of reasoning). Scopes nest and can fork.
- An ARTIFACT is one result/insight. You write a short SUMMARY (this is what gets embedded and
  searched); optionally attach the full CONTENT (kept cold, fetched only on demand).

Typical loop:
1. ioc_create_scope to open a scope for the task (nest under a parent when relevant).
2. ioc_push after each meaningful conclusion: a 1-2 sentence summary + optional full content.
   Set publish=true to let sibling scopes see it.
3. ioc_query (detail=overview) to recall — you get cheap summaries + scores. Read those FIRST.
   Only ioc_drill (detail=raw) into a specific artifact when the summary is not enough.
4. ioc_consolidate when a line of work ends: write one summary that captures what matters; it is
   promoted to the parent's long-term memory.
5. ioc_crossversion when starting a new major version: archive the old one and seed the new with
   the carried-forward constraints/lessons (not all the detail).

Rules: keep summaries short and specific; prefer querying over re-reading; raw content costs
context, summaries do not. Visibility is bottom-up: you see your own scope, your ancestors, and
siblings' PUBLISHED artifacts.

ioc_query returns a query_id (inspect with ioc_trace; list recent ones with ioc_list_traces) and a
weak_match flag. If weak_match is true (top score below ~0.45), there is no specific stored artifact
for your question — do NOT present the returned general context as a precise answer.`

type ioc struct {
	mu sync.Mutex
	e  *engine.Engine
}

func main() {
	dir := envOr("IOC_DIR", ".ioc-data")
	endpoint := os.Getenv("IOC_EMBED")

	var embedder embed.Embedder
	if endpoint == "" {
		embedder = embed.NewMockEmbedder(384)
	} else {
		embedder = embed.NewHTTPEmbedder(endpoint)
	}

	e, err := engine.Open(context.Background(), dir, embedder)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ioc-mcp: open engine:", err)
		os.Exit(1)
	}
	defer e.Close()
	app := &ioc{e: e}

	s := server.NewMCPServer("ioc", "0.1.0",
		server.WithToolCapabilities(false),
		server.WithInstructions(instructions),
	)
	app.register(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "ioc-mcp:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (a *ioc) register(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("ioc_create_scope",
		mcp.WithDescription("Create a scope. parent empty = root. role: worktree|workspace|session."),
		mcp.WithString("parent", mcp.Description("parent scope ID (empty = root)")),
		mcp.WithString("role", mcp.Description("worktree|workspace|session")),
		mcp.WithString("title", mcp.Description("scope title")),
	), a.createScope)

	s.AddTool(mcp.NewTool("ioc_push",
		mcp.WithDescription("Write an artifact: a REQUIRED mini-summary (gets embedded) plus optional full content (stored cold)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("mini-summary; this is what gets embedded")),
		mcp.WithString("kind", mcp.Description("answer|insight|summary|document|reasoning|seed")),
		mcp.WithString("content", mcp.Description("optional full content (kept cold in CAS)")),
		mcp.WithBoolean("publish", mcp.Description("make visible to sibling scopes")),
	), a.push)

	s.AddTool(mcp.NewTool("ioc_query",
		mcp.WithDescription("Progressive-disclosure semantic retrieval from a viewpoint scope. Start at detail=overview (cheap); drill only if needed. Returns query_id (for ioc_trace) and weak_match=true when the top score is low (no specific artifact — don't treat general context as a precise answer)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("viewpoint scope ID")),
		mcp.WithString("text", mcp.Required(), mcp.Description("query text")),
		mcp.WithString("detail", mcp.Description("overview|entry|raw (default overview)")),
		mcp.WithString("tier", mcp.Description("worktree|workspace (empty = both)")),
		mcp.WithNumber("topk", mcp.Description("max results (default 5)")),
		mcp.WithNumber("min_score", mcp.Description("drop hits with cosine score below this (0 = keep all)")),
	), a.query)

	s.AddTool(mcp.NewTool("ioc_list_traces",
		mcp.WithDescription("List recent query traces (newest first); each has a query_id you can pass to ioc_trace."),
		mcp.WithNumber("n", mcp.Description("max traces (default 10)")),
	), a.listTraces)

	s.AddTool(mcp.NewTool("ioc_drill",
		mcp.WithDescription("Fetch one artifact at higher detail (raw loads full content from CAS)."),
		mcp.WithString("artifact", mcp.Required(), mcp.Description("artifact ID")),
		mcp.WithString("detail", mcp.Description("entry|raw (default raw)")),
	), a.drill)

	s.AddTool(mcp.NewTool("ioc_publish",
		mcp.WithDescription("Publish an artifact so sibling scopes can see it."),
		mcp.WithString("artifact", mcp.Required(), mcp.Description("artifact ID")),
	), a.publish)

	s.AddTool(mcp.NewTool("ioc_siblings",
		mcp.WithDescription("Published artifacts of sibling scopes (the blackboard) as cheap summaries."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
	), a.siblings)

	s.AddTool(mcp.NewTool("ioc_ancestors",
		mcp.WithDescription("Ancestor scope chain (nearest parent first)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
	), a.ancestors)

	s.AddTool(mcp.NewTool("ioc_fork",
		mcp.WithDescription("Fork a scope (keep both). Copies published summaries+embeddings into the new scope."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("source scope ID")),
		mcp.WithString("title", mcp.Description("new scope title")),
	), a.fork)

	s.AddTool(mcp.NewTool("ioc_consolidate",
		mcp.WithDescription("Promote a consolidated summary into the parent scope's worktree tier (branch-transition boundary)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("consolidated summary text")),
	), a.consolidate)

	s.AddTool(mcp.NewTool("ioc_crossversion",
		mcp.WithDescription("Archive the scope version and open vN+1 seeded with distilled constraints/lessons."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("constraints", mcp.Description("carried-forward constraints")),
		mcp.WithString("lessons", mcp.Description("carried-forward lessons")),
	), a.crossversion)

	s.AddTool(mcp.NewTool("ioc_trace",
		mcp.WithDescription("Show the exact context a recorded query saw."),
		mcp.WithString("query", mcp.Required(), mcp.Description("query ID")),
	), a.trace)
}

// --- handlers ---

func (a *ioc) createScope(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	parent, err := parseScopeID(r.GetString("parent", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s, err := a.e.CreateScope(ctx, parent, parseRole(r.GetString("role", "session")), r.GetString("title", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(scopeOut(s))
}

func (a *ioc) push(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	scope, err := r.RequireString("scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	scopeID, err := core.ParseID(scope)
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
		Scope:   scopeID,
		Kind:    parseKind(r.GetString("kind", "insight")),
		Summary: summary,
		Content: content,
		Publish: r.GetBool("publish", false),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(artifactOut(art))
}

func (a *ioc) query(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	scope, err := r.RequireString("scope")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	scopeID, err := core.ParseID(scope)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	text, err := r.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	qid, hits, err := a.e.Query(ctx, core.Query{
		Scope:    scopeID,
		Text:     text,
		Detail:   parseDetail(r.GetString("detail", "overview")),
		TopK:     r.GetInt("topk", 5),
		Tier:     parseTier(r.GetString("tier", "")),
		MinScore: r.GetFloat("min_score", 0),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	weak := len(hits) == 0 || hits[0].Score < weakThreshold
	return jsonResult(map[string]any{
		"query_id":   qid.String(),
		"weak_match": weak,
		"hits":       hitsOut(hits),
	})
}

const weakThreshold = 0.45

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
	h, err := a.e.Drill(ctx, id, parseDetail(r.GetString("detail", "raw")))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(hitOut(h))
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
	return jsonResult(hitsOut(hits))
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
		out[i] = scopeOut(s)
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
	return jsonResult(scopeOut(s))
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
	return jsonResult(artifactOut(art))
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
	return jsonResult(scopeOut(s))
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

// --- helpers ---

func requireID(r mcp.CallToolRequest, name string) (core.ID, error) {
	s, err := r.RequireString(name)
	if err != nil {
		return core.NilID, err
	}
	return core.ParseID(s)
}

func parseScopeID(s string) (core.ID, error) {
	if s == "" || s == "root" {
		return core.NilID, nil
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

func parseRole(s string) core.Role {
	switch strings.ToLower(s) {
	case "worktree":
		return core.RoleWorktree
	case "workspace":
		return core.RoleWorkspace
	default:
		return core.RoleSession
	}
}

func parseKind(s string) core.ArtifactKind {
	switch strings.ToLower(s) {
	case "answer":
		return core.KindAnswer
	case "summary":
		return core.KindSummary
	case "document":
		return core.KindDocument
	case "reasoning":
		return core.KindReasoning
	case "seed":
		return core.KindSeed
	default:
		return core.KindInsight
	}
}

func parseDetail(s string) core.Detail {
	switch strings.ToLower(s) {
	case "raw":
		return core.DetailRaw
	case "entry":
		return core.DetailEntry
	default:
		return core.DetailOverview
	}
}

func parseTier(s string) core.Tier {
	switch strings.ToLower(s) {
	case "worktree":
		return core.TierWorktree
	case "workspace":
		return core.TierWorkspace
	default:
		return 0
	}
}

func scopeOut(s core.Scope) map[string]any {
	out := map[string]any{
		"id": s.ID.String(), "role": s.Role.String(), "title": s.Title,
		"version": s.Version, "archived": s.Archived,
	}
	if !s.Parent.IsZero() {
		out["parent"] = s.Parent.String()
	}
	if !s.ForkedFrom.IsZero() {
		out["forked_from"] = s.ForkedFrom.String()
	}
	return out
}

func artifactOut(a core.Artifact) map[string]any {
	return map[string]any{
		"id": a.ID.String(), "scope": a.Scope.String(), "kind": a.Kind.String(),
		"tier": a.Tier.String(), "published": a.Published,
	}
}

func hitOut(h core.Hit) map[string]any {
	out := map[string]any{
		"artifact": h.Artifact.String(), "scope_path": h.ScopePath, "kind": h.Kind.String(),
		"tier": h.Tier.String(), "summary": h.Summary, "score": h.Score,
	}
	if len(h.Content) > 0 {
		out["content"] = string(h.Content)
	}
	return out
}

func hitsOut(hits []core.Hit) []map[string]any {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = hitOut(h)
	}
	return out
}
