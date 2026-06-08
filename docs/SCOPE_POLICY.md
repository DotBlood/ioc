# Scope policy — inducing tree structure (design / FRD)

> **STATUS 2026-06-08 — DESIGN ONLY (Phase 3, design-doc-first).** No code yet. This FRD specifies a
> **no-LLM, opt-in, advisory** mechanism that helps an agent keep memory tree-shaped. It changes NO
> default behavior: retrieval, visibility, and the runtime are untouched until the agent explicitly
> asks for advice and acts on it. Per the repo rule *ask before changing defaults*, anything here ships
> additive/off-by-default; nothing auto-forks, auto-consolidates, or moves artifacts. Implementation
> (P3.2) follows review of this doc.

## 1. The problem (the "we only work in one scope" gap)

IOC's recursive-scope model is sound, but **incomplete**: it provides the *mechanism* for a tree
(`CreateScope`/`Fork`/`Consolidate`/`CrossVersion`/`RollupScope`) yet nothing *induces* the tree. In
practice an agent dumps every insight into a single session scope, because there is no signal, no
affordance, and no automation that makes splitting the path of least resistance.

This matters because IOC's entire retrieval differentiation **depends on the corpus actually being
tree-shaped** (measured, `docs/WALL_EXPERIMENT.md`):

- On a flat, single-scope corpus IOC degenerates into ordinary flat vector RAG.
- The nested case is where IOC wins: `collapsed`/`hierarchical` retrieve a viewpoint's descendants,
  while plain `flat` is **structurally blind to descendant scopes (recall 0.00** on nested memory;
  locked by `engine.TestQuery_Hierarchical_DescendsIntoChild`). `collapsed` dominates on tree-shaped
  corpora (0.85 vs flat).

So if everything lands in one scope, the measured tree advantage **never engages**. The fix is not a
new retrieval mode — it is helping the agent *build the tree in the first place*.

## 2. What IOC has today (verified in code)

- Scope ops: `CreateScope` (`engine.go:287`), `Fork` (`branch.go:15`), `Consolidate` (`branch.go:69`),
  `CrossVersion` (`branch.go:113`), `RollupScope` (`rollup.go:14`). `core.Scope` roles
  worktree/workspace/session are **conventions, not hierarchy** (`core/types.go:100`).
- Cheap per-scope data: `meta.ArtifactsInScope(scope)` (`meta.go:220`); per-artifact embeddings via
  `e.emb.Get(EmbRef)`; brute cosine + `dot()` (`search/brute.go:33-62`).
- **No** variance/dispersion/clustering API anywhere; **no** signal that a scope is over-broad.
- Precedents to imitate:
  - *No-LLM, author-declared* mechanisms: supersession (`docs/SUPERSESSION.md`) and knowledge edges —
    IOC keeps the bookkeeping, the agent/LLM makes the semantic call. Scope policy follows the same rule.
  - *Opt-in, off-by-default re-rank signal*: `blendGraph` (`query.go:89-150`) — a structural axis that
    only activates when explicitly requested and leaves the proven default path untouched.

## 3. Principle & non-negotiable constraints

- **IOC never calls an LLM in its core.** Therefore IOC can only *signal mechanically*; the decision to
  fork/consolidate stays with the agent. (Same boundary as supersession and edges.)
- **Advisory only, opt-in, off by default.** A new `ioc scope-advise` (and engine `ScopeStats`) is a
  read-only report. It is the only entry point; if you never call it, nothing changes.
- **No auto-restructuring.** No automatic fork, no automatic consolidate, no re-homing of artifacts. The
  agent reads the advice and acts (or not).
- **No default-behavior change.** Query/visibility/runtime defaults, the wall numbers, and existing
  tests are all untouched.

## 4. The signals (no-LLM, mechanical)

Computed over a scope's OWN artifacts (`ArtifactsInScope` + their summary embeddings):

1. **Artifact count** — cheap gate; advice is meaningless for a near-empty scope.
2. **Dispersion** — how topically spread the scope is. Candidate metric: **mean pairwise cosine** over
   the scope's artifact embeddings (low mean ⇒ diverse ⇒ a split candidate). Centroid variance is an
   alternative; the exact form is decided in P3.2 with a quick measurement (§8). Honest caveat:
   dispersion is **noisy at small N**, hence the count gate.
3. **Topic-cluster count** — the load-bearing signal. Build a cosine-similarity graph over the scope's
   embeddings (undirected edge when `cos(i,j) ≥ τ`); its **connected components are the topic
   clusters**. `K = #components`. `K ≥ 2` with cohesive, non-trivial components ⇒ the scope is holding
   several distinct topics that *should* be sub-scopes.
   - **Algorithm: threshold-based connected components / single-linkage agglomerative.** Chosen over
     k-means because it is **deterministic** and **parameter-light** (one threshold `τ`, no `k` to
     guess, no random seeding — and `Math.random` is avoided in this codebase). It runs on the existing
     brute-cosine primitive; no new heavy dependency.

