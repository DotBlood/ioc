# Security Policy

IOC is a **local-first, model-agnostic memory & knowledge layer for LLMs**. It runs on the user's own
machine, stores an LLM's distilled reasoning, and never bundles or calls an LLM itself. This document
states what is in scope, the threat model the project is engineered against, and how to report a
vulnerability.

## Reporting a vulnerability

**Please report security issues privately — do not open a public issue or PR for them.**

- Preferred: GitHub **Security Advisories** → *“Report a vulnerability”* on
  https://github.com/DotBlood/ioc (Security tab). This opens a private channel with the maintainer.
- Include: affected file/commit, a description, reproduction steps or a PoC, and the impact under one of
  the threat models below.

You will get an acknowledgement; fixes land on the active development branch (see below). Please allow a
reasonable window for a fix before any public disclosure.

## Supported versions

IOC is **pre-release** (a v0.2/v0.3 thin slice; no tagged releases yet). Security fixes are applied to
the **active development branch** (currently `development/v0.3-research`) and merged to `master`. The
removed v0.1 engine lives only in git history (`development/v0.2-review`) and is **not** supported.

| Version | Supported |
|---|---|
| `master` / active `development/*` | ✅ |
| v0.1 (historical, in git history) | ❌ |

## Threat model

Severity only means something against a threat model. IOC tracks two (full register:
[`docs/SECURITY_AND_VULNERABILITIES.md`](docs/SECURITY_AND_VULNERABILITIES.md)):

- **TM1 — current (local-first).** A single developer on their own machine. The local user is trusted;
  the embedder is local; there are no untrusted network clients. The real risks here are **data
  integrity** (silent loss/corruption of the user's memory) and the fact that IOC is LLM memory —
  **ingested files are fed back to the model**, so untrusted ingested content is an indirect
  prompt-injection vector even on a single machine.
- **TM2 — future (multi-user / multi-agent / SaaS).** Untrusted clients, a shared host, and a network
  surface appear. Network-security items rise sharply in severity. **Multi-user/SaaS is NOT yet
  supported and must not be deployed until the TM2 gate below is fully closed.**

## What IOC does to defend itself

Engineered controls (audited; see the register for file:line and the per-item history):

- **No LLM in the core.** IOC stores/serves results; it never executes model-authored text as
  instructions. There is no `eval`/`exec`/dynamic dispatch on input.
- **Runtime daemon auth.** The daemon binds **loopback only** (`127.0.0.1`), authenticates every RPC
  with a 128-bit CSPRNG bearer token compared in **constant time**, enforces a 2-principal ACL
  (full/owner vs optional read-only) by method tier, and rotates tokens on demand. The token lives in
  `runtime.json` at mode `0600`. **mTLS** (TLS 1.3, mutual cert) is opt-in and *augments* — never
  replaces — the token.
- **DoS bounds.** Pre-auth frames are capped (~1 MiB) and the larger data-frame cap is unlocked only by
  genuine full-token auth; per-frame read deadlines, a max-connections cap, and a JSON nesting-depth
  limit defend against slowloris / resource-pinning / parser abuse.
- **At-rest encryption (opt-in).** AES-256-GCM with a random per-record nonce and address-bound AAD
  (ciphertext cannot be relocated to another key). Off by default; see
  [`docs/encryption.md`](docs/encryption.md). On-disk files are created `0600` (dirs `0700`).
- **Ingest sandbox.** `ioc ingest` is confined to `IOC_INGEST_ROOT` (default: CWD), symlink-resolved and
  traversal-proof, with file-count / chunk / depth caps. Ingested chunks are tagged
  `trust=ingested` and surface an `untrusted_content` flag on query — **treat such content as data, never
  as instructions.**
- **Embedder endpoint policy.** All embedded text is POSTed to `-embed`/`IOC_EMBED`. A non-loopback
  target is refused unless `IOC_ALLOW_REMOTE_EMBED=1`, a plaintext (`http://`) remote is refused outright
  (https required), and loopback is judged by **literal host** (no DNS → no DNS-rebinding). Responses are
  size-capped and validated (NaN/Inf rejected). The Python embed server fails closed on a non-loopback
  bind and enforces a request body-size limit.
- **Supply chain.** Go dependencies are pinned; CI runs `govulncheck` (pinned, blocking) plus
  `go vet`, `-race`, `gofmt`, and `golangci-lint`.

## Hardening status

- **Phase A + Phase B (TM1 + the TM2 network gate): DONE** — ingest containment & untrusted-provenance,
  resource caps, `0600` at-rest modes, token `0600` + constant-time + pre-alloc auth, frame/connection/
  JSON DoS bounds, embedder endpoint policy + response bounds, the Python embed-server gate,
  `govulncheck` in CI.
- **TM2 layer 1: DONE** — opt-in at-rest encryption, 2-principal runtime ACL + token rotation, opt-in
  mTLS, persisted ingest root.
- **TM2 layer 2 (REQUIRED before any multi-user/SaaS step, NOT yet done):** per-tenant principals/ACLs
  (beyond full/read-only), KMS/keyring key management + passphrase/KDF, a plaintext↔encrypted
  re-encryption tool, distinct per-client certs + rotation, and DNS endpoint allow-listing. **Do not ship
  multi-tenant access until these land.**

## Operator responsibilities

- Keep the data directory and any `IOC_ENCRYPTION_KEYFILE` owner-only (`chmod 0600`); the encryption key
  must be 32 cryptographically-random bytes (there is no password KDF).
- Only point `-embed`/`IOC_EMBED` at an endpoint you trust — it receives all stored text.
- Treat anything you `ingest` as untrusted input to the model.

## Scope

In scope: the Go module (`cmd/`, `internal/`), the runtime daemon protocol, the MCP server, and the
Python embed server (`py/`). Out of scope: the behaviour of the external embedder/reranker model and of
the LLM that consumes IOC's output (model-layer concerns), and the historical v0.1 engine.
