# Code & theory review — findings (2026-06-05)

> A dedicated security/robustness deep-dive (threat models, decompression bombs, DoS/parse limits,
> embedder trust boundary, secrets, path containment, prompt injection) lives in
> [`SECURITY_AND_VULNERABILITIES.md`](SECURITY_AND_VULNERABILITIES.md).

Adversarial review of the whole repo via 7 parallel domain reviewers (storage, runtime, engine,
ingest, cmd/MCP, cross-cutting, theory). Each finding was pushed to be **disproved** before listing;
the highest-impact ones were re-verified directly (reproduced or read at file:line). Status legend:
**[verified]** = I reproduced/confirmed it this session; **[reproduced]** = a reviewer reproduced it
with a throwaway test; **[code]** = confirmed by reading the cited code.

> Honest correction: earlier in this project I asserted the suite was "race-clean." That was wrong —
> `go test -race ./internal/runtime/` fails intermittently (see H4). Single runs had passed by luck.

---

## P0 — correctness bugs (silent wrong results / data loss) — fix regardless of strategy

**H1 — Ingest mid-file failure permanently under-indexes a file, silently. [reproduced ×2]**
`internal/ingest/reconcile.go:78-127`. `reconcileFile` deletes a file's existing chunks then pushes new
ones in a loop with no transaction; the per-file `sig` is written on **every** chunk. If `pushChunk`
fails on chunk N>0 (embedder hiccup, ctx timeout, daemon error), chunks 0..N-1 are committed (carrying
the new sig) and the old chunks are already gone. The next re-ingest sees
`existing[0].Meta["sig"] == sig` → marks the file **Unchanged** → the missing chunks are never
recreated until the file content changes. Two reviewers reproduced (7-chunk file left at 1 chunk;
next run reports `unchanged`). Fix: don't trust chunk-0 sig as the completeness marker — require the
expected chunk count to match, or write the sig only after all chunks succeed, or roll back partial
chunks on failure.

**H2 — Wrong embedder/space queried silently → garbage results, no error. [verified/code]**
`internal/engine/engine.go:85-102` persists only `emb_dims` (never the model). Mock and
`bge-small` are **both 384-dim**, so ingesting/pushing with bge then querying with the default mock
(`-embed` empty) — or vice versa — passes the dim check and compares vectors across incompatible
spaces. `internal/search/brute.go:53-55` `dot()` also returns 0 on a length mismatch (so a 32-dim
query vs a 384-dim store yields all-zero scores) with no error, and the query path never checks
`len(qvec)` against `e.emb.Dims()`. `weak_match`/`margin` are then computed on meaningless scores, so
the "I don't have it" safety net also misfires. Fix: persist `emb_model` and refuse to open/query on
mismatch (lazily for HTTP embedders whose model is "" until first call); make `dot()`/Query error on
dim mismatch.

**H3 — Rerank reorders, but Hit.Score/weak_match/margin stay cosine → false confidence. [code]**
`internal/engine/query.go:105` always sets `score = cosineByID[r.ID]`; `internal/engine/rerank.go`
reorders by cross-encoder but the score is discarded, and it reranks `Summary` (the label for
documents, not content — `rerank.go:24`). `internal/iocfmt/out.go:67-79` derives `top_score`/`margin`
from `hits[0].Score`/`hits[1].Score`, so after a rerank reorder `margin` can be **negative** and
`top_score` understated. This is exactly the dogfood Q2 false-confidence (0.747, `weak=false`, wrong
hit). Fix: carry the rerank score into the hit (or both), compute confidence from the active signal,
and rerank/BM25 over **content** for documents (this is also design-doc W1/W4).
> **FIXED** (`development/v0.2-review`): `engine/rerank.go` `rankText` ranks document CONTENT from CAS
> (reasoning ranks its summary, which IS its embedded text); rerank + hybrid-BM25 use it. The
> cross-encoder logit is sigmoid-normalized into `core.Hit.RerankScore` and `iocfmt.QueryOut` derives
> `weak_match`/`margin`/`top_score` from the active signal (rerank vs `core.RerankFloor` 0.5 when
> reranked, else cosine vs `ConfidenceFloor`) and reports `ranked_by`. `MinScore` was made a cosine
> PRE-gate (before rerank) so the cross-encoder can't be undone by a post-rerank cosine drop.
> Deliberately left: rerank only sees the top-RerankN cosine window; NaN/Inf reranker output guarding
> is V8 (input-validation hardening, pre-TM2); hybrid BM25 decompresses all visible docs per query.
> Tests: `engine/rerank_test.go` (content-not-label, rerank-ordered confidence, weak-when-rejected,
> MinScore pre-gate, cosine path unchanged).

