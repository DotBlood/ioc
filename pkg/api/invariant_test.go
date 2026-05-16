package api

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/graph"
	"github.com/DotBlood/ioc/internal/knowledge"
	"github.com/DotBlood/ioc/internal/model"
	"github.com/stretchr/testify/require"
)

// These tests verify architectural invariants and expected usage contracts.
//
// Classification of guarantees tested:
//   - Hard guarantees: enforced structurally by the runtime (I1, I2, I6)
//   - Soft conventions: document caller obligations in v0.1 (I5)
//   - Storage guarantees: verified observable behavior (I7)
//   - Topology assumptions: validated graph structure (I3, fuzz)

// ---------------------------------------------------------------
// I1 — Stable Identity (hard guarantee)
// ---------------------------------------------------------------

func TestI1_StableIdentity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	artifactID, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("stable identity test"), "i1")
	require.NoError(t, err)
	require.NotEmpty(t, artifactID)

	// ID.String() is stable after save → parse → string.
	id, err := model.ParseID(artifactID)
	require.NoError(t, err)
	require.Equal(t, artifactID, id.String())

	// Read from underlying store — ID unchanged.
	node, err := rt.disk.LoadNode(id)
	require.NoError(t, err)
	require.Equal(t, id, node.ArtifactID)
	require.Equal(t, artifactID, node.ArtifactID.String())

	// ContentHash is a separate entity from the ULID ID.
	require.False(t, node.ContentHash.IsZero(), "content hash must be set after artifact creation")
	require.NotEqual(t, id.String(), node.ContentHash.String(),
		"content hash must not equal artifact ID")

	// ContentHash is SHA-256 (32 bytes), not a ULID (26 chars).
	require.Len(t, node.ContentHash, 32)
	_, err = model.ParseID(node.ContentHash.String())
	require.Error(t, err, "SHA-256 hex must not be parseable as a ULID")
}

// ---------------------------------------------------------------
// I2 — Lineage Acyclicity (hard guarantee)
// ---------------------------------------------------------------

func TestI2_LineageAcyclicity(t *testing.T) {
	ctx := context.Background()
	g := graph.NewStatefulGraph()

	now := time.Now()

	idA := model.NewID()
	idB := model.NewID()
	idC := model.NewID()

	for _, n := range []struct {
		id  model.ID
		typ model.NodeType
	}{
		{idA, model.NodeTypeArtifact},
		{idB, model.NodeTypeArtifact},
		{idC, model.NodeTypeArtifact},
	} {
		require.NoError(t, g.AddNode(ctx, &model.Artifact{
			ArtifactID: n.id,
			NodeType:   n.typ,
			Scope:      model.ScopeID(n.id.String()),
			CreatedAt:  now,
		}))
	}

	tracker := knowledge.NewLineageTracker(g)

	// Build chain: A is parent of B, B is parent of C.
	require.NoError(t, tracker.RecordLineage(ctx, idA, idB))
	require.NoError(t, tracker.RecordLineage(ctx, idB, idC))

	// Before cycle attempt: Provenance(C) traces back through B to A.
	prov, err := tracker.Provenance(ctx, idC)
	require.NoError(t, err)
	require.Len(t, prov, 2, "path C→B→A must be 2 edges")
	require.True(t, g.Validate() == nil, "graph must be valid before cycle attempt")

	// Attempt to create cycle: C is parent of A.
	err = tracker.RecordLineage(ctx, idC, idA)
	require.ErrorIs(t, err, model.ErrCycleDetected, "cycle must be rejected")

	// After rejection: graph state is unchanged and still acyclic.
	require.NoError(t, g.Validate(), "graph must remain valid after rejected cycle")

	prov2, err := tracker.Provenance(ctx, idC)
	require.NoError(t, err)
	require.Len(t, prov2, 2, "C→B must still be only 2 edges after cycle rejection")
}

// ---------------------------------------------------------------
// I3a — Scope hierarchy forms an acyclic tree (topology assumption)
// ---------------------------------------------------------------

