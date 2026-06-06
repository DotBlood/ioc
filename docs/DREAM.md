# DREAM — where IOC's theory stands against the field, and where to take it

> A dated, deep-research-grounded rethink of IOC's core bets (2026-06-06). Sober, cited, and explicit
> about uncertainty. Produced by a fan-out research harness (5 angles → 25 sources → 122 extracted
> claims → 25 adversarially verified at 2-of-3 votes → 20 confirmed, 5 refuted). **This is a direction
> document, not a spec** — it argues what to ADOPT / ADAPT / REJECT and why; the binding contract stays
> in [`FSD.md`](FSD.md) / [`FRD.md`](FRD.md), the plan in [`ROADMAP.md`](ROADMAP.md).
>
> **Honest framing:** every IOC-vs-SOTA comparison here is an architectural-design contrast — IOC has
> **not** been benchmarked head-to-head against any of these systems. "Where IOC is ahead/behind" is
> reasoned, not measured. Several plausible claims were **refuted** in verification and are deliberately
> *not* recommended (see §6).

---

## 0. The one-paragraph verdict

The SOTA literature **broadly validates IOC's central bets** and flags **two concrete weaknesses**.
Graph retrieval research now says LLM-extracted graphs (GraphRAG, HippoRAG, Graphiti) *frequently
underperform vanilla vector RAG on simple lookup* and cost 10–40× the tokens, paying off only on
multi-hop / summarization / synthesis — which **vindicates IOC's no-LLM-extraction, author-declared
edges as the sober default**, and makes HippoRAG's query-seeded Personalized PageRank a **literal
precedent** for IOC's seed-anchored graph-boost. Hierarchical-retrieval research (RAPTOR, ReTreever)
**independently confirms IOC's own measurement** that strict coarse→fine routing drops the correct
scope and flat+rerank wins on distinctive corpora — so IOC should make **flat/collapsed retrieval the
default**, not hierarchy. Agent-memory research shows IOC's **no-LLM-in-core stance is genuinely
contrarian** (every leading system puts an LLM inside the memory loop and *revises* old notes) — a
defensible cost/locality trade, but one to hold consciously — and points to a **multi-signal score**
(recency + importance + relevance) IOC is missing an importance term for. Confidence/abstention (IOC's
known weak spot) came back **unresolved** — the weakest-covered area, an explicit open research gap.

**Net moves:** ADAPT graph-boost into a real query-seeded PPR; ADOPT a flat/collapsed default + a
multi-signal score with an *author-declared* importance term; REJECT mandatory LLM graph extraction;
treat confidence/abstention as an open follow-up.

---

## 1. Graph-based retrieval over memory

