# IOC — Stateful Knowledge Graph Runtime

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

IOC is a hybrid cognitive storage system that combines a **static knowledge graph** (disk-persisted facts, files, chat logs) with **stateful runtime state** (active graph, embeddings, summaries) and **temporal versioning** (anchor/delta snapshots with time-travel queries).

Designed for AI agents requiring deterministic retrieval, context isolation via formal scope hierarchy (Worktree → Workspace → Session → Artifact), and structural archiving — with **no LLM dependency** in the core engine.

---

## Quick start

```bash
# 1. Initialize IOC repository
iocctl init

# 2. Create scopes (auto-nests: worktree → workspace → session)
iocctl scope create worktree
iocctl scope create workspace
iocctl scope create session

# 3. Add an artifact
iocctl artifact add <session-id> --text "IOC is a knowledge graph runtime"

# 4. Search
iocctl retrieval query "knowledge graph" --topk 5

# 5. Archive the session
iocctl scope archive <session-id>

# 6. List archive snapshots
iocctl archive list

# 7. Restore from an archive
iocctl scope restore <anchor-id>
```

---

## Architecture

```
CLI / API
    ↓
Pipelines / Runtime          — orchestration, transient state
    ↓
Knowledge Runtime            — cognition semantics (scope, revision, lineage)
    ↓
Graph Engine (PURE)          — physical adjacency, CRUD, indexes
    ↓
Storage                      — bbolt (KV), CAS (SHA-256 zstd), Embedding (file)
```

| Layer | Package | Responsibility |
|-------|---------|----------------|
| CLI | `cmd/iocctl` | Cobra commands, input parsing |
| API | `pkg/api` | Embeddable runtime for programmatic use |
| Pipelines | `internal/pipeline` | Ingestion + Archive orchestration |
| Session | `internal/session` | Ephemeral runtime state (not persisted) |
| Knowledge | `internal/knowledge` | Scope, revision, lifecycle, lineage, archive, time-travel |
| Graph | `internal/graph` | Pure in-memory graph (nodes, edges, BFS/DFS) |
| Storage | `internal/store` | bbolt persistence, CAS file store, embedding store |
| Embedding | `internal/embedding` | Embedder interface, mock, HTTP, weighted averaging |
| Retrieval | `internal/retrieval` | Vector (BruteForce), text (BM25), RRF fusion, trace |
| Model | `internal/model` | Core types: ID, Artifact, Edge, Lifecycle, Anchor |

---

## CLI reference

| Command | Description |
|---------|-------------|
| `iocctl init` | Initialize IOC data directory (`~/.ioc/`) |
| `iocctl scope create <type>` | Create worktree / workspace / session |
| `iocctl scope list` | List all scopes with state and type |
| `iocctl scope archive <id>` | Archive a scope (snapshot + lifecycle transition) |
| `iocctl scope restore <id>` | Restore a scope from archive anchor |
| `iocctl artifact add <scope> [--text\|--file]` | Add artifact (stdin / `--text` / `--file`) |
| `iocctl artifact get <id>` | Show artifact content and metadata |
| `iocctl artifact revisions <id>` | List projection revisions |
| `iocctl retrieval query <text> [--topk]` | Hybrid search (vector + BM25 + fusion) |
| `iocctl retrieval trace <text> [--topk]` | Search with pipeline trace |
| `iocctl archive list [--scope]` | List archive snapshots |
| `iocctl archive show <id>` | Show anchor details |
| `iocctl retention run` | Run retention sweep |
| `--json` | JSON output for all commands |

---

## Library usage

```go
import "github.com/DotBlood/ioc/pkg/api"

ctx := context.Background()
rt, err := api.Open(ctx, api.Config{RootDir: "/path/to/.ioc"})
if err != nil {
    log.Fatal(err)
}
defer rt.Close()

// Create a scope
scope, err := rt.CreateScope(ctx, api.CreateScopeRequest{
    Type: "worktree",
})
if err != nil {
    log.Fatal(err)
}

// Add an artifact
id, err := rt.AddArtifact(ctx, scope.ScopeID, []byte("hello world"), "my artifact")
if err != nil {
    log.Fatal(err)
}

// Query
results, err := rt.Query(ctx, "hello", 10)
if err != nil {
    log.Fatal(err)
}

// Trace
trace, err := rt.Trace(ctx, "hello", 10)
if err != nil {
    log.Fatal(err)
}
```

---

## Development

```bash
make all         # tidy → fmt → vet → test → build
make test        # go test -count=1 ./...
make lint        # golangci-lint run ./...
go build ./cmd/iocctl
```

Run single package tests:
```bash
go test ./internal/graph/
go test ./internal/retrieval/
go test ./pkg/api/
```

Benchmarks (GOMAXPROCS=1 baseline):
```bash
$env:GOMAXPROCS='1'; go test -bench=. -benchmem -count=5 ./internal/graph/ ./internal/retrieval/ ./internal/store/ ./internal/embedding/
```

---

## Known limitations (v0.1)

- **`Graph.Snapshot()`** — `ErrNotImplemented` (full snapshot persistence deferred to v0.2)
- **`policy.PrunableRevisions()`** — `ErrNotImplemented` (retention policy not configurable per-scope)
- **Vector search** — `BruteForceIndex` (O(N×dim) linear scan, no ANN index)
- **BM25** — full rebuild on every `Index()` call, not incremental
- **Embedding** — `MockEmbedder` in-process only; `HTTPEmbedder` requires external Python server
- **Concurrency** — reads are safe; mutations (CreateScope, AddArtifact, ArchiveScope, RestoreScope) are NOT concurrent-safe
- **No metadata scoring** — retrieval ranks by vector similarity + BM25 only
- **No scope state reverse index** — listing scopes by state requires full scan
- **CLI tests** — manual verification only
- **Cross-worktree operations** — defined in spec, not implemented

---



---

## License

CLOSE — see [LICENSE](LICENSE).