func TestI3a_ScopeHierarchyIsTree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	// Build tree: wt1 → ws1 → sess1, plus wt2 (detached root).
	wt1, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)
	wt2, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)
	ws1, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "workspace", ParentID: wt1.ScopeID})
	require.NoError(t, err)
	sess1, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "session", ParentID: ws1.ScopeID})
	require.NoError(t, err)
	_ = sess1
	_ = wt2

	states, err := rt.disk.ListAllScopeStates()
	require.NoError(t, err)

	// Build parent→children map.
	children := make(map[string][]string)
	var roots []string
	for _, s := range states {
		if !s.ParentID.IsZero() {
			pid := s.ParentID.String()
			children[pid] = append(children[pid], string(s.ScopeID))
		} else {
			roots = append(roots, string(s.ScopeID))
		}
	}

	// Exactly 2 root scopes (wt1, wt2).
	require.Len(t, roots, 2)

	// Walk from each root — max depth must not exceed total scopes (acyclic).
	visited := make(map[string]bool)
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if depth > len(states) {
			require.Fail(t, "cycle detected in scope hierarchy")
			return
		}
		visited[id] = true
		for _, child := range children[id] {
			walk(child, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	require.Len(t, visited, len(states), "all scopes must be reachable from roots")

	// Verify parent type constraints.
	for _, s := range states {
		switch s.Type {
		case model.NodeTypeWorktree:
			require.True(t, s.ParentID.IsZero(), "worktree must have no parent")
		case model.NodeTypeWorkspace:
			require.False(t, s.ParentID.IsZero())
			pt, err := rt.disk.NodeType(s.ParentID)
			require.NoError(t, err)
			require.Equal(t, model.NodeTypeWorktree, pt, "workspace parent must be worktree")
		case model.NodeTypeSession:
			require.False(t, s.ParentID.IsZero())
			pt, err := rt.disk.NodeType(s.ParentID)
			require.NoError(t, err)
			require.Equal(t, model.NodeTypeWorkspace, pt, "session parent must be workspace")
		}
	}
}

// ---------------------------------------------------------------
// I3b — Artifacts are created with exactly one assigned ScopeID
// ---------------------------------------------------------------

func TestI3b_ArtifactHasSingleScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	aid, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("scope test content"), "i3b")
	require.NoError(t, err)

	id, err := model.ParseID(aid)
	require.NoError(t, err)

	// Artifact.Scope is non-empty and refers to the intended scope.
	node, err := rt.disk.LoadNode(id)
	require.NoError(t, err)
	require.NotEmpty(t, string(node.Scope))
	require.Equal(t, model.ScopeID(wt.ScopeID), node.Scope)

	// ScopeState exists for that ScopeID.
	_, err = rt.disk.ScopeState(ctx, model.ScopeID(wt.ScopeID))
	require.NoError(t, err, "scope state must exist for the artifact's scope")
}

// ---------------------------------------------------------------
// I4 — Artifact Immutability (hard guarantee by convention)
// ---------------------------------------------------------------

func TestI4_ArtifactImmutability(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	content := []byte("immutable content for verification")
	aid1, err := rt.AddArtifact(ctx, wt.ScopeID, content, "i4")
	require.NoError(t, err)

	id1, err := model.ParseID(aid1)
	require.NoError(t, err)
	node1, err := rt.disk.LoadNode(id1)
	require.NoError(t, err)

	// Read from CAS → content matches original.
	rc, err := rt.cas.Open(ctx, node1.ContentHash)
	require.NoError(t, err)
	readContent, err := io.ReadAll(rc)
	rc.Close()
	require.NoError(t, err)
	require.Equal(t, content, readContent, "stored content must match original")

	// Same content → different artifact ID (CAS dedup ≠ identity dedup).
	aid2, err := rt.AddArtifact(ctx, wt.ScopeID, content, "i4-dup")
	require.NoError(t, err)
	require.NotEqual(t, aid1, aid2, "same content must produce a different artifact ID")

	// Old artifact still exists with original ContentHash.
	node1b, err := rt.disk.LoadNode(id1)
	require.NoError(t, err)
	require.Equal(t, node1.ContentHash, node1b.ContentHash,
		"original artifact content must persist unchanged")
}

