package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
)

func mkVec(dims, seed int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(seed*100 + i)
	}
	return v
}

func TestEmbStorePutGetReopen(t *testing.T) {
	const dims, n = 8, 1000
	p := filepath.Join(t.TempDir(), "emb.dat")

	s, err := OpenEmbeddingStore(p, dims)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < n; i++ {
		ref, err := s.Put(mkVec(dims, i))
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		if int(ref) != i+1 {
			t.Fatalf("ref = %d, want %d", ref, i+1)
		}
	}
	if s.Len() != n {
		t.Fatalf("len = %d, want %d", s.Len(), n)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: count and vectors persist (append + fsync).
	s2, err := OpenEmbeddingStore(p, dims)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != n {
		t.Fatalf("reopen len = %d, want %d", s2.Len(), n)
	}
	for _, idx := range []int{0, 1, n / 2, n - 1} {
		got, err := s2.Get(core.EmbeddingRef(idx + 1))
		if err != nil {
			t.Fatalf("get %d: %v", idx+1, err)
		}
		want := mkVec(dims, idx)
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("ref %d elem %d = %v, want %v", idx+1, j, got[j], want[j])
			}
		}
	}

	// File is exactly header + count*recSize (no whole-buffer padding written).
	fi, _ := os.Stat(p)
	if want := int64(embHeaderSize + n*dims*4); fi.Size() != want {
		t.Fatalf("file size = %d, want %d (header + n*recSize)", fi.Size(), want)
	}
}

func TestEmbStoreTornTailIgnored(t *testing.T) {
	const dims, n = 4, 10
	p := filepath.Join(t.TempDir(), "emb.dat")

	s, err := OpenEmbeddingStore(p, dims)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := s.Put(mkVec(dims, i)); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	s.Close()

	// Simulate a crash that left a torn/partial tail past the committed count
	// (also models the old pow2-padded format: trailing bytes beyond count).
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xDE, 0xAD, 0xBE}) // not a whole record
	f.Close()

	s2, err := OpenEmbeddingStore(p, dims)
	if err != nil {
		t.Fatalf("reopen after torn tail: %v", err)
	}
	defer s2.Close()
	if s2.Len() != n {
		t.Fatalf("len = %d, want %d (torn tail must be ignored)", s2.Len(), n)
	}
	got, err := s2.Get(core.EmbeddingRef(n))
	if err != nil {
		t.Fatalf("get last: %v", err)
	}
	want := mkVec(dims, n-1)
	for j := range want {
		if got[j] != want[j] {
			t.Fatalf("last vec elem %d = %v, want %v", j, got[j], want[j])
		}
	}
	// A new Put after recovery overwrites the torn tail and commits cleanly.
	if _, err := s2.Put(mkVec(dims, 99)); err != nil {
		t.Fatalf("put after recovery: %v", err)
	}
	if s2.Len() != n+1 {
		t.Fatalf("len after recovery put = %d, want %d", s2.Len(), n+1)
	}
}

func TestEmbStoreDimsMismatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "emb.dat")
	s, err := OpenEmbeddingStore(p, 8)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Put(mkVec(8, 0))
	s.Close()
	if _, err := OpenEmbeddingStore(p, 16); err == nil {
		t.Fatal("expected dims-mismatch error on reopen with different dims")
	}
}
