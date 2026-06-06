# Functional Specification Document (FSD) — v0.2

> The precise contract: invariants, domain types, operation semantics, the retrieval/currency/edge
> models, and the runtime protocol. Companion to [`PDR.md`](PDR.md) (why) and [`FRD.md`](FRD.md)
> (what). **The code is the source of truth** — types here are stated from `internal/core/types.go`
> and `internal/core/id.go`; operations from the `runtime.Service` interface
> (`internal/runtime/service.go`) as implemented in `internal/engine/*`. On any conflict, the code wins.

---

## 0. Conventions

- **Errors, not panics.** All public APIs take `context.Context` first and return an `error`. Two
  sentinels classify failures and cross the RPC boundary intact: `core.ErrNotFound`,
  `core.ErrInvalidInput`; anything else is "internal".
- **Append-only semantics.** Artifacts and edges are never mutated destructively; "delete",
  "supersede", and "archive" are bookkeeping that change the *current view*, not history.
- **One embedder per store.** A data dir is bound to a single embedder identity; the store refuses to
  open or query under a different model (mock and real, bge-small and bge-base are different vector
  spaces and must not be mixed).

---

## 1. System invariants (v0.2)

- **INV-1 — Stable identity.** Every entity has a `core.ID` (ULID) assigned once and never changed for
  its lifetime. IDs are time-sortable.
- **INV-2 — Artifact immutability.** An artifact's content and summary never change in place. A changed
  conclusion is a NEW artifact; the prior one is superseded, not edited.
- **INV-3 — Append-only currency monotonicity.** Supersession only adds a forward `SupersededBy`
  back-link and never removes an artifact; the "current" set is a read-time filter. History is always
  reachable (`IncludeSuperseded`).
- **INV-4 — Acyclic system walks.** Scope/lineage walks (ancestors, descendants, scope-path, edge
  traversal) are bounded by a visited-set, so a corrupt or fork-diamond graph can never loop forever or
  double-count.
- **INV-5 — Snapshot consistency.** A single `Query`/operation runs against a consistent view of the
  store (the daemon serializes writes; bbolt serializes its own transactions).
- **INV-6 — Provenance integrity.** `Meta["trust"]` is engine-asserted only (a caller cannot forge
  "ingested"); ingested content is marked untrusted and surfaced as such at read time.

**Dropped from v0.1** (no longer invariants): strict Worktree→Workspace→Session→Artifact *tree
containment* (scopes are now recursive roles), and *bounded determinism* (demoted to optional
trace/replay).

---

## 2. Identity model

| Type | Definition | Role |
|------|------------|------|
| `core.ID` | ULID (`oklog/ulid`) wrapped; `NilID` = zero; `NewID()`, `ParseID()`, `IsZero()`, text-marshaled | primary identity for scopes, artifacts, queries/traces |
| `core.ContentHash` | `[32]byte` SHA-256 (`NewContentHash`) | CAS dedup + integrity — **never** primary identity |
| `core.EmbeddingRef` | `uint64`, **1-indexed** handle into the embedding store (0 = no embedding) | links an artifact/rollup to its stored vector |

---

## 3. Domain types

Stated from `internal/core/types.go`. JSON tags omitted for brevity.

### 3.1 Enums

- **`Role`** (`uint8`): `RoleWorktree` | `RoleWorkspace` | `RoleSession` — *roles* of one recursive
  scope, not fixed levels.
- **`Tier`** (`uint8`): `TierWorkspace` (mutable working memory, default) | `TierWorktree` (canonical,
  long-lived truths).
- **`ArtifactKind`** (`uint8`): `KindAnswer` | `KindInsight` | `KindSummary` | `KindDocument` |
  `KindReasoning` | `KindSeed`.
- **`Detail`** (`uint8`): `DetailOverview` (summary + score; default) | `DetailEntry` (+ metadata/
  lineage/scope-path) | `DetailRaw` (+ full content from CAS).
- **`QueryMode`** (`uint8`): `ModeVector` (cosine, default) | `ModeHybrid` (vector + BM25 via RRF).
- **`RelationKind`** (`string`, open vocabulary): conventional `depends_on` | `contradicts` |
  `answers` | `refines` | `relates_to`.
- **`EdgeDir`** (`uint8`): `DirOut` (edges FROM the artifact) | `DirIn` (edges TO it) | `DirBoth`.

### 3.2 Scope

`Scope{ ID, Parent, Role, Title, Version, SeedFrom, Archived, ForkedFrom, CreatedAt, RollupSummary,
RollupEmbRef }`. `Parent` zero ⇒ root. `Version` is the vN counter; `SeedFrom` references the
`KindSeed` artifact a version started from; `Archived` marks a superseded version (excluded from the
current view); `ForkedFrom` records the idea-evolution fork source; `RollupSummary`/`RollupEmbRef` are
the scope's representative summary+embedding for hierarchical retrieval (set by `RollupScope`).

