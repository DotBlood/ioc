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

### Scope 3.3: Retention & Cleanup [✔]
- `knowledge/retention.go` — `RetentionPolicy`, `Run`, `RunScheduler` (overlap protection via atomic.Bool + defer)
  - `PruneProjections` — mark-and-sweep with protected revision sets
  - `PruneEdgeRevisions` — conservative protection (all revisions of anchor-referenced edges)
  - TTL check by `ValidTo` (not `ValidFrom`)
  - Invariant: latest revision never pruned (`MaxVersions < 1` → `keepAtLeast=1`)
  - `protectedRevisions` — O(all anchors × all refs), TODO(v0.2) reverse index
- `model/edge.go` — `EdgeRevisionKey{EdgeID, Revision}` struct
- `model/anchor.go` — TODO(v0.2) for EdgeRefs → EdgeRevisionKey
- `store/disk.go` — `ListAllArtifactIDs`, `ListEdgeRevisions`, `DeleteEdgeRevision(EdgeRevisionKey)`, `LoadEdgeRevision`
- `store/anchor_store.go` — `ListAll()` (no sentinel scopeID)
- 8 new tests

**Bug fixed:** Go slice aliasing in retention test: mock `ListProjections` returned original slice, causing underlying array mutation during iteration. Fixed by returning a copy.

### Scope 3.4: Time-Travel Queries [✔]
- `knowledge/time_machine.go` — `TimeMachine`, `ScopeStateAt`, `ProjectionAt`, `EdgesAt`
  - `HistoricalState` — internal replay scratch structure (NOT in model/)
  - `anchorToHistoricalState` — full anchor → initial state
  - `applyDiff` — diff anchor patch (add+remove semantics, `map[ArtifactID]ProjectionKey` for replacement)
  - `filterEdgesAt` — temporal validity filter using `EdgeValidAt`
  - Reconstruction starts from `LatestFullAnchorBefore` (never from diff)
  - Diff replay by `Revision` order (deterministic), temporal selection by `CreatedAt`
  - MVCC semantics: latest valid projection at T
  - Missing artifacts collected explicitly (`MissingIDs`), not silently skipped
  - No dependency on current graph state — only anchors + store
- `store/anchor_store.go` — `LatestFullAnchorBefore(ctx, scopeID, at)`, `LatestAnchorsAfter(ctx, scopeID, after, to)`
- `store/disk.go` — `LoadArtifactsByIDs(ctx, ids) ([]*Artifact, []ID, error)` — batch, one bbolt View()
- `model/temporal.go` — `MissingIDs []ID`, `Errors []string` added to `HistoricalScope`
- 7 tests: exact anchor, with diffs, no anchor, MVCC projection, edges by node+type, missing collection, determinism

## Phase 4: Embedding + Retrieval

### Scope 4.1: Embedder Interface [✔]
- `embedding/embedder.go` — `Embedder` interface, `Batch`, `Vector`, sentinel errors
- `embedding/mock.go` — `MockEmbedder` (deterministic, normalized, splitmix64 PRNG)
- 10 tests

### Scope 4.2: External Embedder Service (HTTP + Unix Socket) [✔]
- `embedding/http.go` — `HTTPEmbedder` (implements `Embedder` interface)
  - HTTP over Unix socket transport (not gRPC, not TCP localhost)
  - Dimension runtime-discovery via `atomic.Int32` — 0 until first success
  - `ErrDimensionMismatch` — если сервер возвращает другую dimension после первой
  - `ErrVectorCountMismatch` — если `len(vectors) ≠ len(texts)`
  - `Model()` returns "" until first successful Embed call
  - `http.Client{Timeout: 30 * time.Second}`
  - `TODO(v0.3)`: binary float32 transport + versioned protocol
