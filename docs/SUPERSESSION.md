# Supersession & currency — the core unsolved problem (deep analysis)

> **STATUS 2026-06-05 — IMPLEMENTED (phases 1–3 of §8) on `development/v0.2-review`.** The suppression
> half now exists. Falsified on the real embedder via the currency probe: before, the superseded v0.1
> belief outranked the current v0.2 one (rank 1 vs 2); after, the superseded artifact leaves the
> candidate set entirely and the current one ranks #1 (`docs/WALL_EXPERIMENT.md`). What shipped:
> - `core.Artifact.SupersededBy` (zero = current), `core.PushRequest.Supersedes`, `core.Query.IncludeSuperseded`, `core.Hit.SupersededBy`.
> - `engine.Push` applies `Supersedes` (marks priors, records `DerivedFrom` lineage); standalone `engine.Supersede(old, new)` for post-hoc/batch.
> - Retrieval excludes superseded artifacts AND `Scope.Archived` version scopes by default (the version-boundary reset); `IncludeSuperseded` surfaces them with the back-link for a history drill.
> - Surface: CLI `ioc push -supersedes`, `ioc supersede -old -by`, `ioc query -include-superseded`, `ioc history -artifact`; MCP `ioc_push supersedes`, `ioc_supersede`, `ioc_query include_superseded`. Append-only — nothing is deleted; a wrong supersession is reversible.
> - Append-only, never delete (principle held); detection stays with the agent (no LLM in core).
>
> Deliberately NOT built (documented limits, not silent gaps): per-claim/partial supersession (model is
> whole-atom boolean — transitive chains work, partial overlap is lossy); automatic contradiction
> detection (agent-declared only — the `Neighbors` assist helper and `Consolidate`-time batch
> reconciliation from §4/§5 are deferred); recency tie-breaker among current atoms (§8 phase 4). These
> are tracked here as the next increments; the core current-view mechanism and its falsification are done.


This is the deepest risk in IOC, above any individual bug: an append-only store of LLM-authored
summaries ranked by similarity will confidently surface **superseded** conclusions, because a stale
decision is often the *best semantic match* for a query. If reasoning memory feeds an agent its own
outdated conclusions, it is **worse than no memory**. VISION promises exactly this capability
("consolidation and forgetting … drop the bad part and continue"), but it is not implemented.

## 1. What IOC has today (verified in code)

IOC already has the **full data model** for currency and **zero read-path usage** of it:

- `Scope`: `Version`, `Archived`, `SeedFrom`, `ForkedFrom`, `Parent`. `Artifact`: `DerivedFrom`,
  `CreatedAt`, `Tier`. (`internal/core/types.go`.)
- Write paths populate them: `Fork`, `Consolidate`, `CrossVersion` (`internal/engine/branch.go`).
- **Read paths use none of them.** `visibleArtifacts`, `ancestorsOf`, `siblingsOf` (visibility.go),
  `coarseToFineCandidates`/`descendantScopes` (rollup.go), `Query` (query.go) rank by pure cosine,
  gated only by `Published`/`Tier`/`Kinds`/`MinScore`. `Archived`, `Version`, `DerivedFrom`,
  `CreatedAt` are write-/display-only.

**Consequence (traced):** after `CrossVersion`, the new version scope is a **sibling** of the
archived old scope (`Parent: s.Parent`, `Version+1`, `ForkedFrom`). A non-hierarchical `Query` from
the new scope calls `siblingsOf`, which does **not** exclude archived siblings, and admits their
`Published` artifacts — so **old-version conclusions still compete on equal cosine footing**. The
`Archived` flag is set and then read by nothing. Seeds (`KindSeed`) are ordinary artifacts with no
ranking boost. There is **no "latest wins", no dedup, no replace-by-key, nothing ever suppressed on
currency grounds.** Versioning today is bookkeeping, not suppression.

So "supersession" is not partially built — the suppression half is entirely absent, even at version
boundaries where the design intends a clean reset.

## 2. Taxonomy (the problem is several problems)

1. **Within-version reversal (the hard one).** "head must be metal" supersedes an earlier "wooden head
   is fine" *inside the same line of work* — no version boundary, both are insights in the same scope.
   Nothing today distinguishes them.
