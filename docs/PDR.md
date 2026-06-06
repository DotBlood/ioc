# Product Definition Requirements (PDR) — v0.2

## IOC — a local-first, model-agnostic memory & knowledge layer for LLMs

> Supersedes the v0.1 PDR ("Stateful Knowledge Graph Runtime"), now archived. See
> [`/VISION.md`](../VISION.md) for the direction this makes concrete and [`ROADMAP.md`](ROADMAP.md) for
> what is built / next / deferred.

---

## 1. Executive Summary

**IOC (Input Output Context) is a local-first, model-agnostic memory & knowledge layer for working
with LLMs** — a single model or many agents. It is two things on one store: the **semantic memory**
that keeps an agent's working context small, and a **queryable knowledge base** of author-declared
edges over that memory. The first keeps an LLM from re-deriving and re-reading what it already
concluded; the second lets it ask structural questions ("what depends on X?") the embeddings cannot.

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

This deliberately reframes the v0.1 "stateful knowledge graph runtime for research": that design made a
**heavy, LLM-extracted** knowledge graph *the product*. IOC keeps a **lightweight, author-declared**
graph as one structural axis beside the semantic one (IOC never infers edges — the model declares them,
like supersession). The graph machinery, versioning, and CAS are *means*; the product is a **memory +
knowledge substrate** (a context OS).

---

## 2. Problem Statement

### 2.1 The pain (single LLM, the primary case)

- An LLM's working memory lives in its context window / VRAM — it is finite, expensive, and lost
  between sessions and model swaps.
- During long sessions the model **forgets its own earlier reasoning** and the user's stated intent,
  and re-explains or re-derives them.
- When context drifts, there is no clean way to **drop the bad part and continue** without re-briefing.
- Switching models means re-explaining everything to the new one.
- Even with a memory store bolted on, **stale conclusions resurface** (the old belief is often the best
  semantic match), and there is **no way to ask how facts relate** — only "what is similar to this text".

### 2.2 Where current systems fall short

| Limitation | Plain vector store / RAG memory | GraphRAG-style | Agent-memory frameworks |
|---|---|---|---|
| **No progressive disclosure** — returns chunks, not a cheap overview you drill on demand | ✗ | ✗ | partial |
| **No current-truth view** — superseded beliefs keep ranking | ✗ | ✗ | partial |
| **No recursive scopes / branchable versioning** — memory is flat/global | ✗ | ✗ | partial |
| **Graph needs LLM extraction** — relations cost an LLM pass and are fragile | n/a | ✗ (LLM-built) | n/a |
| **LLM-coupled core** — memory ops call a model | usually no | ✗ | often ✗ |
| **Not self-hosted / offline** | varies | varies | often ✗ |

(The landscape moves fast; these are coarse, honest contrasts, not a benchmark.)

### 2.3 How IOC addresses it

| Need | IOC mechanism |
|------|---------------|
| Externalized working memory | mini-summary + embedding per artifact; full content cold in CAS |
| Stop forgetting earlier reasoning | reasoning is a first-class, storable artifact kind (not ephemeral) |
| Keep context small | **progressive disclosure** — overview first, drill to raw only on demand |
| Drop bad context, keep good | **recursive scopes** that fork/branch; the original is kept, not overwritten |
| Carry intent across model swaps | model-agnostic store; any model reads/writes the same memory |
| Stay current as conclusions evolve | **supersession / current-truth view** — stale beliefs leave default retrieval |
| Ask how things relate | **author-declared typed edges** + `Related` traversal (a knowledge graph beside the index) |
| Bound growth | **consolidation** at branch transitions and a **version-boundary seed** that carries forward distilled constraints/lessons, not bulk |
| Coordinate multiple agents | a shared blackboard: siblings see each other's **published** summaries (bottom-up visibility) |

### 2.4 What IOC is *not*

Not a *heavy, LLM-extracted* knowledge-graph product (the rejected v0.1 framing) — its graph is
lightweight and author-declared. Not a RAG pipeline you query for chunks, not an agent framework, and
not an LLM. It is the memory + knowledge substrate underneath those.

---

## 3. The two-axis model

IOC retrieves along **two complementary axes over one store**:

- **Semantic axis (similarity).** Every artifact carries a mini-summary and the embedding of that
  summary. `Query` ranks by cosine (optionally hybrid BM25 / hierarchical coarse→fine / cross-encoder
  rerank) and returns a cheap overview; the agent drills to raw only when the overview is not enough.
  This is what keeps the working context small.
- **Structural axis (edges).** Artifacts carry **author-declared typed directed edges** —
  `depends_on`, `contradicts`, `answers`, `refines`, `relates_to` (open vocabulary). `Related` walks
  them in a chosen direction and depth.

Why both: similarity cannot answer relational questions. "What depends on the bbolt-storage decision?"
will *not* be served by similarity — a dependent artifact ("the runtime daemon owns the store") need
not be textually similar to "bbolt". The edge `runtime --depends_on--> bbolt-decision` answers it
exactly. Conversely, "what did we conclude about retrieval modes?" is a pure similarity question the
semantic axis already answers. The two axes are independent today (`Query` vs `Related`); blending them
(edge-boosted ranking) is on the roadmap.

**Crucially, IOC never infers the graph.** The authoring model declares an edge at write time (or
post-hoc), exactly as it declares what a new conclusion supersedes — so there is no LLM-driven
entity/relation extraction in the core, and edge quality is the author's responsibility, not a fragile
pipeline's.

---

## 4. Target Audience

- **Primary — a single LLM / its developer.** Long-running agents and sessions that need persistent,
  navigable working memory surviving context resets and model swaps. *Gets:* a small, durable context
  that does not forget; clean branch/rollback; a current-truth view so stale conclusions stop resurfacing.
- **Secondary — multi-agent systems.** Orchestrators and sub-agent fleets. *Gets:* a shared blackboard —
  sub-agents publish results others read, while the orchestrator's own context stays lean because work
  lands in IOC instead of its prompt; branch-and-replay to try alternatives without losing the original.
- **Secondary — long, branching investigations.** Research/analysis spanning many sessions with evolving,
  sometimes contradictory conclusions. *Gets:* idea-evolution branches kept (not overwritten),
  supersession + `contradicts`/`depends_on` edges to track how findings relate and which is current.
- **Tertiary — MCP / library integrators.** *Gets:* any MCP-capable agent gets shared memory via the
  thin `ioc-mcp` wrapper out of the box; Go programs embed the library and call the engine directly.

---

## 5. Usage scenarios

Concrete flows (real CLI; the `ioc_*` MCP tools mirror them).

**(a) Single-LLM long session — keep context small, stay current.**
```bash
ioc create-scope -role worktree -title proj            # open a scope
ioc push -scope <S> -summary "use bbolt for metadata storage" -kind insight
# ...later the model wants to record a refined conclusion; first check what it might replace:
ioc neighbors -scope <S> -text "metadata store choice"   # -> the bbolt insight
ioc push -scope <S> -summary "bbolt confirmed; single-writer is fine via the daemon" -supersedes <bbolt-id>
ioc query -scope <S> -text "how do we store metadata" -detail overview   # overview only; the stale belief is gone
ioc drill -artifact <hit> -detail raw                    # raw content only when the overview isn't enough
ioc consolidate -scope <S> -summary "storage = bbolt, owned by the runtime daemon"
```

**(b) Multi-agent blackboard — coordinate without bloating the orchestrator's prompt.**
```bash
ioc push -scope <child> -summary "embedder picked: bge-small (384d)" -publish   # sub-agent publishes
ioc siblings -scope <other-child>     # another sub-agent sees only PUBLISHED sibling summaries
```
Raw reasoning stays private until published; visibility is bottom-up (own + ancestors + published siblings).

**(c) Knowledge-graph query — structural retrieval similarity can't do.**
```bash
ioc relate -from <runtime-id> -to <bbolt-id> -kind depends_on   # author declares the edge
ioc related -artifact <bbolt-id> -kind depends_on -direction in # "what depends on the bbolt decision?" -> runtime
ioc related -artifact <runtime-id> -direction out               # "what does the runtime depend on?"
```
Edges can also be declared at write time: `ioc push ... -relations depends_on:<bbolt-id>`.

---

## 6. Key differentiators & competitive positioning

**Differentiators:**

