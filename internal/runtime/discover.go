package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// runtimeFile is written inside a store's data dir while a daemon owns it.
const runtimeFile = "runtime.json"

// RuntimeInfo is the published descriptor of a running daemon (one per data dir).
type RuntimeInfo struct {
	PID           int    `json:"pid"`
	Net           string `json:"net"`  // "tcp"
	Addr          string `json:"addr"` // e.g. 127.0.0.1:54321
	Token         string `json:"token"`
	StartedAt     string `json:"started_at"` // RFC3339
	DataDir       string `json:"data_dir"`
	EmbedModel    string `json:"embed_model"`
	TLS           bool   `json:"tls,omitempty"`             // daemon requires mTLS (off by default)
	TLSServerName string `json:"tls_server_name,omitempty"` // SNI / cert name to verify
}

func runtimePath(dir string) string { return filepath.Join(dir, runtimeFile) }

func writeRuntimeInfo(dir string, info RuntimeInfo) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	// 0o600 (V3): runtime.json carries the daemon's bearer token. World-readable
	// (0o644) let any local user on a shared host read it and drive the full RPC
	// (shutdown, delete_scope, ingest, drill any content). Keep it owner-only.
	//
	// Atomic write: publishInfo rewrites this file on token rotation while clients
	// concurrently readRuntimeInfo it in Dial. A plain truncate+write (os.WriteFile)
	// can be observed half-written → json.Unmarshal fails → Open mis-detects "no
	// daemon" and falls back to an embedded engine on the same dir → bbolt lock
	// error. Write to a temp file then rename so a reader sees only the complete
	// old or complete new file, never a torn one.
	return atomicWriteFile(runtimePath(dir), append(b, '\n'), 0o600)
}

// Rename-retry budget for the Windows sharing-violation case (see atomicWriteFile).
// Generous because runtime.json is rewritten only at startup (no readers yet) and on
// the rare manual token rotation, so a few hundred ms worst case never matters; on
// POSIX the first rename succeeds and the loop runs exactly once. Package vars so a
// test could lower them.
var (
	renameAttempts   = 50
	renameRetryDelay = 5 * time.Millisecond
	// Reader-side retry budget for the symmetric Windows race: a reader's os.ReadFile
	// can transiently fail to open runtime.json while the writer is mid-rename. NOT
	// applied to os.IsNotExist (the legitimate "no daemon" signal).
	readAttempts   = 50
	readRetryDelay = 5 * time.Millisecond
)

// atomicWriteFile writes data to path via a same-directory temp file + rename, so a
// concurrent reader never observes a partially written file. Mirrors the CAS blob
// pattern (internal/storage/cas.go). perm is applied to the temp file before rename
// (CreateTemp is already 0o600, but we set it explicitly so a sensitive file is
// never briefly more permissive than intended).
//
// os.Rename replaces the target atomically on the same volume. On POSIX it succeeds
// even while readers hold the old file open (they keep reading the unlinked inode).
// On Windows, os.ReadFile opens WITHOUT FILE_SHARE_DELETE, so MoveFileEx can briefly
// fail with a sharing violation while a reader has the target open. A reader's window
// is sub-millisecond, so we retry the rename a bounded number of times; this preserves
// the no-torn-read guarantee (a failed rename leaves the complete old file in place)
// without a Windows-specific reader path. The symmetric race — a reader's open failing
// while THIS rename is in flight — is handled by readRuntimeInfo's matching retry.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "runtime-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	var rerr error
	for attempt := 0; attempt < renameAttempts; attempt++ {
		if rerr = os.Rename(tmpName, path); rerr == nil {
			return nil
		}
		time.Sleep(renameRetryDelay)
	}
	os.Remove(tmpName)
	return rerr
}

// readRuntimeInfo loads the descriptor; os.IsNotExist(err) means "no daemon".
//
// It tolerates the transient Windows race where a concurrent writer is mid-rename:
// os.ReadFile can briefly fail to open the target (sharing violation) — distinct
// from "file absent". A genuine os.IsNotExist returns immediately (the fast, common
// "no daemon" path Open relies on); any OTHER read or unmarshal error is treated as
// transient and retried a bounded number of times. With atomic writes a torn read
// shouldn't occur, but retrying on an unmarshal error too is cheap insurance.
func readRuntimeInfo(dir string) (RuntimeInfo, error) {
	var info RuntimeInfo
	var lastErr error
	for attempt := 0; attempt < readAttempts; attempt++ {
		b, err := os.ReadFile(runtimePath(dir))
		if err != nil {
			if os.IsNotExist(err) {
				return info, err // definitive: no daemon — never retry
			}
			lastErr = err // transient open failure (e.g. writer mid-rename on Windows)
		} else if uerr := json.Unmarshal(b, &info); uerr != nil {
			lastErr = uerr // partial/torn read — retry
		} else {
			return info, nil
		}
		time.Sleep(readRetryDelay)
	}
	return info, lastErr
}

func removeRuntimeInfo(dir string) error {
	if err := os.Remove(runtimePath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
