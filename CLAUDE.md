# CLAUDE.md

Operational context for working in this repository. Source of truth for the **current** project.
For direction/why see [`VISION.md`](VISION.md); for the delta vs the old specs see
[`docs/MODEL-CHANGES.md`](docs/MODEL-CHANGES.md).

## What this is

IOC — a local-first, model-agnostic **memory/context layer for LLMs** (single model or multi-agent).
The repo currently holds a **greenfield thin slice** (v0.2 direction) whose job is to prove the
"wall": that mini-summary + embedding are good enough that an agent rarely needs raw content, and
memory stays navigable across branch/version boundaries. Not feature-complete by design.

The previous v0.1 engine (graph/knowledge/retrieval/etc.) was removed from the working tree and
lives in git history (branch `development/v0.2-review`). Do not resurrect it; build on the slice.

- Single Go module: `github.com/DotBlood/ioc`, **Go 1.26**.
- Deps: `oklog/ulid/v2`, `klauspost/compress` (zstd), `go.etcd.io/bbolt`, `mark3labs/mcp-go`, `testify`.

## Commands

```bash
make build          # -> bin/ioc, bin/ioc-mcp
make test           # go test ./...   (also: make vet / fmt / tidy / lint)
make scenario                         # dogfood scenario on the mock embedder
make scenario EMBED=http://127.0.0.1:8088   # on the real embedder

go run ./cmd/ioc run-scenario internal/eval/scenarios/hoe.json [-embed <endpoint>]
go run ./cmd/ioc embed-ping -embed http://127.0.0.1:8088

# Ingest a code/doc tree as KindDocument chunks (mechanical, no LLM):
go run ./cmd/ioc ingest internal -dir .ioc/files -embed http://127.0.0.1:8088
go run ./cmd/ioc query -dir .ioc/files -embed http://127.0.0.1:8088 \
  -scope <root> -kind document -mode hierarchical -text "cross-encoder reranker"

# Runtime daemon (single owner of -dir; other commands/MCP auto-route to it):
go run ./cmd/ioc serve -dir .ioc/data -embed http://127.0.0.1:8088
go run ./cmd/ioc runtime status -dir .ioc/data    # also: runtime stop
```

`-race` needs cgo+gcc: `PATH=/d/tools/mingw64/mingw64/bin:$PATH CGO_ENABLED=1 go test -race ./...`.

Embedder `-embed`: empty = deterministic mock (pipeline only, not real wall numbers);
`http://host:port` (TCP, Windows-ok) or `unix:/path` = external `py/embed_server.py`.

Retrieval modes (`-mode` / `Query.Mode`+`Hierarchical`+`CoarseK`):
- `vector` (default) — cosine; best at small scale.
- `hybrid` — vector+BM25 via RRF; helps at scale, can hurt at small N.
- `hierarchical` — coarse-rank scope rollups → **hybrid** fine within top `CoarseK` (~6) scopes;
  needs `RollupScope` on sub-scopes. **Best at scale:** recall@topK ~0.94 at 180 artifacts vs 0.33
  vector-only / 0.67 flat-hybrid (real bge-small). Pure-vector hierarchy does NOT help.

`query` returns `query_id`, `weak_match` (per-embedder `core.ConfidenceFloor`, ~0.68 bge-small),
`top_score`, `margin`. `-kind document,reasoning` restricts results by Kind (files vs thoughts).
`-rerank` (CLI) / `rerank` (MCP) cross-encoder reranks the top-N candidates (needs the py `/rerank`
endpoint) — the universal last-mile precision fix. More commands: `ioc query|drill|traces|trace|
rollup|consolidate|crossversion|...`, `ioc gen-scenario -shape flat|tree`.

**Ingestion (`ioc ingest <path>` / MCP `ioc_ingest`, files as a first-class Kind):** mirrors the
directory tree into nested scopes, chunks each text file at **language-aware semantic boundaries**
(`SplitLang`: Go func/type, Python def/class+decorators, Markdown headings, JS/TS decls, generic
paragraphs; doc-comments lifted to attach to their unit), packing units up to ~1500 chars and
falling back to line-aligned char windows (~200 overlap) only for a single oversize unit. It embeds
the **raw chunk text** (`PushRequest.EmbedText`) while keeping a short `path:lines — first line`
label as `Summary`. Each chunk is `KindDocument` with `Meta{path,lines,chunk}` and the chunk
bytes in CAS (so `drill` returns the source). Per-directory mechanical rollups (filenames) let
hierarchical retrieval route by the file tree. Skips `.git`/`vendor`/`node_modules`/`bin`/binaries
(NUL)/files >512KB. Use a SEPARATE `-dir` from reasoning data (one embedder per data-dir). The
reasoning write path (`Push` without `EmbedText`) is unchanged.

