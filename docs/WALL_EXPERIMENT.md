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

---

## 2026-06-06 re-run — shape-vs-mode matrix corrects the "hierarchical hurts" claim

A fresh full re-run on real bge-small (topK=5, code-measured gold-ref recall@topK +
currency; 27 blind-judge packets graded for the 28-corpus). Two things changed vs the
2026-06-05 record: the **full 4-mode sweep was run on EACH corpus shape** (the earlier table
mixed shapes), and the wall-28 answer grading was repeated with isolated blind judges.

### Wall-28 (hand-authored, real artifacts)

| config | gold-ref recall@topK | currency | answer-grounded (blind judge) |
|---|---|---|---|
| vector | 0.93 | 1/1 | 23 correct + correct INSUFFICIENT on the absent probe |
| vector + rerank | 0.93 | 1/1 | (rerank cannot change recall — see below) |

Blind judging used one isolated subagent per packet (Haiku). Of the 4 non-answers, **1 is a
genuine retrieval miss** (`q-scale-cosine` — its gold R1 is not in topK, rank 0), **1 is the
absent probe answered correctly as INSUFFICIENT** (no false confidence), and **2 are
over-strict judge abstentions** (`q-weak-floor`, `q-ingest-heal`) where the answering note was
retrieved at **rank 1** but the Haiku judge pedantically declined. Adjusting for that judge
noise, answer-grounded sufficiency is **~25/26 ≈ 0.96**, matching the prior record; the lone
real weak spot (`q-scale-cosine`) is unchanged. **Rerank does not move wall-28 recall
(0.93→0.93)** because the missing gold is outside the cosine candidate window — a direct,
independent confirmation of the rerank-window theory.

### 180 scale — the SAME 4 modes on BOTH shapes (this is the correction)

| corpus shape | vector | vector+rerank | hierarchical | hierarchical+rerank | currency |
|---|---|---|---|---|---|
| **flat** (all artifacts in one visible session) | 0.85 | 0.93 | **0.96** | 0.93 | 1/1 |
| **tree** (real artifacts in child sessions of the query viewpoint) | **0.00** | 0.00 | 0.74 | 0.74 | 1/1 |

**The earlier "hierarchical HURTS distinctive (0.74) vs flat+rerank (0.93)" was an
apples-to-oranges comparison across two different shapes** — flat-vector 0.85 was measured on
the *flat* spec, hierarchical 0.74 on the *tree* spec. Measured on a **single, consistent
shape**, hierarchical never loses here:
- **flat shape:** hierarchical **0.96** ≥ flat+rerank 0.93 ≥ flat vector 0.85.
- **tree shape:** flat vector **collapses to 0.00**, and hierarchical (0.74) is the *only* mode
  that retrieves anything.

**Why flat = 0.00 on the tree shape (structural, not density):** in the tree spec all 27
questions query from the workspace `ws`, while the real artifacts live in `ws`'s **child
sessions** (`retrieval/storage/runtime/direction`). IOC's bottom-up visibility
(`visibleArtifacts`) shows a viewpoint its own + ancestor + *published-sibling* artifacts —
**never its descendants.** So a flat query from `ws` cannot see artifacts that live below it; the
gold is not even a candidate (recall 0, deterministically across all 27 questions).
`coarseToFineCandidates` (hierarchical) *does* descend into the viewpoint's child scopes, so it
is the only mode that finds them. (This is exactly the invariant locked by the new unit test
`engine.TestQuery_Hierarchical_DescendsIntoChild`.)

**Corrected lesson.** Pick the retrieval mode by **where the artifacts sit relative to the query
viewpoint**, not by density alone:
- If the answer artifacts are **visible to the viewpoint** (own/ancestor/published-sibling — the
  "flat" shape), every mode works and **hierarchical is at least as good as flat+rerank** (0.96
  vs 0.93 here) — hierarchical does *not* hurt a distinctive corpus.
- If the answer artifacts live in **descendant scopes** (the normal nested-scope case when you
  query from a parent), **flat retrieval is structurally blind (0.00) and hierarchical is
  mandatory.** This is the dominant real-world case for nested worktree/workspace/session memory.

Currency is **1/1 in all eight runs** (both shapes × four modes) — #61's mechanism is robust
across shape and mode. The absent-billing probe remains INSUFFICIENT — no false confidence.

### Reproduce

```bash
go run ./cmd/ioc wall internal/eval/scenarios/wall.json -embed http://127.0.0.1:8088 -out .ioc/wall28          # 0.93, currency 1/1
go run ./cmd/ioc gen-wall -out .ioc/wall-180-flat.json -shape flat -n 180
go run ./cmd/ioc gen-wall -out .ioc/wall-180-tree.json -shape tree -n 180
for s in flat tree; do for m in "vector" "vector -rerank" "hierarchical" "hierarchical -rerank"; do
  go run ./cmd/ioc wall .ioc/wall-180-$s.json -embed http://127.0.0.1:8088 -out .ioc/w-$s -mode $m; done; done
```

> NOTE: the CLAUDE.md "scale levers" table (flat 0.39 / hierarchical 0.94 / +rerank 1.0) was
> measured on the **dense near-duplicate `gen-scenario` corpus**, a different generator than
> `gen-wall`; it is not directly comparable to these distinctive-corpus numbers and was not
> re-run here.

---

## 2026-06-06 — graph-aware retrieval benefit + a blind-agent knowledge-base study

A real edged corpus (20 hand-authored one-sentence facts ABOUT IOC itself + 13 author-declared edges:
`depends_on`/`refines`/`answers`), seeded on real bge-small. Goal: does the structural axis (edges)
help answer questions similarity cannot, and how good is IOC as a *blind* agent's knowledge base.

