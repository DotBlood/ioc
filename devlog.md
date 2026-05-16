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
- `retrieval/types.go` — `RetrievalIndex` interface, `IndexEntry`, `SearchResult`, `SearchOptions`
- `retrieval/brute_force.go` — `BruteForceIndex` (map-based, dot product, min-heap, dim checks, tie-break)
- `retrieval/engine.go` — `Engine` (embedder + index, empty query check)
- 9 tests

### Scope 4.7: Dangling + Orphan Handling [✔]
- `knowledge/dangling.go` — `DanglingChecker` (cached, hybrid traversal-time + lazy cache)
  - `ExistsReference(ctx, target) → (model.ReferenceState, error)` — uses existing `model.ReferenceState`
  - `DanglingGraphReader.Exists(ctx, id) (bool, error)` — lightweight check, no full node load
  - Lazy cache with `InvalidateCache(ids)` / `InvalidateCache(nil)`
  - Returns: `RefActive`, `RefDangling`, `RefUnresolvedRemote`
  - Invariant: eventually consistent. Cache not source of truth.
- `knowledge/dangling.go` — `OrphanScanner` (structural traversal, no string prefix)
  - `StructuralResolver` interface: `Parent`, `Exists`, `Children`
  - `ScanOrphans(ctx, scopeID) → ([]model.ID, error)` — flat scope scan
  - Orphan = missing structural parent. NOT lifecycle-detached/archived
  - Invariant: validates structural containment only. Not lifecycle/permissions/retrieval
- 7 new tests (5 DanglingChecker + 2 OrphanScanner)

**End of Phase 4 — all 7 scopes completed.**

```
Packages:       7 (model + graph + knowledge + store + embedding + retrieval + cmd)
Total tests:    167 (18 model + 35 graph + 52 knowledge + 34 store + 21 embedding + 22 retrieval + 0 cmd)
Suites:         6, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid, JLugagne/bm25 (MIT)
```
- `retrieval/engine.go` — refactored: `runVectorStage`, `runTextStage`, `runFusionStage`, `runPipeline`, `fallbackMerge`
  - `Query()` and `Trace()` share the same pipeline → identical SearchResult ordering
  - `Query()` returns errors if all sources fail (`errors.Join`)
- `retrieval/trace.go` — `Engine.Trace()` method
  - 3 deterministic stages: vector_search, text_search, fusion
  - Stage timing + error collection
  - `newContentHash(query)` — stable content hash for trace
  - `FinalSelection` preserves final fused ranking order
- `retrieval/trace_json.go` — `TraceAsJSON(t *RetrievalTrace) (string, error)` — indented JSON
- 6 new tests: stages present, errors collected, embedder failure, duration, query hash, JSON validity

```
Packages:       7 (model + graph + knowledge + store + embedding + retrieval + cmd)
Total tests:    160 (18 model + 35 graph + 45 knowledge + 34 store + 21 embedding + 22 retrieval + 0 cmd)
Suites:         6, all passing
Build + vet:    clean
```
- `retrieval/types.go` — `TextIndex` interface, `TextDocument`, `TextSearchOptions`, `TextResult`, `Fusion` interface, `QueryOptions`
- `retrieval/bm25.go` — `BM25Index` (wraps `github.com/JLugagne/bm25`, pure Go, SIMD)
  - Full rebuild on each Index() call (v0.1), immutable between calls
  - Top-K via min-heap + deterministic tie-break by ID
  - Dependencies: JLugagne/bm25 (MIT, pure Go, 93× faster than Python)
- `retrieval/fusion.go` — `RRF` (Reciprocal Rank Fusion, k=60), deterministic with lexical ID tie-break
  - `buildRankMap`, `buildTextRankMap`, `collectAllIDs`
- `retrieval/engine.go` — Enhanced Engine (vector + text + fusion coordinator)
  - Always calls fusion when configured (stable ranking semantics)
  - Graceful fallback when one source is empty
- 7 new tests (BM25 search, empty, reindex; RRF determinism, tie-break; Engine hybrid, fallback)
- No Python, no gRPC, no HTTP service

```
Packages:       7 (model + graph + knowledge + store + embedding + retrieval + cmd)
Total tests:    154 (18 model + 35 graph + 45 knowledge + 34 store + 21 embedding + 16 retrieval + 0 cmd)
Suites:         6, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid, JLugagne/bm25 (MIT)
```
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

---

## Phase 5: Runtime + CLI

### Scope 5.1: Session State [✔]