A small combined **recommendation** ("split" / "ok" / "too-small") is derived from count + K +
dispersion (thresholds in §7), with a one-line human explanation.

## 5. Proposed API surface (P3.2 — for review, not yet built)

- **`internal/cluster`** (new, pure, leaf package): `ThresholdComponents(vectors [][]float32, τ float64)
  [][]int` → connected components by cosine ≥ τ. No `core` import; table-tested in isolation.
- **`engine.ScopeStats(ctx, scope) (core.ScopeStats, error)`** where
  `core.ScopeStats{ArtifactCount int; Dispersion float64; ClusterCount int; Clusters [][]core.ID;
  Recommendation string; Reason string}`. Read-only; builds the embedding set from
  `ArtifactsInScope`+`emb.Get` and calls `cluster.ThresholdComponents`.
- **CLI `ioc scope-advise -scope ID [-tau f] [-min-artifacts n]`** → `printJSON(ScopeStats)`. Pure result
  on stdout (Phase-1 stdout invariant), advisory copy in the JSON.
- **Runtime**: a **read-tier** op `scope_stats` (Service/proto/dispatch/client) so it works through a
  running daemon — mirrors the existing read ops; no write lock.
- **Thresholds are config-tunable** (`scope.split.tau`, `scope.split.min_artifacts`,
  `scope.split.min_cluster_size`), defaulting per §7. Reuses the existing per-store config bucket.
- **MCP**: a `ioc_scope_advise` hint tool is **deferred** (repo guidance: avoid MCP time-sinks); the
  CLI/engine API lands first.

## 6. What this deliberately does NOT do (documented limits, not silent gaps)

- No automatic fork/consolidate and no artifact re-homing — *mechanical re-structuring* is a separate,
  riskier future step (P3.3), gated on this advisory proving useful.
- No LLM-based topic labelling — clusters are unnamed index groups; naming a sub-scope is the agent's job.
- No change to retrieval/visibility defaults.
- No cross-scope (sibling/parent) advice in v1 — only "is THIS scope over-broad". The symmetric signals
  (scope too small, or near-duplicate of a sibling → *consolidate* advice) are a natural follow-up but
  out of scope here.

## 7. Thresholds & defaults (tunable, opt-in)

- **`τ` (cluster edge threshold)** — default to the embedder's calibrated **`core.ConfidenceFloor`**
  (~0.68 for bge-small): two summaries above the floor are "about the same thing", which is exactly the
  semantics we want for "same topic". Reusing the calibrated floor avoids a second calibration knob.
- **`min_artifacts`** — default ~8; below this, dispersion/clusters are too noisy to advise on.
- **`min_cluster_size`** — default ~2; singleton components are outliers, not a topic worth its own scope.
- **Recommendation = "split"** when `ArtifactCount ≥ min_artifacts` AND `ClusterCount ≥ 2` AND at least
  two components have size `≥ min_cluster_size`. Otherwise "ok" (or "too-small" below the gate).
- All defaults documented in the advisory output; the agent stays the decision-maker.

## 8. Validation plan (measurement-first, per repo ethos)

The doc is not done until P3.2 ships a falsifiable check, mirroring `WALL_EXPERIMENT.md`:

1. **Detection correctness** — build a single scope dumped with N artifacts drawn from `C` known topics
   (reuse `gen-wall`/`gen-scenario` cluster generators). `scope-advise` must report `ClusterCount ≈ C`;
   a genuinely single-topic scope must report `1` (low false-positive rate is the key guardrail).
2. **The payoff hypothesis (closes the loop with the wall)** — if an agent splits the flat dump per the
   advice (one sub-scope per cluster) and queries the parent with `collapsed`/`hierarchical`, recall must
   improve versus the flat dump — reproducing the measured tree>flat gain (§1). If it does not, the
   signal is not worth shipping. This is the real success criterion, not the clustering metric alone.

## 9. Phasing

- **P3.1 — this design doc** (review gate before any code).
- **P3.2 — implementation**: `internal/cluster` + `engine.ScopeStats` + `ioc scope-advise` + read-tier
  runtime op + tests + the §8 validation. (Sonnet implements the cluster package & plumbing to spec;
  Opus reviews; defaults/thresholds confirmed against the §8 measurement.)
- **P3.3 — deferred**: MCP hint tool; mechanical re-structuring assist (suggest+apply a fork-by-cluster);
  symmetric consolidate advice; an author-declared importance signal.

## 10. Open questions (resolve in P3.2)

- **Dispersion metric** — mean pairwise cosine vs centroid variance; pick by a quick measurement on the
  §8 corpora.
- **`τ` source** — reuse `ConfidenceFloor` (preferred) vs a dedicated `scope.split.tau` calibration.
- **Where advice surfaces** — `ioc scope-advise` only (v1), or also a soft hint inside `query` trace
  output when a viewpoint scope looks over-broad (kept off the default query path either way).
