# devlog — IOC Development Log

## Architecture & purpose

IOC is a stateful knowledge graph runtime — a hybrid cognitive storage system combining:

- **Static Knowledge Graph** (disk) — files, chat logs, hard facts
- **Stateful RAM** (memory) — active graph, summaries, embeddings
- **Temporal Versioning** (anchor+delta) — snapshot history, time-travel

Goal: context isolation for AI agents via formal scope hierarchy (Worktree → Workspace → Session → Artifact), with no LLM dependency in the core engine.

---

## 2026-05-15 — Phase 0: Foundation

### Scope 0.1 — Project scaffold

- `go.mod` with `github.com/DotBlood/ioc`, Go 1.26.3
- Single dependency: `github.com/oklog/ulid/v2`
- `Makefile` (build, test, lint, vet, fmt, tidy, clean, all)
- `.github/workflows/ci.yml` (lint → test → build on push/PR to main)
- Package skeleton created:
  - `cmd/iocctl/` — CLI entry point
  - `internal/model/` — core types (implemented)
  - `internal/graph/` — physical graph (implemented in restructure)
  - `internal/knowledge/` — Knowledge Runtime Layer (new, implemented in restructure)
  - `internal/store/`, `internal/retrieval/`, `internal/embedding/`, `internal/pipeline/` — pending
  - `internal/runtime/` — transient session state (pending)
  - `pkg/api/` — public API (pending)
  - `py/` — Python AI services (pending)
- `AGENTS.md` created — compact instruction file for future sessions
- `iocctl` binary compiles, prints "IOC v0.1.0 — Stateful Knowledge Graph Runtime"

### Scope 0.2 — Core data types (model)

All types in `internal/model/`:

- `id.go`: `ID` (ULID), `NewID()`, `ContentHash` (SHA-256), `ProjectionKey`, `RevisionNumber`, `RevisionRef`, `RevisionFilter`, `BranchName`, `ScopeID`
- `artifact.go`: `Artifact` (immutable — only content + lineage), `NodeType` (4 types)
- `projection.go`: `ArtifactProjection` (versioned metadata), `EmbeddingRefID`, temporal validity
- `edge.go`: 8 edge types (Lineage, Ownership, Retrieval, Reference, Citation, DerivedFrom, RelatedTo, Temporal), `EdgeDirection`, `CyclePolicy`, `ReferenceState`, `EdgeConstraints` with defaults per type
- `readiness.go`: `Readiness` bitflags — independent dimensions (Stored, Embedded, Indexed, Archived)
- `lifecycle.go`: `LifecycleState` (5 states: draft→active→archived→detached→deleted), valid transitions
- `retrieval.go`: `Score` (weighted combination), `TraversalOpts` (no cognition here — just struct), `DanglingConfig`, `RetrievalTrace`, `DeterminismBoundary`, `RetrievalOpts`, sentinel errors

18 tests, all passing.

### Phase 0 extension — Architecture restructure

During design review, we identified that cognition semantics (scope resolution, revision DAG, lifecycle enforcement, context assembly) were starting to mix with the physical graph layer. Added a **Knowledge Runtime Layer** as a formal intermediary:

**Before:**
```
CLI → Pipelines → Graph → Store
                   ↑
              Retrieval (embedded cognition)
```

**After:**
```
CLI / API
    ↓
Pipelines / Runtime
    ↓
Knowledge Runtime (NEW) — cognition semantics
    ↓
Graph Engine (PURE)     — physical graph only
    ↓
Storage
```

Changes:

- **New `internal/knowledge/`** — 6 sub-components:
  - `scope.go` — ScopeResolver (scope containment, visibility rules)
  - `revision.go` — RevisionManager (Revision DAG, branching, head resolution)
  - `lifecycle.go` — LifecycleManager (state transitions, invariant enforcement)
  - `lineage.go` — LineageTracker (lineage DAG, cycle detection, provenance)
  - `assembly.go` — ContextAssembler (workspace-aware context construction, token budgeting)
  - `policy.go` — PolicyEnforcer (archive/retention/retrieval policies)
- **`internal/graph/` stripped to pure physical** — adjacency lists, CRUD, indexes. No ScopeFilter, no ActiveOnly, no cognition. BFS/DFS traverse in both adjOut and adjIn.
- Interfaces for all cross-layer boundaries (GraphStoreReader, RevisionStoreReader, etc.)
- 5 tests on graph/, all passing.

### Docs created

| Doc | Lines | What |
|-----|-------|------|
| `docs/PDR.md` | 88 | Product Definition — vision, audience, differentiators |
| `docs/FRD.md` | 213 | Functional Requirements — 24 FRs, 5 NFRs |
| `docs/FSD.md` | 544 | Formal Specification — invariants, models, contracts |
| `docs/PAD.md` | 320 | Platform Architecture — Go core + Python AI via gRPC |
| `docs/CODE-STYLE.md` | 209 | Go conventions |
| `docs/METHODOLOGY.md` | 156 | Development process |
| `docs/ROADMAP.md` | 432 | 7 phases, 26 scopes, ~23w to v0.1.0 |

### Key design decisions

1. **ULID primary ID** (not SHA-256) — stable references vs content-addressed CAS
2. **Artifact + Projection separation** — immutable content vs versioned metadata
3. **Revision DAG** (not linear) — branching for parallel experiments
4. **Physical vs Cognitive layering** — graph has no cognition semantics
5. **Structural snapshots** (not deep-copy) — artifacts not duplicated in anchors
6. **Bounded determinism** — retrieval deterministic within snapshot+model+config boundary
7. **Graceful degradation** — dangling references never hard-fail
8. **Readiness bitflags** — BM25 can be ready before embeddings (or vice versa)
9. **Cycles forbidden by default** — explicit opt-in for semantic edges only

