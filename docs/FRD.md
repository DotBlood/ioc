# Functional Requirements Document (FRD) — v0.2

> What IOC must do, as testable requirements grounded in the actual engine. Companion to
> [`PDR.md`](PDR.md) (why/for whom) and `FSD.md` (how, precisely — in progress). On any
> code↔doc conflict, the code wins; the canonical operation set is the `runtime.Service` interface
> (`internal/runtime/service.go`) and the core types (`internal/core/types.go`).

Priority: **P0** = load-bearing (the product fails without it); **P1** = important; **P2** = nice.
Status: ✅ implemented · ◐ partial · ⏳ deferred · ❄ frozen.

---

## 1. Scopes (FR-SCOPE)

- **FR-SCOPE-01 (P0, ✅).** Create a scope with a role (`worktree` | `workspace` | `session`) and an
  optional parent (`CreateScope`); a zero parent is a root. Roles are *labels* on one recursive unit,
  not fixed tree levels.
- **FR-SCOPE-02 (P0, ✅).** Nest scopes arbitrarily (a session may parent its own scopes). The
  ancestor chain is retrievable (`Ancestors`).
- **FR-SCOPE-03 (P0, ✅).** Fork a scope (`Fork`) — keep BOTH branches; the fork records `ForkedFrom`
  and receives a copy of the source's published artifacts. Branching is the idea-evolution graph.
- **FR-SCOPE-04 (P0, ✅).** Open a new version at a version boundary (`CrossVersion`): archive the old
  version scope and start vN+1 from a single distilled **seed** artifact (`Seed{Constraints,Lessons}`).
- **FR-SCOPE-05 (P1, ✅).** Attach a scope-level rollup (`RollupScope`: a representative summary +
  embedding) so hierarchical retrieval can rank scopes coarsely.
- **FR-SCOPE-06 (P1, ✅).** List/get scopes (`ListScopes`, `GetScope`); delete an EMPTY scope
  (`DeleteScope` refuses while it holds artifacts or child scopes — guard against data loss).

## 2. Artifacts (FR-ART)

- **FR-ART-01 (P0, ✅).** An artifact is a leaf result of a `Kind`: `answer | insight | summary |
  document | reasoning | seed`. It carries a REQUIRED mini-summary, an embedding of that summary, an
  optional cold content hash (CAS), a `Tier`, provenance (`DerivedFrom`), and currency (`SupersededBy`).
- **FR-ART-02 (P0, ✅).** Artifacts are **immutable**: content never mutates in place; a changed
  conclusion is a NEW artifact (the old one is superseded, not edited).
- **FR-ART-03 (P0, ✅).** Two memory tiers as one type: `workspace` (mutable working memory, default)
  and `worktree` (canonical, long-lived truths). Tier is a filter at retrieval, not a separate store.
- **FR-ART-04 (P1, ✅).** Full content lives cold in content-addressable storage; the working layer
  holds only summary + embedding. Content is fetched only on drill.
- **FR-ART-05 (P1, ✅).** List all artifacts (`ListArtifacts`); delete an artifact (`DeleteArtifact`)
  — its embedding stays in the append-only store but never re-surfaces in search, and its edges are
  removed.

## 3. Write path (FR-WRITE)

- **FR-WRITE-01 (P0, ✅).** `Push` takes an LLM-authored summary (REQUIRED) and optional content; IOC
  embeds the summary (or `EmbedText` when given, e.g. file chunks) and stores the rest. **IOC never
  calls an LLM** to author or summarize.
- **FR-WRITE-02 (P0, ✅).** A push may declare, at write time: what it **supersedes** (`Supersedes`),
  and **knowledge edges** from the new artifact (`Relations []EdgeSpec`). Both are validated before the
  write — a bad target fails the whole push (no half-applied state).
- **FR-WRITE-03 (P0, ✅).** Provenance/trust is **engine-asserted**, not caller-asserted: `Push`
  strips any caller `Meta["trust"]` and sets it only from the dedicated `Trust` field (so an external
  caller cannot forge "ingested" to launder untrusted content as authored).
- **FR-WRITE-04 (P1, ✅).** `Publish` makes an artifact visible to sibling scopes (toggled separately
  from creation).

## 4. Retrieval (FR-RET)

- **FR-RET-01 (P0, ✅).** `Query` is **progressive disclosure**: it returns a cheap overview
  (`DetailOverview`: summary + score) by default; `Drill` re-fetches one artifact at higher detail
  (`DetailEntry` / `DetailRaw` loads cold content from CAS). The default answer must never carry raw
  content.
