package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
)

// failingStore wraps a Store and fails Push after N successful pushes, to
// simulate a mid-file embedder/daemon failure during ingest.
type failingStore struct {
	Store
	failAfter int
	pushes    int
}

func (f *failingStore) Push(ctx context.Context, r core.PushRequest) (core.Artifact, error) {
	f.pushes++
	if f.failAfter > 0 && f.pushes > f.failAfter {
		return core.Artifact{}, fmt.Errorf("injected push failure")
	}
	return f.Store.Push(ctx, r)
}

// H1: a mid-file push failure must NOT leave the file permanently under-indexed.
// A later healthy ingest must detect the incomplete chunk set and re-chunk fully.
func TestIngestPartialWriteHeals(t *testing.T) {
	ctx := context.Background()
	e := openTestEngine(t)
	dir := t.TempDir()
	// 3 short paragraphs → 3 chunks at maxChars=20 (Generic, .txt).
	write(t, filepath.Join(dir, "f.txt"), "alpha one line\n\nbravo two line\n\ncharlie three x\n")
	root := newRoot(t, e)
	opt := Options{MaxChars: 20, Overlap: 5}

	// Confirm the file yields >1 chunk so the test is meaningful.
	if n := len(SplitLang("alpha one line\n\nbravo two line\n\ncharlie three x\n", Generic, 20, 5)); n < 2 {
		t.Fatalf("test setup: expected >1 chunk, got %d", n)
	}

	// Run 1: fail after the first chunk → leaves a partial (1 of N) chunk set.
	fs := &failingStore{Store: e, failAfter: 1}
	if _, err := Ingest(ctx, fs, dir, root, opt); err == nil {
		t.Fatal("expected injected push failure")
	}
	partial := docArtifacts(t, e)
	if len(partial) == 0 {
		t.Fatal("setup: expected a partial chunk to be written")
	}
	full := len(SplitLang("alpha one line\n\nbravo two line\n\ncharlie three x\n", Generic, 20, 5))
	if len(partial) >= full {
		t.Fatalf("setup: expected a PARTIAL write (<%d), got %d", full, len(partial))
	}

	// Run 2: healthy ingest must NOT report the file as unchanged — it must heal.
	st, err := Ingest(ctx, e, dir, root, opt)
	if err != nil {
		t.Fatalf("heal ingest: %v", err)
	}
	if st.FilesUnchanged != 0 {
		t.Fatalf("partial file was wrongly skipped as unchanged: %+v", st)
	}
	if got := len(docArtifacts(t, e)); got != full {
		t.Fatalf("after heal: %d chunks, want full %d", got, full)
	}

	// Run 3: now complete → genuinely unchanged.
	st3, err := Ingest(ctx, e, dir, root, opt)
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if st3.FilesUnchanged != 1 || st3.FilesAdded != 0 || st3.FilesUpdated != 0 {
		t.Fatalf("expected genuinely unchanged on run 3: %+v", st3)
	}
}
