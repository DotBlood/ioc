package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/DotBlood/ioc/internal/core"
)

// EmbeddingStore is a file-backed, fixed-size, append-only embedding store.
// Each record is dims*4 bytes (float32). Refs are 1-indexed.
// In-memory buffer + Sync() to disk (mmap deferred; same Put/Get/Len/Close API).
type EmbeddingStore struct {
	mu      sync.RWMutex
	path    string
	dims    int
	recSize int
	count   int
	data    []byte
	dirty   bool
}

const embHeaderSize = 16 // 8 bytes count + 4 bytes dims + 4 reserved

// OpenEmbeddingStore opens or creates an embedding store file for a fixed dimension.
func OpenEmbeddingStore(path string, dims int) (*EmbeddingStore, error) {
	if dims <= 0 || dims > 4096 {
		return nil, fmt.Errorf("embedding store: invalid dims %d", dims)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("embedding store: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("embedding store: open: %w", err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("embedding store: stat: %w", err)
	}

	recSize := dims * 4
	var count, fileSize int

	switch {
	case fi.Size() == 0:
		header := make([]byte, embHeaderSize)
		binary.LittleEndian.PutUint32(header[8:12], uint32(dims))
		if _, err := file.Write(header); err != nil {
			return nil, fmt.Errorf("embedding store: write header: %w", err)
		}
		fileSize = embHeaderSize
	case fi.Size() < int64(embHeaderSize):
		return nil, fmt.Errorf("embedding store: file too small (%d bytes)", fi.Size())
	default:
		fileSize = int(fi.Size())
		header := make([]byte, embHeaderSize)
		if _, err := file.ReadAt(header, 0); err != nil {
			return nil, fmt.Errorf("embedding store: read header: %w", err)
		}
		if fileDims := int(binary.LittleEndian.Uint32(header[8:12])); fileDims != dims {
			return nil, fmt.Errorf("embedding store: dims mismatch: file=%d requested=%d", fileDims, dims)
		}
		count = int(binary.LittleEndian.Uint32(header[0:8]))
	}

	buf := make([]byte, fileSize)
	if fileSize > 0 {
		if _, err := file.ReadAt(buf, 0); err != nil {
			return nil, fmt.Errorf("embedding store: read: %w", err)
		}
	}
	return &EmbeddingStore{path: path, dims: dims, recSize: recSize, count: count, data: buf}, nil
}

// Put appends a vector and returns its 1-indexed ref.
func (s *EmbeddingStore) Put(vec []float32) (core.EmbeddingRef, error) {
	if len(vec) != s.dims {
		return 0, fmt.Errorf("embedding store: expected %d dims, got %d", s.dims, len(vec))
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	oldCount := s.count
	newCount := oldCount + 1
	newLen := embHeaderSize + newCount*s.recSize
	if newLen > len(s.data) {
		newBuf := make([]byte, alignPow2(newLen))
		copy(newBuf, s.data)
		s.data = newBuf
	}
	offset := embHeaderSize + oldCount*s.recSize
	for i, v := range vec {
		binary.LittleEndian.PutUint32(s.data[offset+i*4:offset+(i+1)*4], math.Float32bits(v))
	}
	s.count = newCount
	binary.LittleEndian.PutUint32(s.data[0:8], uint32(newCount))
	s.dirty = true
	return core.EmbeddingRef(newCount), nil
}

// Get retrieves a vector by 1-indexed ref.
func (s *EmbeddingStore) Get(ref core.EmbeddingRef) ([]float32, error) {
	if ref == 0 {
		return nil, fmt.Errorf("embedding store: ref must be > 0")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx := int(ref) - 1
	if idx < 0 || idx >= s.count {
		return nil, fmt.Errorf("embedding store: ref %d out of range (count=%d)", ref, s.count)
	}
	offset := embHeaderSize + idx*s.recSize
	vec := make([]float32, s.dims)
	for i := 0; i < s.dims; i++ {
		bits := binary.LittleEndian.Uint32(s.data[offset+i*4 : offset+(i+1)*4])
		vec[i] = math.Float32frombits(bits)
	}
	return vec, nil
}

// Len returns the number of stored embeddings.
func (s *EmbeddingStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// Dims returns the fixed vector dimension.
func (s *EmbeddingStore) Dims() int { return s.dims }

// Sync flushes the in-memory buffer to disk.
func (s *EmbeddingStore) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.WriteFile(s.path, s.data, 0o644); err != nil {
		return fmt.Errorf("embedding store: sync: %w", err)
	}
	s.dirty = false
	return nil
}

// Close syncs and releases resources.
func (s *EmbeddingStore) Close() error { return s.Sync() }

// alignPow2 rounds up to the nearest power of 2 (minimum 4096).
func alignPow2(n int) int {
	if n < 4096 {
		return 4096
	}
	v := n - 1
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return v + 1
}
