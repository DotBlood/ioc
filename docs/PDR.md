# Product Definition Requirements (PDR) — v0.2

## IOC — a local-first, model-agnostic memory & context layer for LLMs

> Supersedes the v0.1 PDR ("Stateful Knowledge Graph Runtime"), now archived. See
> [`/VISION.md`](../VISION.md) for the direction this makes concrete.

---

## 1. Executive Summary

**IOC (Input Output Context) is a local-first, model-agnostic memory and context layer for working
with LLMs** — a single model or many agents. Its job is to keep an LLM's *working context* small,
persistent, and (when needed) shared, so the model stops re-deriving and re-reading what it already
concluded.

IOC stores an LLM-authored **mini-summary + the embedding of that summary** for each result, keeps the
full content cold in content-addressable storage, and serves it back by **progressive disclosure** — a
cheap overview by default, raw content only on demand. Reasoning is first-class: distilled insights
become artifacts; the raw transcript stays cheap. Memory is organized as **recursive scopes** that fork
and version, and it stays navigable across branch and version boundaries by maintaining a
**current-truth view** (superseded conclusions drop out of default retrieval). On the same store,
**author-declared typed edges** between artifacts add a structural axis — so IOC unifies *agent memory*
(semantic retrieval) with a *queryable knowledge base* (graph traversal like "what depends on X").

**IOC is not an LLM and does not bundle one.** Reasoning, summarization, and embeddings come from
whatever model the caller chooses; IOC stores, organizes, and serves the results behind a Go library, a
CLI, and a thin MCP wrapper. "No LLM in the core" means **no vendor lock-in and no model baked in** —
not "no LLM ever."

This reframes the v0.1 "stateful knowledge graph runtime for research": the graph, versioning, and CAS
are *means*, not the product. The product is a **context OS**.

---

## 2. Problem Statement

### 2.1 The pain (single LLM, the primary case)

- An LLM's working memory lives in its context window / VRAM — it is finite, expensive, and lost
  between sessions and model swaps.
- During long sessions the model **forgets its own earlier reasoning** and the user's stated intent,
  and re-explains or re-derives them.
- When context drifts, there is no clean way to **drop the bad part and continue** without re-briefing.
- Switching models means re-explaining everything to the new one.

### 2.2 How IOC addresses it

| Need | IOC mechanism |
|------|---------------|
| Externalized working memory | mini-summary + embedding per artifact; full content cold in CAS |
| Stop forgetting earlier reasoning | reasoning is a first-class, storable artifact kind (not ephemeral) |
| Keep context small | **progressive disclosure** — overview first, drill to raw only on demand |
| Drop bad context, keep good | **recursive scopes** that fork/branch; the original is kept, not overwritten |
| Carry intent across model swaps | model-agnostic store; any model reads/writes the same memory |
| Stay current as conclusions evolve | **supersession / current-truth view** — stale beliefs leave default retrieval |
| Bound growth | **consolidation** at branch transitions and a **version-boundary seed** that carries forward distilled constraints/lessons, not bulk |
| Answer structural questions ("what depends on X", "what contradicts this") | **author-declared typed edges** between artifacts + `Related` traversal (a queryable knowledge graph beside the semantic index) |
| Coordinate multiple agents | a shared blackboard: siblings see each other's **published** summaries (bottom-up visibility) |

### 2.3 What IOC is *not*

Not a knowledge-graph product, not a RAG pipeline you query for chunks, not an agent framework, and not
an LLM. It is the memory/context substrate underneath those.

---

## 3. Target Audience

- **Primary — a single LLM / its developer.** Long-running sessions that need persistent, navigable
  working memory that survives context resets and model swaps.
- **Secondary — multi-agent systems.** A shared memory blackboard on the same substrate: sub-agents
  publish results others can see while the orchestrator's own context stays lean.
- **Tertiary — MCP/library integrators.** Any MCP-capable agent gets shared memory via the thin MCP
  wrapper; Go programs embed the library directly.

---

## 4. Key Differentiators

| Differentiator | IOC |
|----------------|-----|
| Progressive disclosure as the core retrieval shape (overview → entry → raw) | ✅ |
| Reasoning stored as a first-class artifact (not ephemeral runtime state) | ✅ |
| Two-tier memory (worktree = canonical truths; workspace = mutable working) | ✅ |
| Recursive scopes (worktree/workspace/session are *roles*; fork + version) | ✅ |
| Maintained current-truth view (supersession excludes stale beliefs by default) | ✅ |
| Author-declared typed edges (depends_on/contradicts/answers…) — a queryable knowledge graph, no LLM-extraction in the core | ✅ |
| Bottom-up visibility (siblings coordinate via *published* summaries) | ✅ |
| Content-addressable storage (dedup, integrity, portability) | ✅ |
| Model-agnostic / no vendor lock-in / no bundled LLM | ✅ |
| Local-first, self-hosted, offline-capable | ✅ |
| Optional at-rest encryption + a single-owner runtime daemon for multi-client sharing | ✅ |

