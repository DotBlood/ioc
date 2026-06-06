package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
)

// openMetaT opens a fresh Meta on a temp dir with the given box (nil = plaintext).
func openMetaT(t *testing.T, box *Box) *Meta {
	t.Helper()
	m, err := OpenMeta(filepath.Join(t.TempDir(), "meta.db"), box)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestMetaCRUD exercises every Meta CRUD path under BOTH plaintext and at-rest
// encryption (Box), so the encode/decode round-trip is covered for each value type.
func TestMetaCRUD(t *testing.T) {
	for _, tc := range []struct {
		name string
		box  func(*testing.T) *Box
	}{
		{"plaintext", func(*testing.T) *Box { return nil }},
		{"encrypted", mustBox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := openMetaT(t, tc.box(t))

			// --- scopes ---
			wt := core.Scope{ID: core.NewID(), Role: core.RoleWorktree, Title: "wt", Version: 1}
			ws := core.Scope{ID: core.NewID(), Parent: wt.ID, Role: core.RoleWorkspace, Title: "ws", Version: 1}
			sess := core.Scope{ID: core.NewID(), Parent: ws.ID, Role: core.RoleSession, Title: "s", Version: 1}
			for _, s := range []core.Scope{wt, ws, sess} {
				require.NoError(t, m.PutScope(s))
			}

			gotWS, err := m.GetScope(ws.ID)
			require.NoError(t, err)
			require.Equal(t, "ws", gotWS.Title)
			require.Equal(t, wt.ID, gotWS.Parent)

			_, err = m.GetScope(core.NewID())
			require.ErrorIs(t, err, core.ErrNotFound)

			scopes, err := m.ListScopes()
			require.NoError(t, err)
			require.Len(t, scopes, 3)

			// ChildScopes returns DIRECT children only (ws under wt, not the session).
			kids, err := m.ChildScopes(wt.ID)
			require.NoError(t, err)
			require.Len(t, kids, 1)
			require.Equal(t, ws.ID, kids[0].ID)

			// --- artifacts ---
			a1 := core.Artifact{ID: core.NewID(), Scope: sess.ID, Kind: core.KindInsight, Tier: core.TierWorktree, Summary: "first", CreatedAt: time.Now()}
			a2 := core.Artifact{ID: core.NewID(), Scope: sess.ID, Kind: core.KindInsight, Tier: core.TierWorkspace, Summary: "second", CreatedAt: time.Now()}
			a3 := core.Artifact{ID: core.NewID(), Scope: ws.ID, Kind: core.KindSummary, Tier: core.TierWorktree, Summary: "elsewhere", CreatedAt: time.Now()}
			for _, a := range []core.Artifact{a1, a2, a3} {
				require.NoError(t, m.PutArtifact(a))
			}

			gotA, err := m.GetArtifact(a1.ID)
			require.NoError(t, err)
			require.Equal(t, "first", gotA.Summary)
			require.Equal(t, core.TierWorktree, gotA.Tier)

			_, err = m.GetArtifact(core.NewID())
			require.ErrorIs(t, err, core.ErrNotFound)

			inSess, err := m.ArtifactsInScope(sess.ID)
			require.NoError(t, err)
			require.Len(t, inSess, 2, "ArtifactsInScope filters by scope")

			all, err := m.ListArtifacts()
			require.NoError(t, err)
			require.Len(t, all, 3)

			// Delete one artifact: gone from scope + global listings.
			require.NoError(t, m.DeleteArtifact(a1.ID))
			_, err = m.GetArtifact(a1.ID)
			require.ErrorIs(t, err, core.ErrNotFound)
			inSess, err = m.ArtifactsInScope(sess.ID)
			require.NoError(t, err)
			require.Len(t, inSess, 1)

			// --- scope delete ---
			require.NoError(t, m.DeleteScope(sess.ID))
			_, err = m.GetScope(sess.ID)
			require.ErrorIs(t, err, core.ErrNotFound)

			// --- traces ---
			tr := core.TraceRecord{QueryID: core.NewID(), Scope: ws.ID, Text: "q", EmbModel: "mock-bow", CreatedAt: time.Now()}
			require.NoError(t, m.PutTrace(tr))
			gotTr, err := m.GetTrace(tr.QueryID)
			require.NoError(t, err)
			require.Equal(t, "q", gotTr.Text)
			traces, err := m.ListTraces()
			require.NoError(t, err)
			require.Len(t, traces, 1)
			_, err = m.GetTrace(core.NewID())
			require.ErrorIs(t, err, core.ErrNotFound)

			// --- config ---
			require.NoError(t, m.PutConfig("emb_model", "BAAI/bge-small-en-v1.5"))
			val, ok := m.GetConfig("emb_model")
			require.True(t, ok)
			require.Equal(t, "BAAI/bge-small-en-v1.5", val)
			_, ok = m.GetConfig("absent")
			require.False(t, ok, "missing config key reports ok=false")
		})
	}
}