**Package:** `internal/session/`

**Deliverable:**
- `session/doc.go` — package doc with explicit deterministic boundary:
  > Session data MUST NOT affect graph correctness, revision semantics, retrieval determinism, or archival behavior.
- `session/cache.go` — `SessionCache` with TTL-based expiration, background sweep goroutine
  - `closed atomic.Bool` for idempotent Close + no-op after close
  - `sync.WaitGroup` for deterministic goroutine shutdown
  - `CachePut`/`CacheGet` with `sync.RWMutex` (no deletion under RLock — lazy-expire only)
  - `Close()` — `CompareAndSwap(false, true)` → `close(stop)` → `wg.Wait()`
  - `sweep()` via `maps.DeleteFunc`
  - Cache values MUST be immutable or externally synchronized
- `session/state.go` — `SessionState` struct
  - `mu sync.RWMutex` — future-proof for concurrent access
  - `ActivePrompt`, `PromptHistory` (capped by `defaultMaxHistory=100`, oldest dropped first)
  - `LastResults []retrieval.SearchResult`, `RetrievalTrace *model.RetrievalTrace`
  - `ReasoningStack` — LIFO, empty strings ignored
  - `SessionCache *SessionCache` — replaced on `Clear()` (swap before close, zero window)
  - `Clear()` — resets all fields, installs new cache, closes old one
  - `NewSessionState()`, `NewSessionStateWithCache(cache)` — DI hook for tests
- 5 tests: cache TTL, cache Close (idempotent, no-op after close), concurrent access, reasoning stack (LIFO, empty ignored), clear behavior

### Scope 5.2: Pipeline Orchestrators [✔]

**Package:** `internal/pipeline/`

**IngestionPipeline:**
- 6 capability interfaces (not concrete store types):
  - `CASWriter` — `Store`, `Open`
  - `ArtifactWriter` — `SaveArtifact`, `SaveProjection`, `LoadProjection`, `LatestProjection`, `ListArtifactsByType`, `LoadArtifact`
  - `EmbeddingWriter` — `SaveEmbedding(ctx, artifactID, Vector) → EmbeddingRefID`
  - `OwnershipWriter` — `AddOwnership(ctx, parent, child) error`
  - `ScopeResolver` — `ResolveScope(ctx, ScopeID) → ID`
  - `embedding.Embedder` + `retrieval.TextIndex`
- `Process(content, scopeID, summary) → ID` — full synchronous lifecycle:
  1. SHA-256 hash → ContentHash
  2. CAS.Store (dedup) → ContentHash
  3. SaveArtifact (immutable)
  4. ScopeResolver.ResolveScope → ScopeNodeID → AddOwnership(scopeNodeID, artifactID)
  5. SaveProjection(Revision=1, Readiness=Stored)
  6. embedder.Embed → batch (empty batch guard)
  7. SaveEmbedding → EmbeddingRefID
  8. Update projection: Readiness+=Embedded
  9. Load all indexable docs from store → BM25.Index (full rebuild, O(N), v0.1)
  10. Update projection: Readiness+=Indexed
- `Process` is progressively committing — earlier stages NOT rolled back on later failure
- `loadAllIndexableDocuments()` — fail-fast, skips empty content
- BM25 rebuild skipped when no indexable documents (empty corpus valid)
- `NewIngestionPipelineFromStore()` — factory wrapping concrete store types
- 5 tests: roundtrip (explicit Readiness: Stored && Embedded && Indexed), CAS dedup, embedding stored, BM25 indexed, empty content

**ArchivePipeline:**
- Thin orchestration wrapper over `knowledge.ArchivePipeline`
- `NewArchivePipeline(...)` — wires dependencies (ScopeFreezer, SnapshotInstaller, etc.)
- `Archive(ctx, scopeID) → *AnchorID` — pure delegation
- `Restore(ctx, anchorID) error` — pure delegation
- Exists as: stable operational boundary, composition root, future extension point
- 2 tests: archive empty scope, restore not found

### Scope 5.3a: Core CLI [✔]

**Package:** `cmd/iocctl/`

**Library:** `github.com/spf13/cobra`

**Pre-requisite: ScopeStateStore (`store/disk.go` additions)**
- New bbolt buckets: `scope_state` (ScopeID → gob ScopeState), `scope_children` (key-based: `parent:child → "1"`)
- Methods: `SaveScopeState`, `ScopeState`, `ListAllScopeStates`, `SetScopeState`, `SaveScopeChild`, `ScopeChildren`, `NodeType`, `GetMeta`, `SetMeta`
- Key-based scope_children: single put, natural dedup, cursor prefix iteration
- 3 new tests: CRUD, children (idempotent create, unrelated parent), SetScopeState transition

