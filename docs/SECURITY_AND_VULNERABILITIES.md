# Errors & vulnerabilities — deep study

Study artifact (no code changed). A security + integrity audit of IOC, grounded in a read-only pass
with file:line. Complements [`REVIEW_FINDINGS.md`](REVIEW_FINDINGS.md) (general code+theory review)
with a dedicated security/robustness lens — decompression bombs, DoS/parse limits, the embedder trust
boundary, secrets, path containment, and the py embed server, which the general review under-covered.

## Threat models (severity is meaningless without one)

- **TM1 — current:** local-first, a single developer on their own machine, dev branch. The local user
  is trusted; the embed server is local; there are no untrusted network clients. BUT IOC is an **LLM
  memory**: content is ingested and fed back to the model (ingested files → drill → back into the
  model), and crashes/data corruption hurt the user directly.
- **TM2 — future (per VISION):** multi-agent / multi-user / SaaS. Untrusted clients, a shared host, a
  network surface appear. Then almost everything below rises sharply in severity.

Each item lists severity **TM1 → TM2**, location, mechanism, impact, and remediation (study only — not
applied).

---

## A. Security / trust boundary

**V1 — `ioc_ingest` reads arbitrary local paths + indirect prompt injection. [TM1: MED → TM2: HIGH]**
`cmd/ioc-mcp/handlers.go:56-81` → `internal/ingest/resolve.go:17` (`filepath.Abs`, no containment),
`reconcile.go:57` (WalkDir from any root). The model chooses `path` (Required, unvalidated) → it can
ingest `~/.ssh`, `.env`, credentials, then read them back via `ioc_query kind=document` / `ioc_drill`.
Two risks: (1) secret exfiltration; (2) **indirect prompt injection** — an ingested untrusted file with
instructions later surfaces in results and the model may follow it (IOC is LLM memory; content returns
to the model). Real even in TM1, because ingested content is often untrusted. Remediation: constrain
`ioc_ingest` to a configured root (`IOC_INGEST_ROOT`)/cwd subtree (`filepath.Rel` after `Abs`, reject
escape); mark/isolate untrusted content; document the trust assumption.

**V2 — `IOC_EMBED` sends all text to any endpoint, plaintext, no TLS pinning. [TM1: LOW-MED → TM2: HIGH]**
`cmd/ioc-mcp/main.go:72` (env), `internal/embed/http.go:44-57` (accepts `http://`/`https://`/`unix:`, a
default `http.Client`, no origin/cert check). The daemon POSTs **all** summaries/chunks to whatever
`IOC_EMBED` names. If the env is tampered or points remote → all memory leaks in clear text.
Remediation: default to loopback/unix; require https for non-loopback or an explicit `--allow-remote-embed`;
never log content.

**V3 — token: world-readable 0o644 + non-constant-time compare + work BEFORE auth. [TM1: LOW → TM2: HIGH]**
`internal/runtime/discover.go:30` (`0o644`), `dispatch.go:25` (`req.Token != s.token`, not CT),
`proto.go:143` + `dispatch.go` (a 64 MiB allocation and JSON unmarshal happen BEFORE the token check).
On a multi-user host any local user can read `runtime.json` → full RPC (`shutdown`, `delete_scope`,
`ingest` of any path, `drill` of any content). The pre-auth work is an unauthenticated local
DoS/amplification. Remediation: `runtime.json` → `0o600`; check the token before large allocations;
`subtle.ConstantTimeCompare` (cosmetic over loopback); lower `maxFrame` for control frames.

**V4 — data at rest is unencrypted, mode 0o644. [TM1: LOW → TM2: MED-HIGH]**
CAS (zstd, not encrypted — `cas.go`), `meta.db` (bbolt plaintext, `meta.go:34` open 0o644), `emb.dat`,
`runtime.json` (the token) are all world-readable plaintext. Memory may hold secrets/proprietary
reasoning. `.gitignore` covers `.ioc*/` (no accidental commit — good). Remediation: 0o600 on the dir/files;
at-rest encryption is a TM2/SaaS question.

---

## B. Resource exhaustion / DoS

**V5 — zstd decompression bomb in `CAS.Load`. [TM1: MED → TM2: MED-HIGH]**
`cas.go:64-83`: `zstd.NewReader(nil)` + `DecodeAll` with NO `WithDecoderMaxMemory`/window limit →
unbounded decompression. CAS sources: ingest (≤512 KB chunks) and `Push.Content` (MCP `ioc_push content`
is unbounded). A crafted object/large content → OOM on `drill`/`Load`. Remediation: `WithDecoderMaxMemory`
(e.g. 64–256 MB) + a size cap on `Push.Content`.

