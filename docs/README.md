# IOC Documentation Map

All project documentation lives in this one `docs/` folder. The v0.2 specifications and the focused
design records sit side by side.

## Start here

| If you want to… | Read |
|-----------------|------|
| Use the CLI / library, see limitations | [`/README.md`](../README.md) |
| The direction & why (product vision) | [`/VISION.md`](../VISION.md) |
| Work on the code as an AI agent (commands, layout, current state) | [`/CLAUDE.md`](../CLAUDE.md) |

## v0.2 specifications (authoritative)

The live, authoritative design documents. They describe what the code actually implements — **where a
spec and the code disagree, the code wins**. These replaced the v0.1 formal specs entirely.

- [`PDR.md`](PDR.md) — Product Definition: what IOC is, who it's for, differentiators, success metrics.
- [`ROADMAP.md`](ROADMAP.md) — what's built, what's next, what's deferred/frozen.
- [`FRD.md`](FRD.md) — Functional Requirements (what the system must do, as testable requirements).
- [`FSD.md`](FSD.md) — Functional Specification (invariants, types, operation contracts, protocols).
- [`PAD.md`](PAD.md) — Platform Architecture (packages, storage, runtime, interfaces).

## Focused design records & references

- [`CODE-STYLE.md`](CODE-STYLE.md) — Go conventions (naming, errors, imports, tests).
- [`MCP_GUIDE.md`](MCP_GUIDE.md) — using IOC over MCP (the `ioc_*` tools).
- [`SUPERSESSION.md`](SUPERSESSION.md) — the supersession / current-truth (currency) mechanism.
- [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md) — the reasoning-wall experiment plus the v0.3 retrieval research (R1–R4): the empirical proof of the core thesis and the experiment record behind the retrieval defaults.
- [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) — threat models, the vulnerability register, the SaaS security gate.
- [`encryption.md`](encryption.md) — opt-in at-rest AES-256-GCM (key handling, caveats).

## Code entry points

- CLI: `cmd/ioc/` — the `ioc` command (memory ops, eval, ingest, daemon).
- MCP server: `cmd/ioc-mcp/` — the `ioc_*` tools (a thin wrapper over the engine).
- Embedding service: `py/embed_server.py` — optional external HTTP embedder.