- `embedding/embedder.go` — + `ErrDimensionMismatch`, `ErrVectorCountMismatch`
- `embedding/http_test.go` — `//go:build integration` (6 tests: dimension discovery, empty input, vector count, dimension mismatch, model empty, server error)
- `py/embed_server.py` — FastAPI service, single-model (`bge-small-en-v1.5`), `GET /health`, `POST /embed`
- `py/requirements.txt` — sentence-transformers, fastapi, uvicorn, numpy
- `embedding/embedder.go` — `Embedder` interface, `Batch`, `Vector`, sentinel errors
  - `Embed(ctx, texts) (*Batch, error)` — core method
  - `Dims() int` — invariant: MUST remain constant for lifetime
  - `Model() string` — model identifier
  - `ErrInferenceFailed`, `ErrInvalidInput`, `InferenceError`
  - `Vector` contains `Data []float32` + `TokenCount int` (no Text field)
  - `Batch.CreatedAt` — for cache invalidation semantics
- `embedding/mock.go` — `MockEmbedder` (deterministic, normalized, dimension-fixed)
  - splitmix64 PRNG (локальный, без глобального math/rand, без lock contention)
  - FNV-1a based hash → seed → deterministic vector generation
  - Normalization to unit length
  - Empty string → valid embedding (не error)
  - Default dimension: 384, model: "mock-v1"
- 10 tests: single text, multiple texts, determinism, normalization, empty input, empty string valid, different texts→different vectors, model name, dims constant, token count

### Scope 4.3: Hierarchical Embedding Averaging [✔]
- `embedding/average.go` — `WeightedAverage` (length-weighted, normalized, NaN/Inf check, pre-allocation dim check)
- `embedding/hierarchy.go` — `AggregationLevel`, `AggregatedEmbedding`, `ScopeHierarchy`, `Hierarchy`, `ComputeAggregate`
- 11 new tests. New sentinel: `ErrCorruptedEmbedding`

### Scope 4.4: Retrieval Engine [✔]
- `retrieval/types.go` — `RetrievalIndex` interface (pluggable), `IndexEntry` (single-mode, no VectorRef),
  `SearchResult` (no Vector), `SearchOptions` (TopK>0, MinScore)
- `retrieval/brute_force.go` — `BruteForceIndex` (map-based, dot product with dim check,
  min-heap top-K, tie-break ordering, Count under RLock, Delete idempotent)
- `retrieval/engine.go` — `Engine` (embedder + index only, empty query check,
  TODO(v0.2): hierarchy retrieval, metadata scoring, reranking)
- 9 tests: search, upsert, delete, dimension mismatch, count under lock,
  query, empty index, empty query, search ordering (tie-break)

```
Packages:       7 (model + graph + knowledge + store + embedding + retrieval + cmd)
Total tests:    144 (18 model + 35 graph + 45 knowledge + 34 store + 21 embedding + 9 retrieval + 0 cmd)
Suites:         6, all passing
Build + vet:    clean
```
- `embedding/average.go` — `WeightedAverage(vectors, weights)` pure function
  - Length-weighted centroid: Σ(w_i × v_i) / Σ(w_i), normalized to unit length
  - float64 accumulation for numerical stability, float32 output for storage
  - Input validation: empty vectors, dimension mismatch, negative weights
  - Deterministic: same inputs → identical result
- `embedding/hierarchy.go` — `AggregationLevel` (Chunk/Artifact/Session), `AggregatedEmbedding` (runtime type, no GeneratedAt, no Revision)
  - `ScopeHierarchy` interface — Children + ArtifactsInScope (stable lexical ordering enforced via sort)
  - `Hierarchy` struct — depends on `EmbeddingReader` + `ProjectionLoader` + `ScopeHierarchy` (NOT Embedder)
  - `ComputeAggregate(ctx, scopeID)` — computes session-level centroids, no recursive traversal
  - Invariants documented: aggregation acyclic, aggregates recomputable, cache advisory
- 11 new tests (12 WeightedAverage + 3 Hierarchy): added NaN/Inf validation, pre-allocation dimension check
- New sentinel error: `ErrCorruptedEmbedding`
- Embedding package now has 18 unit tests total

```
Packages:       6 (model + graph + knowledge + store + embedding + cmd)
Total tests:    135 (18 model + 35 graph + 45 knowledge + 34 store + 21 embedding + 0 cmd)
Suites:         5, all passing
Build + vet:    clean
```