The honest distinction vs. a plain vector DB or a RAG memory layer is **progressive disclosure + a
maintained current view over evolving, branchable reasoning** — not the storage mechanics underneath.

---

## 5. High-Level Concept

```
   External LLM (reasoning / summary / embeddings)        Human (curate / branch)
            │  authors mini-summary + content                    │
            ▼                                                     ▼
   ┌──────────────────────────────────────────────────────────────────┐
   │  IOC core (Go library)                                            │
   │   Push ─ Query(overview) ─ Drill(raw) ─ Publish ─ Neighbors       │
   │   Consolidate ─ Fork ─ CrossVersion ─ Supersede ─ RollupScope     │
   │   retrieval: vector | hybrid | hierarchical (+ optional rerank)   │
   └──────────────────────────────────────────────────────────────────┘
            │ working layer: mini-summary + embedding (hot, cheap)
            ▼
   ┌──────────────────────────────────────────────────────────────────┐
   │  storage:  Meta (bbolt)   CAS (sha256+zstd, cold)   EmbeddingStore │
   │            optional at-rest AES-256-GCM (Box)                      │
   └──────────────────────────────────────────────────────────────────┘

   Access: in-process library · `ioc` CLI · `ioc-mcp` (thin MCP wrapper) ·
           a single-owner runtime daemon (`ioc serve`) shares one store across clients.
```

---

## 6. Success Metrics (v0.2)

v0.2 success is **empirical retrieval/memory quality**, not graph latency. The load-bearing question is
whether the cheap overview is good enough that an agent rarely drills to raw, and whether memory stays
current and navigable at scale. Measured by the eval harness (`internal/eval`, `ioc wall` /
`ioc run-scenario`); see [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md).

| Metric | Target | What it proves |
|--------|--------|----------------|
| Answer-grounded overview-sufficiency | ≥ 0.80 | the wall: a blind judge answers from overview summaries alone |
| Recall@topK (gold artifact retrieved) | ≥ 0.90 | the right memory is actually surfaced |
| Context ratio (IOC tokens / raw baseline) | ≤ 0.25 | working context really shrinks |
| Constraint survival across a version boundary | pass | the seed carries the lesson, not the bulk |
| Currency (current outranks superseded) | 1/1 | stale beliefs do not win retrieval |
| No false confidence (absent question → weak/INSUFFICIENT) | pass | the system abstains instead of inventing |

**Measured status (real bge-small, 2026-06):** overview-sufficiency ~0.96 (28 artifacts) and ~0.96 on a
180-artifact distinctive corpus; recall@topK 0.85 flat / 0.93 flat+rerank / 0.96 hierarchical on a
visible corpus; currency 1/1 across all retrieval modes; absent probe correctly abstains. The wall
holds. (Document-Kind retrieval is a separate, weaker regime — see ROADMAP "frozen".)

Operational/integrity guarantees (not the product thesis, but required): single-writer correctness;
crash-atomic CAS and embedding writes; immutable artifacts; optional at-rest encryption; loopback-only
daemon with a per-store token. Performance is "good enough for local-first interactive use," not a
headline target at this stage.

---

## 7. Constraints & non-goals (v0.2)

- **Local-first now; SaaS later.** The top scope maps to a tenant; single-writer now, MVCC-ready
  interfaces later. Multi-tenant access is gated on the security items in
  [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) (Phase A+B done;
  per-tenant ACLs / KMS / MVCC still required — see ROADMAP).
- **Determinism is optional** (trace/replay for inspectability), not a hard invariant.
- **Document/file retrieval is not the product** — it competes with grep/LSP and is frozen pending the
  reasoning wall; reasoning memory is the unique value.
- **License: AGPL v3.**

---

## Appendix — v0.1→v0.2 reconciliation

This PDR reflects these deltas from the v0.1 specs: **#1** positioning (context OS), **#8**
scoring/metrics (eval-measured, §6), **#9** two-tier memory (§2.2, §5), **#10** no vendor lock-in
(§1, §4), **#11** AGPL v3 (§7). Deltas #2–#7 (recursive scope, session model, reasoning storage,
visibility, retrieval, determinism) are touched here at the product level and specified in detail in
[`FRD.md`](FRD.md) / [`FSD.md`](FSD.md).