**Commands:**

| Command | Description |
|---------|-------------|
| `iocctl init [--dir]` | Create ~/.ioc/ + db + cas + emb, set meta "ioc_version" |
| `iocctl scope create <type>` | Create worktree/workspace/session with auto-nesting, parent type validation |
| `iocctl scope list` | Table output (or `--json`), sorted by ScopeID, children count |
| `iocctl artifact add <scope>` | stdin pipe / `--text` / `--file`, validates scope exists, pipeline ingest |
| `iocctl artifact get <id>` | Show artifact + latest projection + content |
| `iocctl artifact revisions <id>` | List projection revisions with readiness state |

**Architecture:**
- `appState` struct with lazy singletons (`Embedder`, `ArtifactStore`, `AnchorStore`)
- `ensureInitialized()` — checks db + emb + cas before any command
- `requiresStore()` — skips store for `init` command
- `PersistentPreRunE`/`PersistentPostRunE` — open/close per command
- `current_*` meta keys documented as CLI convenience pointers, NOT authoritative graph state
- Flat `ScopeID` — no string hierarchy, topology via ownership edges + scope_children bucket
- Ownership direction: parent → child (`Source: parentID, Target: childID`)
- Automatic nesting: worktree (root) → workspace (parent=current_worktree) → session (parent=current_workspace)
- JSON output via `--json` flag, stable machine-readable format
- 0 CLI tests (manual in v0.1)

**Key architectural decisions (retrospective):**
1. `scope_children` as key-index (`parent:child → "1"`) instead of gob `[]ScopeID` — avoids slice rewrite, race, O(N) mutation
2. Flat `ScopeID` (not hierarchical path) — topology lives in edges, not string prefixes
3. CLI without `LoadSnapshot()` — direct DiskStore ops, no full graph rebuild per command
4. stdin/--text/--file for artifact content — unix-friendly, no positional text args
5. Singleton embedder in `appState` — swap to HTTPEmbedder later without API change
6. Scope creation progressively committing — partial state valid on failure

---

### Scope 5.3b: Operations CLI [✔]

**Package:** `cmd/iocctl/`

**Files:** `cmd_retrieval.go`, `cmd_archive.go`, `cmd_retention.go` — 3 new, 3 modified

**New commands:**

| Command | Description |
|---------|-------------|
| `iocctl retrieval query <text> [--topk]` | Hybrid search (vector + BM25 + fusion), O(N) rebuild per invocation |
| `iocctl retrieval trace <text> [--topk]` | Search with full pipeline trace output |
| `iocctl scope archive <scope-id>` | Archive scope (freeze → snapshot → lifecycle transition) |
| `iocctl scope restore <anchor-id>` | Restore scope from anchor |
| `iocctl archive list [--scope <id>]` | List anchors (all or by exact ScopeID) |
| `iocctl archive show <anchor-id>` | Show detailed anchor info |
| `iocctl retention run` | Run retention sweep |

**Architecture:**
- `appState.RetrievalEngine(ctx)` — O(N) helper that loads all embeddings + documents from store, rebuilds BruteForceIndex + BM25Index + RRF, returns Engine.
  Documented invariant:
  > Retrieval indexes are process-local and rebuilt per CLI invocation in v0.1.
- `appState.BuildGraph(ctx)` — `LoadSnapshot()` → `NewStatefulGraph()` → `AddNode`/`AddEdge` → `RebuildRuntimeState()`.
- `appState.ArchivePipeline(ctx)` — wires `BuildGraph` + `AnchorCreator` + pipeline + adapters.
  Documented invariant:
  > Archive operations rebuild an in-memory graph snapshot per invocation in v0.1.
- CLI-level adapters (4 total, ~15 lines each): `archiveArtifactCreator`, `diskLifecycleAdapter`, `retentionArtifactAdapter`, `retentionEdgeAdapter`.
- `archive list --scope <id>` — exact ScopeID match only, no hierarchy/prefix resolution.
- `retrieval query/trace` — `--topk` (default 10), `--vector-topk` (default 50), `--text-topk` (default 50).

