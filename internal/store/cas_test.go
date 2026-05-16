package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DotBlood/ioc/internal/model"
)

func tempCAS(t *testing.T) *CAS {
	t.Helper()
	root := filepath.Join(os.TempDir(), "ioc-cas-test-"+model.NewID().String())
	return NewCAS(root)
}

func cleanupCAS(t *testing.T, cas *CAS) {
	t.Helper()
	os.RemoveAll(cas.root)
}

func TestCAS_StoreRoundtrip(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	content := "hello cas store"
	hash, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	r, err := c.Open(context.Background(), hash)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Errorf("Open: got %q, want %q", string(got), content)
	}
}

func TestCAS_Dedup(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	content := "deduplicated content"
	h1, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store 1: %v", err)
	}
	h2, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("same content produced different hashes: %x vs %x", h1, h2)
	}

	// Count files in obj — should be exactly 1 (the object).
	var count int
	filepath.Walk(c.root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			count++
		}
		return nil
	})
	if count != 1 {
		t.Errorf("expected 1 stored file (dedup), got %d", count)
	}
}

func TestCAS_Has(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	content := "existence check"
	hash, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	ok, err := c.Has(context.Background(), hash)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !ok {
		t.Error("Has: expected true for stored content")
	}

	var fakeHash model.ContentHash
	fakeHash[0] = 0xff
	ok, err = c.Has(context.Background(), fakeHash)
	if err != nil {
		t.Fatalf("Has fake: %v", err)
	}
	if ok {
		t.Error("Has: expected false for non-existent content")
	}
}

func TestCAS_Delete(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	content := "to be deleted"
	hash, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	if err := c.Delete(context.Background(), hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	ok, _ := c.Has(context.Background(), hash)
	if ok {
		t.Error("Has after Delete: expected false")
	}
}

func TestCAS_StoreAndVerifyCompressed(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	content := strings.Repeat("able was i ere i saw elba", 100)
	hash, err := c.Store(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	r, err := c.Open(context.Background(), hash)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	got, _ := io.ReadAll(r)
	if string(got) != content {
		t.Errorf("content mismatch: len %d vs %d", len(got), len(content))
	}
}

func TestCAS_LargeFileChunking(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	size := chunkThreshold + 100
	content := bytes.Repeat([]byte("A"), size)

	hash, err := c.Store(context.Background(), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Store large: %v", err)
	}

	// Verify manifest exists.
	h := hex.EncodeToString(hash[:])
	manifestPath := c.objPath(h) + ".manifest"
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Fatal("manifest not created for large file")
	}

	r, err := c.Open(context.Background(), hash)
	if err != nil {
		t.Fatalf("Open large: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll large: %v", err)
	}
	if len(got) != size {
		t.Errorf("content size: got %d, want %d", len(got), size)
	}
	for i, b := range got {
		if b != 'A' {
			t.Errorf("byte %d: got %c, want 'A'", i, b)
			break
		}
	}
}

func TestCAS_DifferentContent(t *testing.T) {
	c := tempCAS(t)
	defer cleanupCAS(t, c)

	h1, _ := c.Store(context.Background(), strings.NewReader("content one"))
	h2, _ := c.Store(context.Background(), strings.NewReader("content two"))

	if h1 == h2 {
		t.Error("different content should produce different hashes")
	}
}