### Part A — objective (code-measured), 4 semantic + 4 structural questions

Structural questions ask "what depends on / relies on X", where the gold answer is **edge-connected to a
strong semantic hit but NOT textually similar to the question** (e.g. "if we replaced bbolt, what's
affected?" → the artifacts that `depends_on` bbolt, which never mention "replacing bbolt").

| tool | semantic Qs | structural Qs |
|---|---|---|
| plain `query` (overview) | answers (top-1 right 3/4) | finds the *subject*, scatters/misses the *dependents* |
| **`related` edge-walk** (`-direction in/out -kind depends_on`) | n/a | **exact, noise-free gold every time** (4/4) |
| `query -graph-boost 0.4` | no change | **does NOT help — centrality-biased** |

**Key finding — `Related` (explicit traversal) is the structural win; `graph-boost` v1 is flawed.**
`related` returned exactly `{daemon, edges-bucket}` for "depends on bbolt", `{Neighbors, Consolidate}`
for "depends on supersession", `{token-auth}` for "depends on the daemon", `{edges, query}` for "what
graph-aware retrieval relies on" — precise, with no noise. The implicit `-graph-boost` blend, by
contrast, lifts **global hubs** (the highly-connected `progressive-disclosure` and `graph-aware`
artifacts) on *every* query regardless of the question, and on one structural question it *demoted* the
correct answer (token-auth) from rank 2 to rank 5. Root cause: `g(c)=Σ neighbours' cosine` rewards node
**degree (centrality)**, not connectivity to the *query's* strong hits. So graph-boost v1 is not
query-anchored.

### Part B — blind agent (no context, no repo files, IOC CLI only)

A fresh general-purpose subagent was given only the store + the `ioc` CLI and the 8 questions (no gold,
no labels), forbidden from reading repo files or using prior knowledge. In 8 commands it answered **8/8
correctly**, and independently concluded — unprompted — that **the `related` edge-walk "won decisively"
for the dependency questions** (exact, noise-free), that **plain `query` outright failed to surface the
dependents on the supersession question**, and that **`-graph-boost` was "neutral-to-mildly-helpful and
noticeably noisier… a weaker proxy"** that "scrambled the score ordering" and "never surfaced anything
the edge-walk missed". It also noted IOC's confidence was honest (a broad query correctly flagged
`weak_match=true` with a tiny margin on an ambiguous question).

### Verdict

- **The unified vision is validated on the structural axis: `Related` makes author-declared edges a
  reliable, exact retrieval path for questions similarity cannot answer** — confirmed independently by a
  blind agent. The knowledge graph works, with no LLM extraction.
- **`graph-boost` v1 (the implicit edge-blend into `Query`) does NOT yet add value** — it is
  centrality-biased, noisier than `related`, and occasionally harmful. It stays OFF by default (no
  regression — proven separately), but it is **not the way to do structural retrieval today**.
- **v2 redesign (gates shipping graph-boost as anything but off):** anchor the boost to the *query's*
  top hits (personalized-PageRank seeded at strong hits) and/or degree-normalize `g`, so it rewards
  "connected to what the query matched", not "globally well-connected". Until then, prefer `related`.
- **IOC as a blind agent's knowledge base: it works.** Cheap overviews sufficed for factual recall;
  `related` carried the structural questions; confidence flags were honest.

Reproduce: seed an edged corpus, then `bin/ioc query … -graph-boost 0` vs `0.4` vs `bin/ioc related
-artifact <subject> -direction in -kind depends_on`.

### Follow-up the same day — graph-boost v2 (seed-anchored) fixes the centrality bias

The v1 flaw (g = Σ ALL neighbours' cosine rewards node degree) was fixed by anchoring g to the query's
**strong hits**: a seed set = the top-`graphSeedK` candidates by cosine, and a candidate is boosted only
for its edges to those seeds — "connected to what the query matched", not "globally well-connected". The
edged-corpus structural questions were re-run (real bge-small, `query -graph-boost 0` vs `0.4`):

| structural question | gold | v1 (`graph-boost`) | **v2 (seed-anchored)** |
|---|---|---|---|
| replace bbolt — affected? | {daemon, edges-bucket} | only edges; hubs surfaced | edges (r3) **+ daemon (r4)** |
| graph-aware retrieval relies on? | {edges, query} | neither | **edges (r4) + query (r5)** |
| what depends on the daemon? | {token-auth} | demoted gold rank 2→5 | **gold preserved at rank 2** |
| what depends on supersession? | {Neighbors, Consolidate} | only hubs | **both at ranks 1–2** |

v2 lifts the correct dependents into top-K on all four (and stops demoting an already-correct hit); the
global hubs (`progressive-disclosure`, `graph-aware`) no longer dominate unrelated queries. A unit test
`TestGraphBoost_AnchoredNotCentrality` encodes the fix (a high-degree hub connected only to non-seeds is
NOT lifted, while a dependent of the top seed is). The off-by-default no-op is preserved (`wall`
gb=0 vs gb=0.3 still identical on the edgeless corpus). So **graph-boost v2 now adds value** as an
*implicit* assist (surfaces dependents from a plain query without knowing the anchor ID), complementing
the *explicit* `Related` walk (still cleaner/exact when you have the subject's ID). Some 1-hop-from-a-seed
noise remains (artifacts connected to a different strong hit also rise) — acceptable, tunable via the
blend weight; degree-normalization and kind/direction-filtered boost are the next refinements.