- **FR-RET-02 (P0, ✅).** Retrieval modes: `vector` (cosine, default), `hybrid` (vector + BM25 via
  RRF), `hierarchical` (coarse-rank scope rollups → fine search within top scopes). An optional
  cross-encoder **rerank** re-orders the top candidates over their content.
- **FR-RET-03 (P0, ✅).** Each query result reports a confidence read from the signal that actually
  ordered it: `top_score`, `margin` (top1−top2), `weak_match` (below a per-embedder floor), and
  `ranked_by` (`cosine` | `rerank`). `Hit.Score` is always the cosine similarity.
- **FR-RET-04 (P0, ✅).** Default retrieval returns the **current view only**: artifacts with
  `SupersededBy` set and artifacts in `Archived` version scopes are excluded. `IncludeSuperseded`
  surfaces history with a `superseded_by` back-link.
- **FR-RET-05 (P1, ✅).** Filters: restrict by `Kind` (e.g. documents vs reasoning), by `Tier`, and a
  cosine `MinScore` pre-gate (applied before rerank, not as a post-rerank drop).
- **FR-RET-06 (P2, ✅).** An opt-in, off-by-default recency tie-breaker (`RecencyHalfLifeDays`,
  vector-mode) blends a small age term among current atoms — used sparingly (it demotes stable truths).
- **FR-RET-07 (P1, ✅).** `Neighbors` returns the most similar CURRENT artifacts to some text — the
  "what might this replace?" lookup an agent runs BEFORE a push.

## 5. Knowledge edges (FR-EDGE)

- **FR-EDGE-01 (P0, ✅).** Artifacts carry **author-declared** typed directed edges
  (`RelationKind`: `depends_on | contradicts | answers | refines | relates_to`, open vocabulary).
  Create them at write (`Push.Relations`) or post-hoc (`Relate(from, to, kind)`). **IOC never infers
  edges.**
- **FR-EDGE-02 (P0, ✅).** Edge creation is idempotent (a `(from, to, kind)` triple is unique) and
  validated (both endpoints must exist; no self-edge; non-empty kind).
- **FR-EDGE-03 (P0, ✅).** `Related(artifact, kinds, direction, depth)` walks the graph: `out` = the
  artifact's targets ("what does X depend on"), `in` = artifacts pointing at it ("what depends on X"),
  `both`. Filter by kind, bound by depth (BFS, cycle-guarded). Superseded artifacts are excluded.
- **FR-EDGE-04 (P1, ✅).** Deleting an artifact removes every edge touching it (no dangling edges).
- **FR-EDGE-05 (P2, ⏳).** *Graph-aware retrieval* — blending the structural axis into `Query` ranking
  (edge-boosted candidates) — is deferred; today edges are a separate axis (`Related`).

## 6. Visibility (FR-VIS)

- **FR-VIS-01 (P0, ✅).** Visibility is **bottom-up**: a scope sees its own artifacts, its ancestors'
  artifacts (drill-up), and siblings' **published** artifacts only. Raw reasoning is private until
  published. Archived (superseded-version) siblings are excluded from the current view.
- **FR-VIS-02 (P1, ✅).** `SiblingOverview` returns the published artifacts of sibling scopes (the
  shared blackboard) as cheap hits.

## 7. Supersession / currency (FR-SUP)

- **FR-SUP-01 (P0, ✅).** Supersession is **append-only**: nothing is deleted; the current-truth view
  is a read-time filter and history is always reachable.
- **FR-SUP-02 (P0, ✅).** Declare replacement at write (`Push.Supersedes`) or post-hoc
  (`Supersede(old, by)`); `Consolidate(..., supersedes)` retires what it folds in atomically.
  **Detection of what supersedes what stays with the authoring model** — IOC keeps the bookkeeping.

## 8. Consolidation & version boundaries (FR-CONS)

- **FR-CONS-01 (P0, ✅).** `Consolidate` writes one summary that captures a finished line of work and
  promotes it to the parent scope's worktree (canonical) tier — the branch-transition boundary.
- **FR-CONS-02 (P0, ✅).** `CrossVersion` archives a version and seeds the next from distilled
  `Constraints`/`Lessons` (not bulk results), bounding cross-tier growth ("the head must be metal",
  not "we built a hoe").

## 9. Ingestion (FR-ING)

- **FR-ING-01 (P1, ✅).** Ingest a directory tree into nested scopes, chunking text files at
  language-aware boundaries into `document` artifacts; re-running is **idempotent** (reconciles to the
  current tree — changed files re-chunked, vanished files removed, unchanged = no-op).