2. **Refinement vs contradiction.** B may *replace* A (contradiction) or *narrow/augment* A
   (refinement: "metal, but stainless for outdoor use"). Over-suppressing refinements loses nuance;
   under-suppressing contradictions feeds stale truth. The distinction is semantic and often subtle.
3. **Version-boundary reset.** vN→vN+1 should drop vN's working conclusions and start from seeds.
   The model expresses this (`Archived` + new scope) but retrieval ignores it (§1).
4. **Provenance/validity, not recency.** "We use Postgres" (stable, 2 years old) must outrank "tried
   SQLite yesterday" (superseded). Currency ≠ newness; a stable canonical truth is current.

## 3. Prior art, distilled to what fits IOC

(Full survey with sources in the review notes.) The field reduces every approach to three questions:
**(1) what marks a fact superseded, (2) who/what sets the mark, (3) how retrieval honors it.**

- **Bitemporal DBs (XTDB/Datomic/SQL:2011):** never overwrite; "current" = open valid-interval. Maps
  onto `CreatedAt`(=transaction time) + an `Archived`/`valid_to`. But they supersede **by entity key**
  and assume the writer knows the key — IOC's free-text summaries have **no key**. We get the storage
  model free; we still owe an identity/contradiction detector. (User-asserted *valid-time* second axis:
  overkill for a local tool.)
- **Event sourcing / git:** immutable log + a **mutable pointer to "current"** (HEAD). "Current" is a
  *derived view* (reachable-from-HEAD), not a field on each atom; history is on-demand (`git log/blame`).
  IOC already has the immutable half (CAS, append-only); it lacks the "current view." This is the
  cleanest framing: **don't mutate; define current as a view; expose history via drill.**
- **Graphiti/Zep (closest real system):** on each new fact, an **LLM compares it to semantically-related
  existing facts**; on a temporal contradiction it sets the old fact's `invalid_at` (never deletes);
  retrieval filters to currently-valid. This is implementable on IOC **without a graph**: the "semantic
  neighbors" is the embedding search we already have. Mem0 does similar (ADD/UPDATE/DELETE/NOOP) but its
  DELETE breaks append-only — prefer Mem0**g**'s "mark obsolete". Letta/LangMem rely on the model
  *noticing* during a turn — cheapest, most fragile ("nothing fires if it doesn't notice"). Note:
  none of these are independently benchmarked on conflict resolution — treat "handles contradictions"
  as unproven marketing, validate on our own data.
- **RAG recency decay:** `score = α·cos + (1−α)·0.5^(age/half_life)` — cheap, retrieval-only, but it
  **down-weights all old items including stable truths**. A tie-breaker, **not** a supersession signal.

## 4. Recommended design for IOC

Synthesis of git/event-sourcing framing + Graphiti detection + IOC's no-LLM-in-core principle.

**Principles:**
- **Append-only, never delete.** Supersession sets a link/flag; the superseded atom stays for audit and
  "show history." A wrong supersession must be reversible.