// ---------------------------------------------------------------
// I5 — Revision usage convention (documented caller obligation)
// ---------------------------------------------------------------
//
// NOT an invariant: DiskStore.SaveProjection does not enforce
// monotonicity.  Callers are responsible for maintaining it.

func TestI5_RevisionUsageConvention(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	aid, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("revision convention test"), "i5")
	require.NoError(t, err)

	id, err := model.ParseID(aid)
	require.NoError(t, err)

	// Pipeline creates revision 1.
	proj, err := rt.disk.LoadProjection(model.ProjectionKey{ArtifactID: id, Revision: 1})
	require.NoError(t, err)
	require.Equal(t, model.RevisionNumber(1), proj.Revision)

	// Save projection with revision 2.
	proj2 := &model.ArtifactProjection{
		ArtifactID: id,
		Revision:   2,
		Summary:    "manual revision 2",
		Readiness:  proj.Readiness,
		ValidFrom:  time.Now(),
	}
	require.NoError(t, rt.disk.SaveProjection(proj2))

	// LatestProjection returns the highest revision (2).
	latest, err := rt.artifactStore().LatestProjection(ctx, id)
	require.NoError(t, err)
	require.Equal(t, model.RevisionNumber(2), latest.Revision,
		"LatestProjection must return the highest revision")
}

// ---------------------------------------------------------------
// I6 — Retrieval Bounded Determinism (hard guarantee)
// ---------------------------------------------------------------

func TestI6_BoundedDeterminism(t *testing.T) {
	// Intentionally non-parallel.
	// Determinism guarantees are verified under identical sequential execution.
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte("the quick brown fox"), "animal")
	require.NoError(t, err)
	_, err = rt.AddArtifact(ctx, wt.ScopeID, []byte("jumps over the lazy dog"), "animal")
	require.NoError(t, err)

	// Same query twice — identical results.
	r1, err := rt.Query(ctx, "fox", 10)
	require.NoError(t, err)
	r2, err := rt.Query(ctx, "fox", 10)
	require.NoError(t, err)
	require.Equal(t, len(r1), len(r2), "result count must be identical")
	for i := range r1 {
		require.Equal(t, r1[i].ArtifactID, r2[i].ArtifactID,
			"result %d: ArtifactID mismatch", i)
		require.Equal(t, r1[i].Score, r2[i].Score,
			"result %d: Score mismatch", i)
		require.Equal(t, r1[i].Summary, r2[i].Summary,
			"result %d: Summary mismatch", i)
	}

	// Different query — also deterministic.
	r3, err := rt.Query(ctx, "dog", 10)
	require.NoError(t, err)
	r4, err := rt.Query(ctx, "dog", 10)
	require.NoError(t, err)
	require.Equal(t, len(r3), len(r4))
	for i := range r3 {
		require.Equal(t, r3[i].ArtifactID, r4[i].ArtifactID)
		require.Equal(t, r3[i].Score, r4[i].Score)
	}
}

// ---------------------------------------------------------------
// I7 — Snapshot Consistency (observable storage guarantee)
// ---------------------------------------------------------------

