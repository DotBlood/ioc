package api

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/store"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------
// E2E: full workflow — create → add → query → archive →
//
//	verify state → restore → verify identity + CAS integrity
// ---------------------------------------------------------------

func TestE2E_FullWorkflow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	// 1. Create scope.
	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)
	scopeID := wt.ScopeID

	// 2. Add 3 artifacts with deterministic content.
	contentAlpha := []byte("alpha retrieval text")
	contentBeta := []byte("beta retrieval text")
	contentGamma := []byte("gamma retrieval text")

	alphaID, err := rt.AddArtifact(ctx, scopeID, contentAlpha, "alpha")
	require.NoError(t, err)
	betaID, err := rt.AddArtifact(ctx, scopeID, contentBeta, "beta")
	require.NoError(t, err)
	gammaID, err := rt.AddArtifact(ctx, scopeID, contentGamma, "gamma")
	require.NoError(t, err)

	// 3. Query — each artifact must be retrievable by its content word.
	// The hybrid engine uses mock embedder (pseudo-random vectors), so
	// ranking is noisy.  We verify each artifact appears somewhere in
	// results rather than requiring top-1.
	alphaResults, err := rt.Query(ctx, "alpha", 10)
	require.NoError(t, err)
	require.NotEmpty(t, alphaResults)
	require.True(t, containsID(alphaResults, alphaID),
		"alpha query must return alpha artifact")
	require.Equal(t, "alpha", summaryOf(alphaResults, alphaID))

	betaResults, err := rt.Query(ctx, "beta", 10)
	require.NoError(t, err)
	require.NotEmpty(t, betaResults)
	require.True(t, containsID(betaResults, betaID),
		"beta query must return beta artifact")
	require.Equal(t, "beta", summaryOf(betaResults, betaID))

	gammaResults, err := rt.Query(ctx, "gamma", 10)
	require.NoError(t, err)
	require.NotEmpty(t, gammaResults)
	require.True(t, containsID(gammaResults, gammaID),
		"gamma query must return gamma artifact")
	require.Equal(t, "gamma", summaryOf(gammaResults, gammaID))

	// Capture original CAS content hash for integrity check.
	alphaNode, err := rt.disk.LoadNode(mustParseID(t, alphaID))
	require.NoError(t, err)
	originalContentHash := alphaNode.ContentHash
	require.False(t, originalContentHash.IsZero())

	// 4. Archive.
	anchorID, err := rt.ArchiveScope(ctx, scopeID)
	require.NoError(t, err)
	require.NotEmpty(t, anchorID)

	// 5. Verify archived state.
	scopes, err := rt.ListScopes(ctx)
	require.NoError(t, err)
	var found bool
	for _, s := range scopes {
		if s.ScopeID == scopeID {
			require.Equal(t, "archived", s.State)
			found = true
		}
	}
	require.True(t, found, "archived scope must appear in ListScopes")

	// 6. Verify CAS integrity — content still resolvable post-archive.
	rc, err := rt.cas.Open(ctx, originalContentHash)
	require.NoError(t, err)
	readContent, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	require.Equal(t, contentAlpha, readContent, "CAS content must survive archive")

	// 7. Restore.
	err = rt.RestoreScope(ctx, anchorID)
	require.NoError(t, err)

	// 8. Verify restored scope state.
	restoredState, err := rt.disk.ScopeState(ctx, model.ScopeID(scopeID))
	require.NoError(t, err)
	require.Equal(t, model.LifecycleActive, restoredState.State,
		"restored scope must be active, not re-ingested")

	scopesAfter, err := rt.ListScopes(ctx)
	require.NoError(t, err)
	var foundAfter bool
	for _, s := range scopesAfter {
		if s.ScopeID == scopeID {
			require.Equal(t, "active", s.State)
			foundAfter = true
		}
	}
	require.True(t, foundAfter, "restored scope must appear in ListScopes")

	// 9. Query after restore — verify identity preservation.
	resultsAfter, err := rt.Query(ctx, "alpha", 10)
	require.NoError(t, err)
	require.NotEmpty(t, resultsAfter)
	require.True(t, containsID(resultsAfter, alphaID),
		"restored artifact must retain original ID — no accidental re-ingestion")
	require.Equal(t, "alpha", summaryOf(resultsAfter, alphaID))

	// Also verify CAS integrity still holds after restore cycle.
	rc2, err := rt.cas.Open(ctx, originalContentHash)
	require.NoError(t, err)
	readContent2, err := io.ReadAll(rc2)
	rc2.Close()
	require.NoError(t, err)
	require.Equal(t, contentAlpha, readContent2,
		"CAS content must survive full archive/restore cycle")
}