**V6 — framing/connections: 64 MiB pre-auth, no read deadline, unbounded connections. [TM1: MED → TM2: HIGH]**
`proto.go:17,143` (64 MiB), `server.go:116` (a goroutine per conn, no cap), `serveConn` with no
`SetReadDeadline`. Half-open connections pin goroutines/memory forever; max-size frames pin gigabytes;
all before the token. Local DoS. Remediation: per-frame idle deadline, max-conns, token-before-alloc,
a smaller `maxFrame`.

**V7 — JSON with no depth/size limit. [TM1: LOW → TM2: MED]**
`dispatch.go:66` / `server.go:133` / `meta.go` — `encoding/json` has no nesting limit; `Params`
(`json.RawMessage`) has no size cap (within the 64 MiB frame). Deep nesting → CPU/stack. Remediation: a
decoder with a depth/size limit; cap `Params`.

**V8 — embedder/reranker response read unbounded; NaN/Inf not validated. [TM1: LOW → TM2: MED]**
`http.go:146`, `reranker.go:66` — `json.NewDecoder(resp.Body)` with no `io.LimitReader`; dims/counts are
checked but `NaN/Inf` are not. A buggy/hostile endpoint (see V2) → OOM, or NaN in cosine → ranking
garbage. Remediation: `io.LimitReader`, `IsNaN/IsInf` checks, normalization (see also review L6).

**V9 — ingest with no file-count/depth limit; whole index in memory. [TM1: LOW-MED → TM2: MED]**
`reconcile.go:57` (WalkDir over the whole tree), `ensureDirScope` recursion with no depth cap;
`buildIndex` loads ALL artifacts/scopes into memory. A huge tree → memory / many scopes. (WalkDir does
not follow symlinks — ok.) Remediation: caps on files/chunks per run, depth, and index size.

**V10 — py embed server has no auth; bind host is env-controlled. [TM1: LOW → TM2: MED]**
`py/embed_server.py:117-126` — `uvicorn.run(host=IOC_EMBED_HOST)`; no API key/middleware, no explicit
body-size limit. The default is a unix socket/loopback (ok), but a `IOC_EMBED_HOST=0.0.0.0` misconfig
exposes an unauthenticated service (compute abuse/DoS). Remediation: default loopback/unix; warn on
non-loopback; body-size limit.

---

## C. Integrity/availability errors (security lens; full detail in REVIEW_FINDINGS.md)

- **V11 = H1** ingest partial-write → silent permanent under-indexing (data loss). [TM1: HIGH]
- **V12 = H2** mock↔bge-small (both 384d) → silent query in the wrong vector space (integrity). [HIGH]
- **V13 = M1** embstore reopen with no count clamp → **panic** on a corrupt/over-counted file
  (availability). [MED]
- **V14 = M2** CAS non-atomic write + existence-only `has()` → a "poisoned" object on crash (permanent
  corruption). [MED]
- **V15 = H4** intermittent runtime data race under `-race` (undefined behavior). [MED-HIGH]
- **V16 = L1** no cycle guard in scope-graph walks → infinite loop on a corrupt graph (availability).
  [LOW→MED]

## D. Injection / input trust

- **V17 — indirect prompt injection via ingested content** (see V1): ingested untrusted text returns to
  the model via query/drill. A systemic RAG problem, amplified by arbitrary `ioc_ingest`. Remediation:
  mark provenance/untrusted; never present as instructions; isolate.

## E. Supply chain / platform

- **V18** — deps (bbolt, mcp-go, ulid, klauspost/zstd, testify) are current, low risk; go 1.26.3 is
  bleeding edge (no pinning audit / no `govulncheck` in CI). Remediation: add `govulncheck` to CI.

---

## Priority summary (honest, by threat model)

**Matters even in TM1 (current, single developer):**
1. Data integrity: **V11/V12** (silent loss/garbage), **V13/V14** (panic/corruption on crash).
2. **V5** decompression bomb (OOM on your own data) and **V1/V17** prompt injection via ingested content
   (because that content is often untrusted and is fed back to the model).
3. **V15** runtime race.

**Deferrable while purely local-first, but MUST-FIX before TM2/SaaS:**
- V2 (embed exfiltration/TLS), V3 (token/0600/pre-auth), V4 (at-rest/0600), V6 (conn/frame DoS),
  V7/V8 (parse/embedder limits), V9 (ingest limits), V10 (py server), V18 (govulncheck).

**Bottom line:** for current use (your own machine) the dangerous items are the **integrity errors** (C)
and the **decompression-bomb / prompt-injection** pair (V5/V1/V17); classical "network" security
(V2–V10) is low risk now but is **debt that must be cleared before any multi-user/SaaS step** — worth
recording as an explicit gate in VISION/roadmap.
