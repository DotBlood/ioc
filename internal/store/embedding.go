package store

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/DotBlood/ioc/internal/model"
)

// EmbeddingStore is a file-backed fixed-size embedding vector store.
// Each record is `dims * 4` bytes (float32). Records are append-only.
// The file is read/written with a memory buffer; periodic sync to disk.
//
// In a future version this can be replaced with mmap for zero-copy reads;
// the API surface (Put/Get/Len/Close) remains the same.
type EmbeddingStore struct {
	mu      sync.RWMutex
	path    string
	dims    int
	recSize int // dims * 4
	count   int
	data    []byte // in-memory buffer: header + records
	dirty   bool   // unsynchronized writes
}

const embHeaderSize = 16 // 8 bytes count + 4 bytes dims + 4 reserved

// OpenEmbeddingStore opens or creates an embedding store file.
func OpenEmbeddingStore(path string, dims int) (*EmbeddingStore, error) {
	if dims <= 0 || dims > 4096 {
		return nil, fmt.Errorf("embedding store: invalid dims %d", dims)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("embedding store: %w", err)
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("embedding store: open: %w", err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("embedding store: stat: %w", err)
	}

	recSize := dims * 4
	var count int
	var fileSize int

	if fi.Size() == 0 {
		// New file: create with header only.
		header := make([]byte, embHeaderSize)
		binary.LittleEndian.PutUint32(header[8:12], uint32(dims))
		if _, err := file.Write(header); err != nil {
			return nil, fmt.Errorf("embedding store: write header: %w", err)
		}
		fileSize = embHeaderSize
	} else if fi.Size() < int64(embHeaderSize) {
		return nil, fmt.Errorf("embedding store: file too small (%d bytes)", fi.Size())
	} else {
		fileSize = int(fi.Size())
		// Read header.
		header := make([]byte, embHeaderSize)
		if _, err := file.ReadAt(header, 0); err != nil {
			return nil, fmt.Errorf("embedding store: read header: %w", err)
		}
		fileDims := int(binary.LittleEndian.Uint32(header[8:12]))
		if fileDims != dims {
			return nil, fmt.Errorf("embedding store: dims mismatch: file=%d, requested=%d", fileDims, dims)
		}
		count = int(binary.LittleEndian.Uint32(header[0:8]))
	}

	// Read full file into memory.
	buf := make([]byte, fileSize)
	if fileSize > 0 {
		if _, err := file.ReadAt(buf, 0); err != nil {
			return nil, fmt.Errorf("embedding store: read: %w", err)
		}
	}

	return &EmbeddingStore{
		path:    path,
		dims:    dims,
		recSize: recSize,
		count:   count,
		data:    buf,
	}, nil
}

// Put appends an embedding vector and returns its reference ID (1-indexed).
func (s *EmbeddingStore) Put(vec []float32) (model.EmbeddingRefID, error) {
	if len(vec) != s.dims {
		return 0, fmt.Errorf("embedding store: expected %d dims, got %d", s.dims, len(vec))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	oldCount := s.count
	newCount := oldCount + 1

	// Extend buffer.
	oldLen := len(s.data)
	newLen := embHeaderSize + newCount*s.recSize
	if newLen > oldLen {
		newBuf := make([]byte, alignPow2(newLen))
		copy(newBuf, s.data)
		s.data = newBuf
	}

	// Write vector at the end.
	offset := embHeaderSize + oldCount*s.recSize
	for i, v := range vec {
		binary.LittleEndian.PutUint32(s.data[offset+i*4:offset+(i+1)*4], float32AsBits(v))
	}

	// Update count in header.
	s.count = newCount
	binary.LittleEndian.PutUint32(s.data[0:8], uint32(newCount))
	s.dirty = true

	return model.EmbeddingRefID(newCount), nil
}

// Get retrieves an embedding vector by reference ID (1-indexed).
func (s *EmbeddingStore) Get(ref model.EmbeddingRefID) ([]float32, error) {
	if ref == 0 {
		return nil, fmt.Errorf("embedding store: ref ID must be > 0")
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
		vec[i] = bitsAsFloat32(bits)
	}
	return vec, nil
}

// Len returns the number of stored embeddings.
func (s *EmbeddingStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// Sync writes the in-memory buffer to disk. Call periodically or on Close.
func (s *EmbeddingStore) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.WriteFile(s.path, s.data, 0644); err != nil {
		return fmt.Errorf("embedding store: sync: %w", err)
	}
	s.dirty = false
	return nil
}

// Close syncs and releases resources.
func (s *EmbeddingStore) Close() error {
	return s.Sync()
}

// alignPow2 rounds up to the nearest power of 2 (minimum 4096).
func alignPow2(n int) int {
	if n < 4096 {
		return 4096
	}
	v := n
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

func float32AsBits(f float32) uint32 {
	return *(*uint32)(unsafe.Pointer(&f))
}

func bitsAsFloat32(bits uint32) float32 {
	return *(*float32)(unsafe.Pointer(&bits))
}
