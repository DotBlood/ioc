package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// startDaemon opens an engine on dir and serves it; returns the running server.
func startDaemon(t *testing.T, dir string) *Server {
	t.Helper()
	e, err := engine.Open(context.Background(), dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	srv := NewServer(e, dir)
	go func() { _ = srv.Serve() }()
	<-srv.Ready()
	return srv
}

func TestRuntimeRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := startDaemon(t, dir)

	cli, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	root, err := cli.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	art, err := cli.Push(ctx, core.PushRequest{
		Scope:   root.ID,
		Summary: "alpha beta gamma reranker insight",
		Content: []byte("the full raw content of the artifact"),
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	_, hits, err := cli.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(hits) == 0 || hits[0].Artifact != art.ID {
		t.Fatalf("query did not return the pushed artifact: %+v", hits)
	}

	h, err := cli.Drill(ctx, art.ID, core.DetailRaw)
	if err != nil {
		t.Fatalf("drill: %v", err)
	}
	if string(h.Content) != "the full raw content of the artifact" {
		t.Fatalf("drill content = %q", h.Content)
	}

	// Unknown artifact → error class preserved across the wire.
	if _, err := cli.Drill(ctx, core.NewID(), core.DetailRaw); err == nil {
		t.Fatal("expected error drilling a missing artifact")
	}

	_ = cli.Close()
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Durability: reopen the store after shutdown — the lock is released and the
	// embedding written through the daemon survived (Sync on write + on Stop).
	e2, err := engine.Open(ctx, dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("reopen after stop: %v", err)
	}
	defer e2.Close()
	_, hits2, err := e2.Query(ctx, core.Query{Scope: root.ID, Text: "alpha beta gamma", TopK: 5})
	if err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if len(hits2) == 0 || hits2[0].Artifact != art.ID {
		t.Fatalf("artifact/embedding did not persist across restart: %+v", hits2)
	}
}

func TestRuntimeConcurrentClients(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	seed, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	root, err := seed.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	_ = seed.Close()

	// Many connections hitting one owner concurrently: mixed reads + writes.
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := Dial(dir)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			if _, err := c.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "concurrent write probe"}); err != nil {
				errs <- err
				return
			}
			if _, _, err := c.Query(ctx, core.Query{Scope: root.ID, Text: "concurrent", TopK: 3}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent client error: %v", err)
	}
}
