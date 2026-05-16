package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func tempEmbStore(t *testing.T, dims int) *EmbeddingStore {
	t.Helper()
	path := filepath.Join(os.TempDir(), "ioc-emb-"+model.NewID().String()+".emb")
	s, err := OpenEmbeddingStore(path, dims)
	if err != nil {
		t.Fatalf("OpenEmbeddingStore: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		os.Remove(path)
	})
	return s
}

func TestEmbeddingStore_PutGet(t *testing.T) {
	s := tempEmbStore(t, 4)

	// Initial state.
	if s.Len() != 0 {
		t.Errorf("initial Len = %d, want 0", s.Len())
	}

	vec := []float32{0.1, 0.2, 0.3, 0.4}
	ref, err := s.Put(vec)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref != 1 {
		t.Errorf("first Put: ref = %d, want 1", ref)
	}

	got, err := s.Get(ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Get len = %d, want 4", len(got))
	}
	for i, v := range vec {
		if got[i] != v {
			t.Errorf("Get[%d] = %f, want %f", i, got[i], v)
		}
	}
}

func TestEmbeddingStore_MultiplePuts(t *testing.T) {
	s := tempEmbStore(t, 3)

	for i := model.EmbeddingRefID(1); i <= 10; i++ {
		vec := []float32{float32(i), float32(i) * 2, float32(i) * 3}
		ref, err := s.Put(vec)
		if err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		if ref != i {
			t.Errorf("Put %d: ref = %d, want %d", i, ref, i)
		}
	}

	if s.Len() != 10 {
		t.Errorf("Len = %d, want 10", s.Len())
	}

	// Verify each.
	for i := model.EmbeddingRefID(1); i <= 10; i++ {
		got, err := s.Get(i)
		if err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
		if got[0] != float32(i) {
			t.Errorf("Get[%d][0] = %f, want %f", i, got[0], float32(i))
		}
	}
}

func TestEmbeddingStore_GetOutOfRange(t *testing.T) {
	s := tempEmbStore(t, 2)

	_, err := s.Get(1)
	if err == nil {
		t.Error("Get nonexistent: expected error")
	}

	_, err = s.Get(0)
	if err == nil {
		t.Error("Get ref 0: expected error")
	}
}

func TestEmbeddingStore_WrongDims(t *testing.T) {
	s := tempEmbStore(t, 4)

	_, err := s.Put([]float32{0.1, 0.2}) // only 2 dims
	if err == nil {
		t.Error("Put with wrong dims: expected error")
	}
}

func TestEmbeddingStore_DimsMismatch(t *testing.T) {
	path := filepath.Join(os.TempDir(), "ioc-emb-mismatch-"+model.NewID().String()+".emb")
	s, err := OpenEmbeddingStore(path, 128)
	if err != nil {
		t.Fatalf("OpenEmbeddingStore 128: %v", err)
	}
	s.Close()

	// Reopen with different dims — should fail.
	_, err = OpenEmbeddingStore(path, 256)
	if err == nil {
		t.Error("reopen with different dims: expected error")
	}
	os.Remove(path)
}

func TestEmbeddingStore_SyncRoundtrip(t *testing.T) {
	path := filepath.Join(os.TempDir(), "ioc-emb-sync-"+model.NewID().String()+".emb")

	s1, err := OpenEmbeddingStore(path, 3)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	s1.Put([]float32{1, 2, 3})
	s1.Put([]float32{4, 5, 6})
	s1.Close()

	s2, err := OpenEmbeddingStore(path, 3)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	defer os.Remove(path)

	if s2.Len() != 2 {
		t.Errorf("reopened Len = %d, want 2", s2.Len())
	}

	got, err := s2.Get(2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got[0] != 4 || got[1] != 5 || got[2] != 6 {
		t.Errorf("Get(2) = %v, want [4 5 6]", got)
	}
}

func TestEmbeddingStore_DifferentDims(t *testing.T) {
	// Two stores with different dims.
	s384 := tempEmbStore(t, 384)
	s768 := tempEmbStore(t, 768)

	ref1, _ := s384.Put(make([]float32, 384))
	ref2, _ := s768.Put(make([]float32, 768))

	if ref1 != 1 || ref2 != 1 {
		t.Errorf("refs: want 1 and 1, got %d and %d", ref1, ref2)
	}
}
