# IOC MCP — guide for agents

This explains, for an LLM/agent, what the `ioc` MCP server is and how to drive it. The same text
(condensed) is sent automatically as the server's `instructions` on initialize.

## What IOC is

IOC is your **external memory**. Instead of keeping every detail in your context window, you
store distilled insights in IOC and retrieve them later. It is not an LLM — *you* write the
summaries; IOC embeds, stores, and serves them.

## The two objects

- **Scope** — a unit of work: a project, an area of study, or a single line of reasoning. Scopes
  nest (a scope has a parent) and can **fork** (branch a line of thought, keeping both). Roles are
  just labels: `worktree` (top/project), `workspace` (area), `session` (a reasoning thread).
- **Artifact** — one result or insight inside a scope. You always provide a short **summary**
  (1–2 sentences — this is what gets embedded and searched). You may attach full **content**,
  which is stored cold and only returned when you explicitly drill into it.

## Tools

| Tool | Use it to |
|------|-----------|
| `ioc_create_scope` | open a scope (`parent` empty = root; `role` = worktree/workspace/session) |
| `ioc_push` | record an insight: `summary` (required) + optional `content`; `publish=true` to share with siblings |
| `ioc_ingest` | load a code/doc tree into memory as document chunks: `path` (required); idempotent re-sync; language-aware chunking. Then `ioc_query kind=document` instead of re-reading files |
| `ioc_query` | recall from a scope: `detail=overview` returns cheap summaries + scores — **read these first** |
| `ioc_drill` | fetch ONE artifact at `detail=raw` (full content) — only when the summary isn't enough |
| `ioc_publish` | make an existing artifact visible to sibling scopes |
| `ioc_siblings` | list sibling scopes' published artifacts (the shared "blackboard") |
| `ioc_ancestors` | list the scope's ancestor chain |
| `ioc_fork` | branch a scope (keep both); copies published summaries into the new scope |
| `ioc_consolidate` | end a line of work: write one summary, promoted to the parent's long-term memory |
| `ioc_crossversion` | start a new version: archive the old, seed the new with carried-forward constraints/lessons |
| `ioc_trace` | inspect exactly what context a past query saw (pass a query_id) |
| `ioc_list_traces` | list recent query traces (newest first) to get a query_id |

## Recommended loop

1. **Open** a scope for the task (`ioc_create_scope`), nesting under a relevant parent.
2. **Push** after each meaningful conclusion (`ioc_push`) — short summary, optional full content,
   `publish=true` when others should see it.
3. **Recall** with `ioc_query` at `detail=overview`. Use the summaries. Only `ioc_drill` to raw
   when a summary is insufficient — raw content costs context, summaries don't.
4. **Consolidate** when a line of work ends (`ioc_consolidate`) — one summary that captures what matters.
5. **Cross a version** when starting something new on top (`ioc_crossversion`) — carry forward the
   distilled constraints/lessons, not all the detail, so the new version starts clean but informed.

## Rules of thumb

- Summaries short and specific; they are the cheap surface you'll navigate by.
- Prefer querying IOC over re-reading or re-deriving.
- Visibility is **bottom-up**: you see your own scope, your ancestors, and siblings' *published*
  artifacts — not other scopes' private reasoning.
- One IOC data directory must use one embedder consistently (mock vs real are different vector spaces).
- `ioc_query` returns `query_id` (inspect with `ioc_trace` / `ioc_list_traces`) and `weak_match`.
  **If `weak_match` is true (top cosine score below ~0.45), there is no specific stored artifact** —
  don't present the returned general context as a precise answer; say you don't have it, or push it.
- Retrieval is semantic (vector) by default. **At scale (many artifacts), split into sub-scopes,
  `ioc_rollup` each, and query the parent with `hierarchical=true`** — this runs a coarse rollup
  rank then a hybrid (vector+BM25) fine search and recovers recall (≈0.94 vs 0.33 vector-only at
  180 artifacts on bge-small). Tune `coarsek` (~6) if needed.

## Setup (operator)

Build and register the server, then reconnect the client so the tools appear:

```bash
make build      # -> bin/ioc-mcp
```

`.mcp.json` (repo root):

```json
{
  "mcpServers": {
    "ioc": {
      "command": "F:\\projects\\IOC\\bin\\ioc-mcp.exe",
      "env": { "IOC_DIR": "F:\\projects\\IOC\\.ioc-mcp-data", "IOC_EMBED": "http://127.0.0.1:8088" }
    }
  }
}
```

With `IOC_EMBED` set to the embedding service, start it first (see [`../README.md`](../README.md)).
Leave `IOC_EMBED` empty to use the offline mock embedder (fine for trying the tools, but recall
quality is only a lexical proxy).