**H4 — `go test -race ./internal/runtime/` fails intermittently. [verified]**
Reproduced at `-count=20` and `-count=40`. The race is a Write by `sync.(*Once).doSlow` from
`defer srv.Stop()` (`internal/runtime/runtime_test.go:191`) concurrent with the daemon's Serve
goroutine doing `engine.Open`/`Meta` writes (`startDaemon` `go srv.Serve()`, runtime_test.go:18). Root
cause is at least a test-lifecycle gap (deferred `Stop` racing the spawned `Serve` goroutine; possibly
leaked goroutine across `-count`), and possibly unsynchronized `Server` setup fields (`s.ln/s.token/
s.startedAt` written by `Serve` without a lock, read elsewhere). Fix: synchronize `Server` lifecycle
fields and/or deterministically join the `Serve` goroutine; add a repeat-count race job to CI. NOTE:
this means S1–S3 were NOT actually race-clean as claimed.

---

## P1 — robustness / safety (real, but need crash/corruption/edge or are bounded)

**M1 — embstore reopen has no count clamp → panic on a corrupt/over-counted file. [reproduced]**
`internal/storage/embstore.go` (Open): `count` comes from the header with no check against the physical
size; `Get` then slices `s.data[offset:offset+recSize]` out of range → panic (violates "no panics").
This is the state a power-loss between the record write and the header write can leave (the
append-only "crash-atomic" claim only holds across a clean Sync). Fix: clamp
`count = min(count, (fileSize-header)/recSize)` on Open (self-healing); soften the comment.

**M2 — CAS non-atomic write + existence-only `has()` poisons an object on crash. [reproduced]**
`internal/storage/cas.go:34-53,99` writes via `os.WriteFile` directly to the final path; `has()` returns
true once the file *exists*. A crash mid-write leaves a partial file that `has()` reports present →
`StoreBytes` no-ops forever and `Load` fails (`zstd: unexpected EOF`). Fix: temp file + `os.Rename`
(atomic on same volume); treat a decode failure as "absent, re-store".

**M3 — Runtime RPC has no deadlines/cancellation → stuck write wedges all clients; half-open conn leak.
[code]** `internal/runtime/client.go` ignores `ctx` and does blocking framed I/O; `server.go` serveConn
uses `context.Background()` with no read deadline. A slow/hung embedder under the exclusive write Lock
blocks every reader's RLock with no timeout; a peer sending a length prefix and no body parks a
goroutine in `io.ReadFull` forever. Fix: thread ctx + `SetDeadline`; per-frame idle deadline; optional
max-conns.

