package runtime

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// startDaemon opens an engine on dir and serves it; returns the running server.
func startDaemon(t *testing.T, dir string) *Server {
	srv, _ := startDaemonDone(t, dir)
	return srv
}

// startDaemonDone also returns a channel closed when Serve has fully returned.
// Serve returns only after Stop closes its `stopped` channel — which Stop does
// after the cleanup (Sync/Close/removeRuntimeInfo) — so receiving on done is a
// deterministic "the daemon has fully shut down" signal (no polling).
func startDaemonDone(t *testing.T, dir string) (*Server, <-chan struct{}) {
	t.Helper()
	e, err := engine.Open(context.Background(), dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open engine: %v", err)
	}
	srv := NewServer(e, dir)
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve() }()
	<-srv.Ready()
	return srv, done
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

// Supersede must round-trip over the RPC and take effect: a superseded artifact
// leaves the default current view served by the daemon.
func TestRuntimeSupersede(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	cli, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	root, err := cli.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	a, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use sqlite for storage"})
	if err != nil {
		t.Fatalf("push a: %v", err)
	}
	b, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use postgres, sqlite was rejected", Supersedes: []core.ID{a.ID}})
	if err != nil {
		t.Fatalf("push b: %v", err)
	}

	_, hits, err := cli.Query(ctx, core.Query{Scope: root.ID, Text: "storage engine", TopK: 5})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for _, h := range hits {
		if h.Artifact == a.ID {
			t.Fatalf("superseded artifact %s should not be in the default view", a.ID)
		}
	}

	// Post-hoc Supersede RPC: mark b superseded by a fresh artifact, then b drops out.
	c, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "use a managed cloud database"})
	if err != nil {
		t.Fatalf("push c: %v", err)
	}
	if err := cli.Supersede(ctx, b.ID, c.ID); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	_, hits2, err := cli.Query(ctx, core.Query{Scope: root.ID, Text: "storage engine", TopK: 5})
	if err != nil {
		t.Fatalf("query2: %v", err)
	}
	for _, h := range hits2 {
		if h.Artifact == b.ID {
			t.Fatalf("artifact %s superseded via RPC should be excluded", b.ID)
		}
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

func TestRuntimeShutdownRPC(t *testing.T) {
	dir := t.TempDir()
	_, done := startDaemonDone(t, dir)

	cli, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := cli.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	_ = cli.Close()

	// The daemon stops asynchronously after replying. Wait for Serve to return
	// (deterministic; closed only after Stop's cleanup incl. removeRuntimeInfo) —
	// no flaky polling.
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("daemon did not fully shut down after shutdown RPC")
	}
	if _, err := Info(dir); !os.IsNotExist(err) {
		t.Fatalf("runtime.json was not removed after shutdown (err=%v)", err)
	}
	// Lock released: the store reopens.
	e, err := engine.Open(context.Background(), dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("reopen after shutdown: %v", err)
	}
	_ = e.Close()
}

func TestOpenFallbackAndClient(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// No daemon → embedded engine.
	svc, err := Open(dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open embedded: %v", err)
	}
	if _, isClient := svc.(*Client); isClient {
		t.Fatal("expected embedded engine, got a client")
	}
	if _, err := svc.CreateScope(ctx, core.NilID, core.RoleWorktree, "root"); err != nil {
		t.Fatalf("embedded create scope: %v", err)
	}
	_ = svc.Close()

	// Daemon running → client.
	srv := startDaemon(t, dir)
	defer srv.Stop()
	svc2, err := Open(dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open with daemon: %v", err)
	}
	if _, isClient := svc2.(*Client); !isClient {
		t.Fatal("expected a client when a daemon is running")
	}
	_ = svc2.Close()
}
