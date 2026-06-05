# Fixing IOC retrieval & validating the "wall" — design (not quick fixes)

Status: design document. Drives the next implementation + review sessions. Grounded in a code-level
map of the query / ingest / eval paths (file:line references inline). Follows
[`VISION.md`](../VISION.md) §"The load-bearing risk".

## Why this exists

A dogfood run (IOC ingesting and then studying its own repo via MCP) produced **overview-sufficiency
0/4** and only **1/4 correct answers even with drill**. The cause is **not** a single "weak summaries"
issue — it is a set of concrete bugs in the retrieval/ingest code **plus several errors in the "wall"
theory itself**. This document separates the two, then lays out a correct, measured fix.

Confirmed decisions: document summaries = **extractive by default + an optional LLM hook**; embedder =
**fix the pipeline on bge-small first**, escalate to bge-base only if measurements still fall short.

---

## Part I — Root causes (with code evidence)

**RC1 — Document representation is incoherent (the core defect).** For `KindDocument`, `engine.Push`
embeds the **raw chunk** (`EmbedText`), while `Summary` is the label `path:lines — first line`
(`internal/ingest/reconcile.go:142-143`). But:
- **B1** — hybrid BM25 indexes the *label*, not the content: `bm.Add(a.ID, a.Summary)`
  (`internal/engine/query.go:72`). BM25 is near-useless for code.
- **B2** — the reranker scores the *label*, not the content: `docs[i] = byID[r.ID].Summary`
  (`internal/engine/rerank.go:24`). The cross-encoder sees `path:lines — first line`, never the code —
  this is why "rerank didn't help."
- **B3** — overview shows a label that was never embedded and does not represent the chunk, so the
  agent must drill just to learn what matched. The "mini-summary + embedding" model is violated for
  documents: shown ≠ embedded ≠ content.

**RC2 — Confidence is incoherent.** `Hit.Score` is **always cosine**, even after the reranker
reorders (`query.go:105` reads `cosineByID`; rerank only changes order). So `top_score / weak_match /
margin` (`internal/iocfmt/out.go:67-82`) don't reflect the real top and can be falsely confident
(dogfood Q2: cosine 0.747, `weak=false`, but the top hit was wrong). Cosine magnitude is a poor
calibrated signal under distractors (score compression).

**RC3 — Weak coarse routing.** Document rollups are `"directory X; files: a.go, b.go"`
(`internal/ingest/sync.go:90`), embedded verbatim. `coarseToFineCandidates`
(`internal/engine/rollup.go:54-115`) keeps the top ~CoarseK=6 scopes by rollup similarity; a semantic
query vs a filename list mis-routes, so the right scope is never selected and **its chunks are never
searched** (recall miss, dogfood Q1/Q4). This violates VISION's "summaries of summaries"
(VISION.md:102-103).

**RC4 — No provenance/currency in ranking.** Ingest tags everything `TierWorktree`
(`reconcile.go`); `close/`, `devlog.md`, `go.sum` are not in `skipDirs` (`internal/ingest/ingest.go:13-21`);
there is no `.iocignore`. Ranking is pure cosine; `Archived/CreatedAt/Version/DerivedFrom` are never
used. So historical v0.1 docs (a removed engine) semantically outrank the current code.

**RC5 — Evaluation blind spot.** The harness (`internal/eval`) has **never tested documents** — every
scenario uses reasoning insights with hand-written summaries. The green wall-metrics say nothing about
document retrieval; the dogfood was the first such measurement, and it lives outside the harness.

---

## Part II — Theory review (errors & corrections)

- **E1 (representation).** "mini-summary + embedding" is **not** one uniform mechanism. Two regimes:
  reasoning embeds the summary (shown == embedded); a document embeds raw content and shows a label.
  The theory must distinguish them explicitly.
