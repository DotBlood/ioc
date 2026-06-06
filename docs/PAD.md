# Platform Architecture Document (PAD) — v0.2

> How IOC is built: packages and their dependency direction, the storage layout, embedding, retrieval,
> the runtime daemon, and the interfaces. Companion to [`FSD.md`](FSD.md) (the contract) and
> [`FRD.md`](FRD.md) (the requirements). **The code is the source of truth** — this describes the actual
> tree under `internal/` and `cmd/`. On any conflict, the code wins.

---

## 1. Technology & shape

- **Single Go module** `github.com/DotBlood/ioc`, **Go 1.26**. Pure-Go core; Windows-friendly.
- **Dependencies (few):** `oklog/ulid/v2` (IDs), `klauspost/compress` (zstd, CAS), `go.etcd.io/bbolt`
  (metadata), `mark3labs/mcp-go` (the MCP server), `testify` (tests).
- **Local-first.** Everything runs on one machine against a local data dir. The **only** optional
  external service is the embedder (`py/embed_server.py`, FastAPI) — and a deterministic in-process mock
  replaces it offline.
- **No LLM, no graph database, no message bus.** Reasoning/summaries/embeddings come from the caller's
  model; the "graph" is author-declared edges in a bbolt bucket, not a graph engine.

---

## 2. Package layout & dependency direction

```
internal/
  core/     domain types (Scope, Artifact, Tier, Kind, Detail, Query, Hit, Seed, Edge, Trace, IDs)
  embed/    Embedder + Reranker interfaces; MockEmbedder; HTTPEmbedder/HTTPReranker; endpoint policy
  search/   brute-force cosine (Set) + Okapi BM25 + RRF fusion
  storage/  CAS (sha256+zstd) · EmbeddingStore (float32, append-only) · Meta (bbolt) · Box (AES-256-GCM)
  engine/   the public API: Open + all operations (Push/Query/Drill/Relate/Related/Consolidate/…)
  ingest/   directory tree → nested scopes; language-aware chunking; containment sandbox
  runtime/  the daemon owning one store + a framed-JSON client (Service/proto/server/client/discover/tls)
  eval/     scenario + wall harness, metrics (the eval that proves the thesis)
  iocfmt/   JSON shaping for CLI/MCP output (HitOut/QueryOut: weak_match/margin/ranked_by/trust)
cmd/
  ioc/      CLI (memory ops, eval, ingest, the daemon)
  ioc-mcp/  stdio MCP server (the ioc_* tools)
py/
  embed_server.py   optional external embedding/rerank service (FastAPI); venv in py/.venv (gitignored)
```

**Dependency direction (acyclic).** `core` is a leaf (no internal imports). `embed` and `search` are
leaves (no `core` import — `search` is keyed by plain string IDs). `storage`, `engine`, `eval`,
`iocfmt`, `ingest` import `core`; `engine` additionally imports `embed`/`storage`/`search`; `runtime`
imports `engine`; `cmd/*` wire everything together. There are no cycles.

---

## 3. Storage architecture

A store is one data dir holding three substores plus a daemon descriptor. Sensitive files are owner-only
(`0o600`).

- **Meta — `meta.db` (bbolt).** Buckets: `config`, `scopes`, `artifacts`, `traces`, `edges`. Keys are
  plaintext (needed for lookup); record **values** are optionally encrypted at rest via `Box`. Scans
  (e.g. artifacts-in-scope, edges-from/to) are linear `ForEach` over a bucket — adequate at the slice's
  scale; secondary indexes are deferred.
- **CAS — content-addressed blobs.** SHA-256 address, zstd-compressed, optionally `Box`-sealed (AAD =
  the hash). Writes are **crash-atomic**: write to a temp file in the same dir, then `os.Rename` into
  place, so a half-written blob never becomes visible under its hash. Loads are bounded by a
  decompression-memory cap (anti-bomb).
- **EmbeddingStore — append-only float32 vectors.** A vector is addressed by a 1-indexed
  `core.EmbeddingRef`. A write becomes visible only after a **count-header commit-pointer** advances
  past the new record, with `fsync` on flush — a crash sees either the old count or the complete
  record. On reopen an over-stated count is clamped to the real file size (fails safe, never panics on
  an out-of-range read). Orphaned vectors of deleted/re-chunked artifacts stay as dead weight (never
  surfaced; compaction deferred).
- **Box — at-rest encryption (opt-in, OFF by default).** AES-256-GCM over record values / CAS blobs;
  a `config["enc"]` sentinel lets the engine detect a key/format mismatch before any encrypted read.
  Key = data: losing the key loses the store (documented in [`encryption.md`](encryption.md)).
- **`runtime.json` — the daemon descriptor** (pid, addr, token, embedder identity, TLS flags); written
  atomically (temp + rename, with a bounded retry for the Windows sharing-violation case).

---

## 4. Embedding & reranking

- **`embed.Embedder` interface** (`Embed`, `Dims`, `Model`). Two implementations: **`MockEmbedder`**
  (deterministic, offline — validates the pipeline, not real wall numbers) and **`HTTPEmbedder`**
  (POSTs text to the external service over TCP `http://host:port` or a Unix socket `unix:/path`).
- **`embed.Reranker` / `HTTPReranker`** — an optional cross-encoder used by the last-mile rerank stage.
- **Endpoint policy (security).** IOC POSTs embedded text to the endpoint, so a **non-loopback** target
  is refused unless `IOC_ALLOW_REMOTE_EMBED=1`, and a **plaintext (http) remote** is refused outright
  (https required); unix/loopback are always allowed. Loopback is judged by literal host (no DNS).
  Responses are size-bounded and NaN/Inf-checked.