**Key decisions:**
1. O(N) index rebuild per retrieval query — acceptable for v0.1 small corpora
2. Full graph rebuild per archive command — archive operations are infrequent
3. Archive/restore go under `scope` subcommand (user-facing grouping)
4. `archive list` uses `--scope` flag (not positional) — extensible for `--state`, `--limit`, `--before` later
5. All adapters in CLI package — zero changes to store/knowledge layer
6. Retention adapters handle ctx mismatches between DiskStore and knowledge interfaces

---

### Scope 5.4: Retrieval API (library) [✔]

**Package:** `pkg/api/`

**Files:** 6 created, 2 modified

| File | Action | Lines |
|------|--------|-------|
| `pkg/api/types.go` | Create — DTOs | 46 |
| `pkg/api/errors.go` | Create — 5 stable sentinels | 12 |
| `pkg/api/runtime.go` | Create — Runtime, Open/Close, lazy embedder, retrievalEngine, archivePipeline, adapters | 151 |
| `pkg/api/retrieval.go` | Create — Query/Trace with input validation + stage name mapping | 94 |
| `pkg/api/admin.go` | Create — CreateScope/ListScopes/ArchiveScope/RestoreScope/AddArtifact | 183 |
| `pkg/api/api_test.go` | Create — 16 sub-tests | 194 |
| `internal/model/lifecycle.go` | Modify — add `CreatedAt time.Time` to `ScopeState` | +1 |
| `cmd/iocctl/cmd_scope.go` | Modify — set `CreatedAt: time.Now()` | +1 |

**Architecture:**
- `api.Runtime` is an explicit runtime object (no globals, no package-level state)
- `api.Open(ctx, Config{RootDir})` with `ensureInitialized` → returns `ErrNotInitialized` if repo missing
- All internal/model types translated to stable DTOs at the boundary
- Internal retrieval stage names mapped to public names:
  - `vector_search` → `"dense_search"`
  - `text_search` → `"sparse_search"`
  - `fusion` → `"rerank"`
- `api.ErrNotFound` ≠ `model.ErrNotFound` — fully decoupled

**Design decisions (from review + user feedback):**
1. Explicit parent semantics in API (no auto-nesting) — CLI keeps auto-nesting via meta keys
2. `CreateScopeRequest.ParentID` — required for workspace/session, empty for worktree
3. `Runtime` doc comment explicitly states v0.1 concurrency limits (reads OK, mutations not concurrent-safe)
4. `AddArtifact` is the minimal ingestion entrypoint — without it the API would only administrate empty scopes
5. Input validation at boundary (empty query → `ErrInvalidInput`, topK clamped to 1000)
6. Integration test does full roundtrip: CreateScope → AddArtifact → Query → Archive → Restore → Query

**Public API surface:**

```go
func Open(ctx context.Context, cfg Config) (*Runtime, error)
func (r *Runtime) Close() error
func (r *Runtime) Query(ctx context.Context, query string, topK int) ([]QueryResult, error)
func (r *Runtime) Trace(ctx context.Context, query string, topK int) (*TraceResult, error)
func (r *Runtime) CreateScope(ctx context.Context, req CreateScopeRequest) (*ScopeInfo, error)
func (r *Runtime) ListScopes(ctx context.Context) ([]ScopeInfo, error)
func (r *Runtime) ArchiveScope(ctx context.Context, scopeID string) (string, error)
func (r *Runtime) RestoreScope(ctx context.Context, anchorID string) error
func (r *Runtime) AddArtifact(ctx context.Context, scopeID string, content []byte, summary string) (string, error)
```

**16 tests, all passing:** OpenClose, NotInitialized, CreateScope (worktree, explicit parent, invalid type, wrong parent, worktree+parent rejected), ListScopes, AddArtifact+Query, Trace (stage names), EmptyInput, ArchiveRestoreRoundtrip, EmptyContent, NonexistentScope, NonexistentArchive.

---

## Project state

```
Git log:
  059fc77 fix .gitignore
  8f0a101 init
  (working tree: Phases 0-5 done, no code committed yet)

Packages:       10 (model + graph + knowledge + store + embedding + retrieval + session + pipeline + cmd + api)
Total tests:    213 (18 model + 35 graph + 52 knowledge + 37 store + 21 embedding + 22 retrieval + 5 session + 7 pipeline + 0 cmd + 16 api)
Suites:         9, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid, JLugagne/bm25 (MIT), cobra, testify
Placeholders:   graph.Snapshot(), policy.PrunableRevisions() → ErrNotImplemented
TODO(v0.2):     incremental BM25, metadata scoring, scope state reverse index, CLI integration tests
```

