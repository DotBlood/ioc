# devlog — IOC Development Log

## Architecture & purpose

IOC is a stateful knowledge graph runtime — a hybrid cognitive storage system combining:

- **Static Knowledge Graph** (disk) — files, chat logs, hard facts
- **Stateful RAM** (memory) — active graph, summaries, embeddings
- **Temporal Versioning** (anchor+delta) — snapshot history, time-travel

Goal: context isolation for AI agents via formal scope hierarchy (Worktree → Workspace → Session → Artifact), with no LLM dependency in the core engine.

---

## Phase 0: Foundation

### Scope 0.1 — Project scaffold
- `go.mod` with `github.com/DotBlood/ioc`, Go 1.26.3
- Single dependency: `github.com/oklog/ulid/v2`
- `Makefile` (build, test, lint, vet, fmt, tidy, clean, all)
- `.github/workflows/ci.yml` (lint → test → build on push/PR to main)
- Package skeleton: `cmd/iocctl/`, `internal/{model,graph,knowledge,store,retrieval,embedding,pipeline,runtime}`, `pkg/api/`, `py/`
- `AGENTS.md` created
- `iocctl` binary compiles, prints "IOC v0.1.0 — Stateful Knowledge Graph Runtime"

### Scope 0.2 — Core data types
All types in `internal/model/`:
- `id.go`: ID (ULID), ContentHash (SHA-256), ProjectionKey, RevisionNumber, RevisionRef, RevisionFilter, BranchName, ScopeID
- `artifact.go`: Artifact (immutable — only content + lineage), NodeType (4 types)
- `projection.go`: ArtifactProjection (versioned metadata), EmbeddingRefID, temporal validity
- `edge.go`: 8 edge types (Lineage, Ownership, Retrieval, Reference, Citation, DerivedFrom, RelatedTo, Temporal), EdgeDirection, CyclePolicy, ReferenceState, EdgeConstraints
- `readiness.go`: Readiness bitflags (Stored, Embedded, Indexed, Archived)
- `lifecycle.go`: LifecycleState (5 states: draft→active→archived→detached→deleted), valid transitions
- `retrieval.go`: Score, TraversalOpts, DanglingConfig, RetrievalTrace, DeterminismBoundary, RetrievalOpts, sentinel errors
- 18 tests

### Phase 0 extension — Architecture restructure
Added Knowledge Runtime Layer as formal intermediary between pipelines and graph engine:

```
Before:                     After:
CLI → Pipelines → Graph     CLI → Pipelines → Runtime
                   ↑                 ↓ Knowledge Runtime (NEW) — cognition
              Retrieval              ↓ Graph Engine (PURE) — physical only
                                     ↓ Storage
```

New `internal/knowledge/`: ScopeResolver, RevisionManager, LifecycleManager, LineageTracker, ContextAssembler, PolicyEnforcer
`internal/graph/` stripped to pure physical — no cognition semantics.

### Docs created
PDR, FRD, FSD, PAD, CODE-STYLE, METHODOLOGY, ROADMAP — 7 documents.

---

## Phase 1: Physical Graph + Knowledge Layer

### Scope 1.1: Physical StatefulGraph
- `graph/graph.go`: `Node(ctx, id)`, `AddNode`, `RemoveNode`, `Edge`, `AddEdge`, `BFS`/`DFS` (bidirectional), `NodesByType`, `Snapshot` (placeholder)
- `graph/interfaces.go`: `StoreReader` + `StoreWriter` cross-layer contracts
- 10 tests

### Scope 1.2: Knowledge Runtime Layer
All 6 components implemented:

| Component | Key Methods |
|-----------|-------------|
| ScopeResolver | `ResolveScope`, `IsVisibleFrom` (context isolation) |
| RevisionManager | `ResolveRevision` (4 filters), `IsActive`, `IsSuperseded` |
| LifecycleManager | `Transition`, `TransitionIfValid`, `EnforceInvariants` |
| LineageTracker | `RecordLineage` (cycle detection), `RecordOwnership`, `Provenance` |
| ContextAssembler | `Plan` (scope-aware), `Assemble` (token-budgeted) |
| PolicyEnforcer | `ShouldArchive`, `DefaultRetrievalScope` |

18 tests.

### Bug fixes
- **Cycle detection**: bidirectional traverse (EdgesOut + EdgesIn) — detects 2-node cycles
- **Error comparison**: `errors.Is` instead of `== sentinel` for wrapped errors

---

## Phase 2: Storage Layer

### Scope 2.1: DiskStore (bbolt)
- `store/disk.go`: 8 buckets (nodes, projections, edges, adj_out, adj_in, anchors, rev_dag, idx_type, meta)
- Full CRUD for nodes, projections, edges with adjacency lists
- `SaveSnapshot/LoadSnapshot` — full graph roundtrip
- Serialization via encoding/gob
- 7 tests

### Scope 2.4: EmbeddingStore
- `store/embedding.go`: File-backed fixed-size embedding vectors
- `Put(vec) → EmbeddingRefID`, `Get(ref) → vec`, `Len()`
- Append-only, fixed-size records (dims×4 bytes), in-memory buffer + file sync
- Per-model files (different dims), mmap planned for future
- 7 tests