**M4 — Chunk boundary detection has false positives inside strings/fenced code. [reproduced]**
`internal/ingest/lang.go:76-90` `isUnitStart` is a column-0 prefix match, so a line starting with
`func `/`#`/`export ` inside a raw string or a Markdown ``` fence is treated as a unit boundary,
mis-segmenting the chunk. Content isn't lost (packing re-merges) but chunk text/line-ranges are wrong.
Fix: track fenced/raw-string/block-comment state while scanning.

**M5 — CRLF files leave a stray `\r` in embeddings, CAS content, and labels. [reproduced]**
`internal/ingest/chunk.go` splits on `"\n"` only; on Windows (the primary dev platform) every line keeps
a trailing `\r`, contaminating the embedded text and summaries. Fix: normalize `\r\n`→`\n` at read time
(decide whether `Sig` is over original or normalized bytes).

**M6 — `ioc_ingest` reads arbitrary local paths (MCP, model-driven). [code] (security/by-design)**
`cmd/ioc-mcp/handlers.go` → `internal/ingest/resolve.go` does `filepath.Abs` with no containment, so a
model (or a prompt-injected one) can `ioc_ingest path="~/.ssh"` and read secrets back via
`ioc_query kind=document`. The skip list is a quality filter, not a boundary. Fix: constrain to a
configured root (e.g. `IOC_INGEST_ROOT`)/cwd subtree; at minimum document the trust assumption.

**M7 — `rerankTop` drops the candidate tail and panics on a bad reranker. [code]**
`internal/engine/rerank.go:14-41` returns only the reranked head (`ordered[:n]`), discarding
`ordered[n:]` (shrinks results below topK if `RerankN<topK`), and indexes `scores[i]` assuming the
reranker returned exactly `len(docs)` scores (panic otherwise; recovered in the daemon, crashes the CLI).
Fix: append the tail; validate score count.

**M8 — Hierarchical retrieval: un-rolled scopes are invisible, and it bypasses Published visibility.
[code]** `internal/engine/rollup.go:54-115` only considers scopes with a `RollupEmbRef`; ingest creates
rollups only for dirs with direct files, so intermediate/relevant scopes are unreachable in
hierarchical mode. It also returns all artifacts of selected descendant scopes with no `Published`
gate (only a tier filter), diverging from `visibleArtifacts`' bottom-up rules — an undocumented
visibility asymmetry. Fix: fallback tier for un-rolled scopes; decide/document the publication rule.

---

## P2 — lower severity / hardening

- **L1** Parent/child scope walks (`visibility.go` ancestorsOf, `query.go` scopePath, rollup descendants)
  have **no cycle guard** → infinite loop on a corrupt scope graph. Add a `seen` set. [code]
- **L2** `Fork` (`branch.go`) hardcodes `Tier: TierWorkspace` (demotes canonical truths) and does not copy
  the source rollup (forked scope drops out of hierarchical retrieval). [code]
- **L3** `runtime.json` token written `0o644` (world-readable on multi-user hosts). Use `0o600`. [code]
- **L4** Client doesn't verify `resp.ID == req.ID` (latent desync trap). [code]
- **L5** Lazy `ensureEmb`/`openEmb` (`engine.go:94`) mutate `e.emb` without a mutex — safe under the
  daemon's exclusive write Lock today, unsafe if the engine is used concurrently otherwise. [code]
- **L6** HTTP embedder vectors are not guaranteed normalized; `dot()`==cosine assumes they are. Normalize
  on store or assert once. [code]
- **L7** CLI uses `flag.ExitOnError` with the `Parse` error discarded, and has no required-arg validation
  (e.g. `query` with empty `-text` embeds "" and returns hits) — inconsistent with the MCP side's
  `RequireString`. [code]
- **L8** `proto.go` allows a 64 MiB allocation per frame **before** the token check (loopback DoS). Lower
  `maxFrame` for control frames / check token first. [code]

## Good properties confirmed (keep)
- embstore torn-tail recovery + Put error rollback; bbolt usage (View/Update, no nested txns); the
  daemon's read-path is safe because storage layers are independently concurrency-safe; shutdown-RPC
  ordering (reply then `go Stop`) avoids the wg self-deadlock; `stopOnce` idempotency; deterministic
  ranking (stable sorts + id tie-break); ingest idempotency on the clean path; oversize-fallback line
  math is correct; `FirstLine` truncates on rune boundaries.

## Test-gap map (no tests today)
- `internal/search` — **0 tests** (cosine `dot` silent-zero, BM25, RRF). This is where H2's footgun lives.
- `internal/eval` — **0 tests** (the wall-metric math is unverified).
- `internal/iocfmt` — **0 tests** (weak_match/margin/ConfidenceFloor + all parsers).
- `cmd/ioc`, `cmd/ioc-mcp` — **0 tests** (arg wiring, daemon-vs-embedded routing, MCP handlers).
- Untested risk paths: ingest partial-write/rollback (H1), embedder/model mismatch (H2), rerank reorder
  vs reported score (H3), runtime concurrent write+read & slow-write wedging & stale-discovery, embstore
  over-counted reopen (M1), CAS partial object (M2).

---

## Theory review — the important part

**T1 — The wall metric is keyword-circular and gameable. [verified by reading `internal/eval/query_eval.go:41-51`].**
`overviewSatisfied = targetOK && mentionOK && ForbidOK`, where `mentionOK` substring-matches
author-chosen `MustMentionAny` keywords against author-written summaries. In `hoe.json` the summary is
"the wooden tool head splits…" and the expectation is `["wood","split"]`. The metric passes iff the
author put the same keywords in both places — it measures author self-consistency, not whether an agent
could act on the summary. Combined with the lexical MockEmbedder, the green wall metrics certify the
**pipeline**, not the wall. The design doc's W6 must also fix this (blind/independent questions,
LLM-judge answering from overview text only, and **lift over a no-IOC baseline**, not an absolute 0.8).

**T2 — Supersession/currency is the biggest unexamined assumption (deepest risk).**
Everything is built for "store a good summary, retrieve by similarity." But the hard problem of
reasoning memory is **supersession**: decision B contradicts earlier decision A, and the system must
surface B and suppress A even though A is still the best *semantic* match. There is no implemented
mechanism — `DerivedFrom`/`Archived`/`Version` exist in the type but are unused in ranking. Content-based
rerank (W1/W4) does not touch this. If this assumption is false (and across versions it is exactly the
failure VISION promises to solve), similarity-ranked immutable summaries will confidently feed agents
their own outdated conclusions — worse than no memory.

**T3 — Strategic: reasoning memory is the unique value; document retrieval competes with grep and likely
loses.** grep/ripgrep/LSP are exact, always-current, zero-infra. A 384-d chunk index loses on exact
symbols and only sometimes wins on conceptual queries. The dogfood stressed the commodity case
(whole-repo code search) and barely tested the unique value. Implication the design doc under-commits to:
W1–W3 (+ much of W4/W5/W6) polish the commodity feature. Consider **freezing document/ingest polish** and
funding it only if reasoning memory proves out.

**T4 — The two-regime reframe is partly a rationalization.** "overview-sufficiency was mis-applied to
code" is true for the *broken* implementation, but the design's own extractive summaries + semantic
rollups (W1/W3) put documents back into the "shown is a lossy summary" regime where the metric *does*
apply. The 0/4 was relocated, not exonerated; the reasoning wall remains untested.

---

## Revised priorities (supersedes the staging in RETRIEVAL_AND_WALL_FIXES.md Part IV)

1. **P0 correctness bugs first (strategy-independent):** H1 (ingest partial-write), H2 (emb_model guard +
   query dim check), H3 (rerank/confidence coherence + content-based rerank/BM25), H4 (runtime race).
   These cause silent wrong results / data loss / false confidence today. — **ALL DONE** on
   `development/v0.2-review` (H1 `0cbf4a0`, H2 `b63a686`, M1 `aea8c8f`, M2 `be6ef17`, V5 `40fb33f`,
   H4 `a3ffc80`, H3 this change); suite + `-race` green.
2. **P1 — test the reasoning wall BEFORE polishing document retrieval.** Build the falsifiable experiment:
   real distilled decisions from this repo's own history across ≥1 version boundary (~100–300 artifacts),
   ~25 **blind, independently-authored** questions, scored by an LLM judge answering **from overview text
   only** against gold answers, plus a planted **superseded-vs-current currency probe**. Failure =
   answer-grounded overview-sufficiency <0.8 on the real embedder, or stale-over-current.
3. **P1 — design a supersession mechanism** (T2): pushing an artifact with `DerivedFrom`/contradiction
   marks the prior superseded; ranking demotes/hides it by default with an explicit "show history" drill.
4. **P2 — document retrieval (design-doc W1–W3) only if** the reasoning wall holds and there is spare
   effort; otherwise reposition IOC as reasoning memory + (optionally) a thin wrapper over the agent's own
   code tools rather than a code index.

The robustness items (M1–M8, L1–L8) are folded into the relevant fix sessions; M1/M2 (crash robustness)
and M3 (RPC timeouts) should ship alongside P0.