| Differentiator | IOC |
|----------------|-----|
| Progressive disclosure as the core retrieval shape (overview → entry → raw) | ✅ |
| Reasoning stored as a first-class artifact (not ephemeral runtime state) | ✅ |
| Two-tier memory (worktree = canonical truths; workspace = mutable working) | ✅ |
| Recursive scopes (worktree/workspace/session are *roles*; fork + version) | ✅ |
| Maintained current-truth view (supersession excludes stale beliefs by default) | ✅ |
| Author-declared typed edges — a queryable knowledge graph, **no LLM-extraction in the core** | ✅ |
| Bottom-up visibility (siblings coordinate via *published* summaries) | ✅ |
| Content-addressable storage (dedup, integrity, portability); optional at-rest encryption | ✅ |
| Model-agnostic / no vendor lock-in / no bundled LLM | ✅ |
| Local-first, self-hosted, offline-capable; single-owner daemon for multi-client sharing | ✅ |

**Honest competitive positioning** (coarse — capabilities, not a benchmark; the field evolves):

| Capability | IOC | Plain vector DB | GraphRAG | Agent-memory (Mem0/Letta) | Zep | grep / LSP |
|---|---|---|---|---|---|---|
| Progressive disclosure (overview→raw) | ✅ | ❌ | ❌ | partial | partial | n/a |
| Current-truth view / supersession | ✅ | ❌ | ❌ | partial | ✅ (temporal) | ❌ |
| Recursive scopes + branch/version | ✅ | ❌ | ❌ | partial | partial | ❌ |
| Knowledge graph over the memory | ✅ author-declared | ❌ | ✅ LLM-extracted | ❌ | partial | ❌ |
| No LLM needed in the core | ✅ | ✅ | ❌ | often ❌ | partial | ✅ |
| Self-hosted / offline | ✅ | varies | ✅ | varies | varies | ✅ |
| Exact symbol / code search | ❌ (defers to grep) | ❌ | ❌ | ❌ | ❌ | ✅ |

The honest distinction vs. a plain vector DB or a RAG memory layer is **progressive disclosure + a
maintained current view over evolving, branchable reasoning, plus an author-declared graph over it** —
not the storage mechanics underneath. For exact code/symbol lookup, grep/LSP win and IOC defers to them
(document retrieval is frozen — see §11).

---

## 7. High-level concept

```
   External LLM (reasoning / summary / embeddings)        Human (curate / branch)
            │  authors mini-summary + content + declared edges      │
            ▼                                                        ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │  IOC core (Go library)                                                │
   │   semantic axis:  Push ─ Query(overview→raw) ─ Drill ─ Neighbors      │
   │                   modes: vector | hybrid | hierarchical (+ rerank)    │
   │   structural axis: Relate ─ Related (walk edges: "what depends on X") │
   │   memory mechanics: Publish · Supersede · Consolidate · Fork ·        │
   │                     CrossVersion · RollupScope · Trace                │
   └──────────────────────────────────────────────────────────────────────┘
            │ working layer: mini-summary + embedding (hot, cheap)
            ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │  storage:  Meta (bbolt: scopes/artifacts/edges/traces)                │
   │            CAS (sha256+zstd, cold)   EmbeddingStore (float32)         │
   │            optional at-rest AES-256-GCM (Box)                         │
   └──────────────────────────────────────────────────────────────────────┘

   Access: in-process library · `ioc` CLI · `ioc-mcp` (thin MCP wrapper) ·
           a single-owner runtime daemon (`ioc serve`) shares one store across clients.
```

---

## 8. Design principles

- **No LLM in the core.** IOC embeds and stores what a model authored; it never calls a model to
  reason, summarize, or *extract* a graph. Edges and supersession are **author-declared**, not inferred.
- **Progressive disclosure first.** The default answer is a cheap overview; raw content is opt-in.
- **Local-first; SaaS-ready interfaces.** Single-writer now; the top scope maps to a tenant later.
- **Append-only & reversible.** Nothing is destroyed on supersession/versioning; the current view is a
  read-time filter, history is always reachable.
- **The code is the source of truth.** These specs describe the implementation; on conflict, the code
  wins.
- **License: AGPL v3** (network-copyleft, chosen for the SaaS path).

---

## 9. Success metrics (v0.2)

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

## 10. Risks, assumptions & dependencies

