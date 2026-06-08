package runtime

import (
	"context"
	"net"
	"os"
	"strings"
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

// Relate + Related round-trip over the RPC: an edge declared at push and a post-hoc
// edge are both walkable through the daemon.
func TestRuntimeRelate(t *testing.T) {
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
	a, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "bbolt storage decision"})
	if err != nil {
		t.Fatalf("push a: %v", err)
	}
	// b declares depends_on a at push time, over the wire.
	b, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "runtime owns the store",
		Relations: []core.EdgeSpec{{Kind: core.RelDependsOn, Target: a.ID}}})
	if err != nil {
		t.Fatalf("push b: %v", err)
	}

	// "what depends on a?" over the wire → b.
	deps, err := cli.Related(ctx, a.ID, []core.RelationKind{core.RelDependsOn}, core.DirIn, 1)
	if err != nil {
		t.Fatalf("related: %v", err)
	}
	found := false
	for _, h := range deps {
		if h.Artifact == b.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected b among a's dependents, got %+v", deps)
	}

	// Post-hoc Relate over the wire, then walk OUT from b.
	c, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "an unrelated note"})
	if err != nil {
		t.Fatalf("push c: %v", err)
	}
	if err := cli.Relate(ctx, b.ID, c.ID, core.RelRelatesTo); err != nil {
		t.Fatalf("relate: %v", err)
	}
	out, err := cli.Related(ctx, b.ID, nil, core.DirOut, 1)
	if err != nil {
		t.Fatalf("related out: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("b should point at a (depends_on) and c (relates_to), got %d", len(out))
	}
}

// Neighbors round-trips over the RPC and returns current artifacts.
func TestRuntimeNeighbors(t *testing.T) {
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
	a, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "alpha beta neighbor probe"})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	hits, err := cli.Neighbors(ctx, root.ID, "alpha beta", 5)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.Artifact == a.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("neighbors did not return the pushed artifact: %+v", hits)
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

// --- bug #3: Open classifies Dial errors instead of treating all as "no daemon" ---

// TestOpenStaleRuntimeJSONFallsBack: runtime.json present but the socket is dead
// (daemon gone, stale descriptor) → connection refused → embedded fallback.
func TestOpenStaleRuntimeJSONFallsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Grab a real free port then close it → connects to it are refused deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if err := writeRuntimeInfo(dir, RuntimeInfo{Net: "tcp", Addr: addr, Token: "stale", DataDir: dir}); err != nil {
		t.Fatalf("write stale info: %v", err)
	}

	svc, err := Open(dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open over stale runtime.json: %v", err)
	}
	defer svc.Close()
	if _, isClient := svc.(*Client); isClient {
		t.Fatal("stale runtime.json should fall back to an embedded engine, got a client")
	}
	if _, err := svc.CreateScope(ctx, core.NilID, core.RoleWorktree, "root"); err != nil {
		t.Fatalf("embedded engine should work (lock free): %v", err)
	}
}

// TestOpenLiveDaemonHandshakeFailureSurfaces: a live mTLS daemon owns the store; a
// client that cannot complete the dial (no TLS material) must get a SURFACED error,
// not a silent embedded fallback that would then fail on the daemon's bbolt lock.
func TestOpenLiveDaemonHandshakeFailureSurfaces(t *testing.T) {
	genTLSEnv(t)
	dir := t.TempDir()
	srv := startTLSDaemon(t, dir)
	defer srv.Stop()

	// Strip the client's TLS env so Dial cannot build a client config (the daemon is
	// alive and holds the store lock).
	os.Unsetenv(tlsCertEnv)
	os.Unsetenv(tlsKeyEnv)
	os.Unsetenv(tlsCAEnv)

	svc, err := Open(dir, embed.NewMockEmbedder(16))
	if err == nil {
		svc.Close()
		t.Fatal("expected Open to surface the dial failure against a live TLS daemon")
	}
	if !strings.Contains(err.Error(), "cannot reach daemon") {
		t.Fatalf("error should explain the daemon was unreachable, got: %v", err)
	}
}

// TestRuntimeCompact verifies the compact round-trip through the client: push an
// artifact (which creates an embedding record), delete it, then call Compact via
// the client — CompactStats.Reclaimed must be >= 1 (the orphaned embedding is
// gone). This exercises the full write-lock path in invoke (mCompact ∈ writeMethods).
func TestRuntimeCompact(t *testing.T) {
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
	art, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "compact probe artifact"})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := cli.DeleteArtifact(ctx, art.ID); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	st, err := cli.Compact(ctx)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if st.Reclaimed < 1 {
		t.Fatalf("expected Reclaimed >= 1 after deleting embedded artifact, got %+v", st)
	}
}

// TestRuntimeStatsSchemaVersion checks that Stats() carries SchemaVersion ==
// engine.CurrentSchemaVersion, so health consumers can assert the store is at
// the expected schema without a separate RPC.
func TestRuntimeStatsSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	cli, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	st, err := cli.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.SchemaVersion != engine.CurrentSchemaVersion {
		t.Fatalf("stats.SchemaVersion = %d, want %d", st.SchemaVersion, engine.CurrentSchemaVersion)
	}
}

// TestOpenWrongTokenStillReturnsClient: a reachable daemon with a stale/wrong token
// in runtime.json must yield a CLIENT (connect succeeded); the bad token surfaces as
// an auth error on the first call — NOT a silent embedded fallback (store-lock error).
func TestOpenWrongTokenStillReturnsClient(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	info, err := readRuntimeInfo(dir)
	if err != nil {
		t.Fatalf("read info: %v", err)
	}
	info.Token = "definitely-not-the-real-token"
	if err := writeRuntimeInfo(dir, info); err != nil {
		t.Fatalf("rewrite info: %v", err)
	}

	svc, err := Open(dir, embed.NewMockEmbedder(32))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer svc.Close()
	if _, isClient := svc.(*Client); !isClient {
		t.Fatal("a reachable daemon must yield a client even with a bad published token")
	}
	if _, err := svc.CreateScope(ctx, core.NilID, core.RoleWorktree, "root"); err == nil {
		t.Fatal("expected an auth error from the wrong token, got nil")
	}
}

// TestRuntimeScopeStats verifies the ScopeStats read-tier op round-trips through
// the daemon: push two artifacts then call ScopeStats; ArtifactCount must equal 2
// and ClusterCount must be >= 1 (at least one component for the embedded vectors).
func TestRuntimeScopeStats(t *testing.T) {
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
	if _, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "alpha topic one"}); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	if _, err := cli.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "beta topic two"}); err != nil {
		t.Fatalf("push 2: %v", err)
	}

	st, err := cli.ScopeStats(ctx, root.ID, 0.5, 1)
	if err != nil {
		t.Fatalf("ScopeStats: %v", err)
	}
	if st.ArtifactCount != 2 {
		t.Fatalf("ArtifactCount = %d, want 2", st.ArtifactCount)
	}
	if st.ClusterCount < 1 {
		t.Fatalf("ClusterCount = %d, want >= 1", st.ClusterCount)
	}
}