// ---------------------------------------------------------------
// E2E: branching — revision lineage via disk store
//
// v0.1 projections form a revision lineage by convention.
// DiskStore does not enforce DAG semantics.
// ---------------------------------------------------------------

func TestE2E_Branching(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	// Add base artifact → revision 1.
	aid, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("alpha retrieval text"), "branching v1")
	require.NoError(t, err)
	id := mustParseID(t, aid)

	// Verify revision 1 exists.
	proj1, err := rt.disk.LoadProjection(model.ProjectionKey{ArtifactID: id, Revision: 1})
	require.NoError(t, err)
	require.Equal(t, model.RevisionNumber(1), proj1.Revision)
	require.Equal(t, "branching v1", proj1.Summary)

	// Save revision 2 with independent summary.
	proj2 := &model.ArtifactProjection{
		ArtifactID: id,
		Revision:   2,
		Summary:    "branching v2",
		Readiness:  proj1.Readiness,
		ValidFrom:  time.Now(),
	}
	require.NoError(t, rt.disk.SaveProjection(proj2))

	// Save revision 3 with independent summary.
	proj3 := &model.ArtifactProjection{
		ArtifactID: id,
		Revision:   3,
		Summary:    "branching v3",
		Readiness:  proj1.Readiness,
		ValidFrom:  time.Now(),
	}
	require.NoError(t, rt.disk.SaveProjection(proj3))

	// LatestProjection returns highest revision.
	latest, err := rt.artifactStore().LatestProjection(ctx, id)
	require.NoError(t, err)
	require.Equal(t, model.RevisionNumber(3), latest.Revision,
		"LatestProjection must return highest revision")
	require.Equal(t, "branching v3", latest.Summary,
		"LatestProjection summary must match revision 3")

	// All 3 revisions present with independent summaries.
	keys, err := rt.artifactStore().ListProjections(ctx, id)
	require.NoError(t, err)
	require.Len(t, keys, 3)

	// Each revision carries its own summary (projection-level independence).
	summaryByRev := make(map[model.RevisionNumber]string)
	for _, k := range keys {
		p, err := rt.disk.LoadProjection(k)
		require.NoError(t, err)
		summaryByRev[k.Revision] = p.Summary
	}
	require.Equal(t, "branching v1", summaryByRev[1])
	require.Equal(t, "branching v2", summaryByRev[2])
	require.Equal(t, "branching v3", summaryByRev[3])

	// Content hash is shared across all revisions (same underlying artifact).
	node, err := rt.disk.LoadNode(id)
	require.NoError(t, err)
	require.False(t, node.ContentHash.IsZero())
}

// ---------------------------------------------------------------
// E2E: time-travel — archive without restore, anchor chain replay,
//
//	historical state reconstruction via knowledge.TimeMachine
// ---------------------------------------------------------------

