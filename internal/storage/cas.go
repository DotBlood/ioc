// Package storage provides the on-disk plumbing for the IOC slice:
// a content-addressable blob store (CAS), a fixed-size embedding store,
// and a bbolt-backed metadata store. It imports core for domain types.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"

	"github.com/DotBlood/ioc/internal/core"
)

// CAS is a content-addressable file store: full blobs, sha256-keyed, zstd-compressed.
//
// Layout: <root>/obj/aa/bb/<full_hash>
//
// The slice stores whole blobs (no FastCDC chunking — deferred until a real
// large artifact appears). Deduplication is automatic by content hash.
type CAS struct {
	root string
	box  *Box // at-rest encryption (nil = off)
}

// maxCASDecodedBytes bounds zstd decompression on Load to defend against a
// decompression bomb (a tiny stored object that expands to gigabytes → OOM).
// Legitimate blobs (chunks ≤512 KiB, distilled summaries/answers) are far below
// this. Package var so tests can lower it.
var maxCASDecodedBytes uint64 = 128 << 20 // 128 MiB

// NewCAS creates a content-addressable store rooted at the given directory. A
// non-nil box encrypts object payloads at rest (AES-256-GCM); nil = plaintext.
func NewCAS(root string, box *Box) *CAS { return &CAS{root: root, box: box} }

// StoreBytes stores content addressably and returns its hash.
// If the content already exists it is not rewritten (dedup).
func (c *CAS) StoreBytes(_ context.Context, data []byte) (core.ContentHash, error) {
	h := core.NewContentHash(data)
	if c.has(h) {
		return h, nil
	}
	path := c.objPath(h)
	dir := filepath.Dir(path)
	// 0o700 dirs: stored blobs may be proprietary; keep the object tree owner-only.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return core.ContentHash{}, fmt.Errorf("cas store: mkdir: %w", err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return core.ContentHash{}, fmt.Errorf("cas store: zstd writer: %w", err)
	}
	compressed := enc.EncodeAll(data, nil)
	enc.Close()
	// Encrypt after compress (V4/at-rest): the content hash is of the PLAINTEXT, so
	// dedup/Has/naming are unchanged; bind the ciphertext to its address via AAD.
	if c.box.Enabled() {
		compressed, err = c.box.Seal(compressed, h[:])
		if err != nil {
			return core.ContentHash{}, fmt.Errorf("cas store: encrypt: %w", err)
		}
	}
	// Write to a temp file in the same directory, then rename into place (atomic
	// on the same volume). The previous direct WriteFile could leave a truncated
	// blob on a crash/concurrent read that has() then reported as present,
	// permanently poisoning the entry (StoreBytes would dedup-skip it and Load
	// would fail forever). Rename makes the object appear only when complete.
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return core.ContentHash{}, fmt.Errorf("cas store: temp: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600) // owner-only blob (CreateTemp is already 0o600, but be explicit vs umask)
	if _, err := tmp.Write(compressed); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return core.ContentHash{}, fmt.Errorf("cas store: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return core.ContentHash{}, fmt.Errorf("cas store: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return core.ContentHash{}, fmt.Errorf("cas store: rename: %w", err)
	}
	return h, nil
}

// Store reads all of r and stores it. Convenience over StoreBytes.
func (c *CAS) Store(ctx context.Context, r io.Reader) (core.ContentHash, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return core.ContentHash{}, fmt.Errorf("cas store: read: %w", err)
	}
	return c.StoreBytes(ctx, data)
}

// Load returns the decompressed content for a hash.
func (c *CAS) Load(_ context.Context, hash core.ContentHash) ([]byte, error) {
	compressed, err := os.ReadFile(c.objPath(hash))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cas load: %w", os.ErrNotExist)
		}
		return nil, fmt.Errorf("cas load: read: %w", err)
	}
	// Decrypt before decompress (the bomb cap still applies to the decoded size).
	if c.box.Enabled() {
		compressed, err = c.box.Open(compressed, hash[:])
		if err != nil {
			return nil, fmt.Errorf("cas load: %w", err)
		}
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxCASDecodedBytes))
	if err != nil {
		return nil, fmt.Errorf("cas load: zstd reader: %w", err)
	}
	defer dec.Close()
	data, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("cas load: decode: %w", err)
	}
	return data, nil
}

// Open returns a reader for the content (convenience over Load).
func (c *CAS) Open(ctx context.Context, hash core.ContentHash) (io.ReadCloser, error) {
	data, err := c.Load(ctx, hash)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Has reports whether content with the given hash exists.
func (c *CAS) Has(_ context.Context, hash core.ContentHash) (bool, error) {
	return c.has(hash), nil
}

func (c *CAS) has(hash core.ContentHash) bool {
	_, err := os.Stat(c.objPath(hash))
	return err == nil
}

func (c *CAS) objPath(hash core.ContentHash) string {
	h := hash.String()
	return filepath.Join(c.root, "obj", h[:2], h[2:4], h[4:])
}
