# Scope policy — inducing tree structure (design / FRD)

> **STATUS 2026-06-09 — IMPLEMENTED (P3.2) AS A STRUCTURAL ADVISORY; RECALL PAYOFF NOT CONFIRMED.**
> The `no-LLM, opt-in, advisory` mechanism shipped (`engine.ScopeStats` / `ioc scope-advise`) and changes
> NO default behavior. BUT the §8 payoff measurement (real bge) **falsified the recall premise** of §1:
> acting on the advice does NOT reliably improve retrieval. The proven default mode (`collapsed`) is
> partition-independent and already ties flat; `hierarchical`'s outcome is dominated by ROLLUP quality
> (which needs an LLM IOC won't call), not by the clustering. An earlier "payoff confirmed 0.93" reading
> was an artifact of a rollup that leaked member-summary text into the routing key (§8). So scope-advise
> stands as a **structural/navigational detector** ("this scope holds K topics"), not a measured recall
> win. The τ default stays `ConfidenceFloor` (the "recalibrate to 0.75" idea was dropped — it rested on
> the artifact). Nothing auto-forks/consolidates/moves artifacts.

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
  riskier future step (P3.3). **It was gated on this advisory proving useful; the §8 payoff did NOT
  confirm a recall benefit, so P3.3's rationale must be re-examined before it is built.**
- No LLM-based topic labelling — clusters are unnamed index groups; naming a sub-scope is the agent's job.
- No change to retrieval/visibility defaults.
- No cross-scope (sibling/parent) advice in v1 — only "is THIS scope over-broad". The symmetric signals
  (scope too small, or near-duplicate of a sibling → *consolidate* advice) are a natural follow-up but
  out of scope here.

## 7. Thresholds & defaults (tunable, opt-in)

- **`τ` (cluster edge threshold)** — defaults to the embedder's calibrated **`core.ConfidenceFloor`**
  (~0.68 for bge-small), overridable via `scope.split.tau`. CAVEAT (§8, 2026-06-09): τ controls cluster
  granularity but, because the recall payoff is unconfirmed, **no τ is recall-validated**. A dedicated
  higher split-τ (~0.75) was tested and rejected — its apparent win was a rollup-leak artifact, not τ.
  Treat τ as a granularity knob for the structural report, not a tuned retrieval parameter.
- **`min_artifacts`** — default ~8; below this, dispersion/clusters are too noisy to advise on.
- **`min_cluster_size`** — default ~2; singleton components are outliers, not a topic worth its own scope.
- **Recommendation = "split"** when `ArtifactCount ≥ min_artifacts` AND `ClusterCount ≥ 2` AND at least
  two components have size `≥ min_cluster_size`. Otherwise "ok" (or "too-small" below the gate).
- All defaults documented in the advisory output; the agent stays the decision-maker.

## 8. Validation — RESULTS (real bge-small, 2026-06-09; `internal/eval/payoff_test.go`)

Falsifiable check, real embedder, same corpus (real wall + synthetic distractors, 118/328/628 artifacts —
identical at all scales) in flat vs tree layouts; **gold-recall@5**. The decisive variable turned out to
be the ROLLUP, so it was made a control (`IOC_PAYOFF_ROLLUP`), holding the PARTITION fixed:

| rollup synthesis | mech-tree τ=0.75 (hier) | hand-tree (hier) | flat (vector) | any tree (collapsed) |
|---|---|---|---|---|
| `join` — concatenate ALL member summaries (LEAKS gold text into the rollup) | 0.93 | 0.89 | 0.85 | 0.85 |
| `first` — one representative member summary (realistic short rollup) | 0.89 | 0.63 | 0.85 | 0.85 |
| `label` — content-free placeholder | 0.41 | 0.70 | 0.85 | 0.85 |

**Verdict: the recall payoff is NOT confirmed.** Reading the controls:

1. **`collapsed` = flat = 0.85, always** — independent of the partition and τ. Splitting buys the *proven
   default mode* **nothing** (it descends into all descendants regardless). Consistent with R6 in
   `WALL_EXPERIMENT.md` ("coarse routing beats collapsed in no measured regime").
2. **`hierarchical` is dominated by rollup quality** (0.41 → 0.93), not by the clustering. The initial
   "0.93 payoff" used `join`, whose rollup literally contains the gold summary → the coarse stage routes
   trivially. That is a harness leak, not a real signal. With a realistic one-summary rollup (`first`) the
   mechanical tree gives 0.89 — only +0.04 over flat, fragile, and the hand-authored tree *drops* to 0.63.
3. A good rollup is exactly what IOC **cannot** synthesize mechanically (`RollupScope` expects an
   LLM-authored summary; the core never calls an LLM). So the lever that would make hierarchical win is
   not available to the no-LLM signal.

**Consequence for the feature:** scope-advise is kept as an honest **structural detector** (it does find
topic clusters) with navigational value, but it must NOT claim a retrieval-recall improvement. The τ
default stays `ConfidenceFloor`; a dedicated higher split-τ was considered and dropped (it only "won" via
the rollup leak). Auto-restructuring (P3.3) is **no longer justified by a measured payoff** — revisit its
rationale before building it.

### Original validation plan (kept for reference)

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
- ~~**`τ` source**~~ **RESOLVED (§8, 2026-06-09):** kept at `ConfidenceFloor`. A dedicated higher split-τ
  was tested and dropped — it only "won" via a rollup-leak artifact, and no τ delivers a confirmed recall
  payoff. τ is a granularity knob for the structural report, not a tuned retrieval parameter.
- **Where advice surfaces** — `ioc scope-advise` only (v1), or also a soft hint inside `query` trace
  output when a viewpoint scope looks over-broad (kept off the default query path either way).
