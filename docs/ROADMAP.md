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
| **Graph-aware retrieval (graph-boost)** — `Query.GraphBoost` (opt-in, OFF by default; reorder-only, `Hit.Score` stays cosine, no-op with no edges → proven default path byte-for-byte untouched). CLI `query/wall -graph-boost`; MCP `graph_boost`. **v1 was centrality-biased; v2 (seed-anchored: g counts edges to the query's top-cosine hits, not global degree) fixes it** — re-running the edged-corpus experiment, v2 lifts the correct dependents into top-K on all four structural questions and stops demoting an already-correct hit (WALL_EXPERIMENT.md, 2026-06-06). Now a useful *implicit* assist alongside the *explicit* `Related` walk. A later v3 query-seeded PPR variant (R2) was **tried and rejected** — no gain over v2 for the added complexity (WALL_EXPERIMENT.md, R2). | DONE (v2) | `internal/engine` (`blendGraph`) |
| **`Related` edge-walk validated** as the reliable structural-retrieval path — exact, noise-free answers to "what depends on X" on a real edged corpus, confirmed by a blind agent. | DONE | `internal/engine` (`Related`) |
| **Collapsed-tree retrieval (R1)** — `Query.Collapsed`: flat over the visible set ∪ ALL descendant-scope artifacts in one pass (no coarse→fine routing, no rollups needed). **Now the user-facing default** (`ioc query`, MCP `ioc_query`) — it strictly dominates plain flat (0.85 vs 0.00 on tree; ties on distinctive) and beats hierarchical on tree (0.85 vs 0.74). (The earlier "hierarchical wins on dense 0.96" claim was retracted in R6 as a hybrid/BM25 confound — coarse routing wins in no regime; see `WALL_EXPERIMENT.md`.) Engine zero-value stays flat. | DONE (default) | `internal/engine` (`collapsedCandidates`), `WALL_EXPERIMENT.md` |
| **Security** — all deferred V-vulnerabilities Phase A (TM1) + Phase B (TM2 network gate); + TM2 layer-1 (at-rest encryption, runtime ACL, mTLS, persisted ingest_root) | DONE | [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md) |
| **All P0 review bugs** — H1 ingest partial-write self-heal, H2 emb-model guard, H3 rerank/confidence coherence, H4 runtime race | DONE | `internal/{ingest,engine,runtime}` |
| **Eval harness + wall proof** — `ioc wall`/`run-scenario`, blind-judge methodology, currency probe; wall holds at 28 and 180 (real bge-small) | DONE | `internal/eval`, `WALL_EXPERIMENT.md` |
| **Multi-signal ranking — author-declared importance (R3)** — opt-in `Query.ImportanceWeight` (CLI `-importance-weight`, OFF by default) blends `(1−w)·cosine + w·importance`, importance derived from `Tier` (worktree=canonical > workspace) — a third axis beside relevance + recency, kept no-LLM (declared, not LLM-scored). Clean no-op on the single-tier wall corpus (recall identical at w=0 vs 0.3 → no regression). | DONE (opt-in) | `internal/engine` (`blendImportance`), `WALL_EXPERIMENT.md` |
| **Margin-aware confidence + per-embedder calibration (R4/R4b)** — `weak_match = empty OR top<floor OR margin<MarginFloor` (margin gate cosine-path only) plus a distinct `confidence` code (`ok`/`floor_miss`/`margin_ambiguous`/`empty`); `ioc calibrate` derives the floor per-embedder via split-conformal and writes `conf.floor.<model>` to config (output carries `calibrated`). Removes the hardcoded-floor dependency; honest finding: on bge-small the margin gate is largely redundant with the well-tuned floor — its value is the embedder-independent confidence-code affordance. | DONE | `internal/engine`, `internal/core` (`ResolveConfidence`), `WALL_EXPERIMENT.md` |
| **Cross-encoder abstention — borderline auto-rerank + rerank-floor calibration (R5)** — root-caused the gameable cosine floor (absent questions clear it on shared vocabulary) and dense-cluster recall misses to one cause: bi-encoder cosine ranks/abstains on topical similarity, not answer-relevance. Shipped: `core.Decide` (shared verdict), an **abstention metric** (`eval.Abstention` FPR/FNR over present/absent probes, printed by `ioc calibrate`), `ioc calibrate -rerank` → `conf.rerank.<model>`, **borderline `Query.AutoRerank`** (ON by default for `ioc query`/`ioc_query`; reranks only weak/near-tied cosine results, cosine fallback when no reranker), RerankN 20→50 (+`-rerank-n`), `config set/get`, and a `/rerank` **double-sigmoid fix** in `py/embed_server.py` (raw logits → ~5× wider present/absent margin). Verified FPR 0 / FNR 0 with the rerank floor (gap 0.106 vs cosine 0.019). | DONE | `internal/{core,engine,eval,iocfmt}`, `cmd/ioc`, `py/embed_server.py`, `WALL_EXPERIMENT.md` |
| **Core unit-test coverage** — search 0→98.8%, engine ~80%, storage ~79%, iocfmt ~53% | DONE | `internal/{search,engine,storage,iocfmt}/*_test.go` |

## NOW — formal v0.2 specs (this phase)

The formal v0.2 specs are written and live in `docs/` — PDR, ROADMAP, FRD, FSD, PAD — grounded in the
code and reconciling the v0.1→v0.2 model deltas (the v0.1 specs are archived). Remaining work on them is
upkeep: keep each spec in step with the code as the open NEXT items land.

## NEXT (open, on-thesis; pick by need, not all required)

The v0.3 retrieval-research program (R1–R6, recorded in [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md))
closed most of this list — its shipped and rejected outcomes are in DONE above (collapsed default,
graph-boost v2 / v3-PPR rejected, author-declared importance, margin-aware confidence + calibration,
cross-encoder abstention + borderline auto-rerank). **The "auto mode-selector (hierarchical on dense)"
item was investigated (R6) and DROPPED:** its premise — "hierarchical wins on dense 0.96 vs collapsed
0.85" — was a confound (the `hierarchical` mode bundles a hybrid/BM25 fine stage; coarse routing alone
scores 0.85 = flat, and *hurts* on tree at 0.74; the hybrid gain itself is a synthetic-corpus artifact,
neutral on the real wall). Collapsed dominates coarse routing in every measured regime, so there is no
regime an auto-selector would improve. See [`WALL_EXPERIMENT.md`](WALL_EXPERIMENT.md) R6. What remains
open and on-thesis:

| Item | What | Source |
|------|------|--------|
| **Memory-evolution superseding summaries** | Let the authoring agent optionally author a merged/rewritten **superseding** summary (still its words, still append-only) — revise-in-place behavior without an LLM in core. | [`SUPERSESSION.md`](SUPERSESSION.md), code (`internal/engine`) |
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