func TestI7_SnapshotConsistency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	initRepo(t, dir)

	rt, err := Open(ctx, Config{RootDir: dir})
	require.NoError(t, err)
	defer rt.Close()

	wt, err := rt.CreateScope(ctx, CreateScopeRequest{Type: "worktree"})
	require.NoError(t, err)

	aid, err := rt.AddArtifact(ctx, wt.ScopeID, []byte("snapshot consistency"), "i7")
	require.NoError(t, err)
	id, err := model.ParseID(aid)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(1)

	// Writer: continuously update projections.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			proj := &model.ArtifactProjection{
				ArtifactID: id,
				Revision:   model.RevisionNumber(i + 2),
				Summary:    fmt.Sprintf("snapshot iteration %d", i),
				ValidFrom:  time.Now(),
			}
			_ = rt.disk.SaveProjection(proj)
			time.Sleep(time.Microsecond)
		}
	}()

	// Reader: repeatedly load snapshot, verify internal consistency.
	for i := 0; i < 50; i++ {
		snap, err := rt.disk.LoadSnapshot()
		require.NoError(t, err)
		require.NotNil(t, snap)

		nodeSet := make(map[string]bool)
		for _, n := range snap.Nodes {
			require.NotNil(t, n, "nil node in snapshot")
			nodeSet[n.ArtifactID.String()] = true
		}
		for _, e := range snap.Edges {
			require.NotNil(t, e, "nil edge in snapshot")
			require.True(t, nodeSet[e.Source.String()],
				"snapshot edge %s: source %s not in nodes", e.EdgeID, e.Source)
			require.True(t, nodeSet[e.Target.String()],
				"snapshot edge %s: target %s not in nodes", e.EdgeID, e.Target)
		}
		for _, p := range snap.Projections {
			require.NotNil(t, p, "nil projection in snapshot")
			require.True(t, nodeSet[p.ArtifactID.String()],
				"snapshot projection for %s: artifact not in nodes", p.ArtifactID)
		}
	}

	wg.Wait()
}

// ---------------------------------------------------------------
// Fuzz: Graph topology structural integrity
// ---------------------------------------------------------------

func FuzzGraphTopology(f *testing.F) {
	f.Add(uint8(0), uint8(1))
	f.Add(uint8(1), uint8(0))
	f.Add(uint8(2), uint8(1))

	const maxNodes = 256
	const maxEdges = 1024

	f.Fuzz(func(t *testing.T, op1, op2 uint8) {
		ctx := context.Background()
		g := graph.NewStatefulGraph()

		var nodeIDs []model.ID
		var edgeIDs []model.EdgeID

		ops := []struct {
			name string
			fn   func()
		}{
			{
				name: "AddNode",
				fn: func() {
					if len(nodeIDs) >= maxNodes {
						return
					}
					id := model.NewID()
					typ := model.NodeType((len(nodeIDs) % 5) + 1)
					if err := g.AddNode(ctx, &model.Artifact{
						ArtifactID: id,
						NodeType:   typ,
						Scope:      model.ScopeID(id.String()),
						CreatedAt:  time.Now(),
					}); err != nil {
						return
					}
					nodeIDs = append(nodeIDs, id)
				},
			},
			{
				name: "AddEdge",
				fn: func() {
					if len(nodeIDs) < 2 || len(edgeIDs) >= maxEdges {
						return
					}
					src := nodeIDs[int(op1)%len(nodeIDs)]
					tgt := nodeIDs[int(op2)%len(nodeIDs)]
					if src == tgt {
						return
					}
					eid := model.NewID()
					if err := g.AddEdge(ctx, &model.Edge{
						EdgeID:    eid,
						Type:      model.EdgeType((len(edgeIDs) % 8) + 1),
						Direction: model.DirectionDirected,
						Source:    src,
						Target:    tgt,
						Valid:     true,
					}); err != nil {
						return
					}
					edgeIDs = append(edgeIDs, eid)
				},
			},
			{
				name: "RemoveNode",
				fn: func() {
					if len(nodeIDs) == 0 {
						return
					}
					idx := int(op2) % len(nodeIDs)
					id := nodeIDs[idx]
					if err := g.RemoveNode(ctx, id); err != nil {
						return
					}
					nodeIDs = append(nodeIDs[:idx], nodeIDs[idx+1:]...)
				},
			},
		}

		for i := 0; i < 30; i++ {
			idx := (int(op1) + int(op2) + i) % len(ops)
			ops[idx].fn()
			require.NoError(t, g.Validate(),
				"graph invalid after %s (op cycle %d)", ops[idx].name, i)
		}
	})
}
