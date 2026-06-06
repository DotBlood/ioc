# Research Roadmap — v0.3

> The **experiment program** that turns [`DREAM.md`](DREAM.md)'s findings into measured improvements.
> Distinct from [`ROADMAP.md`](ROADMAP.md): that tracks shipped product; this tracks **open research
> questions, the experiment that answers each, and the gate that decides whether to ship**. Every track
> is **experiment-first** — we build the minimal opt-in, measure it on IOC's own reproducible eval
> (`internal/eval` + `ioc wall`), and only then flip a default. No vendor benchmarks; honest negatives
> are kept (see graph-boost v1 in `WALL_EXPERIMENT.md`).
>
> Branch: `development/v0.3-research`. Method discipline: one experiment per run, recorded as a dated
> block in [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md).

---

## How a track works

`Question → Hypothesis → Method (corpus + metric) → Success gate → Ship-if-validated`. A track that
fails its gate is recorded honestly and the feature stays opt-in/off — we do not tune the corpus to the
result.

---

## R1 — Collapsed-tree retrieval *(IN PROGRESS, this run)*

- **Question.** Can a single flat pass over *all* levels (visible set ∪ all descendant-scope artifacts)
  beat both flat retrieval (blind to descendants) and hierarchical coarse→fine (which can route the
  correct scope away)?
- **Hypothesis.** Collapsed ≥ hierarchical on dense/distinctive corpora **and** fixes flat's
  descendant-blindness on tree-shaped corpora — making it the right *default*.
- **Why (evidence).** RAPTOR (arXiv:2401.18059) found collapsed-tree consistently beats top-down
  traversal; ReTreever (arXiv:2502.07971) found routing that drops the correct node hurts vs flat; IOC's
  own measurement (`WALL_EXPERIMENT.md`) shows flat recall 0.00 on tree corpora and hierarchical's coarse
  routing dropping the target's scope. See [`DREAM.md`](DREAM.md) §2.
- **Method.** Reuse `engine.descendantScopes`. New `collapsedCandidates = visibleArtifacts ∪
  all-descendant artifacts`. `gen-wall -shape tree` and a distinctive corpus; compare flat vs
  hierarchical vs collapsed recall@topK + currency on real bge-small.
- **Gate.** collapsed ≥ hierarchical on tree **and** ≥ flat on distinctive → **flip the default to
  collapsed**. Otherwise keep opt-in and record why.
- **Ship.** `Query.Collapsed` (opt-in) → CLI/wall/MCP `-mode collapsed`; flip default on a passed gate.

## R2 — Graph-boost v3: query-seeded Personalized PageRank

- **Question.** Does a 1–2 step PPR seeded on the query's top-cosine hits, over author-declared edges,
  measurably beat IOC's current seed-anchored PPR-lite reorder on multi-hop structural questions?
- **Hypothesis.** Query-seeded PPR (HippoRAG's mechanism) finds multi-hop dependents the 1-hop reorder
  misses — *if* the author-declared graph is dense enough for paths to exist.
- **Why.** HippoRAG (arXiv:2405.14831) is a literal precedent (single-step PPR, seed = query nodes).
  [`DREAM.md`](DREAM.md) §1. Open question: author-declared edges may be too sparse — HippoRAG relies on
  a dense LLM-extracted graph.
- **Method.** Formalize `blendGraph` as k-step PPR (seed = top-cosine, equal restart mass, damping ~0.5,
  re-rank over the vector candidate set — never the primary index). Add **cheap no-LLM synonym edges**
  (encoder-cosine links between near-duplicate summaries) to densify. Re-run the edged-corpus experiment
  + a new multi-hop (2+ edge) structural set.
- **Gate.** PPR beats the 1-hop v2 on multi-hop recall **without** regressing 1-hop or the no-edge
  no-op. Measure whether synonym edges are needed for paths to exist.
- **Ship.** Replace `blendGraph` internals (still opt-in `GraphBoost`); keep degree-normalize / kind
  filter as sub-experiments.

## R3 — Multi-signal ranking with author-declared importance

- **Question.** Does adding an **author-declared importance** signal (beside relevance + recency)
  improve ranking, staying true to no-LLM-in-core?
- **Hypothesis.** `relevance(cosine) + recency + importance` (importance from `Tier`: worktree=canonical
  > workspace=mutable, or an explicit author field) outranks the 2-signal baseline on a corpus where
  canonical truths should win ties.
- **Why.** Generative Agents' canonical `recency + importance + relevance` (UIST'23). [`DREAM.md`](DREAM.md)
  §3. Caveat: their all-weights=1 is untuned — copy the *structure*, tune weights on our eval.
- **Method.** Add an importance term derived from `Tier` (and/or an optional `PushRequest` importance
  field — author-declared, never LLM-scored). Sweep weights on a currency/importance eval; compare to
  cosine-only and cosine+recency.
- **Gate.** A weight setting beats the 2-signal baseline on importance-sensitive queries **without**
  hurting the proven wall.
- **Ship.** Opt-in ranking weights; default only if the gate passes.

## R4 — Confidence / abstention / calibration *(OPEN — research before code)*

- **Question.** What is the actual SOTA for retrieval confidence / selective prediction / abstention,
  and does it confirm IOC's empirical move from an absolute cosine floor to a top-1-vs-runner-up margin?
- **Status.** The dream's verification surfaced **no confirmed evidence** here — area 4 is the weakest
  covered. This is a **dedicated deep-research task first**, then code. Do NOT build the margin-aware
  weak_match before it.
- **Method.** A focused deep-research pass (conformal prediction for retrieval, selective prediction,
  margin calibration, RAG abstention). Then calibrate `weak_match = top<floor OR (margin>0 && margin<m)`
  per-embedder on wall data.
- **Gate.** A calibrated rule that separates present/absent on the wall better than the current floor,
  with a citable basis.

## Cross-cutting

- **Build IOC's own reproducible eval** — the through-line of the dream: do not chase vendor benchmarks
  (Zep/Mem0 LOCOMO numbers are disputed). Extend `internal/eval`/`ioc wall` to cover each track's metric.
- **Optional: author-rewritten superseding summaries** ("memory evolution" without an LLM in core) —
  a small ergonomics change to supersession; sequence after R1–R3.

## Rejected (with reason)

- **Mandatory LLM graph extraction as a default** — GraphRAG-Bench: graph loses on simple lookup and
  costs 10–40× tokens; Mem0g only ~2% over base. Keep author-declared edges (offer LLM-extraction only
  as an explicit opt-in for known-multi-hop corpora). [`DREAM.md`](DREAM.md) §1.
- **Late-interaction / multi-vector (ColBERT/PLAID) on current evidence** — the supporting claims were
  refuted in the dream's verification; the storage/complexity cost is real and the benefit unproven *for
  IOC's summary substrate*. Revisit only with new evidence. [`DREAM.md`](DREAM.md) §2, §6.
