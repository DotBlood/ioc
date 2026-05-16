package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	"github.com/tigerwill90/fastcdc"

	"github.com/DotBlood/ioc/internal/model"
)

// CAS is a content-addressable file store.
//
// Layout:
//
//	<root>/obj/fi/le/<full_hash>           — small file (< 1MB), zstd compressed
//	<root>/obj/fi/le/<full_hash>.manifest  — chunk manifest (large file)
//	<root>/chk/<prefix>/<chunk_hash>       — individual chunk, zstd compressed
type CAS struct {
	root string
}

// chunkThreshold is the threshold above which files are chunked.
const chunkThreshold = 1 << 20 // 1 MB

// chunkManifest describes how a large file is split into chunks.
type chunkManifest struct {
	FileHash    string       `json:"file_hash"`
	FileSize    int64        `json:"file_size"`
	Compression string       `json:"compression"`
	Chunks      []chunkEntry `json:"chunks"`
}

type chunkEntry struct {
	Offset int64  `json:"offset"`
	Hash   string `json:"hash"`
	Size   int    `json:"size"`
}

// NewCAS creates a content-addressable store rooted at the given directory.
func NewCAS(root string) *CAS {
	return &CAS{root: root}
}

// Store reads content from r, stores it addressably by SHA-256 hash,
// and returns the hash. If the content already exists, returns the existing
// hash without writing (automatic deduplication).
func (c *CAS) Store(ctx context.Context, r io.Reader) (model.ContentHash, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return model.ContentHash{}, fmt.Errorf("cas store: read: %w", err)
	}

	hash := sha256.Sum256(data)

	// Dedup: already stored?
	var existing model.ContentHash
	copy(existing[:], hash[:])
	if c.has(existing) {
		return existing, nil
	}

	if int64(len(data)) > chunkThreshold {
		if err := c.storeChunked(existing, data); err != nil {
			return model.ContentHash{}, err
		}
	} else {
		if err := c.storeFull(existing, data); err != nil {
			return model.ContentHash{}, err
		}
	}

	return existing, nil
}

// Open returns a reader for the content identified by hash.
func (c *CAS) Open(ctx context.Context, hash model.ContentHash) (io.ReadCloser, error) {
	h := hex.EncodeToString(hash[:])

	// Check if it's a chunked file.
	manifestPath := c.objPath(h) + ".manifest"
	if _, err := os.Stat(manifestPath); err == nil {
		return c.openChunked(manifestPath)
	}

	// Full blob.
	return c.openFull(c.objPath(h))
}

// Has reports whether content with the given hash exists in the store.
func (c *CAS) Has(ctx context.Context, hash model.ContentHash) (bool, error) {
	return c.has(hash), nil
}

// Delete removes content from the store. No-op if not found.
func (c *CAS) Delete(ctx context.Context, hash model.ContentHash) error {
	h := hex.EncodeToString(hash[:])

	// Delete manifest + chunks if present.
	manifestPath := c.objPath(h) + ".manifest"
	if data, err := os.ReadFile(manifestPath); err == nil {
		var mf chunkManifest
		if json.Unmarshal(data, &mf) == nil {
			for _, ch := range mf.Chunks {
				os.Remove(c.chunkPath(ch.Hash))
			}
		}
		os.Remove(manifestPath)
	}

	// Delete the full blob.
	os.Remove(c.objPath(h))
	return nil
}

// Root returns the storage root path.
func (c *CAS) Root() string { return c.root }

func (c *CAS) has(hash model.ContentHash) bool {
	h := hex.EncodeToString(hash[:])
	if _, err := os.Stat(c.objPath(h)); err == nil {
		return true
	}
	if _, err := os.Stat(c.objPath(h) + ".manifest"); err == nil {
		return true
	}
	return false
}

func (c *CAS) storeFull(hash model.ContentHash, data []byte) error {
	h := hex.EncodeToString(hash[:])
	path := c.objPath(h)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("cas store: mkdir: %w", err)
	}

	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return fmt.Errorf("cas store: zstd writer: %w", err)
	}
	compressed := enc.EncodeAll(data, nil)
	enc.Close()

	if err := os.WriteFile(path, compressed, 0644); err != nil {
		return fmt.Errorf("cas store: write: %w", err)
	}
	return nil
}

func (c *CAS) openFull(path string) (io.ReadCloser, error) {
	compressed, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cas open: %w", os.ErrNotExist)
		}
		return nil, fmt.Errorf("cas open: read: %w", err)
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("cas open: zstd reader: %w", err)
	}
	data, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("cas open: zstd decode: %w", err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (c *CAS) storeChunked(hash model.ContentHash, data []byte) error {
	h := hex.EncodeToString(hash[:])

	chunker, err := fastcdc.NewChunker(context.Background(),
		fastcdc.WithStreamMode(),
		fastcdc.With64kChunks(),
	)
	if err != nil {
		return fmt.Errorf("cas store: chunker: %w", err)
	}

	enc, _ := zstd.NewWriter(nil)

	var entries []chunkEntry
	var offset int64

	processChunk := func(off, length uint, chunk []byte) error {
		chHash := sha256.Sum256(chunk)
		chHex := hex.EncodeToString(chHash[:])

		compressed := enc.EncodeAll(chunk, nil)
		chPath := c.chunkPath(chHex)
		if err := os.MkdirAll(filepath.Dir(chPath), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(chPath, compressed, 0644); err != nil {
			return err
		}

		entries = append(entries, chunkEntry{
			Offset: offset,
			Hash:   chHex,
			Size:   len(chunk),
		})
		offset += int64(len(chunk))
		return nil
	}

	err = chunker.Split(bytes.NewReader(data), processChunk)
	if err != nil {
		return fmt.Errorf("cas store: split: %w", err)
	}

	err = chunker.Finalize(processChunk)
	if err != nil {
		return fmt.Errorf("cas store: finalize: %w", err)
	}
	enc.Close()

	mf := chunkManifest{
		FileHash:    h,
		FileSize:    int64(len(data)),
		Compression: "zstd",
		Chunks:      entries,
	}

	mfData, _ := json.Marshal(mf)
	manifestPath := c.objPath(h) + ".manifest"
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		return fmt.Errorf("cas store: mkdir manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, mfData, 0644); err != nil {
		return fmt.Errorf("cas store: write manifest: %w", err)
	}
	return nil
}

func (c *CAS) openChunked(manifestPath string) (io.ReadCloser, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("cas open: read manifest: %w", err)
	}
	var mf chunkManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("cas open: parse manifest: %w", err)
	}

	dec, _ := zstd.NewReader(nil)
	var readers []io.Reader
	for _, entry := range mf.Chunks {
		compressed, err := os.ReadFile(c.chunkPath(entry.Hash))
		if err != nil {
			return nil, fmt.Errorf("cas open: read chunk %s: %w", entry.Hash, err)
		}
		chunkData, err := dec.DecodeAll(compressed, nil)
		if err != nil {
			return nil, fmt.Errorf("cas open: decode chunk %s: %w", entry.Hash, err)
		}
		readers = append(readers, bytes.NewReader(chunkData))
	}
	return io.NopCloser(io.MultiReader(readers...)), nil
}

func (c *CAS) objPath(h string) string {
	return filepath.Join(c.root, "obj", h[:2], h[2:4], h[4:])
}

func (c *CAS) chunkPath(h string) string {
	return filepath.Join(c.root, "chk", h[:2], h[2:4], h[4:])
}