- **E2 (metric).** Overview-sufficiency is mis-applied to code. For a document, success = "located the
  right chunk among hundreds of files and read it" — a drill is cheap and *expected*, not a failure.
  We need **per-Kind metrics**: reasoning → overview-sufficiency; document → recall@k of the right
  chunk + drill-cost (chunk tokens vs whole file/repo).
- **E3 (proxy).** Hybrid/rerank silently degrade because their "cheap representation" (Summary) is not
  a faithful proxy of a document's content. Lexical and rerank stages must run on the content.
- **E4 (recursive memory).** VISION calls for "summaries of summaries"; rollups are filename lists, so
  the coarse stage has no semantic signal. Risk #2 (navigability at scale) is unmet for documents.
- **E5 (relevance).** Relevance ≠ semantic similarity alone. There is no currency/provenance/validity
  axis, so stale-but-similar beats the truth. The data model already has `Tier/Archived/Version` —
  unused in ranking.
- **E6 (confidence).** Cosine-top vs a fixed floor is incoherent after rerank and yields false
  confidence. Needs: rerank score as the signal, margin, and a distractor guard.
- **Strategic correction.** IOC's unique value is **reasoning memory** (decisions/insights/constraints
  carried across versions), where it has no competitor; document retrieval competes with grep/LSP and
  only wins on conceptual queries via lexical+vector together. The dogfood stressed the weak, commodity
  case (whole-repo code search) and barely tested the unique value (reasoning memory).
  → Priority: (1) make document retrieval not-embarrassing, (2) **invest most in reasoning memory and
  its measurement** — that's where the real wall is.

**Corrected success model:**
- reasoning artifact: embed the summary; goal = overview-sufficiency (answer from the summary, no drill).
- document chunk: embed the content; lexical+rerank over content; overview = a meaningful extract
  (signatures/heading) for triage; goal = recall@k + cheap drill; provenance-aware ranking.

---

## Part III — Correct design (workstreams)

**W1 — Make document representation coherent (fixes RC1/E1/E3).**
- BM25 and the reranker must operate on **content**, not the label: for documents, index/score the
  chunk text (available in CAS / `Content`; load content for the top rerank candidates). Generalize:
  give an artifact a "lexical/rerank text" (the content / EmbedText-equivalent); keep `Summary` for
  display only.
- **Extractive structural summary** (default, no LLM): from the boundaries we already compute
  (`internal/ingest/lang.go` `isUnitStart`/`boundaries`), build a summary from the chunk's declaration
  signatures (func/type/class/heading) + symbols + path; show that as overview. Reasoning summaries
  unchanged.
- **Optional `Summarizer` hook** (flag): an ingest interface an external LLM can implement to replace
  the extractive summary with a semantic one; the core stays no-LLM, extractive is the fallback.

**W2 — Corpus hygiene & provenance (fixes RC4/E5).**
- `.iocignore` (gitignore-style globs) + sensible defaults: lock files (`go.sum`, `*.lock`),
  generated/minified, historical trees. Do not hardcode `close/` — make it configurable.
- Provenance: mark subtrees `archived/historical` (via `Meta` or `Tier`) and record currency. Ranking
  **down-weights/excludes** archived; by default, do not ingest historical docs.

**W3 — Rollup quality (fixes RC3/E4).**
- Replace filename lists with **semantic rollups**: summaries-of-summaries built from child signatures/
  summaries (extractive) or via the same `Summarizer` hook. This realizes recursive memory and fixes
  coarse routing. Alternative at small scale: flat hybrid + rerank with a larger candidate set —
  decided by W6 measurement.

**W4 — Ranking & confidence coherence (fixes RC2/E6).**
- When reranking, carry the **rerank score** into the hit (or both cosine + rerank); compute
  `weak_match` from the active signal (rerank if used, else cosine) + margin + a distractor guard.
  Remove the false confidence (Q2).
- Candidate sizing: ensure the right chunk reaches rerank (raise RerankN / fine-set size; at 593 chunks
  the cosine top-20 can miss it).

