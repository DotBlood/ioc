package engine

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
)

// withinDeadline runs fn in a goroutine and fails if it does not return quickly —
// a missing cycle guard would loop forever, so this turns a hang into a clear fail.
func withinDeadline(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not terminate — cycle guard missing", what)
	}
}

// A corrupt scope graph (self-parent or an N-cycle) must not hang the scope walks.
// We write the cycles directly via meta (CreateScope would reject them).
func TestScopeWalks_CycleSafe(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	// Self-parent: S points at itself.
	self := core.NewID()
	require.NoError(t, e.meta.PutScope(core.Scope{ID: self, Parent: self, Role: core.RoleWorkspace, Title: "self"}))

	// 2-cycle: A.Parent=B, B.Parent=A.
	a, b := core.NewID(), core.NewID()
	require.NoError(t, e.meta.PutScope(core.Scope{ID: a, Parent: b, Role: core.RoleWorkspace, Title: "a"}))
	require.NoError(t, e.meta.PutScope(core.Scope{ID: b, Parent: a, Role: core.RoleWorkspace, Title: "b"}))

	for _, id := range []core.ID{self, a, b} {
		id := id
		withinDeadline(t, "ancestorsOf", func() { _, _ = e.ancestorsOf(id) })
		withinDeadline(t, "descendantScopes", func() { _, _ = e.descendantScopes(id) })
		withinDeadline(t, "scopePath", func() { _ = e.scopePath(id) })
		withinDeadline(t, "Query", func() {
			_, _, _ = e.Query(ctx, core.Query{Scope: id, Text: "x", TopK: 5})
		})
	}

	// descendantScopes must not emit a scope twice even with the self-loop.
	got, err := e.descendantScopes(self)
	require.NoError(t, err)
	seen := map[core.ID]bool{}
	for _, s := range got {
		require.False(t, seen[s.ID], "descendantScopes returned a duplicate scope")
		seen[s.ID] = true
	}
}

// A normal tree still returns the exact expected ancestor/descendant sets (the
// guard must not over-prune a legitimate hierarchy).
func TestScopeWalks_NormalTreeUnaffected(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, t.TempDir(), embed.NewMockEmbedder(16))
	require.NoError(t, err)
	defer e.Close()

	wt, err := e.CreateScope(ctx, core.NilID, core.RoleWorktree, "wt")
	require.NoError(t, err)
	ws, err := e.CreateScope(ctx, wt.ID, core.RoleWorkspace, "ws")
	require.NoError(t, err)
	s1, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "s1")
	require.NoError(t, err)
	s2, err := e.CreateScope(ctx, ws.ID, core.RoleSession, "s2")
	require.NoError(t, err)

	ancs, err := e.ancestorsOf(s1.ID)
	require.NoError(t, err)
	require.Len(t, ancs, 2) // ws, wt

	descs, err := e.descendantScopes(wt.ID)
	require.NoError(t, err)
	require.Len(t, descs, 3) // ws, s1, s2
	_ = s2
}