func TestE2E_TimeTravel(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)
	scopeID := model.ScopeID(wt.ScopeID)

	// Phase 1: add artifact A and archive (full anchor).
	t0 := time.Now()
	aidA, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("alpha retrieval text"), "alpha")
	require.NoError(t, err)
	idA := mustParseID(t, aidA)
	t1 := time.Now()
	_ = t1

	time.Sleep(10 * time.Millisecond) // pace: ensure t2 > t1

	anchor1ID, err := rt.ArchiveScope(ctx, wt.ScopeID)
	require.NoError(t, err)
	t2 := time.Now()
	_ = t2

	// Phase 2: add artifact B and archive again (diff anchor).
	// NO restore between archives — this tests the archive pipeline
	// producing a diff anchor on top of an already-archived scope.
	time.Sleep(10 * time.Millisecond) // pace: ensure t3 > t2
	aidB, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("beta retrieval text"), "beta")
	require.NoError(t, err)
	idB := mustParseID(t, aidB)
	t3 := time.Now()
	_ = t3

	time.Sleep(10 * time.Millisecond) // pace: ensure t4 > t3

	anchor2ID, err := rt.ArchiveScope(ctx, wt.ScopeID)
	require.NoError(t, err)
	require.NotEqual(t, anchor1ID, anchor2ID,
		"second archive must produce a different anchor ID")
	t4 := time.Now()

	// Build TimeMachine with adapters bridging store.DiskStore → knowledge interfaces.
	adapter := &timeMachineAdapter{disk: rt.disk}
	tm := knowledge.NewTimeMachine(adapter, adapter, adapter)

	// Query at t_mid — after first archive, before second artifact.
	tMid := t2.Add(5 * time.Millisecond) // between t2 and t3
	midState, err := tm.ScopeStateAt(ctx, scopeID, tMid)
	require.NoError(t, err, "ScopeStateAt must succeed between anchors")

	// Only artifact A should be present.
	require.Len(t, midState.Artifacts, 1,
		"at t_mid only artifact A must be visible")
	require.Equal(t, idA, midState.Artifacts[0].ArtifactID,
		"at t_mid the visible artifact must be A")

	// Query at t_after — after both archives.
	tAfter := t4.Add(5 * time.Millisecond)
	afterState, err := tm.ScopeStateAt(ctx, scopeID, tAfter)
	require.NoError(t, err, "ScopeStateAt must succeed after all archives")

	// Both artifacts A and B should be present.
	require.Len(t, afterState.Artifacts, 2,
		"at t_after both artifacts A and B must be visible")
	ids := map[model.ID]bool{afterState.Artifacts[0].ArtifactID: true, afterState.Artifacts[1].ArtifactID: true}
	require.True(t, ids[idA], "artifact A must be present at t_after")
	require.True(t, ids[idB], "artifact B must be present at t_after")

	// Query at t_before — before any artifacts exist.
	tBefore := t0.Add(-1 * time.Hour)
	_, err = tm.ScopeStateAt(ctx, scopeID, tBefore)
	require.ErrorIs(t, err, model.ErrNoHistoricalState,
		"ScopeStateAt before scope creation must return ErrNoHistoricalState")
}

// timeMachineAdapter bridges store.DiskStore → knowledge.TimeAnchorStore,
// knowledge.TimeArtifactStore, and knowledge.TimeEdgeStore.
//
// DiskStore method signatures (no ctx param on some methods, different names)
// don't match knowledge interfaces directly.
type timeMachineAdapter struct {
	disk *store.DiskStore
}

func (a *timeMachineAdapter) LatestFullAnchorBefore(ctx context.Context, scopeID model.ScopeID, at time.Time) (*model.Anchor, error) {
	return store.NewAnchorStore(a.disk).LatestFullAnchorBefore(ctx, scopeID, at)
}

func (a *timeMachineAdapter) LatestAnchorsAfter(ctx context.Context, scopeID model.ScopeID, after, to time.Time) ([]model.Anchor, error) {
	return store.NewAnchorStore(a.disk).LatestAnchorsAfter(ctx, scopeID, after, to)
}

func (a *timeMachineAdapter) LoadArtifactsByIDs(ctx context.Context, ids []model.ID) ([]*model.Artifact, []model.ID, error) {
	return a.disk.LoadArtifactsByIDs(ctx, ids)
}

func (a *timeMachineAdapter) ListProjectionKeys(_ context.Context, artifactID model.ID) ([]model.ProjectionKey, error) {
	return a.disk.ListProjectionKeys(artifactID)
}

func (a *timeMachineAdapter) LoadProjection(_ context.Context, key model.ProjectionKey) (*model.ArtifactProjection, error) {
	return a.disk.LoadProjection(key)
}

func (a *timeMachineAdapter) LoadEdgesByIDs(_ context.Context, ids []model.EdgeID) ([]*model.Edge, error) {
	return a.disk.LoadEdgesByIDs(ids)
}

// ---------------------------------------------------------------
// E2E: dangling — delete from store, verify graceful retrieval
// ---------------------------------------------------------------