**W5 — Embedder/scale policy.** Stay on bge-small, ship W1–W4, measure. Escalation criterion to
**bge-base (768d)**: if repo-eval recall@k is still below target after the fixes, switch (and
recalibrate `core.ConfidenceFloor`). 2× storage is an accepted cost.

**W6 — Evaluation redesign (the lens; gates everything).**
- **Per-Kind metrics** (E2): document → recall@k of the right file:lines + drill-cost; reasoning →
  overview-sufficiency. The harness must distinguish Kind.
- **Repo-grounded document eval:** ingest a *clean* snapshot of this repo + 15–20 ground-truth
  questions Q→expected file:lines (the dogfood Q1–Q4 plus more). Automated, reproducible.
- **Distractor test:** include a known-stale doc and assert it does NOT outrank the current source.
- **Reasoning-memory eval (priority):** a scenario that models real development — record decisions/
  constraints, then across versions check overview-sufficiency and constraint-survival on the **real**
  embedder.

---

## Part IV — Staged plan (eval-first; every stage measured)

0. **W6 minimum first:** build the document eval (repo snapshot + Q→chunk) and per-Kind metrics; record
   the baseline (today's 0/4). Without this, fixes are blind.
1. **W1** (content-based rerank/BM25 + extractive summary) → re-measure. Expect the main jump.
2. **W2** (hygiene/provenance) → re-measure (distractors gone).
3. **W3** (semantic rollups) or a data-driven "flat+rerank" decision → re-measure.
4. **W4** (coherent confidence/candidates) → re-measure; kill false confidence.
5. **W5** if needed (bge-base) → re-measure.
6. **Reasoning-memory eval** (the unique value) → drive overview-sufficiency/constraint-survival on the
   real embedder toward the README targets (a≥0.80, d≥1.0).
Each stage is a separate commit with before/after numbers.

## Part V — Open risks / theory humility

- Document retrieval may stay weaker than grep/LSP on exact symbols — then position IOC honestly as
  lexical+vector for conceptual queries + reasoning memory, not "code search."
- Extractive summaries may be insufficient for code overview → use the LLM hook (already designed).
- bge-small may not scale to 600+ chunks even after the fixes → bge-base.
- The wall may not hold for reasoning either if authored summaries are poor — that's writing discipline,
  not code; the eval will show it.

## Part VI — Review plan (second block, up to 3 sessions)

- **Code review** (subagents by domain): storage (append-only/durability/concurrency), runtime
  (RWMutex/protocol/lifecycle), engine (visibility/branch/rollup/rerank), ingest
  (chunk/lang/reconcile/sig), cmd/MCP. Hunt for: races, edge cases, leaked handles, chunk-boundary
  errors, unchecked errors, test gaps. Each finding with file:line + a proposed fix; verify
  adversarially (don't trust the first impression).
- **Theory review:** re-test VISION's assumptions on fresh data — does the wall hold for reasoning; is
  the two-regime model right; are we over-investing in document retrieval; do the metrics reflect real
  value. Record conclusions in `docs/MODEL-CHANGES.md` / memory.

## Appendix — key code locations

- Query/ranking: `internal/engine/query.go` (candidate set, BM25 on Summary L72, cosine score L105,
  RRF L86, rerank gate L90).
- Rerank: `internal/engine/rerank.go` (passages = Summary L24, default N=20).
- Search primitives: `internal/search/` (cosine `brute.go`, BM25 + RRF `bm25.go`).
- Coarse→fine: `internal/engine/rollup.go` (`coarseToFineCandidates`, adaptive CoarseK).
- Confidence: `internal/iocfmt/out.go` `QueryOut`; `internal/core/types.go` `ConfidenceFloor`.
- Ingest/summaries/rollups: `internal/ingest/{chunk,lang,reconcile,sync,ingest}.go`.
- Eval/metrics/scenarios: `internal/eval/` (no document scenarios today).
