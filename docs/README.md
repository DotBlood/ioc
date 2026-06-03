# IOC Documentation Map

Where each kind of documentation lives.

## Start here

| If you want to… | Read |
|-----------------|------|
| Use the CLI / library, see limitations | [`/README.md`](../README.md) |
| Work on the code as an AI agent | [`/CLAUDE.md`](../CLAUDE.md) |
| Set up a dev environment, conventions | [`/AGENTS.md`](../AGENTS.md) |
| Understand the design history & decisions | [`/devlog.md`](../devlog.md) |

## `docs/` — process & measurements

- [`CODE-STYLE.md`](CODE-STYLE.md) — Go conventions (naming, errors, imports, tests).
- [`METHODOLOGY.md`](METHODOLOGY.md) — development process, commits, PR size, CI.
- [`benchmarks/v0.1.md`](benchmarks/v0.1.md) — performance baselines (graph, retrieval, CAS, embedding).

## `close/` — locked specifications

Formal, stable design documents. Treat these as the contract; do not edit casually.

- `PDR.md` — product definition & vision.
- `FRD.md` — functional requirements.
- `FSD.md` — formal specification, system invariants **I1–I7**.
- `PAD.md` — platform/technology architecture.
- `ROADMAP.md` — phased implementation plan.
- `Phase5.md`, `Phase6.md` — per-phase plans.
- `SESSION_REPORT.md` — state snapshot after early phases.
- `research/01–06` — background research (graph engines, CAS, embeddings, BM25, MVCC).

## Code entry points

- CLI: `cmd/iocctl/` — Cobra commands.
- Library API: `pkg/api/` — embeddable `Runtime`.
- Embedding service: `py/embed_server.py` — optional external HTTP embedder.


1. Как memory сама не разбухает? - для этого есть 2 memory. в worktree memory идет только уже готовый результат, истина, допустим информация как мы сделали матыгу, так же ее имбединги и какие то данные, workspace memory - изменяеммая, мало живуая, в целом для решения какой то маленькой задачи, она живет внутри scope, и делает сумарайз в переходе в новую ветку, она более мутабельна, и нужна именно для работы.
2.  мы не храним в меммори всю информацию, мы храним мини сумарайз + эмбединги, миниум контекста
3. про это я и говорил выше, и в прошлых ответах

твои вопросы в конце:
1. это так де было написанно в документации, а так вроде ответил в 1 ответе выше
2. в документации так же все указанно: у memory есть графа на childe и parrent в котором есть просто указание почему мы пришли к этому вопросу, мы не храним весь контекст
3. memory солой тоже важен но он идет паралельно главной задачи 