- **FR-ING-02 (P0, ✅).** Ingestion is **sandboxed** (confined to `IOC_INGEST_ROOT`, symlink-resolved,
  traversal-proof) and tags chunks `trust=ingested` (untrusted provenance); query output surfaces the
  flag so callers treat ingested content as data, not instructions.
- **FR-ING-03 (P1, ❄).** Document-*retrieval* quality work (content-coherent rollups, etc.) is
  **frozen** — document retrieval competes with grep/LSP; reasoning memory is the value. Ingestion
  itself stays (it feeds the store).

## 10. Runtime & sharing (FR-RT)

- **FR-RT-01 (P0, ✅).** A single long-lived daemon (`ioc serve`) owns one store dir and serves many
  clients over an internal framed-JSON RPC on loopback. CLI/MCP/sub-agents **auto-route** to it
  (discovered via `<dir>/runtime.json`) and fall back to an embedded engine when none runs.
- **FR-RT-02 (P0, ✅).** Concurrency is in-process: parallel reads, serialized writes (`RWMutex`);
  writes flush embeddings for durability; graceful shutdown via signal or control op.
- **FR-RT-03 (P1, ✅).** Auth: a per-store owner token in `runtime.json` plus an optional read-only
  token, gated by method tier (control/write require the owner token; reads accept either). Tokens can
  be rotated; mTLS is opt-in (loopback-prep, does not replace the token).
- **FR-RT-04 (P1, ⏳).** Multi-process direct access to one store is deliberately rejected in favor of
  the daemon; MVCC/multi-tenant is deferred.

## 11. Inspectability (FR-TRACE)

- **FR-TRACE-01 (P1, ✅).** Every query records a trace (the exact visible set + hits it saw),
  retrievable by `Trace(queryID)` / `RecentTraces(n)`. This is **inspectability/replay**, NOT a
  determinism guarantee — bounded determinism is explicitly demoted from a hard invariant.

## 12. Interfaces (FR-IF)

- **FR-IF-01 (P0, ✅).** The engine is an **embeddable Go library** (the public API).
- **FR-IF-02 (P0, ✅).** A **CLI** (`cmd/ioc`) drives every operation from a shell (an agent can call
  it via Bash); JSON output is machine-parseable.
- **FR-IF-03 (P0, ✅).** A **thin MCP server** (`cmd/ioc-mcp`) exposes the engine as `ioc_*` tools over
  stdio. The MCP surface mirrors the library; it adds no logic of its own.

---

## Non-functional requirements (NFR)

- **NFR-01 (P0, ✅).** **No LLM in the core.** No core path calls a model to reason, summarize, or
  extract a graph; embeddings come from an external embedder the caller configures.
- **NFR-02 (P0, ✅).** **Integrity / crash-safety.** CAS writes are crash-atomic (temp+rename);
  the embedding store is append-only with a commit-pointer header + fsync; a torn/over-counted store
  fails safe (clamped), never panics.
- **NFR-03 (P0, ✅).** **Single-writer correctness** now; interfaces stay MVCC-ready for later.
- **NFR-04 (P1, ✅).** **Confidentiality at rest** is available opt-in (AES-256-GCM); sensitive files
  are owner-only (`0o600`); the daemon is loopback-only with a per-store token.
- **NFR-05 (P1, ✅).** **One embedder per store dir.** A given `-dir` must always use the same embedder
  (mock vs real, bge-small vs bge-base are different vector spaces); the store persists the embedder
  identity and refuses a mismatched open/query.
- **NFR-06 (P1, ✅).** **DoS/abuse bounds** on the daemon and inputs: framed-size tiers (full-token
  gated), idle/conn caps, JSON depth limit, decompression-bomb cap, bounded embedder responses, ingest
  file/depth/size caps.
- **NFR-07 (P1, ✅).** **Portability / local-first.** Pure-Go core, single module (`github.com/DotBlood/ioc`,
  Go 1.26), Windows-friendly; the only external service is an optional embedder.
- **NFR-08 (P2, ✅).** **Determinism is optional** (trace/replay), not a hard invariant.
- **NFR-09 (P0, ✅).** **License: AGPL v3.**

---

## Explicitly out of scope (vs the v0.1 FRD)

A user-facing low-level **graph/edge-CRUD API** and graph traversal as the primary retrieval surface;
**bounded-determinism** as a P0 invariant; complex **archive/retention pipelines** (TTL pruning,
structural-snapshot anchors); **hierarchical embedding averaging**; multi-stage ANN/HNSW retrieval.
These were v0.1 requirements; v0.2 replaces them with author-declared edges, optional trace/replay,
archive = mark-archived, scope rollups, and brute-force + BM25/RRF retrieval.
