# IOC — Vision & Direction

> Status: direction document — the *why*. The formal, authoritative v0.2 specifications that make this
> concrete live in [`docs/`](docs/) — [`PDR.md`](docs/PDR.md), [`ROADMAP.md`](docs/ROADMAP.md), and the
> planned FRD/FSD/PAD.

## What IOC is

IOC is a **local-first, model-agnostic memory & knowledge layer for working with LLMs** — a
single model or many agents. It keeps an LLM's working context small, persistent, and (when
needed) shared; lets reasoning evolve as a branchable graph; and consolidates/forgets at
version boundaries so memory does not grow without bound.

It unifies **two retrieval axes on one store**:
- **Semantic memory** — a mini-summary + embedding per result, retrieved by similarity with
  progressive disclosure (cheap overview → raw on demand) and a maintained current-truth view.
- **A queryable knowledge base** — **author-declared typed edges** between artifacts
  (`depends_on`, `contradicts`, `answers`, …), traversed structurally ("what depends on X?").

So IOC is *both* the memory that keeps an agent's context small *and* a knowledge graph you can
query — not one or the other.

IOC is **not** an LLM and does not bundle one. Reasoning, summarization, and embeddings are
produced by whatever model the caller chooses; IOC stores, organizes, and serves the results,
and exposes an API (plus an MCP wrapper) that any model can reach. "No LLM in the core" means
**no vendor lock-in and no model baked in** — not "no LLM ever". Crucially, **IOC never *infers*
the graph**: edges are declared by the authoring model at write time (just like it declares what
a new conclusion supersedes), so there is no LLM-driven entity/relation extraction in the core.

This deliberately reframes the earlier "stateful knowledge graph runtime for research": that
design made a **heavy, LLM-extracted** knowledge graph *the product*. IOC keeps a **lightweight,
author-declared** graph as one structural axis beside the semantic one. The graph machinery,
versioning, and content-addressable storage are *means*; the product is a **memory + knowledge
substrate** (a context OS).

## Who it's for, and the pain

The first and primary case is a **single LLM**:
- externalized working memory that lives in IOC, not in the model's context window / VRAM;
- the model stops forgetting its own earlier reasoning and the user's stated intent during long sessions;
- when context drifts, you can drop the bad part and continue **without re-explaining** everything;
- state carries across model swaps — you don't re-brief a second model on what the first did.

A second mode, on the same substrate, is **multi-agent**:
- a shared "blackboard": sub-agents read/write IOC and see each other's *published* results, and
  the orchestrator's own context stays lean because work lands in IOC instead of in its prompt.

Common to both: the ability to **branch and replay reasoning** — return to an earlier point and
try a different path without losing the original.

## Core model

**Recursive scope.** "Worktree", "workspace", and "session" are *roles* of one recursive unit —
a scope — not a fixed four-level tree. A session can grow into a workspace and nest its own
sessions. (worktree ≈ project; in SaaS, the top scope is the tenant.)

**Session = a direction of reasoning, not a time window.** Sessions form an *idea-evolution
graph*: one idea forks into another (a stick → a digging stick → a hoe), and branches are kept
because an earlier idea stays valuable even after a later one supersedes it. Each session
carries insights.

**Artifact = a leaf result / insight** — an answer, a distilled piece of reasoning, a summary, a
document. Full content lives in content-addressable storage (cold); only a mini-summary +
embedding represent it in the working layer.

**Reasoning is first-class and storable.** Distilled insights become artifacts; the raw
transcript stays cheap/cold. (This deliberately reversed the old v0.1 design, which treated
runtime/reasoning as ephemeral.)

**Two-tier memory:**
- *worktree memory* — canonical, finished truths + embeddings; long-lived.
- *workspace memory* — mutable, short-lived working memory; produces a summary when a branch transitions.
- Both store only **mini-summary + embedding** (minimum context). Full content stays in artifacts/CAS.

**Visibility is a separate axis, bottom-up.** A scope does not read a sibling directly — it sees
the sibling's summary/embedding (cheap) and can drill *up* to ancestors on demand. Siblings
coordinate through artifacts *published* to a shared parent (the blackboard). Raw reasoning is
private until published. Isolation is about *write/context*, not about hiding everything.

**Progressive disclosure is the core retrieval idea.** The API is not "return top-k chunks"; it
is *resolution-controllable*: a cheap overview by default → more detail on request → raw content.
This is the main thing that distinguishes IOC from a plain vector database.