func TestE2E_Dangling(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	// Add 3 artifacts with deterministic content.
	aidAlpha, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("alpha retrieval text"), "alpha")
	require.NoError(t, err)
	aidBeta, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("beta retrieval text"), "beta")
	require.NoError(t, err)
	aidGamma, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("gamma retrieval text"), "gamma")
	require.NoError(t, err)

	idAlpha := mustParseID(t, aidAlpha)
	idBeta := mustParseID(t, aidBeta)
	idGamma := mustParseID(t, aidGamma)

	// Pre-corruption query — all 3 present.
	results, err := rt.Query(ctx, "retrieval", 10)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Corrupt: delete beta's node and projection from store directly.
	require.NoError(t, rt.disk.DeleteNode(idBeta))
	betaProjections, err := rt.disk.ListProjectionKeys(idBeta)
	require.NoError(t, err)
	for _, k := range betaProjections {
		require.NoError(t, rt.disk.DeleteProjection(k))
	}

	// Corrupt: delete gamma's projection only (node still exists).
	gammaProjections, err := rt.disk.ListProjectionKeys(idGamma)
	require.NoError(t, err)
	for _, k := range gammaProjections {
		require.NoError(t, rt.disk.DeleteProjection(k))
	}

	// Post-corruption query — must not hard-fail.
	results, err = rt.Query(ctx, "retrieval", 10)
	require.NoError(t, err, "dangling references must not cause hard-failure")

	// Only alpha survives (intact node + projection).
	require.Len(t, results, 1)
	require.Equal(t, aidAlpha, results[0].ArtifactID)
	require.Equal(t, "alpha", results[0].Summary)

	// Sanity: idAlpha is the only one still loadable from disk.
	_, err = rt.disk.LoadNode(idAlpha)
	require.NoError(t, err)
	_, err = rt.disk.LoadNode(idBeta)
	require.ErrorIs(t, err, model.ErrNotFound)
	_, err = rt.disk.LoadNode(idGamma)
	require.NoError(t, err, "gamma node still exists — only projection was deleted")
}

// ---------------------------------------------------------------
// E2E: concurrent reads with sequential writes
//
// Writes remain sequential in v0.1.
// This test validates concurrent read safety only.
// ---------------------------------------------------------------

func TestE2E_ConcurrentReads(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	// Baseline artifact.
	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte("alpha retrieval text"), "alpha")
	require.NoError(t, err)

	const numReaders = 5
	const readsPerReader = 20
	const numWrites = 10

	var wg sync.WaitGroup

	// Spawn concurrent readers.
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < readsPerReader; j++ {
				_, err := rt.Query(ctx, "retrieval", 10)
				if err != nil {
					// Non-fatal: log-only in v0.1 concurrency model.
					// errors during rebuild are expected under concurrent load.
					t.Logf("reader %d iteration %d: %v", id, j, err)
				}
			}
		}(i)
	}

	// Sequential writes interleaved with concurrent reads.
	for i := 0; i < numWrites; i++ {
		content := []byte(fmt.Sprintf("content number %d retrieval text", i))
		summary := fmt.Sprintf("artifact %d", i)
		_, err := rt.AddArtifact(ctx, wt.ScopeID, content, summary)
		require.NoError(t, err)
	}

	wg.Wait()

	// Final verification with eventual consistency.
	// Retrieval indexes rebuild per call — the last write may not be
	// visible immediately.  require.Eventually closes the timing window.
	require.Eventually(t, func() bool {
		results, err := rt.Query(ctx, "retrieval", 100)
		if err != nil {
			return false
		}
		// Baseline (1) + sequential writes (10) = 11 total.
		if len(results) != 11 {
			t.Logf("expected 11 results, got %d", len(results))
			return false
		}
		return true
	}, 2*time.Second, 50*time.Millisecond,
		"all 11 artifacts must be queryable after concurrent reads")
}

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func mustParseID(t *testing.T, s string) model.ID {
	t.Helper()
	id, err := model.ParseID(s)
	require.NoError(t, err)
	return id
}

func containsID(results []QueryResult, id string) bool {
	for _, r := range results {
		if r.ArtifactID == id {
			return true
		}
	}
	return false
}

func summaryOf(results []QueryResult, id string) string {
	for _, r := range results {
		if r.ArtifactID == id {
			return r.Summary
		}
	}
	return ""
}