### Scope 2.2: CAS File Store
- `store/cas.go`: Content-addressable file storage
- SHA-256, zstd compression, automatic deduplication
- Two modes: full blob (<1MB) and FastCDC chunked (>1MB, `github.com/tigerwill90/fastcdc` MIT)
- Path layout: `obj/fi/le/<hash>` for blobs, `chk/xx/yy/<chunk_hash>` for chunks
- JSON manifest for chunked files
- 7 tests

### Scope 2.3: Artifact + Projection Store
- `store/artifact.go`: `ArtifactStore` — high-level wrapper over `DiskStore`
- `SaveArtifact/LoadArtifact/DeleteArtifact`, `ListArtifactsByType`
- `SaveProjection/LoadProjection/ListProjections/LatestProjection`
- `SaveArtifactWithProjection` — atomic batch
- `SaveRevisionDAG/LoadRevisionDAG` — DAG persistence
- 8 tests

---

## Scope 1.3: Property Indexes + Bloom Filter

### Deliverable
- `RuntimeConfig` + `RuntimeIndexes` — materialized runtime structures, не персистятся
- `installSnapshot()` — загрузка canonical maps без двойной индексации
- `RebuildRuntimeState()` — один проход: bloom + propertyIdx
- `ProbablyHas(id)` — optimization hint, не source of truth
- `PropertyIndexFind(prop, value)` — lookup через `map[ID]struct{}`, O(1) delete, dedup
- Bloom filter: FNV-1a 128-bit split (double hashing), zero-allocation `forEachKey`, FPR ~1.5%

### Key architecture decisions
- Bloom filter disposable — не персистится, rebuild после `installSnapshot()`
- `ProbablyHas` вместо `HasActive` — избегаем overloading "active" с lifecycle
- Property index `map[ID]struct{}` — O(1) delete, no duplicates, easy merge
- 20 new tests (10 bloom + 8 property + 2 integration)

---

## Project state

```
Git log:
  059fc77 fix .gitignore
  8f0a101 init
  (working tree: Phases 0-2 + Scopes 1.3 done, no code committed yet)

Packages:       5 (model + graph + knowledge + store + cmd)
Total tests:    77 (18 model + 30 graph + 18 knowledge + 29 store + 0 cmd)
Suites:         4, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid
Placeholders:   graph.Snapshot(), policy.PrunableRevisions() → ErrNotImplemented
```

## Phase 3: Temporal + Archive (Scope 3.1: Structural Snapshots)

### Scope 3.1: Structural Snapshots [✔]
- `model/anchor.go` — `Anchor` (full+diff hybrid), `AnchorKind`, `AnchorID`
- `model/temporal.go` — `EdgeValidAt`, `ProjectionValidAt`, `ErrNoHistoricalState`, `HistoricalScope`
- `graph/freeze.go` — `scopeLock` (RWMutex + atomic.Bool), `WriteHandle`, `FreezeScope`/`UnfreezeScope`/`BeginWrite`/`EndWrite`, race-free via `scopeLocksMu`
- `store/anchor_store.go` — `AnchorStore` (Create/Load/List/ListRange/Delete)
- `store/disk.go` — `LoadEdgesByIDs` (sorted cursor), `DeleteProjection`, `DeleteEdgeRevision`
- `knowledge/snapshot.go` — `AnchorCreator` (full+diff logic, diff computation)
- 13 new tests (5 freeze + 5 anchor store + 3 creator)

**Bug fixes:** scopeLocks race (double-check locking), AnchorCreator not persisting to store.

### Scope 3.2: Archive Pipeline [✔]
- `knowledge/archive.go` — `ArchivePipeline`, `ArchiveStage` (monotonic FSM), `ArchiveResult`
  - `Archive()` — freeze → anchor → summary → lifecycle transition
  - `Restore()` — freeze → resolve anchor → install → rebuild → transition
  - `buildSummary()` — deterministic structured summary (no LLM)
  - Orphan anchor policy: allowed, retention may prune in future
- `graph/interfaces.go` — `ScopeFreezer`, `SnapshotInstaller` interfaces
- `graph/graph.go` — exported `FreezeScope` (+ `ErrScopeFrozen`), `UnfreezeScope`, `InstallSnapshot`
- `graph/freeze.go` — double-freeze guard check
- `model/artifact.go` — `NodeTypeSummary = 5`
- `model/edge.go` — `TargetKind`, `EdgeTarget`
- `model/retrieval.go` — `ErrScopeFrozen`, `ErrScopeExists`
- 9 new tests: full cycle, freeze failure, anchor failure, transition failure, defer, restore, summary, resolve full, kind string

**Bug fixes:** mockLifecycle didn't enforce state transitions

```
Packages:       5 (model + graph + knowledge + store + cmd)
Total tests:    99 (18 model + 35 graph + 30 knowledge + 34 store + 0 cmd)
Suites:         4, all passing
Build + vet:    clean
```