---

## Phase 6: Testing + Hardening

### Scope 6.2: Integration Tests [✔]

**Package:** `pkg/api/integration_test.go` (new)

**5 new tests, 218 lines, all passing:**

| Test | Description | Lines |
|------|-------------|-------|
| `TestE2E_FullWorkflow` | create scope → 3× add artifact → query → archive → verify `ListScopes`/`ScopeState`/CAS integrity → restore → query identity preservation | ~90 |
| `TestE2E_Branching` | revision lineage via `DiskStore.SaveProjection`, 3 independent summaries, `LatestProjection` returns highest, convention documented as caller obligation | ~50 |
| `TestE2E_TimeTravel` | archive → add → archive (diff) → `TimeMachine.ScopeStateAt` at 3 timestamps: `t_mid` (only A), `t_after` (A+B), `t_before` (`ErrNoHistoricalState`). Paced timestamps with `Sleep(10ms)` | ~100 |
| `TestE2E_Dangling` | delete node+projection directly from store → query returns `err==nil`, exactly 1 surviving result, corrupt entries absent | ~55 |
| `TestE2E_ConcurrentReads` | 5 readers × 20 iterations concurrent, 10 sequential writes interleaved, `require.Eventually` closing timing window | ~70 |

**Adapters:** `timeMachineAdapter` — bridges `store.DiskStore` → `knowledge.TimeAnchorStore` / `TimeArtifactStore` / `TimeEdgeStore` (3 method name/signature mismatches).