- **Summary/embedding quality is load-bearing.** If the mini-summaries are poor, agents drill
  constantly and the context savings evaporate. *Status:* proven on **reasoning** artifacts (the wall);
  **document/file** retrieval dogfooded poorly (0/4 overview-sufficiency) and is frozen — grep wins there.
- **Edge coverage depends on the author.** The knowledge graph is only as complete as the edges the
  model declares; IOC will not invent them. This is a deliberate trade (no LLM-extraction cost/fragility)
  but means structural recall is bounded by author discipline.
- **Embedder dependency / one vector space per store.** Retrieval quality tracks the embedder; a given
  `-dir` must always use the **same** embedder (mock vs real, bge-small vs bge-base are different vector
  spaces and must not be mixed). Switching embedders requires recalibrating the confidence floor.
- **Confidence is a heuristic.** `weak_match` uses a per-embedder cosine floor that is gameable by
  vocabulary at small scale; margin separates present/absent more honestly. A margin-aware weak_match is
  an open experiment (ROADMAP).
- **Single-writer now.** Concurrency is in-process (the daemon serializes writes). Multi-writer/MVCC and
  multi-tenant access are deferred behind the security gate.
- **Retrieval mode must match corpus shape, not just density.** Flat retrieval is structurally blind to
  artifacts living in *descendant* scopes; hierarchical is mandatory there (see WALL_EXPERIMENT).

---

## 11. Scope & phasing

Built: the core slice (recursive scopes, two-tier artifacts, progressive disclosure), retrieval
(vector/hybrid/hierarchical + rerank, confidence), storage (Meta/CAS/EmbeddingStore + opt-in
encryption), ingestion, the runtime daemon, supersession/currency, **author-declared edges**, the
security gate's first layers, and the proven wall. Next: the remaining formal specs (FRD, FSD, PAD),
graph-aware retrieval, a margin-aware weak_match, robustness. Deferred: TM2 layer-2 (per-tenant
ACL/KMS/MVCC) behind the SaaS gate. Frozen: document/file retrieval polish (competes with grep). Full
detail in [`ROADMAP.md`](ROADMAP.md).

---

## 12. Constraints & non-goals (v0.2)

- **Local-first now; SaaS later.** The top scope maps to a tenant; single-writer now, MVCC-ready
  interfaces later. Multi-tenant access is gated on the security items in
  [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) (Phase A+B done; per-tenant
  ACLs / KMS / MVCC still required — see ROADMAP).
- **Determinism is optional** (trace/replay for inspectability), not a hard invariant.
- **Document/file retrieval is not the product** — it competes with grep/LSP and is frozen pending the
  reasoning wall; reasoning memory plus the knowledge graph over it is the unique value.
- **No bundled LLM; no user-facing low-level graph engine** (the graph is internal plumbing + the
  author-declared edge API, not a general graph database).
- **License: AGPL v3.**

---

## 13. Integration & deployment

- **Embeddable core** — a Go library; the engine is the public API.
- **CLI** (`cmd/ioc`) — drive memory from a shell (an agent can call it via Bash).
- **MCP server** (`cmd/ioc-mcp`) — a thin wrapper exposing the engine as `ioc_*` tools, so any
  MCP-capable agent gets shared memory out of the box.
- **Runtime daemon** (`ioc serve`) — a single long-lived owner of a store dir; CLI/MCP/sub-agents
  auto-route to it (discovered via `<dir>/runtime.json`) and share one memory instead of fighting the
  bbolt lock. Falls back to an embedded engine when no daemon runs.
- **Local-first now; SaaS later** — the top scope is the tenant; ship multi-tenant access only after the
  security gate's remaining items (per-tenant ACLs, KMS, MVCC, per-client certs) land.

---

## Appendix — v0.1→v0.2 reconciliation

This PDR reflects these deltas from the v0.1 specs: **#1** positioning (memory + knowledge substrate,
not a research graph runtime), **#8** scoring/metrics (eval-measured, §9), **#9** two-tier memory
(§2/§3), **#10** no vendor lock-in (§1/§8), **#11** AGPL v3 (§8/§12). Deltas #2–#7 (recursive scope,
session model, reasoning storage, visibility, retrieval, determinism) are stated here at the product
level and will be specified in detail in `FRD.md` / `FSD.md` (in progress). The author-declared
**knowledge-edge axis** is a v0.2 addition beyond the original delta list.