`ingest` is **idempotent / synchronizing**: re-running reconciles the store to the current tree —
changed files (detected via `Meta["sig"]` = content hash + chunk params) are re-chunked, new files
added, vanished files' chunks removed, emptied directory scopes pruned; an unchanged re-run is a
no-op (no new scopes/chunks/embeddings). Directory scopes are reused by `(parent, title)`. The
abspath→root-scope mapping is remembered in the meta config bucket (shared resolver `ingest.RootScope`
used by both the CLI and the MCP `ioc_ingest` tool), so a later `ioc ingest <path>` without `-scope`
re-syncs the same tree in place. `ioc_ingest` writes into the MCP server's own store (IOC_DIR/IOC_EMBED);
documents and reasoning coexist there, separated at query time by `kind=document`. Caveats: the
chunk boundary detector is heuristic (line prefixes, not an AST) — odd formatting may misplace a
boundary; semantic units carry no inter-chunk overlap. Orphaned embeddings of
deleted/re-chunked artifacts stay in the append-only `EmbeddingStore` (dead weight, never surfaced
in search — store compaction deferred); a mid-run crash can leave partial state (no transaction;
`-force` deferred). Deferred: language-aware chunking, MCP `ioc_ingest`.

**Runtime daemon (`internal/runtime`, `ioc serve`):** a long-lived process owns ONE store
(`engine.Open` once) and serves clients over an internal framed-JSON RPC (length-prefixed; loopback
TCP; random per-store token in `<dir>/runtime.json`). `runtime.Service` mirrors the engine op set;
both `*engine.Engine` and the remote `*runtime.Client` satisfy it. `runtime.Open(dir, …)` returns a
client when a daemon owns `dir`, else an embedded engine — so CLI memory commands (`openService`) and
the MCP server route through a daemon when present and fall back to embedded otherwise; `serve` and
`run-scenario` use a direct embedded engine (`openEngineEmbedded`). `ingest` takes a small `Store`
interface so it works over either path. Concurrency is in-process: `RWMutex` (parallel reads,
serialized writes); writes flush embeddings (`engine.Sync`) for durability; per-request panic
recovery; graceful shutdown via signal or the `shutdown` control op (`ioc runtime stop`). One daemon
= one data-dir; **sharing = same `-dir`**. Multi-process access to one store is deliberately rejected
in favor of the daemon (see `docs/RUNTIME_ROADMAP.md`). Deferred: MVCC/multi-tenant, networked
MCP/HTTP, auto-start, auth beyond the loopback token.

**Scale levers — measured (real, 180 artifacts, top-5, recall@topK):**

| | bge-small (384d) | bge-base (768d) |
|---|---|---|
| flat vector | 0.39 | 0.72 |
| hierarchical (no rerank) | 0.94 | **1.00** |
| hierarchical + rerank | 1.00 | 1.00 |

Two independent paths to recall 1.0: **bge-small + hierarchical + rerank**, or **bge-base +
hierarchical (no rerank)**. bge-base (set `IOC_EMBED_MODEL=BAAI/bge-base-en-v1.5`) closes the tail
without a reranker but costs a heavier embedder (768d, ~440MB, 2× vector storage). NOTE: switching
embedder requires recalibrating `core.ConfidenceFloor` (the weak_match threshold is bge-small-tuned).

## Layout

```
internal/core/     domain types (Scope, Artifact, Tier, Kind, Detail, Query, Hit, Seed, Trace)
internal/embed/    Embedder iface + MockEmbedder + HTTPEmbedder
internal/storage/  CAS (sha256+zstd), EmbeddingStore (float32), Meta (bbolt)
internal/search/   brute-force cosine (leaf; no core import)
internal/engine/   the public API (Open/Push/Query/Drill/Publish/Fork/Consolidate/CrossVersion/...)
internal/eval/     scenario runner + metrics; eval/scenarios/hoe.json
internal/ingest/   file→chunk→KindDocument ingestion (chunk.go + ingest.go); dir tree → scopes
internal/runtime/  daemon owning the store + framed-JSON client (Service/proto/server/client/discover)
cmd/ioc/           CLI (run-scenario, embed-ping, ingest, memory commands)
cmd/ioc-mcp/       stdio MCP server (ioc_* tools)
py/                embed_server.py (FastAPI) + .venv (gitignored)
```

Dependency direction (no cycles): `core` is a leaf; `embed`/`search` are leaves; `storage`,
`engine`, `eval` import `core` (and engine imports embed/storage/search); `cmd/*` wire it together.

## Core model (from VISION)

- **Recursive scope** — worktree/workspace/session are *roles* of one `core.Scope`; scopes fork and version.
- **Artifact** = leaf result/insight; full content in CAS (cold); working layer holds mini-summary +
  embedding **of the summary**.
- **Two-tier memory** = artifacts tagged `Tier` (worktree = canonical truths, workspace = mutable).
- **Visibility (bottom-up)** — a scope sees siblings only via published summaries; can drill up to
  ancestors; raw reasoning is private until published.
- **Progressive disclosure** — `Query` at `DetailOverview` (cheap) → `Drill` to `DetailRaw` on demand.
- **External LLM writes** — `Push` takes an LLM-authored summary; IOC embeds + stores; IOC never calls an LLM.
- **Consolidation** at branch transition (`Consolidate`) and version boundary (`CrossVersion` + seed).

## Conventions

- All public APIs take `context.Context` first. Return errors, no panics.
- Table-driven tests with `testify/require`.
- `internal/` for implementation; `cmd/` for binaries. One `-dir` = one embedder (vector spaces differ).

## Current state

Slice builds and `go test ./...` is green. Scenario PASSes on the mock embedder (pipeline validated).
**The real wall test (real embedder + a live agent session) is the open item.** See
`internal/eval/` and the metrics in `README.md`.