**SOTA.** GraphRAG-Bench (arXiv:2506.05690, ICLR'26) finds basic RAG **beats** Microsoft GraphRAG on
simple fact retrieval (Novel 60.9% vs 49.3%; Medical 64.7% vs 38.6%) and states "GraphRAG frequently
underperforms vanilla RAG on many real-world tasks"; a second study (arXiv:2502.11371) independently
concludes RAG wins single-hop, graph wins multi-hop/reasoning. Graph structure helps **only** on
complex reasoning (HippoRAG2 62.0% vs RAG 58.6%), contextual summarization, and creative generation —
and even there *with high variance* (RAPTOR, original HippoRAG, LightRAG each scored *below* RAG on
summarization in that benchmark). Cost is real: MS-GraphRAG(global) inflates prompts to ~40,000 tokens
vs vanilla RAG's ~900; HippoRAG builds its graph by LLM OpenIE over GPT-3.5. HippoRAG (arXiv:2405.14831,
NeurIPS'24) retrieves via a **single-step Personalized PageRank** seeded with equal mass on
query-extracted nodes, zero elsewhere — "comparable or better than iterative retrieval like IRCoT."

**Where IOC stands.** IOC's edges are **author-declared, no LLM extraction** — it sidesteps the cost
and the extraction noise the benchmarks penalize, at the price of depending on author discipline for
edge coverage. IOC's seed-anchored graph-boost (v2, this session) is a **PPR-lite**: lift candidates
edge-connected to the query's top-cosine seeds — structurally the same idea as HippoRAG's seeded PPR,
minus the iteration and minus the LLM-built graph.

**Recommendation.**
- **REJECT** a mandatory LLM-extraction indexing pass as the default. The evidence says it loses on the
  common case and costs the most; Mem0's own graph variant (Mem0g) adds only ~2% over base Mem0. Keep
  author-declared edges as the no-LLM default. *(Open: offer LLM-extraction as an explicit opt-in for
  corpora known to be multi-hop-heavy — never the default.)*
- **ADAPT** graph-boost (v3) into a proper **query-seeded PPR over the author-declared edges**: seed =
  top-cosine hits with equal restart mass, one or two propagation steps, applied as a **re-rank over the
  vector candidate set** (not as the primary index). This buys HippoRAG's multi-hop benefit at IOC's
  cost profile. Cheap, no-LLM **synonym edges** (encoder-cosine links between near-duplicate summaries)
  are worth borrowing to densify the graph so PPR can actually find paths.
- **Trade-off / open question.** Author-declared edges may be too **sparse** for PPR to find multi-hop
  paths (HippoRAG relies on a dense LLM-extracted graph). Whether IOC's graph is dense enough is the
  central unknown — measure before investing (see §7).

---

## 2. Hierarchical retrieval & the representation substrate

**SOTA.** RAPTOR (arXiv:2401.18059, ICLR'24) recursively embeds/clusters/summarizes into a tree of
abstract summary nodes — **directly analogous to IOC's scope rollups** — and sets SOTA on QuALITY
(82.6%). Its key ablation: **"collapsed tree" retrieval (search all summary+leaf nodes simultaneously)
consistently beats top-down "tree traversal,"** because searching all levels at once retrieves at the
right granularity. ReTreever (arXiv:2502.07971) corroborates: hierarchical methods "do not match the
efficiency and performance of flat retrieval," and routing that drops the correct node hurts vs flat.

**Where IOC stands.** This is an **external confirmation of IOC's own finding** (`WALL_EXPERIMENT.md`):
for distinctive artifacts among distractors, strict coarse→fine **hurts** (recall 0.74 — coarse routing
drops the target's scope) while **flat vector + rerank wins (0.93)**; hierarchy only wins on *dense
near-duplicate* corpora (0.94–1.00). IOC already has the rollups RAPTOR validates — it's the *routing
discipline* that's wrong as a default.

**Recommendation.**
- **ADOPT** flat-vector(+rerank) as the **default**, and add a **collapsed-tree mode** that scores
  rollups *and* leaves in one pass (instead of routing coarse→fine then searching). Reserve strict
  coarse→fine for the dense-near-duplicate regime IOC measured it winning. This is already half-captured
  by ROADMAP's "shape-aware retrieval" item — the research says make the *flat/collapsed* path primary,
  not a fallback.
- **Substrate (summary-embedding):** keep it. QuOTE (arXiv:2502.10976) embeds *questions a chunk can
  answer* instead of raw tokens — the same family as IOC's "embed a derived representation, not raw
  content" bet — and is cheaper than multi-vector. **Caveat:** QuOTE's *accuracy* gain was **refuted**
  in verification, so cite it only as design corroboration, not proof. Optional cheap experiment: have
  the authoring agent also emit "questions this artifact answers" as an extra embedded field for
  reasoning artifacts.
- **Do NOT** adopt late-interaction (ColBERT/PLAID) on this evidence — the supporting claims were
  refuted (§6). It remains genuinely open whether token-level multi-vector beats IOC's single
  summary-vector; the cost (storage, complexity) is real and the benefit is unproven *here*.

---

## 3. Agent memory: consolidation, currency, scoring

**SOTA.** Every leading system puts an **LLM inside the memory loop** and *revises* old memories:
- **A-MEM** (arXiv:2502.12110, NeurIPS'25): LLM auto-generates structured notes (description, keywords,
  tags) per memory, LLM-links them to related historical memories, and does **"memory evolution"** — a
  new memory triggers LLM **rewrites of existing notes' attributes** (old notes revised, not only
  appended).
- **Zep/Graphiti** (arXiv:2501.13956): a temporally-aware KG that LLM-extracts entities/relations/
  timestamps and runs an LLM step to compare new edges against existing ones to **detect contradictions**.
- **Generative Agents** (UIST'23, DOI 10.1145/3586183.3606763): **reflection** synthesizes higher-level
  inferences from base memories with cited evidence; retrieval scores by
  **`recency + importance + relevance`** (importance = LLM-assigned 1–10).

**Where IOC stands.** IOC deliberately keeps the LLM **out of core** — extraction/consolidation/linking
is delegated to the *authoring* agent. This is **genuinely contrarian** (a real differentiator:
local-first, model-agnostic, no extraction cost/noise) but it is a **cost/complexity trade-off, not an
established best practice**. IOC's append-only + supersession already captures "revision as a new
version"; consolidation + the version seed already do reflection-like distillation — but driven by the
agent, not the store.

**Recommendation.**
- **KEEP** no-LLM-in-core as the identity bet — but hold it *consciously*: the whole field disagrees,
  so the burden is on IOC to show author-declared quality matches LLM-in-loop recall (build the eval).
- **ADAPT "memory evolution":** today supersession only *flags* the old artifact. Let the authoring
  agent optionally **author a merged/rewritten superseding summary** (still its words, still append-only)
  — closing the gap with A-MEM's revise-in-place without putting an LLM in core.
- **ADOPT a multi-signal score:** Generative Agents' `recency + importance + relevance` is the canonical
  baseline. IOC has relevance (cosine) + opt-in recency but **no importance**. Add an **author-declared
  importance/Tier weight** (IOC already distinguishes `worktree`=canonical vs `workspace`=mutable) as a
  third ranking signal — importance *declared by the agent*, not LLM-scored, staying true to the core
  bet. **Caveat:** Generative Agents set all weights = 1 with no tuning — copy the *structure*, tune the
  *weights* on IOC's own eval.
- **Benchmark skepticism (load-bearing):** the leading systems' headline wins are **vendor self-reports
  on contested benchmarks** — Zep beats MemGPT on DMR by 1.4pt on an admittedly *saturated* benchmark;
  the Zep↔Mem0 LOCOMO numbers are disputed (84% → 58.4% → counter 75.1%); in several cases a plain
  full-context baseline beats the memory system. **Do not chase these numbers** — IOC should build its
  **own reproducible eval** (it has `internal/eval` + the wall) and report honestly.

---

## 4. Confidence / abstention / calibration — the open gap

**SOTA.** *Unresolved by this pass.* Of 20 confirmed claims, **none** addressed selective prediction,
calibration, or RAG abstention directly — this was the weakest-covered angle (the fan-out surfaced
sources — Google's "sufficient context" work, a conformal-prediction-for-NLP TACL paper, several
RAG-abstention preprints — but their specific claims did not survive verification). So this report
**cannot** make evidence-backed recommendations here yet.

**Where IOC stands.** IOC's known problem (`ioc-weakmatch-threshold-too-low`, `WALL_EXPERIMENT.md`): the
absolute cosine `weak_match` floor is **gameable by vocabulary** at small scale; IOC's empirical move —
prefer the **top-1-vs-runner-up margin** over an absolute floor — is *consistent with* selective-
prediction intuition but currently **unsupported by external citation**.

**Recommendation.**
- Treat area 4 as an **explicit open research gap**: run a *dedicated* follow-up search on conformal
  prediction for retrieval, selective prediction / margin calibration, and RAG abstention **before**
  building the margin-aware weak_match (ROADMAP "Margin-aware weak_match" — keep it experiment-first).
- Until then, the only validated adjacent signal is the multi-signal score (§3) — a confident answer is
  one with high relevance *and* a clear margin over the runner-up.

---

## 5. Prioritized synthesis — what to build, in order

1. **Shape-aware default = flat/collapsed retrieval** (Area 2, *high confidence, externally + internally
   confirmed*). Make flat-vector+rerank the default; add a collapsed-tree (all-levels-at-once) mode;
   keep strict hierarchy only for dense near-duplicate corpora. Biggest, best-supported win.
2. **graph-boost v3 = query-seeded PPR over author-declared edges** (Area 1, *high confidence
   precedent*). Formalize the seed-anchored reorder as 1–2 step PPR seeded on top-cosine hits; add cheap
   no-LLM synonym edges to densify. Re-rank, never primary index.
3. **Multi-signal ranking with an author-declared importance term** (Area 3, *high confidence baseline*).
   relevance(cosine) + recency(have) + importance(Tier/author-declared). Benchmark vs the 3-signal
   baseline; tune weights on IOC's eval.
4. **Optional author-rewritten superseding summaries** ("memory evolution" without LLM-in-core) (Area 3).
5. **Confidence/abstention follow-up research → then margin-aware weak_match** (Area 4, *open*).
6. **Build IOC's own reproducible cross-system eval** — the through-line of every area: don't trust
   vendor benchmarks; measure IOC honestly. (Decide whether author-declared edges are dense enough for
   PPR; whether collapsed-tree recovers the hierarchical loss; whether the importance signal helps.)

**Explicitly rejected as defaults:** mandatory LLM graph extraction (Area 1); late-interaction/multi-
vector on current evidence (Area 2); chasing vendor benchmark numbers (Area 3).

---

## 6. What was refuted (honesty box)

These plausible claims were **killed** in adversarial verification (≥2/3 refute) and are therefore *not*
recommended — their absence is intentional:
- Question-augmented embedding *materially* improves top-1 accuracy (QuOTE SQuAD 74.9% vs 66.6%) — **1-2,
  refuted**. (Mechanism kept as design corroboration; the accuracy claim dropped.)
- A two-stage hybrid(BM25+dense)→cross-encoder pipeline is the strongest strategy — **0-3, refuted**.
- Cross-encoder reranking is the single most impactful component (+17pp MRR) — **0-3, refuted**.
- Mem0 cuts cost ~90% vs full-context while keeping accuracy — **1-2, refuted** (vendor self-report).
- Generative Agents' memory-stream "validates IOC's no-LLM substrate as established practice" — **0-3,
  refuted** (it's a contrast, not an endorsement; see §3).

---

## 7. Open questions (for the next research/build cycle)

1. **Confidence/abstention SOTA** (Area 4 gap): does conformal/selective-prediction work confirm IOC's
   margin-over-floor move? Dedicated search needed.
2. **Edge density vs PPR:** are author-declared edges dense enough for a query-seeded PPR to find
   multi-hop paths, or does PPR need LLM-extracted (or synonym-edge) density to pay off?
3. **Late-interaction worth it?** Does ColBERTv2/PLAID multi-vector beat IOC's single summary-vector
   enough to justify the storage/complexity? Genuinely open (supporting claim refuted).
4. **Collapsed-tree vs auto mode-selector:** does a flat-all-levels mode close IOC's hierarchical loss
   on distinctive corpora (0.74) *while* keeping its dense-corpus win (0.94–1.00), or is a per-query
   density-keyed mode selector needed?

---

## Sources (verified primary)

- HippoRAG — arXiv:2405.14831 · GraphRAG-Bench — arXiv:2506.05690 · RAG-vs-GraphRAG — arXiv:2502.11371
- RAPTOR — arXiv:2401.18059 · ReTreever — arXiv:2502.07971 · QuOTE — arXiv:2502.10976
- A-MEM — arXiv:2502.12110 · Zep/Graphiti — arXiv:2501.13956 · Mem0 — arXiv:2504.19413
- Generative Agents — DOI 10.1145/3586183.3606763 (UIST'23)
- Area-4 leads (claims unverified): Google "sufficient context" RAG; conformal-prediction-for-NLP (TACL
  10.1162/tacl_a_00715); RAG-abstention preprints (arXiv:2411.10513, 2509.01476, 2510.24020).

*Method: deep-research fan-out — 5 angles, 25 sources fetched, 122 claims extracted, 25 verified at
2-of-3 adversarial votes (20 confirmed / 5 refuted), synthesized to 8 findings. Verification covers
recall, not exhaustiveness; area 4 is under-covered.*
