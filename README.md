# IOC

[![License: AGPL v3](https://img.shields.io/badge/License-AGPLv3-blue.svg)](LICENSE)

IOC is a **local-first, model-agnostic memory & knowledge layer for working with LLMs** — a single
model or many agents. It unifies two retrieval axes on one store: **semantic memory** (mini-summary
+ embedding, progressive disclosure, a maintained current-truth view) and a **queryable knowledge
base** (author-declared typed edges between artifacts — a knowledge graph IOC never has to extract
with an LLM). See [`VISION.md`](VISION.md) for the direction and [`docs/`](docs/) (PDR, ROADMAP) for
the formal v0.2 specifications.

> **Status: v0.2 slice — the wall holds.** The load-bearing claim ("the wall": mini-summary +
> embedding are good enough that an agent rarely drills to raw content) has been validated on the
> real embedder (~0.96 overview-sufficiency, currency robust, no false confidence — see
> [`docs/WALL_EXPERIMENT.md`](docs/WALL_EXPERIMENT.md)). On top of the proven semantic memory sit the
> author-declared knowledge-edge axis and the v0.3 retrieval research: **collapsed-tree retrieval is
> now the default**, graph-aware boost (v2, seed-anchored) and an opt-in author-declared importance
> signal are in, and the confidence/abstention layer is margin-aware with a per-embedder calibrator
> (`ioc calibrate`). Still intentionally not feature-complete. The previous v0.1 engine lives in git
> history (branch `development/v0.2-review`).

## Layout

```
internal/
  core/      pure domain types (Scope, Artifact, Tier, Kind, Detail, Query, Hit, Seed, Edge, Trace)
  embed/     Embedder interface + MockEmbedder (offline) + HTTPEmbedder (py/embed_server.py)
  storage/   CAS (sha256+zstd), EmbeddingStore (float32), Meta (bbolt: scopes/artifacts/edges/traces)
  search/    brute-force cosine + BM25 + RRF
  engine/    public API: Open / Push / Query / Drill / Publish / SiblingOverview / Ancestors /
             Neighbors / Supersede / Relate / Related / Fork / Consolidate / CrossVersion / Trace
  eval/      scripted scenario runner + metrics; eval/scenarios/hoe.json
cmd/
  ioc/       CLI: run-scenario, embed-ping, and persistent memory commands
  ioc-mcp/   stdio MCP server (agent-native surface)
py/
  embed_server.py   optional external embedding service (FastAPI); venv in py/.venv
```

## Build & test

```bash
make build          # -> bin/ioc, bin/ioc-mcp
make test           # go test ./...
go run ./cmd/ioc run-scenario internal/eval/scenarios/hoe.json   # scenario on the offline mock embedder
```

## Real embedder (real wall numbers)

The `-embed` endpoint is a TCP URL (`http://host:port`, works on Windows) or a Unix socket
(`unix:/path`). Set up the service once via a virtualenv:

```bash
python -m venv py/.venv
py/.venv/Scripts/python -m pip install -r py/requirements.txt   # Windows; */bin/* on Unix
#   downloads BAAI/bge-small-en-v1.5 (~130MB) on first run

# start it (TCP — works anywhere incl. Windows):
IOC_EMBED_HOST=127.0.0.1 IOC_EMBED_PORT=8088 py/.venv/Scripts/python py/embed_server.py

go run ./cmd/ioc embed-ping   -embed http://127.0.0.1:8088
go run ./cmd/ioc run-scenario internal/eval/scenarios/hoe.json -embed http://127.0.0.1:8088
```

## Memory commands (drive IOC from the shell)

Operate IOC as a persistent store (an agent can call these via the shell). All take `-dir`
(persistent; default `.ioc/data`) and `-embed`. **One `-dir` must always use the SAME embedder** —
mock and real embeddings are different vector spaces.

```bash
ioc create-scope -role worktree -title proj                  # -> {"id": ...}
ioc create-scope -parent <ID> -role session -title t
ioc push  -scope <ID> -summary "..." [-content "..."|-content-file f] [-publish] \
          [-supersedes id1,id2] [-relations depends_on:ID,answers:ID]   # declare currency + edges at write
ioc query -scope <ID> -text "..." [-detail overview|entry|raw] [-topk 5] \
          [-mode collapsed|vector|hybrid|hierarchical] [-rerank] [-graph-boost W] [-importance-weight W]  # collapsed is the default
ioc drill -artifact <ID> -detail raw
ioc neighbors -scope <ID> -text "..."           # most similar CURRENT memory (run before push to find what to supersede)
ioc supersede -old <ID> -by <ID>                # mark an artifact replaced (currency)
ioc relate    -from <ID> -to <ID> -kind depends_on        # author-declared knowledge edge
ioc related   -artifact <ID> -kind depends_on -direction in   # walk edges: e.g. "what depends on X?"
ioc fork  -scope <ID> -title t
ioc consolidate  -scope <ID> -summary "..."
ioc crossversion -scope <ID> -constraints "..." -lessons "..."
ioc siblings -scope <ID>      # published sibling artifacts
ioc ancestors -scope <ID>
ioc publish  -artifact <ID>
ioc trace    -query <ID>
ioc calibrate -probe spec.json [-coverage 0.9] [-write]   # derive the per-embedder confidence floor (split-conformal)
```

## MCP server (agent-native surface)

`cmd/ioc-mcp` is a stdio MCP server exposing the engine as tools (`ioc_create_scope`, `ioc_push`,
`ioc_query`, `ioc_drill`, `ioc_publish`, `ioc_neighbors`, `ioc_supersede`, `ioc_relate`,
`ioc_related`, `ioc_siblings`, `ioc_ancestors`, `ioc_fork`, `ioc_consolidate`, `ioc_crossversion`,
`ioc_trace`). Config via env `IOC_DIR`, `IOC_EMBED`.

```bash
make build      # -> bin/ioc-mcp
```

Register in Claude Code (`.mcp.json`), then reconnect so the tools appear:

```json
{
  "mcpServers": {
    "ioc": {
      "command": "F:\\projects\\IOC\\bin\\ioc-mcp.exe",
      "env": { "IOC_DIR": "F:\\projects\\IOC\\.ioc\\mcp-data", "IOC_EMBED": "http://127.0.0.1:8088" }
    }
  }
}
```

## Runtime (daemon)

A long-lived daemon owns a store and serves many clients over a local framed-JSON protocol, so the
CLI, the MCP server, and sub-agents **share one memory** instead of fighting bbolt's exclusive lock.

```bash
ioc serve -dir .ioc/data -embed http://127.0.0.1:8088   # run the daemon (single owner of -dir)
ioc runtime status -dir .ioc/data                        # running/stale, pid, conns, uptime
ioc runtime stop   -dir .ioc/data                        # graceful shutdown
```

Every other command (`push`, `query`, ingest, the MCP server, …) **auto-routes** to a daemon that
owns the same `-dir` (discovered via `<dir>/runtime.json`) and falls back to opening the store
embedded when none is running. Concurrency lives inside the daemon: reads run in parallel, writes are
serialized (`RWMutex`). Sharing = point clients at the same `-dir`. See [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Wall metrics (success targets)

- **(a) overview-sufficiency** ≥ 0.80 — recall turns satisfied at `DetailOverview` with no drill-to-raw.
- **(b) context ratio** ≤ 0.25 — tokens consumed via IOC vs the raw an agent would carry without it.
- **(c) constraint survival** = pass — a distilled lesson surfaces after a version boundary.
- **(d) recall@topK** ≥ 1.0 — expected items appear within top-K.

With `MockEmbedder` these numbers validate the **pipeline** only (it is a lexical proxy). Real wall
numbers require the real embedder via `-embed`.

## License

IOC is licensed under the **GNU Affero General Public License v3.0** (AGPL-3.0) — see [LICENSE](LICENSE).
