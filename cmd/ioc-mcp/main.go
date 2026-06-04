// Command ioc-mcp exposes the IOC engine as a stdio MCP server, so an
// MCP-capable agent can use IOC as memory natively.
//
// Config via environment:
//
//	IOC_DIR    persistent data directory (default ".ioc/mcp-data")
//	IOC_EMBED  embedder endpoint (empty=mock; http://host:port or unix:/path)
//
// A single engine instance is guarded by a mutex (single-writer).
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

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
  searched); optionally attach the full CONTENT (kept cold, fetched only on demand). Kind marks
  what it is: document (a file), reasoning, insight, answer, summary, seed.

Typical loop:
1. ioc_create_scope to open a scope for the task (nest under a parent when relevant).
2. ioc_push after each meaningful conclusion: a 1-2 sentence summary + optional full content.
   Set publish=true to let sibling scopes see it.
3. ioc_query (detail=overview) to recall — you get cheap summaries + scores. Read those FIRST.
   Only ioc_drill (detail=raw) into a specific artifact when the summary is not enough.
   Use kind=... to search only files (document) or only thoughts (reasoning,insight).
   To pull existing code/docs into memory, ioc_ingest a directory once (idempotent; re-run to
   re-sync), then ioc_query kind=document instead of re-reading files from disk.
4. ioc_consolidate when a line of work ends: write one summary that captures what matters; it is
   promoted to the parent's long-term memory.
5. ioc_crossversion when starting a new major version: archive the old one and seed the new with
   the carried-forward constraints/lessons (not all the detail).

Rules: keep summaries short and specific; prefer querying over re-reading; raw content costs
context, summaries do not. Visibility is bottom-up: you see your own scope, your ancestors, and
siblings' PUBLISHED artifacts.

ioc_query returns a query_id (inspect with ioc_trace; list recent ones with ioc_list_traces) plus
weak_match, top_score and margin. weak_match uses a per-embedder confidence floor; if it is true
(or margin between the top two hits is tiny), there is no specific stored artifact for your question
— do NOT present the returned general context as a precise answer; say you don't have it.

Scaling: if a scope accumulates many artifacts, split work into sub-scopes, call ioc_rollup on each
with a short "what this scope is about" summary, then query the parent with hierarchical=true — IOC
ranks the rollups first and searches only the best scopes (coarse→fine).`

type ioc struct {
	mu sync.Mutex
	e  *engine.Engine
}

func main() {
	dir := envOr("IOC_DIR", ".ioc/mcp-data")
	endpoint := os.Getenv("IOC_EMBED")

	var embedder embed.Embedder
	if endpoint == "" {
		embedder = embed.NewMockEmbedder(384)
	} else {
		embedder = embed.NewHTTPEmbedder(endpoint)
	}

	var opts []engine.Option
	if endpoint != "" {
		opts = append(opts, engine.WithReranker(embed.NewHTTPReranker(endpoint)))
	}
	e, err := engine.Open(context.Background(), dir, embedder, opts...)
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

	s.AddTool(mcp.NewTool("ioc_ingest",
		mcp.WithDescription("Mechanically load a code/doc directory tree into memory as document chunks (no LLM): mirrors dirs to scopes, language-aware chunking, embeds raw chunk text. Idempotent — re-running syncs in place (changed files re-chunked, removed files pruned). Then retrieve with ioc_query kind=document."),
		mcp.WithString("path", mcp.Required(), mcp.Description("directory (or file) path to ingest")),
		mcp.WithString("scope", mcp.Description("root scope ID to ingest under (empty = reuse remembered root for this path, else create a worktree)")),
		mcp.WithString("title", mcp.Description("title for the created root scope (default: base name of path)")),
		mcp.WithNumber("maxchars", mcp.Description("chunk window size in chars (default 1500)")),
		mcp.WithNumber("overlap", mcp.Description("chunk overlap in chars for oversize units (default 200)")),
	), a.ingest)

	s.AddTool(mcp.NewTool("ioc_push",
		mcp.WithDescription("Write an artifact: a REQUIRED mini-summary (gets embedded) plus optional full content (stored cold)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("mini-summary; this is what gets embedded")),
		mcp.WithString("kind", mcp.Description("answer|insight|summary|document|reasoning|seed (document = a file)")),
		mcp.WithString("content", mcp.Description("optional full content (kept cold in CAS)")),
		mcp.WithBoolean("publish", mcp.Description("make visible to sibling scopes")),
	), a.push)

	s.AddTool(mcp.NewTool("ioc_query",
		mcp.WithDescription("Progressive-disclosure semantic retrieval from a viewpoint scope. Start at detail=overview (cheap); drill only if needed. Returns query_id (for ioc_trace), weak_match, top_score, margin."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("viewpoint scope ID")),
		mcp.WithString("text", mcp.Required(), mcp.Description("query text")),
		mcp.WithString("detail", mcp.Description("overview|entry|raw (default overview)")),
		mcp.WithString("tier", mcp.Description("worktree|workspace (empty = both)")),
		mcp.WithString("kind", mcp.Description("restrict to kinds, comma list e.g. document,reasoning (empty = all)")),
		mcp.WithNumber("topk", mcp.Description("max results (default 5)")),
		mcp.WithNumber("min_score", mcp.Description("drop hits with cosine score below this (0 = keep all)")),
		mcp.WithBoolean("hierarchical", mcp.Description("coarse→fine: rank scope rollups, then search within top scopes (needs ioc_rollup on sub-scopes)")),
		mcp.WithNumber("coarsek", mcp.Description("hierarchical coarse stage: # scopes to keep (0=default)")),
		mcp.WithBoolean("rerank", mcp.Description("cross-encoder rerank the top candidates for higher precision")),
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

	s.AddTool(mcp.NewTool("ioc_rollup",
		mcp.WithDescription("Attach a short summary representing a scope's contents, so hierarchical queries can rank scopes and search inside the best ones. Use on sub-scopes when you have many."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("what this scope is about")),
	), a.rollup)

	s.AddTool(mcp.NewTool("ioc_crossversion",
		mcp.WithDescription("Archive the scope version and open vN+1 seeded with distilled constraints/lessons (one per line)."),
		mcp.WithString("scope", mcp.Required(), mcp.Description("scope ID")),
		mcp.WithString("constraints", mcp.Description("carried-forward constraints (one per line)")),
		mcp.WithString("lessons", mcp.Description("carried-forward lessons (one per line)")),
	), a.crossversion)

	s.AddTool(mcp.NewTool("ioc_trace",
		mcp.WithDescription("Show the exact context a recorded query saw."),
		mcp.WithString("query", mcp.Required(), mcp.Description("query ID")),
	), a.trace)
}
