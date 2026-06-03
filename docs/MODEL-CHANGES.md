# IOC — Model Changes vs Current Specs

A delta checklist: how the direction in [`/VISION.md`](../VISION.md) changes the current locked
specs in `close/` (`PDR`, `FRD`, `FSD`, `PAD`, `ROADMAP`) and `research/DESIGN.md`.

**The specs are not rewritten yet.** This records what a future rewrite must reconcile.

| # | Area | Current spec | New direction | Affected docs |
|---|------|--------------|---------------|---------------|
| 1 | Positioning | "stateful knowledge graph runtime for research" | model-agnostic memory/context OS for LLM work (single model first, multi-agent as one mode) | PDR §1–4, README |
| 2 | Scope hierarchy | fixed Worktree→Workspace→Session→Artifact (I3 strict tree) | recursive scope; worktree/workspace/session are roles; a session can promote & nest | FSD §1 (I3), §7; PAD; FRD FR-CORE-01 |
| 3 | Session | temporal leaf (minutes–hours) | a direction of reasoning; a node in an idea-evolution graph; branchable; old branches kept | FSD §6–7; DESIGN §5–6 |
| 4 | Reasoning storage | Runtime Boundary: reasoning NOT stored, cleared on session end (FSD §11, R1–R5) | reasoning is first-class & storable (distilled → artifacts; raw stays cold) | FSD §11; DESIGN §10 |
| 5 | Visibility / isolation | strict scope isolation; cross-session refs forbidden by default | isolation of write/context only; visibility is a separate **bottom-up** axis (summary/embedding by default, drill up on demand); siblings coordinate via published artifacts | FSD §7.3; DESIGN §6.3 |
| 6 | Retrieval | multi-stage ANN pipeline returning ranked candidates | add **progressive disclosure** / resolution-controllable retrieval as the core API shape | FSD §14; PAD §5.3 |
| 7 | Determinism | I6 bounded determinism as a hard invariant + DeterminismBoundary machinery | demote to **optional trace/replay**; keep inspectability, drop the strict requirement | FSD §1 (I6), §15; DESIGN §14 |
| 8 | Relevance / scoring | fixed weighted `Score.Compute` (0.5/0.2/0.15/0.1/0.05) | weights are tunable, not final; **relevance must be measured** (add an eval harness) — no quality benchmark exists today | FSD §14.2 |
| 9 | Memory layer | "worktree/scope stateful" named, internals unspecified | explicit **two-tier memory** (worktree = finished truths; workspace = mutable working); store mini-summary+embedding only; consolidation at branch + version boundaries; seed carries distilled lessons; memory itself recursive | PDR §1; DESIGN §6, §12 |
| 10 | LLM dependency | "no LLM dependency for core" (ambiguous) | clarified: **no vendor lock-in / no model bundled**; external LLM does reasoning/summarization via API/MCP | PDR §2, §4 |
| 11 | License | custom Non-Commercial | **AGPL v3** | LICENSE, README, PDR §4 |

## Not changing (still good)

- Immutable artifacts + versioned projections (semantic-drift prevention).
- Content-addressable storage (dedup, integrity, portability).
- Single-writer now; MVCC-ready interfaces later.
- Stable ULID identity; lineage acyclicity for system edges.

## The hardest open problem (the real core)

Memory consolidation under a context budget, with provenance and evolving/branching
conclusions — and proving **summary quality + memory navigability** empirically on a real loop,
before investing further in specs or storage.
