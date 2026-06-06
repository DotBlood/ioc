# Roadmap — v0.2

> Supersedes the v0.1 implementation plan (Phases 0–6 of the removed graph/knowledge engine; complete
> but historical, now archived). This roadmap tracks the **v0.2 thin slice** (module
> `github.com/DotBlood/ioc`, Go 1.26, branch `development/v0.2-review`). Status reflects the code and the
> living docs as of 2026-06.

## Where v0.2 stands

The greenfield slice exists and is green (`go build ./...`, `go test ./...`, `-race`, `go vet`,
`govulncheck` all clean). **The core thesis — the reasoning wall — has been proven empirically on the
real embedder** (see [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md)). Per VISION, proving the
wall was the gate before formal spec rewrites and further storage investment; that gate is cleared.

## DONE

| Area | Status | Reference |
|------|--------|-----------|
| **Core slice** — recursive scopes, two-tier artifacts, progressive disclosure (overview/entry/raw), Push/Query/Drill/Publish/Neighbors/RollupScope/Consolidate/Fork/CrossVersion/Supersede/Trace | DONE | `internal/{core,engine}`, `/CLAUDE.md` |
| **Retrieval modes** — vector, hybrid (BM25+RRF), hierarchical coarse→fine, optional cross-encoder rerank; per-embedder confidence floor + margin + `ranked_by` | DONE | `internal/{search,engine}`, `internal/iocfmt` |
| **Storage** — Meta (bbolt), CAS (sha256+zstd, crash-atomic), EmbeddingStore (append-only), optional at-rest AES-256-GCM (Box) | DONE | `internal/storage`, `encryption.md` |
| **Ingestion** — directory tree → nested scopes; language-aware chunking; idempotent re-sync; sandboxed (`IOC_INGEST_ROOT`); untrusted provenance (`trust=ingested`) | DONE | `internal/ingest` |
| **Runtime daemon** (S1–S3) — single store owner; framed-JSON RPC; CLI/MCP auto-route; RWMutex concurrency; graceful shutdown; 2-principal token ACL + rotation; opt-in mTLS | DONE | `internal/runtime` |
| **Supersession / currency** (layer-1 + layer-2) — `SupersededBy`/`Supersedes`/`IncludeSuperseded`, default-exclude superseded+archived; `Neighbors` (dedup-before-write); `Consolidate(...,supersedes)` batch retire; opt-in recency tie-breaker | DONE | [`SUPERSESSION.md`](SUPERSESSION.md) |
| **Knowledge edges (author-declared)** — typed directed relations (`depends_on`/`contradicts`/`answers`/…) between artifacts; `PushRequest.Relations` + `Relate` (write), `Related(kinds,dir,depth)` traversal (read, excludes superseded); Meta `edges` bucket; CLI `relate`/`related` + MCP `ioc_relate`/`ioc_related`. Unifies semantic memory with a queryable knowledge graph; no LLM-extraction in core. | DONE | `internal/{core,storage,engine}` |
| **Graph-aware retrieval (graph-boost)** — `Query.GraphBoost` (opt-in, OFF by default; reorder-only, `Hit.Score` stays cosine, no-op with no edges → proven default path byte-for-byte untouched). CLI `query/wall -graph-boost`; MCP `graph_boost`. **v1 was centrality-biased; v2 (seed-anchored: g counts edges to the query's top-cosine hits, not global degree) fixes it** — re-running the edged-corpus experiment, v2 lifts the correct dependents into top-K on all four structural questions and stops demoting an already-correct hit (WALL_EXPERIMENT.md, 2026-06-06). Now a useful *implicit* assist alongside the *explicit* `Related` walk. | DONE (v2) | `internal/engine` (`blendGraph`) |
| **`Related` edge-walk validated** as the reliable structural-retrieval path — exact, noise-free answers to "what depends on X" on a real edged corpus, confirmed by a blind agent. | DONE | `internal/engine` (`Related`) |
| **Security** — all deferred V-vulnerabilities Phase A (TM1) + Phase B (TM2 network gate); + TM2 layer-1 (at-rest encryption, runtime ACL, mTLS, persisted ingest_root) | DONE | [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) |
| **All P0 review bugs** — H1 ingest partial-write self-heal, H2 emb-model guard, H3 rerank/confidence coherence, H4 runtime race | DONE | `internal/{ingest,engine,runtime}` |
| **Eval harness + wall proof** — `ioc wall`/`run-scenario`, blind-judge methodology, currency probe; wall holds at 28 and 180 (real bge-small) | DONE | `internal/eval`, `WALL_EXPERIMENT.md` |
| **Core unit-test coverage** — search 0→98.8%, engine ~80%, storage ~79%, iocfmt ~53% | DONE | `internal/{search,engine,storage,iocfmt}/*_test.go` |

## NOW — formal v0.2 specs (this phase)

Write the formal v0.2 specs in `docs/` (PDR, ROADMAP done; FRD, FSD, PAD next), grounded in the code and
reconciling the 11 v0.1→v0.2 model deltas. The v0.1 specs are archived. One doc at a time with review.

## NEXT (open, on-thesis; pick by need, not all required)

| Item | What | Source |
|------|------|--------|
| **Margin-aware weak_match** | The cosine floor is gameable by vocabulary at small N; margin separates present/absent more honestly. Calibrate a per-embedder margin threshold on wall data, then `weak_match = top<floor OR (margin>0 && margin<m)`. **Experiment first, code after.** | [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md) |
| **Shape-aware retrieval** | Flat retrieval is structurally blind to descendant-scope artifacts (recall 0.00 when querying from a parent). Either auto-route to hierarchical when the viewpoint has rolled-up descendants, or loudly warn. | [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md) (2026-06-06) |
| **Graph-aware retrieval v3 refinements** | v2 (seed-anchored) shipped and works (see DONE). Remaining polish: degree-normalize `g` (some 1-hop-from-a-different-seed noise remains), kind/direction-filtered boost, recall expansion (pull non-visible edge-neighbours), rerank composition, and tuning the blend weight / seed count. | code (`internal/engine`) + eval |
| **Robustness cleanup** | RPC ctx/deadlines, chunk boundaries inside strings/fenced code, CRLF→\n normalization on Windows, `rerankTop` tail + score-count validation | code (`internal/{runtime,ingest,engine}`) |

## DEFERRED

- **TM2 layer-2** (required before multi-tenant SaaS, not before): per-tenant principals/ACLs, KMS/keyring
  key management, MVCC/multi-writer, distinct per-client certs / rotation, plaintext↔encrypted re-encrypt
  tool. See [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) and the
  [`/VISION.md`](../VISION.md) security gate.
- **Supersession layer-3**: per-claim/partial supersession, automatic contradiction detection. Layer-2 is
  sufficient for the proven loop; layer-3 is not currently justified. See
  [`SUPERSESSION.md`](SUPERSESSION.md).

## FROZEN

- **Document/file retrieval polish**: content-coherent BM25/rerank, `.iocignore`/provenance, semantic
  rollups. Document retrieval competes with grep/LSP (commodity) and dogfooded at 0/4
  overview-sufficiency; the unique value is **reasoning memory**. Do not invest here unless the reasoning
  wall demands it. (`ingest` itself stays — it feeds the store; only the document-*retrieval* quality work
  is frozen.)

## Out of scope (v0.2)

Networked/HTTP transport for the daemon; auto-start beyond the opt-in flag; a bundled LLM; a user-facing
graph/edge query API (the graph is internal plumbing). These are explicitly not part of the v0.2 slice.
