# Runtime Roadmap — a daemon that owns the store, serving many clients

Status: planned (implementation spans ~3 sessions). This document is the source of truth for the
runtime work; it follows [`VISION.md`](../VISION.md) (§ "Integration & deployment").

## Why

Today only one process can use a store at a time. bbolt takes an exclusive file lock, so a second
`ioc`/`ioc-mcp` on the same `-dir` fails with "data dir busy" (see `internal/storage/meta.go`
`OpenMeta`). `*engine.Engine` has **no internal synchronization** — serialization is the caller's
job (the MCP server's `a.mu`). That blocks the multi-agent use case VISION calls for: a main agent
plus sub-agents sharing **one** memory (the bottom-up "blackboard": write to IOC, see each other's
*published* artifacts).

VISION already names this direction (§ Integration & deployment): *"the core is an embeddable
database/graph/knowledge OS; an MCP server is a thin wrapper over the core … HTTP API and library
use are also possible. Local-first now; SaaS later … Implement single-writer now; design interfaces
so MVCC can be added when multi-tenant concurrency is needed."*

**Goal:** one long-lived **daemon** owns a store (`engine.Open` once) and serves multiple
clients/agents concurrently over a local protocol. The CLI and the stdio MCP server become thin
**clients** of the daemon (falling back to embedded `engine.Open` when no daemon is running). This
removes the lock conflict and gives real shared memory.

## Why a daemon, not multi-process access to one store

Short version: building multi-process access to a single store as its own feature has little value —
the same need ("several agents on one memory") is served more cleanly by the runtime daemon.

Why multi-process is a dead end:

1. **It duplicates the runtime's goal.** Both "N processes on one store" and "a daemon" solve the
   same problem; the daemon does it correctly (one owner, serialization *inside* the process), while
   multi-process pushes coordination down into the file layer.
2. **High cost, little gain.** Safely opening one store from 2+ processes would require: replacing
   bbolt's exclusive lock with a shared protocol (or single-writer + read-replicas); making
   `EmbeddingStore` cross-process coordinated — today it reads the whole file into memory and does a
   blind `os.WriteFile` on Sync (`internal/storage/embstore.go`), so two writers race and lose
   records; and making CAS dedup atomic across processes. That is a large change in the most fragile
   part of the system, for a scenario the daemon covers more cleanly.
3. **VISION points the other way.** "Single-writer now; design interfaces so MVCC can be added when
   multi-tenant concurrency is needed" and "an MCP server is a thin wrapper over the core." The
   direction is one owner + MVCC-ready interfaces, not many writers into one file.

What to do instead:

- **The daemon is the sole store owner**; clients (CLI, MCP, sub-agents) talk to it over the
  transport. Concurrency lives *inside* the daemon at the goroutine level (RWMutex: parallel reads,
  serialized writes), not across OS processes. That is exactly this roadmap.
- **A clear error when a second process tries** to open a live store (already shipped in `OpenMeta`)
  is enough as a guardrail for a CLI run next to a live daemon.
- **MVCC / snapshot isolation comes later**, and also inside one owner — not between processes.

The one niche where multi-process would be justified: if the goal were "several *independent* tools
occasionally touch the store with no live daemon" (a pure CLI world, no server), a minimal
shared-read + single-write lock would make sense. But IOC needs the daemon anyway for multi-agent, so
that work would be thrown away.

**Conclusion:** don't build multi-process as a separate feature. Invest in the daemon and solve
concurrency inside it (goroutines + RWMutex, then MVCC). Multi-process is a solution the daemon makes
unnecessary.

## Decisions (locked)

- **Transport:** internal **framed-JSON RPC** over a local socket / loopback (stdlib `net` +
  `encoding/json`, no new dependencies). Unix socket on POSIX; TCP loopback on Windows (consistent
  with how the embedder already runs over TCP on Windows).
- **Role:** the daemon is the **default owner**. CLI/MCP route through it when it is up; embedded is
  only a fallback when no daemon is present.
- **Scope:** one daemon owns one data-dir. Shared memory = pointing clients at the **same** `-dir` /
  `IOC_DIR`. The daemon also owns the embedder (clients send text; the daemon embeds), centralizing
  the "one embedder per data-dir" rule.
- **Trust:** local only — loopback + a random per-store token in each request. Hardening against
  malicious local clients is explicitly out of scope.
- **Concurrency progression:** a single `sync.Mutex` first (correctness; mirrors today's MCP `a.mu`)
  — already enough to let many processes share one store (serialized) — then upgrade to `RWMutex`
  for concurrent reads. MVCC stays deferred (VISION: "single-writer now").

## Target architecture

New package `internal/runtime` (a layer above `engine`, preserving the dependency direction):

- `service.go` — the `Service` interface = the engine's public operation set. `*engine.Engine`
  already satisfies it; both the embedded engine and the remote client implement the **same**
  interface, so CLI/MCP depend on `Service`, not a concrete type. Methods to cover (confirmed in
  `internal/engine/*.go`):
  `CreateScope, Push, Query, Drill, Publish, SiblingOverview, Ancestors, Fork, Consolidate,
  CrossVersion, RollupScope, Trace, RecentTraces, ListScopes, GetScope, ListArtifacts,
  DeleteArtifact, DeleteScope, Config, SetConfig, EmbModel, Close`.
  (`RootScope`/`Ingest` live in `internal/ingest` and call `Service`, so they work over either path.)
- `proto.go` — length-prefixed JSON framing; `Request{id, method, params}` /
  `Response{id, result|error}`; a method registry; error mapping (`core.ErrNotFound` /
  `core.ErrInvalidInput` → a code in the envelope). Domain types serialize via their existing
  `core.*` JSON tags (content `[]byte` → base64 automatically).
- `server.go` (+ `dispatch.go` if it exceeds ~300 LOC) — the daemon: listener, accept loop, one
  goroutine per connection, dispatch, the serialization guard, durability, graceful shutdown, and
  the `runtime.json` info file.
- `client.go` — `Client` implements `Service` over the wire; `Dial(dir)`.
- `discover.go` — read/write `<dir>/runtime.json` `{pid, net, addr, token, started_at, data_dir,
  embed_model}`; liveness (ping / pid) and staleness checks.

Visibility under multiple clients is already correct: every request carries a viewpoint scope, and
`Query`/`SiblingOverview`/`Ancestors` compute bottom-up, published-only visibility **from that
scope** (`internal/engine/visibility.go`). The daemon hides nothing new — it is just the single
owner.

## Session 1 — contract + daemon + client + protocol (serialized)

- Build `internal/runtime`: `Service`, `proto.go`, `server.go`, `client.go`, `discover.go`.
- Daemon `ioc serve -dir <d> -embed <e>`: `engine.Open` once; a single `sync.Mutex` around every
  call; accept loop + per-connection goroutine; dispatch all methods; write `<dir>/runtime.json`.
- **Durability (important):** `EmbeddingStore` keeps vectors in memory until `Sync()` (today Sync
  only runs on Close — see `internal/storage/embstore.go`). A long-lived daemon must `Sync()` after
  each write op (or batch / timer), or a crash loses embeddings. bbolt and CAS are durable on their
  own.
- Graceful shutdown: signal → stop accepting → drain in-flight → `emb.Sync` → `engine.Close` →
  remove `runtime.json`.
- Tests: in-process daemon on a temp dir + mock embedder; a client does
  create_scope/push/query/drill; several concurrent connections (serialized) smoke test;
  `go test -race`.
- **Outcome:** you can talk to a daemon. CLI/MCP not yet switched over.

## Session 2 — CLI and MCP become clients; daemon as default owner; lifecycle

- `resolveService(dir, endpoint) Service`: a live `runtime.json` → `Client`; otherwise embedded
  `engine.Open` (fallback). Used by both `cmd/ioc` and `cmd/ioc-mcp`.
- Refactor `cmd/ioc/engineutil.go` (`openEngine*` → `resolveService`; commands depend on `Service`)
  and `cmd/ioc-mcp` (`ioc{ e *engine.Engine }` → `ioc{ svc runtime.Service }`; handlers call
  `svc.*`; keep `a.mu` for the embedded-fallback path — when remote, the daemon serializes anyway).
- Subcommands: `ioc serve` (foreground daemon), `ioc runtime status|stop` (read `runtime.json`,
  ping, signal). `ioc-mcp` stays the binary Claude launches via `.mcp.json`, but internally becomes
  a client of the daemon.
- **Real multi-client:** several processes (main agent + sub-agents + CLI) on one `-dir` no longer
  conflict — all go through the daemon (still one mutex → serialized, but this already solves
  "dir busy" and delivers shared memory).
- Tests/dogfood: `ioc serve` on `.ioc/data`; in parallel several `ioc push/query` all succeed with
  no "dir busy"; stdio MCP through the daemon + the embedded-fallback path.
- **Outcome:** shared memory works end-to-end.

## Session 3 — concurrent reads, hardening, observability, docs

- Upgrade the guard `sync.Mutex` → `sync.RWMutex`, classifying methods:
  - **reads (RLock):** Query, Drill, Trace, RecentTraces, ListScopes, ListArtifacts, GetScope,
    SiblingOverview, Ancestors, Config, EmbModel.
  - **writes (Lock):** CreateScope, Push, Publish, Fork, Consolidate, CrossVersion, RollupScope,
    DeleteArtifact, DeleteScope, SetConfig.
  - **Audit the nuances:** `Query` appends a trace (a storage write) and can lazily open `e.emb`.
    Fix by opening the embedding store **eagerly** at daemon startup, so the read class is safe under
    RLock (storage is internally thread-safe: bbolt MVCC, `EmbeddingStore` RWMutex, CAS stateless).
    Benchmark concurrent reads vs serialized.
- Hardening: recover from a stale `runtime.json` (dead pid), connection timeouts/limits, per-conn
  panic recovery, a protocol version/handshake field, a clean error envelope.
- Observability: `ioc runtime status` shows uptime, connection count, data dir, embed model;
  basic counters/log.
- Auto-start (optional, off by default): CLI/MCP spawn the daemon on first use.
- Docs: a runtime section in `README.md` / `CLAUDE.md` / `docs/MCP_GUIDE.md`; `.mcp.json` guidance
  (point clients at a shared `-dir`; the daemon owns the embedder).
- **Outcome:** concurrent-read throughput, a robust lifecycle, documented.

## Cross-cutting invariants

- One daemon = one data-dir; shared memory = the same `-dir`. The CLI default `.ioc/data` and the
  MCP default `.ioc/mcp-data` are different stores (different daemons) — point them at one dir to
  share.
- The daemon owns the embedder (the "one embedder per data-dir" rule lives in the daemon).
- ≤ ~300 LOC per file; table-driven tests with `testify/require`; `context.Context` first; return
  errors, no panics. Commit per session on `development/v0.2-review`.

## Deferred (out of runtime scope)

- **MVCC / multi-tenant** (VISION: "MVCC-ready interfaces later") — keep single-writer / RWMutex;
  the `Service` contract is shaped so MVCC slots in later without breaking it.
- **Networked MCP / HTTP transport**, remote/cross-host clients, auth beyond the loopback token.
- **Orphaned-embedding compaction.**
- **Cross-process coordination without a daemon — deliberately rejected**, not merely deferred (see
  "Why a daemon, not multi-process access to one store" above).

## End-to-end verification (after all three sessions)

- `go build/vet/test ./...` and `go test -race ./internal/runtime/...` green.
- `ioc serve -dir .ioc/data -embed <bge>` in the background; in parallel N× `ioc push/query` and the
  stdio MCP — no "dir busy", consistent results; `ioc runtime status` lists connections;
  `ioc runtime stop` shuts down cleanly (embeddings persisted — a query after a daemon restart still
  finds them).
- Multi-agent scenario: two clients under a shared worktree scope — one publishes an artifact, the
  other sees it via `SiblingOverview`/`Query` (bottom-up visibility holds).