**Knowledge edges (author-declared) are the structural axis.** Beside similarity, artifacts carry
typed directed relations the authoring model declares — `depends_on`, `contradicts`, `answers`,
`refines`, `relates_to` (the vocabulary is open). Retrieval can then be *structural*, not just
semantic: walk the edges to answer "what depends on X?", "what contradicts this?", or a multi-hop
chain — questions similarity cannot answer because the related artifact need not be textually
similar. IOC stores and traverses these edges but never extracts them; declaring them is the
author's job, exactly like declaring supersession. (Provenance edges — `derived_from`, fork/seed
lineage, `superseded_by` — already exist; these add the *semantic* relations.)

**Consolidation and forgetting happen at two discrete boundaries:**
1. *branch transition* within a version — workspace memory is summarized;
2. *version boundary* — archive vN (still retrievable), start vN+1 from a clean, minimal **seed**.

The seed carries distilled **constraints/lessons**, not just results — so vN+1 does not repeat
vN's mistakes ("the wooden head broke under load → the head must be metal", not merely "we built
a hoe"). Embeddings (and scope-level aggregates) defer the within-tier limit; the version reset
bounds growth across tiers.

**Curation:** the LLM decides what to commit to memory; the human can add / edit / delete.
**Branching:** fork (keep both) or discard — the human decides.
**Determinism:** demoted from a hard invariant to an **optional trace/replay** feature ("show the
exact context the agent saw"). Inspectability is kept; strict bounded-determinism as a
requirement is dropped.

## Integration & deployment

- The core is an embeddable database / graph / knowledge OS.
- An **MCP server is a thin wrapper** over the core, so any MCP-capable agent gets shared memory
  out of the box. (HTTP API and library use are also possible.)
- **Local-first now; SaaS later.** The top scope maps to a tenant. Implement single-writer now;
  design interfaces so MVCC can be added when multi-tenant concurrency is needed.
- **Security gate (hard precondition for SaaS/multi-user).** The network-hardening items in
  [`docs/SECURITY_AND_VULNERABILITIES.md`](docs/SECURITY_AND_VULNERABILITIES.md) — token/at-rest
  modes, framing/conn/JSON DoS bounds, embed-endpoint policy, ingest containment + caps — must ALL be
  in place before any multi-user/SaaS step. As of 2026-06 those Phase A + Phase B items are implemented,
  AND the first TM2 layer: opt-in at-rest encryption (AES-256-GCM, `docs/encryption.md`), a 2-principal
  runtime ACL + token rotation, and opt-in mTLS. Still required before true multi-tenant SaaS:
  per-tenant principals/ACLs (beyond full/read-only), MVCC/multi-writer, key management (KMS/keyring),
  and distinct per-client certs / cert rotation. Do not ship multi-tenant access until those land.
- **Runtime:** a single long-lived daemon owns a store and serves many clients/agents over a local
  protocol, so CLI/MCP/sub-agents share one memory instead of fighting the bbolt lock. Implemented; see
  [`docs/ROADMAP.md`](docs/ROADMAP.md).

## The load-bearing risk (honest)

The value of everything above rests on two things, and both are **empirical, not architectural**:
1. the *quality* of the mini-summaries/embeddings — if they're poor, agents drill down constantly
   and the context savings evaporate;
2. the *aggregate navigability* of memory at scale — which requires memory to be itself recursive
   (summaries of summaries) plus retrieval over memory.

Graph, versioning, CAS, and snapshotting are plumbing around these. They matter (especially for
SaaS), but they do not decide whether IOC works. Summary quality + memory navigability decide it.

A first dogfood (IOC studying its own repo) confirmed this risk empirically and surfaced concrete
retrieval/ingest bugs plus errors in the wall theory itself (e.g. document chunks embed raw content
but the lexical/rerank stages run on a label, not the content). The reasoning wall has since been
proven on the real embedder — see [`docs/WALL_EXPERIMENT.md`](docs/WALL_EXPERIMENT.md); document-Kind
retrieval is the weaker, now-frozen regime ([`docs/ROADMAP.md`](docs/ROADMAP.md)).

## Where this stands, and next

The load-bearing bet — **prove the wall** — has been met. On the real embedder, overview summaries
answered ~0.96 of answerable questions with no drill, the current-truth view held in every
configuration, and the system abstained ("INSUFFICIENT") on absent questions rather than inventing
(see [`docs/WALL_EXPERIMENT.md`](docs/WALL_EXPERIMENT.md)). The supersession/currency loop, the
runtime daemon, the security gate's first layers, and the author-declared knowledge-edge axis are
all built. That unblocks the formal-spec rewrite and further investment.

Next (tracked in [`docs/ROADMAP.md`](docs/ROADMAP.md)): finish the formal v0.2 specs (FRD, FSD,
PAD); **graph-aware retrieval** (blend the structural edge axis into the semantic ranking, not just
a separate traversal); a margin-aware weak-match experiment; and the remaining robustness items.
Document-Kind retrieval stays frozen (it competes with grep) — the unique value is reasoning memory
plus the knowledge graph over it.