- **One embedder per store dir.** The store persists the embedder model identity; opening/querying under
  a different model is refused (vector spaces differ). Switching embedders requires recalibrating the
  confidence floor.

---

## 5. Retrieval internals

- **`search` (leaf).** `Set` — brute-force cosine over normalized vectors (dot product, O(N),
  id-tiebreak). `BM25` — Okapi (k1=1.2, b=0.75, smoothed IDF, lazy avg-length). `RRF` — reciprocal-rank
  fusion (k=60) for hybrid. No `core` import; keyed by string IDs.
- **`engine` orchestration.** `Query` builds the candidate set (visibility or coarse→fine rollups),
  applies currency/kind filters, ranks (cosine, or RRF in hybrid), an optional recency tie-break, a
  cosine MinScore pre-gate, and an optional cross-encoder rerank; it returns hits whose `Score` is
  always cosine (rerank score carried separately) and records a trace. The structural axis (`Related`)
  is a separate BFS over the `edges` bucket. Full contract in [`FSD.md`](FSD.md) §5–6.
- **`iocfmt`.** Shapes hits to JSON and computes the surfaced confidence (`weak_match`, `margin`,
  `top_score`, `ranked_by`) from the signal that ordered the hits, plus the `trust`/`untrusted_content`
  provenance flags.

---

## 6. Runtime daemon

`internal/runtime` makes one store shareable across processes without fighting bbolt's exclusive lock.

- **Ownership.** `ioc serve` opens the engine ONCE and serves clients; the data dir's `runtime.json`
  advertises the bound loopback address + token. `runtime.Open(dir, …)` returns a remote `*Client` when
  a daemon owns `dir`, else an embedded `*engine.Engine` — so CLI/MCP transparently route to a daemon
  when present and fall back to embedded otherwise. One daemon = one data dir.
- **Protocol.** Internal framed-JSON RPC: a 4-byte big-endian length prefix + a JSON
  `request`/`response` envelope, over loopback TCP (random per-store token). `runtime.Service` mirrors
  the engine op set; both `*engine.Engine` and `*runtime.Client` satisfy it.
- **Concurrency.** In-process `RWMutex`: parallel reads, serialized writes; writes flush embeddings for
  durability; per-request panic recovery; graceful shutdown via signal or a control op.
- **Auth & hardening.** Constant-time token compare; a 2-principal ACL (full owner token + optional
  read-only token) gated by method tier; token rotation; pre-auth frame-size caps (only a full-token
  request lifts the 1 MiB→64 MiB tier); idle/conn caps; JSON depth limits. **mTLS** is opt-in and does
  NOT replace the token (loopback-prep for a future non-loopback deployment).

---

## 7. Interfaces

- **Library** (`internal/engine`) — the embeddable public API; the engine *is* the contract.
- **CLI** (`cmd/ioc`) — every operation from a shell, JSON output; an agent can drive it via Bash.
  Memory commands route through `runtime.Open` (daemon-or-embedded); `serve`/`run-scenario` use a direct
  embedded engine.
- **MCP server** (`cmd/ioc-mcp`) — a **thin** stdio wrapper exposing the engine as `ioc_*` tools (config
  via `IOC_DIR`/`IOC_EMBED`). It mirrors the library and adds no logic of its own; it routes through the
  same `runtime.Open`.

---

## 8. Ingestion pipeline

`internal/ingest` mirrors a directory tree into nested scopes and chunks text files into
`KindDocument` artifacts at language-aware boundaries (Go/Python/Markdown/JS-TS/generic), embedding the
**raw chunk text** while keeping a short `path:lines` label as the summary. It is **idempotent**
(reconciles the store to the current tree via a per-chunk content signature), **sandboxed** (confined to
`IOC_INGEST_ROOT`, symlink-resolved, traversal-proof), and tags chunks `trust=ingested`. It takes a
small `Store` interface, so it works over either the embedded engine or the daemon client. (Document
*retrieval* quality is frozen — see ROADMAP — but ingestion itself feeds the store.)

---

## 9. Deployment & operational model

- **Local-first.** Run against a local `-dir`; start the daemon (`ioc serve`) to share it across the
  CLI, the MCP server, and sub-agents, or omit it and use an embedded engine (single owner of the dir).
- **The embedder** runs as a separate process (`py/embed_server.py`) on loopback, or use the offline
  mock for pipeline validation.
- **SaaS path (gated).** The top scope maps to a tenant; single-writer now, MVCC-ready interfaces
  later. Multi-tenant access must wait for the remaining security items (per-tenant principals/ACLs,
  KMS/keyring, MVCC/multi-writer, per-client certs) — see
  [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) and the ROADMAP.

---

## 10. Removed from the v0.1 PAD

The in-RAM **StatefulGraph** engine and its BFS/DFS/traversal API; the multi-stage **ANN/HNSW**
retrieval pipeline; **structural-snapshot anchors** and the deep archive/diff machinery; a
**Python summarization/AI layer** (v0.2's Python is embedding/rerank only — no LLM); and the v0.1
package tree / entry points (`cmd/iocctl`, `pkg/api`, `internal/{graph,knowledge,pipeline,session,…}`).
v0.2 replaces these with the lean `internal/` tree above, brute-force + BM25/RRF retrieval,
author-declared edges in a bbolt bucket, and archive = mark the scope `Archived`.