### Current state

```
Git log:
  059fc77 fix .gitignore
  8f0a101 init
   (working tree: Phase 0 + Phase 1 complete, no feature code committed yet)
```

---

## 2026-05-15 — Phase 1: Physical Graph + Knowledge Layer

### Scope 1.1: Physical StatefulGraph [✔] (completed in Phase 0 extension)

- `graph/graph.go` — pure physical: `Node(ctx, id)`, `AddNode`, `RemoveNode`, `Edge`, `AddEdge`, `BFS`/`DFS` (bidirectional), `NodesByType`, `Snapshot` (placeholder)
- `graph/interfaces.go` — `StoreReader` + `StoreWriter` cross-layer contracts
- 10 tests covering CRUD, BFS/DFS, multi-edge, concurrent reads

### Scope 1.2: Knowledge Runtime Layer [✔]

All six components implemented + tested:

| Component | File | Key Methods | Sub-tests |
|-----------|------|-------------|-----------|
| ScopeResolver | `scope.go` | `ResolveScope`, `IsVisibleFrom` | 5 (exact match, parent→child, sibling isolation, unrelated) |
| RevisionManager | `revision.go` | `ResolveRevision` (latest/pinned/branch-local/ancestor), `ResolveForRetrieval`, `IsActive`, `IsSuperseded` | 5 (latest, pinned, active, superseded, not-superseded) |
| LifecycleManager | `lifecycle.go` | `Transition`, `TransitionIfValid`, `EnforceInvariants` | 2 (valid transition, invalid, invariants) |
| LineageTracker | `lineage.go` | `RecordLineage` (cycle detection), `RecordOwnership`, `Provenance` | 3 (lineage, cycle detection, provenance chain, ownership) |
| ContextAssembler | `assembly.go` | `Plan` (scope-aware), `Assemble` (token-budgeted) | 1 (plan creation) |
| PolicyEnforcer | `policy.go` | `ShouldArchive`, `DefaultRetrievalScope`, `PrunableRevisions` (placeholder) | 2 (default scope, should-archive) |

18 tests, all passing.

### Bug fixes

- **Cycle detection**: `checkCycle` originally only traversed `EdgesOut` (one direction). Fixed to traverse both `EdgesOut` + `EdgesIn`. Now detects 2-node lineage cycles correctly.
- **Error comparison**: lifecycle Transition compared with `err == sentinel` but `fmt.Errorf("%w")` wraps the error. Fixed to use `errors.Is`.

### Current state

```
All packages:       5 (model + graph + knowledge + store + cmd)
Total tests:        42 (18 model + 10 graph + 0 cmd + 14 store)
Total test suites:  4 packages, all passing
Build + vet:        clean
Placeholders:       graph.Snapshot() + policy.PrunableRevisions() → ErrNotImplemented
Design docs:        7 (PDR, FRD, FSD, PAD, CODE-STYLE, METHODOLOGY, ROADMAP)
Dev log:            this file
```

---

## 2026-05-15 — Phase 2: Storage Layer (partial)

### Scope 2.1: DiskStore (bbolt) [✔]

**Deliverable:**
- `store/disk.go` — bbolt-backed persistent store
  - 8 buckets: nodes, projections, edges, adj_out, adj_in, anchors, rev_dag, idx_type, meta
  - SaveNode/LoadNode/DeleteNode — artifact CRUD
  - SaveProjection/LoadProjection/ListProjectionKeys — projection CRUD
  - SaveEdge/LoadEdge/LoadEdgesOut/LoadEdgesIn — edge CRUD with adjacency lists
  - SaveSnapshot/LoadSnapshot — full graph snapshot roundtrip
  - Serialization via encoding/gob
- 7 tests: node CRUD, projection CRUD, edge CRUD, snapshot roundtrip, not-found, reopen, multiple projections

### Scope 2.4: EmbeddingStore (mmap-backed, file-backed for now) [✔]

**Deliverable:**
- `store/embedding.go` — file-backed fixed-size embedding vector store
  - `OpenEmbeddingStore(path, dims)` — create/open, header with dims check
  - `Put(vec []float32) (EmbeddingRefID, error)` — append, 1-indexed
  - `Get(ref EmbeddingRefID) ([]float32, error)` — retrieve by ID
  - `Len() int` — count of stored vectors
  - `Sync()` / `Close()` — write buffer to disk
  - Append-only, fixed-size records (dims*4 bytes each)
  - In-memory buffer with file sync (mmap planned for future)
- 7 tests: put/get, multiple puts, out-of-range, wrong dims, dims mismatch, sync roundtrip, different dims
- **Design note:** EmbeddingStore is an isolated component — no dependency on graph engine, CAS, or revision DAG. The API surface (Put/Get/Len/Close) is stable for future mmap migration.

### Scope 2.2 (CAS) + Scope 2.3 (Artifact/Projection Store) — pending

### Current state

```
All packages:       5 (model + graph + knowledge + store + cmd)
Total tests:        42 (18 model + 10 graph + 0 cmd + 14 store)
Total test suites:  4 packages, all passing
Build + vet:        clean
Dependencies:       bbolt, klauspost/compress, ulid
```