### 3.3 Artifact

`Artifact{ ID, Scope, Kind, Tier, Summary, EmbRef, Content, DerivedFrom[], Published, Meta, CreatedAt,
SupersededBy }`. `Summary` is the cheap representation (embedded); `EmbRef` the embedding of the summary
(or of `EmbedText` for chunks); `Content` the cold CAS hash (zero ⇒ summary-only); `DerivedFrom` the
idea-evolution lineage; `Published` toggles sibling visibility; `SupersededBy` (non-zero) the artifact
that replaced this one.

### 3.4 PushRequest (the write)

`PushRequest{ Scope, Kind, Summary(REQUIRED), Content, DerivedFrom[], Tier, Publish, EmbedText, Meta,
Supersedes[], Trust, Relations[] }`. `EmbedText` overrides what is embedded (raw chunk text, keeping
`Summary` as a short label). `Supersedes` lists prior artifacts this push retires. `Trust` is the only
way to set provenance (engine strips caller `Meta["trust"]`). `Relations` are edges FROM the new
artifact (see §6). Defaults: `Tier=TierWorkspace`, `Kind=KindInsight`.

### 3.5 Query & Hit

`Query{ Scope, Text, Detail, TopK, Tier, Mode, MinScore, Hierarchical, CoarseK, Kinds[], Rerank,
RerankN, IncludeSuperseded, RecencyHalfLifeDays }`. `Hit{ Artifact, Scope, ScopePath, Kind, Tier,
Summary, Score(cosine, always), RerankScore(*float64, set only when reranked), Meta, Content(only at
DetailRaw), SupersededBy(only via IncludeSuperseded) }`.

### 3.6 Edge / EdgeSpec

`Edge{ From, To, Kind, CreatedAt }` — a stored directed relation. `EdgeSpec{ Kind, Target }` — an edge
to create at push time, FROM the pushed artifact TO `Target`.

### 3.7 Seed / TraceRecord

`Seed{ Constraints, Lessons }` — carried across a version boundary. `TraceRecord{ QueryID, Scope, Text,
EmbModel, VisibleSet[], Hits[], Drills[], CreatedAt }` — the exact context a query saw (inspectability).

---

## 4. Operations contract

Signatures mirror `runtime.Service` (both `*engine.Engine` and the remote `*runtime.Client` satisfy
it). Pre = precondition; Eff = effect; Err = error classes.

### Scopes
- **`CreateScope(parent, role, title) → Scope`.** Pre: `parent` is `NilID` or an existing scope. Eff:
  creates a scope (fresh `ID`, `CreatedAt`). Err: `ErrNotFound` (parent), `ErrInvalidInput`.
- **`Fork(source, title) → Scope`.** Pre: `source` exists. Eff: new scope with `ForkedFrom=source`,
  receiving a copy of `source`'s published artifacts; both branches are kept.
- **`CrossVersion(scope, seed) → Scope`.** Eff: marks `scope` `Archived`; opens vN+1 seeded from a new
  `KindSeed` artifact built from `seed`; the new scope's `SeedFrom` points at it.
- **`RollupScope(scope, summary)`.** Pre: non-empty summary, `scope` exists. Eff: embeds `summary` and
  sets the scope's `RollupSummary`/`RollupEmbRef`.
- **`Ancestors(scope) → []Scope`** (nearest first); **`ListScopes()`**, **`GetScope(id)`** (Err
  `ErrNotFound`); **`DeleteScope(id)`** — Err `ErrInvalidInput` if the scope still has artifacts or
  children.

### Artifacts / write
- **`Push(req) → Artifact`.** Pre: non-empty `Summary`, existing `Scope`, all `Supersedes` and
  `Relations.Target` exist. Eff (ordered): store content (CAS) → embed summary/EmbedText → write the
  artifact → mark each superseded target `SupersededBy=new.ID` → create the declared edges. A bad
  target aborts the whole push (fail-fast). Err: `ErrInvalidInput` (empty summary, empty edge kind),
  `ErrNotFound` (scope/target).
- **`Publish(id)`.** Eff: sets `Published=true`. **`Drill(id, detail) → Hit`.** Eff: re-fetches one
  artifact at `detail` (DetailRaw loads CAS). Err: `ErrNotFound`.
