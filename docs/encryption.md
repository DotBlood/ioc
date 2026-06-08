# At-rest encryption (opt-in)

IOC can encrypt its on-disk payloads with **AES-256-GCM** (stdlib only). It is
**off by default** — without a key the store is byte-identical to an unencrypted
one. Threat model: another local user / a stolen disk or backup reading the store
files directly. (For a single-user machine, `0o600` modes + OS full-disk encryption
already cover most of this; app-level encryption matters mainly for shared storage,
backups, and the eventual multi-tenant/SaaS step.)

## Enabling it

Provide a **raw 32-byte key** via either environment variable (the first set wins):

- `IOC_ENCRYPTION_KEY` — the key as **hex** (64 chars), **base64**, or raw 32 bytes.
- `IOC_ENCRYPTION_KEYFILE` — a file whose (trimmed) contents decode the same way.

```bash
export IOC_ENCRYPTION_KEY=$(openssl rand -hex 32)   # 64 hex chars
ioc serve -dir .ioc/data -embed http://127.0.0.1:8088
```

A **new** store created with a key on is marked encrypted; thereafter it can only
be opened with that key.

## ⚠️ Lose the key = lose the data

There is **no recovery and no password reset**. If you lose the key, the encrypted
store is unreadable. Back the key up independently of the data. There is also **no
in-place migration** between plaintext and encrypted: pick one when you create the
store. (A separate `reencrypt` tool is a possible future follow-up.)

## What is and isn't protected

- **Encrypted:** CAS object payloads (after zstd compression), every embedding
  record (`emb.dat`), and all bbolt **values** in `meta.db` (scope/artifact/trace
  JSON, and config values except the `enc` sentinel).
- **NOT encrypted (leaks):** bbolt **keys** — scope/artifact/trace IDs (ULIDs) and
  the `ingest:<abspath>` mapping keys, so the absolute path of an ingested root is
  visible. CAS object filenames are `sha256(plaintext)`, so identical content is
  correlatable by filename across stores. Keys must stay plaintext for lookup and
  iteration; only values are sealed.

## Fail-closed behavior

IOC refuses to mix formats rather than silently read garbage:

| store on disk | key present? | result |
|---|---|---|
| plaintext (new/empty) | no | OK, plaintext |
| plaintext (has data) | yes | **error** (no in-place migration) |
| new/empty | yes | OK, becomes encrypted |
| encrypted | no | **error** (set `IOC_ENCRYPTION_KEY`) |
| encrypted | yes (wrong) | opens, but the first read fails GCM auth with a clear error |
| encrypted | yes (right) | OK |

Detection uses a plaintext `enc` sentinel in `meta.db` and a per-file encrypted
flag in the embedding-store header (defense in depth).

## Implementation

`internal/storage/crypto.go` (`Box`: AES-256-GCM, random 12-byte nonce per record,
AAD binds each ciphertext to its address — content hash for CAS, bbolt key for
meta, record ref for `emb.dat`). The key threads from `engine.Open` (env or
`engine.WithEncryptionKey`) into `storage.NewCAS/OpenMeta/OpenEmbeddingStore`.