- **"Current" is a derived view, not newness.** Default retrieval returns the current set (not
  superseded); superseded atoms are reachable only via an explicit history drill (the git HEAD model,
  which fits IOC's progressive-disclosure ethos exactly).
- **Detection stays with the LLM, not in IOC core.** The agent that authors the summary is *already
  reasoning about the change* — it is the cheapest, most reliable detector. IOC supplies the
  bookkeeping and the read-time view.

**Model (minimal additions, `internal/core`):**
- Artifact-level currency: add `SupersededBy ID` (zero = current) — or equivalently a `Superseded bool`
  + reuse `DerivedFrom` on the new atom as the back-link. (Today only `Scope` has `Archived`; decisions
  supersede *within* a version, so currency must exist at the **artifact** level.)

**API (engine):**
- `PushRequest.Supersedes []core.ID`: when the agent writes the new insight, it names the prior
  artifact(s) it replaces; `Push` sets their `SupersededBy = newID` and links `DerivedFrom`. Cheap,
  reliable, no LLM in core — the agent already knows.
- `Neighbors(scope, text, k)` helper (optional): returns the top-k semantically-similar *current* atoms,
  so the agent can decide what it supersedes before/at Push — turning detection into a cheap,
  agent-driven step. (This is Graphiti's "LLM diff against neighbors", with the LLM being the caller.)
- Batched reconciliation at `Consolidate`/version boundary: the natural place to ask "what does this
  consolidated truth supersede?" — amortizes the LLM cost and matches the existing hook.

**Retrieval (engine):**
- Default: exclude `SupersededBy != 0` atoms (and `Scope.Archived` scopes) from the candidate set;
  add a `Query.IncludeSuperseded`/a `history` drill to surface them with their `SupersededBy` link.
- Align the existing flag: **make retrieval honor `Scope.Archived`** (today it doesn't) — exclude
  archived-version scopes by default. This is also a standalone correctness fix.
- Recency decay only as a **tie-breaker** among current atoms (α·cos + small recency term), never as the
  supersession mechanism.

**MCP/CLI surface:** `ioc_push supersedes=<ids>`; `ioc_supersede <old> <new>`; `ioc_query include_superseded=false` (default) + a `ioc_history <artifact>` drill.

This turns IOC from "append + similarity-retrieve" into "append + maintain a current-truth view with
history on demand" — which is arguably **the** feature that makes reasoning memory more than a vector
store, and the thing VISION promised but never built.

## 5. The hardest sub-problem: who/what detects supersession, and when

Every prior-art system either keys on an identity it's *given* (no help for free text), needs a logical
KB (overkill), or pays an **LLM contradiction check** (the only thing that works on free text). The
detection is **non-local** (B may contradict an A that isn't B's top match) and **subjective**
(refinement vs contradiction). IOC's answer:

- **Primary: agent-declared at write** (`Supersedes`). The authoring agent just reasoned about the
  change; making it *name* what it replaces is the cheapest, most accurate signal and keeps IOC core
  LLM-free. This is the 90% solution for single-user dogfooding.
- **Assist: `Neighbors` helper** so the agent doesn't have to remember IDs — it queries current
  neighbors, judges, and declares. (Graphiti-style, LLM = the caller.)
- **Batch: reconciliation at `Consolidate`** for contradictions the agent missed in the moment — the
  natural "what is the current truth here?" checkpoint.
- **Avoid:** eager auto-archive on every Push (cost + over-suppression), and relying solely on the model
  noticing mid-turn (Letta's silent-non-detection failure).

## 6. Falsification test (must precede shipping)

The **currency probe** from the review's reasoning-wall experiment: plant a superseded decision A and
its superseding decision B (e.g. wooden→metal; "multiprocess access" → "rejected in favor of the
daemon"). Ask the question whose correct answer is B. **Failure** = A ranks at/above B, or an LLM judge
answering *from overview only* gives the stale answer. Run on the real embedder, before and after the
mechanism, and across a version boundary. This is the test the current harness **cannot express**
(`forbidMention` is keyword-circular).

## 7. Open problems / honesty

- **Detection quality is the ceiling.** If the agent under-declares supersession, stale truth leaks; if
  it over-declares, nuance is lost. IOC can't guarantee the caller's discipline — same dependency as
  summary quality. Mitigate with down-rank-not-hide + a visible "superseded by" link + reconciliation.
- **Transitive/partial supersession.** B supersedes A on one point but A still holds on another. A
  boolean `SupersededBy` is lossy here; a finer model (per-claim) is much more complex. Start coarse
  (whole-atom), accept the limitation, keep history reachable.
- **This touches the core theory, not the edges.** It means the "wall" claim must be restated: it's not
  "store a good summary, find it later," but "maintain the *current* distilled truth and find it later,
  with superseded history on demand." Append-immutability + a current-view is the reconciliation.
- **Sequencing vs the dogfood failure.** Supersession does not fix the document-retrieval 0/4 (that's
  the P0 representation bugs). It is orthogonal and more important for the *reasoning-memory* value —
  which is why it should be designed/validated alongside the reasoning-wall experiment, before
  polishing document retrieval.

## 8. Phasing

1. **Quick correctness:** make retrieval honor `Scope.Archived` (exclude archived versions by default).
   Standalone, small, removes the worst version-boundary leak.
2. **Artifact currency + API:** add `SupersededBy`, `PushRequest.Supersedes`, default-exclude-superseded
   + `history` drill, `Neighbors` helper. Engine + MCP/CLI.
3. **Reconciliation at Consolidate** + the currency-probe eval (falsification).
4. **Recency tie-breaker** among current atoms (optional, cheap).
Each step measured by the currency probe on the real embedder.
