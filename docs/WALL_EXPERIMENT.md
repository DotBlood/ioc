# The reasoning-wall experiment

> Falsifiable test of IOC's core bet: **is mini-summary + embedding enough that an
> external agent can answer real questions from the cheap overview alone, and does
> retrieval surface the *current* truth over a *superseded* one?**
>
> This replaces the old, gameable metric. The scenario harness (`eval.Run`) scored
> "overview-sufficiency" by checking whether an author-chosen keyword appeared in an
> author-written summary — circular, and green even when the wall does not hold (see
> `REVIEW_FINDINGS.md`, theory T1). The wall harness scores the real question instead.

## Method

- **Corpus** (`internal/eval/scenarios/wall.json`): 28 real distilled decisions from
  this project's own history, pushed as `KindReasoning`, spanning the v0.1→v0.2
  boundary. Summaries are written as an external LLM would author them (1–2 sentences),
  not keyword lists. A planted **currency pair** co-locates the superseded v0.1 belief
  ("IOC's memory is a knowledge-graph engine") and the current v0.2 truth ("a thin slice:
  mini-summary + embedding; no knowledge graph") in one queryable scope — the faithful
  reproduction of the unenforced-supersession problem (the read path marks nothing stale).
- **Questions**: 27 blind questions, phrased differently from the summaries, each with a
  fixed **gold** answer (withheld from the judge). One probe asks an **absent** question
  (no artifact answers it) to catch false confidence; one is the **currency** probe.
- **Harness** (`ioc wall <spec> -embed …`, `internal/eval/wall.go`): builds the corpus,
  runs overview-only retrieval per question, and emits two files — `packets.jsonl`
  (question + retrieved summaries, **gold withheld**) for a blind judge, and `gold.jsonl`
  (reference answer + objective, code-measured retrieval facts: gold-ref ranks and the
  current-vs-superseded rank). IOC never calls an LLM; judging is out-of-process.
- **Judge**: fresh blind subagents that see ONLY a packet (no gold, no corpus) and answer
  strictly from the retrieved notes or reply `INSUFFICIENT`. A separate grader subagent
  scores each answer against gold (it sees question+answer+gold, never the corpus).
- **Config**: bge-small (real, 384d), plain vector, topK=5, no reranker — the strongest
  mode at this corpus size (hybrid/hierarchical hurt at small N; see `CLAUDE.md`).

## Results (2026-06-05, BAAI/bge-small-en-v1.5)

| metric | value | target | verdict |
|---|---|---|---|
| answer-grounded overview-sufficiency | **26/27 = 0.96** | ≥0.80 | **PASS** |
| gold-ref recall@topK (objective) | 0.93 | — | — |
| absent probe (no false confidence) | abstained correctly | abstain | **PASS** |
| currency — answer level | current, not stale | current | **PASS** |
| currency — retrieval rank (before #61) | stale #1 > current #2 | current above | **FAIL** |
| currency — retrieval rank (after #61) | current #1, stale excluded | current above | **PASS** |

**Reading it honestly:**

- **The wall mostly holds at this scale.** For every question whose answer-bearing
  artifact was retrieved (25/26 answerable), the blind judge produced a gold-correct
  answer from the overview summaries alone — no drill to raw content. The summaries are
  sufficient.
- **The one failure is a *retrieval* miss, not an overview-insufficiency.**
  `q-scale-cosine` ("at what size does similarity search fail?") did not retrieve its
  gold artifact (R1): the corpus is dense with retrieval-topic artifacts that crowded it
  out of the top-5. The summary would have sufficed had it been retrieved. So the wall
  bet (summary is enough) is not what failed here — recall is. At larger scale recall is
  exactly where IOC needs hierarchical retrieval + rerank (the measured scale levers).
- **Absent probe passed:** asked which database IOC uses to bill customers, the blind
  judge correctly answered `INSUFFICIENT` rather than inventing one.
- **Currency is the real open problem, and the experiment localizes it precisely.** At
  the **retrieval-rank** level the probe FAILS: the superseded v0.1 summary outranks the
  current v0.2 one (rank 1 vs 2) on the real embedder, because nothing in the read path
  demotes stale artifacts (`DerivedFrom`/`Archived` are unused — see `SUPERSESSION.md`).
  At the **answer** level it PASSED only because these particular summaries *self-mark*
  their version ("original v0.1 design" / "current v0.2 direction"), so the judge could
  disambiguate. Real summaries usually do **not** carry such markers, and even when they
  do, the stale artifact still consumes a top-K slot and can crowd out a different useful
  result (the same mechanism that lost `q-scale-cosine`). This is the motivation for the
  supersession mechanism (#61): mark superseded artifacts and exclude/demote them by
  default, with an explicit "show history" path.

## Caveats / threats to validity

- **Scale.** 28 artifacts is small; recall@topK is near-ceiling here. The known wall is
  ~180 dense artifacts (vector recall ~0.33), where this same test should be re-run with
  hierarchical + rerank to see whether overview-sufficiency survives the recall drop.
- **Authoring independence.** The corpus, questions, and gold were authored within one
  agent lineage. Mitigations: the *judge* is a fresh blind subagent (sees only packets);
  the *grader* is a separate subagent (never sees the corpus); questions are phrased to
  differ in surface wording from summaries, so a pass requires meaning, not keyword
  overlap. It is not fully adversarial; an externally-authored question set would be
  stronger.
- **Judge batching contamination — found and corrected.** The first judging pass put 9
  packets in one subagent prompt; for `q-scale-cosine` (whose gold was *not* retrieved)
  the judge borrowed R1's text from a sibling packet's notes and produced a falsely
  grounded answer. Re-running that question in a strictly isolated single-packet judge
  exposed it: with only its real notes the judge could not ground the answer (it
  hallucinated the "~180" figure, absent from its notes). Verdict corrected to FAIL.
  **Methodology rule going forward: one packet per judge invocation** (no batching), so
  no question can borrow another's retrieved context.
- **Currency answer-level pass is fragile** — it depends on version-marked summaries, as
  noted above. Do not read it as "currency works."

## Reproduce

```bash
# build packets + gold on the real embedder (py server must be running)
go run ./cmd/ioc wall internal/eval/scenarios/wall.json -embed http://127.0.0.1:8088 -out .ioc/wall
# packets.jsonl  -> blind judge subagents (ONE packet per invocation; notes only)
# gold.jsonl     -> grader subagent (question + judge answer + gold; no corpus)
# objective retrieval facts (gold-ref ranks, current-vs-superseded) are in gold.jsonl
```

## Scale (~180 artifacts, 2026-06-05)

To test whether the wall survives the recall drop at the known hard scale, the **same 27
real questions + gold + their real artifacts** were padded with **~152 synthetic distractor**
reasoning artifacts (generic infra decisions) to 180, via a reusable generator
(`ioc gen-wall`, `internal/eval/scenarios/wall-180-{flat,tree}.json`). The questions/gold
stay genuine; the distractors create the recall pressure. `ioc wall` gained `-mode/-coarsek/
-rerank`. Run on real bge-small, topK=5; gold-ref recall@topK and currency are code-measured.

| corpus / config | gold-ref recall@topK | currency |
|---|---|---|
| flat / vector | 0.85 | 1/1 |
| **flat / vector + rerank** | **0.93** | 1/1 |
| tree / hierarchical | 0.74 | 1/1 |
| tree / hierarchical + rerank | 0.74 | 1/1 |
| tree / hierarchical, coarseK=10 | 0.78 | 1/1 |

Blind-judged (one packet per judge; isolated for the recall-miss and the absent probe) +
graded vs gold in the best config (**flat + rerank**): **answer-grounded sufficiency 27/27**.

**Reading it honestly:**
- **The wall holds at 180.** Flat vector recall fell only 0.93→0.85 going 28→180 — the
  distinctive real reasoning artifacts resist the scale crater (unlike the near-duplicate
  generic scale corpus where flat vector collapses to ~0.33). **Rerank recovers 0.85→0.93**:
  the cross-encoder promotes a gold the embedding ranked 6–20 back into the top-5. Every
  retrieved gold summary was answerable by the blind judge; distractors that leaked into the
  lower slots (PostgreSQL, JWT, Redis…) did **not** mislead it.
- **Hierarchical HURTS this corpus (0.74), and rerank can't save it.** This refines the
  CLAUDE.md claim "hierarchical is best at scale": that holds only for **dense near-duplicate
  clusters**, where flat vector cannot separate hits. For **distinctive** reasoning artifacts
  scattered among topical distractors, the coarse stage routes the query to the wrong session
  and **drops the gold artifact's scope entirely** — so rerank never sees it (still 0.74), and a
  wider coarseK only nudges it to 0.78. Lesson: pick the retrieval mode by corpus density, not
  by scale alone; flat + rerank is the right default for a distinctive reasoning corpus.
- **Currency holds at scale (1/1 in every config).** The superseded v0.1 artifact is excluded
  from candidates regardless of corpus size or retrieval mode — #61's mechanism is robust at 180.
- **No false confidence under distractor pressure.** The absent probe ("which database does IOC
  use to BILL customers?") now retrieves only distractors about a PostgreSQL *storage* decision;
  the blind judge still correctly answered **INSUFFICIENT**, distinguishing "storage engine" from
  "billing." This is the strongest honesty signal of the run.
- **Honest caveat on the 27/27.** `q-scale-cosine` still does not retrieve its own gold artifact
  (R1) — at 180 it passed only because a *sibling* note ("…0.94 recall at 180 vs 0.33 for vector
  alone") happened to land in its top-5 and grounded the size, while the judge explicitly flagged
  that the mechanism was not in its notes. So the wall's one weak spot is unchanged; it was merely
  answerable from a neighbour this time. Also: synthetic distractors are less adversarial than a
  real foreign corpus — see the document-ingest 0/4 result, which this experiment does not refute.

## Verdict

The reasoning wall **holds on the real embedder at both 28 and ~180 artifacts** — 0.96 and
27/27 answer-grounded, no false confidence even under distractor pressure — with the single
weak spot (`q-scale-cosine`) attributable to recall, not to the overview being too thin. The
decisive scale lesson is that **retrieval mode must match corpus density**: flat + rerank is the
right default for distinctive reasoning memory; hierarchical helps only dense near-duplicate
corpora and otherwise drops the target by misrouting.

**Currency — moved (#61).** The baseline showed, on the real embedder, that a superseded
artifact outranked the current one and was only saved at the answer level by self-marking
summaries (luck, not a mechanism). The supersession mechanism (`docs/SUPERSESSION.md`)
fixed it: re-running the same spec with `M_NEW` declaring `supersedes: [M_OLD]`, the
superseded v0.1 artifact is excluded from the candidate set entirely and the current v0.2
one ranks #1 — currency 1/1 at the retrieval-rank level. The freed top-K slot is taken by
a different useful artifact instead of a stale near-duplicate. This no longer depends on
the summary self-marking its version.

Re-run after #61 (only the currency line changes):

```bash
go run ./cmd/ioc wall internal/eval/scenarios/wall.json -embed http://127.0.0.1:8088 -out .ioc/wall
# currency probes: 1/1 current outranked superseded (retrieval only)
```
