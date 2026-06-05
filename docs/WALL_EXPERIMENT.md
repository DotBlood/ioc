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
| currency — retrieval rank | stale #1 > current #2 | current above | **FAIL** |

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

## Verdict

The reasoning wall **holds at small scale on the real embedder** (0.96 answer-grounded,
no false confidence), with the single miss attributable to recall, not to the overview
being too thin. The decisive open item is **currency**: the experiment shows, on the real
embedder, that a superseded artifact outranks the current one and is only saved at the
answer level by self-marking summaries — which is not a mechanism, just luck. That result
is the falsifiable baseline #61 (the supersession mechanism) must move.
