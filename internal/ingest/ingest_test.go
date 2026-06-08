package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// openTestEngine opens an engine on a temp data dir with the mock embedder.
func openTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.Open(context.Background(), t.TempDir(), embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// docArtifacts returns all KindDocument artifacts in the store.
func docArtifacts(t *testing.T, e *engine.Engine) []core.Artifact {
	t.Helper()
	all, err := e.ListArtifacts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var docs []core.Artifact
	for _, a := range all {
		if a.Kind == core.KindDocument {
			docs = append(docs, a)
		}
	}
	return docs
}

func countScopes(t *testing.T, e *engine.Engine) int {
	t.Helper()
	scs, err := e.ListScopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(scs)
}

func newRoot(t *testing.T, e *engine.Engine) core.ID {
	t.Helper()
	s, err := e.CreateScope(context.Background(), core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

// ingestRootDir returns a fresh temp dir AND declares it the ingest sandbox root
// (IOC_INGEST_ROOT) — required since V1 confines ingest to that root, and a test
// tree lives under the OS temp dir, outside the process CWD default.
func ingestRootDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(IngestRootEnv, dir)
	return dir
}

func TestIngestIdempotent(t *testing.T) {
	ctx := context.Background()
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	write(t, filepath.Join(dir, "a.txt"), "alpha file\nsecond line\n")
	write(t, filepath.Join(dir, "sub", "b.txt"), "bravo content here\n")
	root := newRoot(t, e)

	st1, err := Ingest(ctx, e, dir, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st1.FilesAdded != 2 {
		t.Fatalf("first run FilesAdded = %d, want 2", st1.FilesAdded)
	}
	docs1 := len(docArtifacts(t, e))
	scopes1 := countScopes(t, e)
	if docs1 == 0 {
		t.Fatal("no document chunks after first ingest")
	}

	// Second run, no changes: must be a pure no-op (no duplicates).
	st2, err := Ingest(ctx, e, dir, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st2.FilesUnchanged != 2 || st2.FilesAdded != 0 || st2.FilesUpdated != 0 {
		t.Fatalf("re-run stats = %+v, want 2 unchanged / 0 added / 0 updated", st2)
	}
	if st2.ChunksAdded != 0 || st2.ScopesCreated != 0 {
		t.Fatalf("re-run created chunks/scopes: %+v", st2)
	}
	if got := len(docArtifacts(t, e)); got != docs1 {
		t.Fatalf("doc count changed on no-op re-run: %d -> %d", docs1, got)
	}
	if got := countScopes(t, e); got != scopes1 {
		t.Fatalf("scope count changed on no-op re-run: %d -> %d", scopes1, got)
	}
}

func TestIngestUpdateAddRemove(t *testing.T) {
	ctx := context.Background()
	e := openTestEngine(t)
	dir := ingestRootDir(t)
	write(t, filepath.Join(dir, "a.txt"), "alpha\n")
	write(t, filepath.Join(dir, "keep", "k.txt"), "keep me\n")
	write(t, filepath.Join(dir, "gone", "g.txt"), "delete me\n")
	root := newRoot(t, e)

	if _, err := Ingest(ctx, e, dir, root, Options{}); err != nil {
		t.Fatal(err)
	}
	scopesBefore := countScopes(t, e)

	// Change a.txt, add c.txt, remove the whole gone/ directory.
	write(t, filepath.Join(dir, "a.txt"), "alpha CHANGED with more text\nplus a line\n")
	write(t, filepath.Join(dir, "c.txt"), "charlie new file\n")
	if err := os.RemoveAll(filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}

	st, err := Ingest(ctx, e, dir, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.FilesUpdated != 1 {
		t.Fatalf("FilesUpdated = %d, want 1", st.FilesUpdated)
	}
	if st.FilesAdded != 1 {
		t.Fatalf("FilesAdded = %d, want 1", st.FilesAdded)
	}
	if st.FilesUnchanged != 1 {
		t.Fatalf("FilesUnchanged = %d, want 1 (keep/k.txt)", st.FilesUnchanged)
	}
	if st.FilesRemoved != 1 {
		t.Fatalf("FilesRemoved = %d, want 1 (gone/g.txt)", st.FilesRemoved)
	}
	if st.ScopesRemoved != 1 {
		t.Fatalf("ScopesRemoved = %d, want 1 (empty gone/ scope pruned)", st.ScopesRemoved)
	}

	// No chunk should still reference the deleted file path.
	for _, a := range docArtifacts(t, e) {
		if a.Meta["path"] == "gone/g.txt" {
			t.Fatalf("chunk for deleted file still present: %s", a.ID)
		}
	}
	// The pruned scope reduced the scope count by exactly one.
	if got := countScopes(t, e); got != scopesBefore-1 {
		t.Fatalf("scope count = %d, want %d after pruning gone/", got, scopesBefore-1)
	}
}