- **`ListArtifacts()`**, **`DeleteArtifact(id)`** (also removes the artifact's edges).
- **`Supersede(old, by)`.** Pre: both exist, `old≠by`. Eff: sets `old.SupersededBy=by`. Err:
  `ErrInvalidInput` (self), `ErrNotFound`.

### Retrieval (see §5 for the full pipeline)
- **`Query(q) → (queryID, []Hit)`.** Eff: progressive-disclosure retrieval from `q.Scope`; records a
  trace. Err: `ErrNotFound` (scope), `ErrInvalidInput` (query/store dimension mismatch).
- **`Neighbors(scope, text, k) → []Hit`.** A plain overview query restricted to the current view — the
  pre-push "what might this replace?" lookup.
- **`SiblingOverview(scope) → []Hit`** — published artifacts of sibling scopes (the blackboard).

### Edges (see §6)
- **`Relate(from, to, kind)`.** Pre: `from≠to`, both exist, non-empty `kind`. Eff: idempotent
  `PutEdge`. Err: `ErrInvalidInput`, `ErrNotFound`.
- **`Related(artifact, kinds, dir, depth) → []Hit`.** Eff: BFS over edges (§6), excluding superseded.

### Memory mechanics / admin
- **`Consolidate(scope, summary, supersedes) → Artifact`** — promotes a `TierWorktree` summary to the
  parent scope; retires `supersedes` atomically.
- **`Trace(queryID) → TraceRecord`**, **`RecentTraces(n) → []TraceRecord`** (newest first).
- **`Config(key)→(val,ok)`**, **`SetConfig(key,val)`**, **`EmbModel()→string`**, **`Close()`**.

---

## 5. Retrieval contract

For a `Query` from viewpoint `q.Scope` (pipeline as implemented in `internal/engine/query.go`):

1. **Embed** `q.Text`; **guard** its dimension against the store's embedder (mismatch ⇒
   `ErrInvalidInput`, not silent zeros).
2. **Candidate set.** If `q.Hierarchical`: coarse-rank descendant scope rollups by cosine, keep the top
   `CoarseK` (adaptive; default ~6), and gather their artifacts — this **descends into child scopes**.
   Else: `visibleArtifacts` (own + ancestors + published siblings — see §9). **Shape rule:** flat
   retrieval is *structurally blind* to artifacts that live in descendant scopes; when querying from a
   parent whose answers live in child scopes, hierarchical is mandatory (measured: flat recall 0.00 vs
   hierarchical 0.74 on such a corpus — see [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md)).
3. **Currency filter.** Unless `IncludeSuperseded`, drop artifacts with `SupersededBy` set (archived
   version scopes are already excluded at the visibility layer).