**Key decisions:**
- Tests use real bbolt/CAS storage (no mocks)
- Branching tested below public API (`rt.disk.SaveProjection`) — correct abstraction level
- TimeTravel uses archive pipeline without `RestoreScope` between anchors — clean temporal reconstruction testing
- Deterministic content (`"alpha retrieval text"`, `"beta...`", `"gamma..."`) for reproducibility
- Post-restore identity check via `containsID(results, originalID)` — guards against accidental re-ingestion
- Concurrent reads with sequential writes — matches v0.1 bbolt concurrency model

### Project state update

```
Packages:       10 (model + graph + knowledge + store + embedding + retrieval + session + pipeline + cmd + api)
Total tests:    218 (18 model + 35 graph + 52 knowledge + 37 store + 21 embedding + 22 retrieval + 5 session + 7 pipeline + 0 cmd + 21 api)
Suites:         9, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid, JLugagne/bm25 (MIT), cobra, testify
Placeholders:   graph.Snapshot(), policy.PrunableRevisions() → ErrNotImplemented
TODO(v0.2):     incremental BM25, metadata scoring, scope state reverse index, CLI integration tests
```

---

### Scope 6.3: Performance Benchmarks [✔]

**4 new files, ~230 lines, all passing:**

| File | Lines | Benchmarks |
|------|-------|------------|
| `internal/graph/bench_test.go` | 233 | BFS Chain/Star 100K, DFS Unlimited/Depth100 10K, BFS 10K |
| `internal/embedding/bench_test.go` | 28 | MockEmbedder 100 texts |
| `internal/retrieval/bench_test.go` | 108 | Coarse 10K×384, Full/Full_NoEmbed/OnlyVector 1K |
| `internal/store/bench_test.go` | 97 | CAS Store 1MB/1KB, CAS Open 1MB |

**Key results (GOMAXPROCS=1, 5 iter median):**

| Benchmark | Latency | Allocations |
|-----------|---------|-------------|
| BFS Chain 100K | 57ms | 16.9MB, 100K allocs |
| BFS Star 100K | 32ms | 22.6MB, 585 allocs |
| DFS Unlimited 10K | 3.1ms | 1.3MB, 98 allocs |
| DFS Depth100 10K | 25µs | 8.5KB, 17 allocs |
| MockEmbedder 100 texts | 184µs | 157KB, 102 allocs |
| BruteForce 10K×384d | 5.3µs | 1.6KB, 30 allocs |
| Full pipeline 1K | 605µs | 68KB, 462 allocs |
| Full_NoEmbed 1K | 596µs | 66KB, 458 allocs |
| OnlyVector 1K | 333µs | 6.8KB, 112 allocs |
| CAS Store 1MB | 1.0ms, ~1GB/s | 2.2MB, 31 allocs |
| CAS Store 1KB | 24µs, ~42MB/s | 3KB, 11 allocs |
| CAS Open 1MB | 1.0ms, ~1GB/s | 1.4MB, 32 allocs |

**Key conventions:**
- No `require.*` in hot path — only `b.Fatal`/`b.Fatalf` to avoid testify overhead in measurement
- Deterministic RNG per benchmark (`rand.New(rand.NewPCG(1, 2))`) — no shared global state across sub-benchmarks
- Semi-compressible CAS payload (4 cyclic paragraphs) — realistic compression ratio
- `io.Copy(io.Discard)` for CAS Open — zero-allocation read path
- Retrieval decomposition via Engine (`Full`) vs direct index+text+fusion (`Full_NoEmbed`) vs vector only (`OnlyVector`)
- BFS 100K ~45% slower under multi-core (memory contention) — single-core baseline is canonical

**Report filed at:** `docs/benchmarks/v0.1.md`

---

### Scope 6.4: Documentation [✔]

**5 deliverables, all completed:**

| Item | Description |
|------|-------------|
| `README.md` | Quick start, architecture overview, CLI reference table, library usage snippet, development guide, known limitations section |
| `pkg/api/doc.go` | GoDoc package comment for the public API |
| `examples/quickstart.sh` + `quickstart.ps1` | Full roundtrip: init → create → add → query → archive → list → restore → verify |
| Docs updated | `FSD.md` §11 (`RuntimeState` → `SessionState`), `PAD.md` (package statuses), `ROADMAP.md` (Phase 6 [✔]), `devlog.md` (this entry) |
| Verified clean | `PDR.md`, `FRD.md`, `CODE-STYLE.md`, `METHODOLOGY.md` — no stale content |

**Doc fixes:**
- `FSD.md` Section 11: struct name `RuntimeState` → `SessionState` to match `session/state.go`
- `PAD.md`: `pipeline/`, `runtime/`, `api/` package statuses updated from "pending" to [✔]
- `ROADMAP.md`: Scopes 6.1-6.4 marked [✔], P6 summary row [✅], totals updated

### Project state update

```
Packages:       10 (model + graph + knowledge + store + embedding + retrieval + session + pipeline + cmd + api)
Total tests:    218 (18 model + 35 graph + 52 knowledge + 37 store + 21 embedding + 22 retrieval + 5 session + 7 pipeline + 0 cmd + 21 api)
Benchmark files: 4 (graph + embedding + retrieval + store)
Doc files:      14 (README + PDR + FRD + FSD + PAD + CODE-STYLE + METHODOLOGY + ROADMAP + Phase5 + Phase6 + devlog + benchmarks + AGENTS + LICENSE)
Suites:         9, all passing
Build + vet:    clean
Dependencies:   bbolt, klauspost/compress, fastcdc (MIT), ulid, JLugagne/bm25 (MIT), cobra, testify
Placeholders:   graph.Snapshot(), policy.PrunableRevisions() → ErrNotImplemented
TODO(v0.2):     incremental BM25, metadata scoring, scope state reverse index, CLI integration tests
```

### Makefile overhaul

Complete rewrite — 123 lines, 22 targets, sectioned with `##` headers:

| Category | Targets |
|----------|---------|
| Combined | `all`, `dev`, `ci` |
| Build | `build`, `build-all`, `build-linux`, `build-darwin`, `build-windows` |
| Test | `test`, `test-race`, `test-short`, `test-coverage` |
| Benchmark | `bench`, `bench-short` |
| Quality | `lint`, `vet`, `fmt`, `tidy` |
| Release | `release` (build-all + SHA256SUMS) |
| Utility | `install`, `clean`, `help` |

**Key changes:**
- Removed `win`/`linux` dummy targets — replaced with `$(MAKE)` recursion + `PLATFORMS` filter
- `test` without `-race` by default (works everywhere); `test-race` for CI with `RACE_PKGS := ./internal/... ./pkg/...`
- `bench` (`GOMAXPROCS=1`, 5 iter) + `bench-short` (1 iter) — не надо помнить флаги
- `build-all` — shell loop по `PLATFORMS` (6 платформ), layout `dist/iocctl-{os}-{arch}{.exe}`
- `release` — build-all + SHA256 checksums
- `help` — auto-generated from `##` comments via `grep`+`sed`+`awk`
- `-count=1` во всех test/bench targets
- Version injection via `git describe` → `-X main.version`
- `$(SHA256)` — auto-detects `sha256sum` / `shasum`
- `cd . &&` prefix on `mkdir`/`rm` recipes — forces shell mode on Windows (GNU Make bypass)
```
