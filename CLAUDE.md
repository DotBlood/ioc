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
```

Embedder `-embed`: empty = deterministic mock (pipeline only, not real wall numbers);
`http://host:port` (TCP, Windows-ok) or `unix:/path` = external `py/embed_server.py`.

## Layout

```
internal/core/     domain types (Scope, Artifact, Tier, Kind, Detail, Query, Hit, Seed, Trace)
internal/embed/    Embedder iface + MockEmbedder + HTTPEmbedder
internal/storage/  CAS (sha256+zstd), EmbeddingStore (float32), Meta (bbolt)
internal/search/   brute-force cosine (leaf; no core import)
internal/engine/   the public API (Open/Push/Query/Drill/Publish/Fork/Consolidate/CrossVersion/...)
internal/eval/     scenario runner + metrics; eval/scenarios/hoe.json
cmd/ioc/           CLI (run-scenario, embed-ping, memory commands)
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