4. **Kind filter** (`q.Kinds`), if any.
5. **Rank.** Build a cosine set (and, in `ModeHybrid`, a BM25 index over each artifact's rank-text).
   `ordered` = cosine by default, or `RRF(60, cosine, bm25)` in hybrid. Optional recency blend
   (vector-mode, non-rerank only).
6. **MinScore** is a **cosine pre-gate** on candidates (before rerank, never a post-rerank drop).
7. **Rerank** (optional): a cross-encoder re-orders the top `RerankN` candidates over their content;
   `Hit.RerankScore` = sigmoid(logit). On rerank error, keep the existing order.
8. **Build hits** up to `TopK`: `Hit.Score` is **always cosine**; `RerankScore` is set only when
   reranked. Record a `TraceRecord`.

**Confidence** (computed in `internal/iocfmt`, surfaced by CLI/MCP): `top_score`, `margin` (top1−top2
of the *active* signal), `weak_match` (`len==0 OR top < floor`), and `ranked_by` (`cosine` | `rerank`).
The floor is per-embedder (`core.ConfidenceFloor`, ~0.68 for bge-small) for cosine, or
`core.RerankFloor` (~0.5) when reranked — **confidence always reads the signal that ordered the hits**,
never a mix (mixing produced the negative-margin false-confidence H3 bug). Known caveat: the cosine
floor is gameable by vocabulary at small N; margin separates present/absent more honestly (a
margin-aware weak_match is an open experiment).

---

## 6. Knowledge-edge model

- **Storage.** Edges live in a Meta `edges` bucket keyed by the composite `from|to|kind`, so re-putting
  the same triple is **idempotent** (one edge, not duplicates). The value is a `core.Edge`.
- **Creation.** `Push.Relations` (at write) or `Relate` (post-hoc). Both validate endpoints exist,
  reject a self-edge and an empty kind, and never infer edges — the author declares them.
- **Traversal (`Related`).** BFS from the start artifact, bounded by `depth` (default 1) and a
  visited-set (cycle-safe). `dir` selects which edges to follow per node: `DirOut` (this node's
  targets), `DirIn` (nodes pointing at it — e.g. `depends_on` + `DirIn` answers "what depends on X"),
  `DirBoth`. `kinds` filters by relation kind (empty = all). Results **exclude superseded** artifacts
  (current view) and never include the start artifact; edges to a deleted artifact are skipped.
- **Cleanup.** `DeleteArtifact` removes every edge touching the id (as From or To) — no dangling edges.
- **Relation to provenance.** Distinct from `DerivedFrom`, `ForkedFrom`/`SeedFrom`, and `SupersededBy`
  (which IOC maintains): knowledge edges are the *semantic* relations the author adds on top.

---

## 7. Supersession / currency model

Append-only (INV-3). A new conclusion declares what it retires (`Push.Supersedes`), or it is marked
post-hoc (`Supersede`), or folded in by `Consolidate(...,supersedes)`. The engine sets
`old.SupersededBy = new.ID` and records the lineage in `DerivedFrom`. Default retrieval excludes
artifacts with `SupersededBy` set and artifacts in `Archived` version scopes; `IncludeSuperseded` (CLI
`-include-superseded`, MCP `include_superseded`) surfaces history with a `superseded_by` back-link.
**Detection stays with the authoring model** (assisted by `Neighbors` run before a push); IOC keeps the
bookkeeping and the read-time view. An opt-in `RecencyHalfLifeDays` tie-breaker exists but is *not* the
currency mechanism (it demotes stable old truths) — supersession is.

---

## 8. Version boundaries & seed

Two consolidation boundaries bound growth: (1) a **branch transition** within a version
(`Consolidate` summarizes a finished line into the parent's worktree tier); (2) a **version boundary**
(`CrossVersion` archives vN — still retrievable via `IncludeSuperseded` — and starts vN+1 from a single
`KindSeed` artifact built from `Seed{Constraints,Lessons}`). The seed carries distilled
constraints/lessons, not bulk, so vN+1 does not repeat vN's mistakes.

---

## 9. Visibility model

`visibleArtifacts(scope, tier, includeArchived)` is **bottom-up**: the viewpoint's own artifacts + all
ancestor-scope artifacts (drill-up lineage) + **published-only** artifacts of sibling scopes. It does
**not** descend into child scopes (that is what hierarchical retrieval does — §5 step 2). Archived
(superseded-version) siblings are excluded unless `includeArchived`. `tier` (if set) filters to one
memory tier. This is why a flat query from a parent cannot see a child's unpublished artifact, and why
hierarchical retrieval is required to reach descendant memory.

---

## 10. Runtime protocol & concurrency

(`internal/runtime/{proto,server,client,dispatch}.go`.)

- **Framing.** Length-prefixed: a 4-byte big-endian `uint32` length + a JSON payload. Frame-size tiers
  bound pre-auth amplification: an unauthenticated connection may send at most `maxControlFrame` (1 MiB);
  only a request that authenticates with the **full (owner) token** lifts the cap to `maxDataFrame`
  (64 MiB) — never an error response, never a read-only token.
- **Envelope.** `request{ id, v(proto version), method, token, params }` →
  `response{ id, result, error{ code, msg } }`. Error codes map to sentinels: `not_found`, `invalid`,
  `internal`, `auth`; the client re-wraps `not_found`/`invalid` so `errors.Is(core.ErrNotFound/…)`
  works across the wire.
- **Auth / ACL.** Stateless per-request bearer token compared in **constant time** (both full and
  read-only compared unconditionally so timing reveals nothing). Tiers: control & write require the
  full token; reads accept full **or** read-only.
- **Concurrency.** Writes take an exclusive lock and flush embeddings (`Sync`) for durability; reads
  take a shared lock (parallel). One daemon owns one store dir; multi-process direct access is rejected
  in favor of the daemon. mTLS is opt-in and does NOT replace the token.

---

## 11. Storage & consistency guarantees

- **Meta (bbolt):** buckets `config`, `scopes`, `artifacts`, `traces`, `edges`. Record values are
  optionally encrypted at rest (AES-256-GCM); bbolt keys stay plaintext (needed for lookup). Owner-only
  file mode.
- **CAS (sha256 + zstd):** content-addressed, **crash-atomic** (write to a temp file + rename), with a
  decompression-memory cap. A half-written object never becomes visible under its hash.
- **EmbeddingStore (float32):** append-only; a write becomes visible only after a count-header
  commit-pointer advances past the record, with `fsync` on flush — a crash sees either the old count or
  the complete record; an over-counted header is clamped on reopen (fails safe, never panics).
- **runtime.json:** the daemon descriptor (token, addr, embedder identity); written atomically.

---

## 12. Removed from the v0.1 FSD

The v0.1 spec's strict-tree containment (I3) and bounded-determinism (I6) invariants; the formal
edge/graph model and graph-traversal semantics; the runtime-boundary "reasoning is not stored" rule
(v0.2 **inverts** it — reasoning is first-class and storable); structural-snapshot anchors and the
multi-step archive pipeline (v0.2 archive = mark the scope `Archived`); and the fixed
`Score.Compute` weight vector (v0.2 ranking is tunable and eval-measured). These are intentionally not
part of v0.2.
