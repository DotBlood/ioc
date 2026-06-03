package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/DotBlood/ioc/internal/core"
)

// Fork creates a new scope branched from source, keeping both. Published
// artifacts of source are copied (summary + embedding + content hash) into the
// new scope's workspace memory; raw content stays shared in CAS by hash.
func (e *Engine) Fork(_ context.Context, source core.ID, title string) (core.Scope, error) {
	src, err := e.meta.GetScope(source)
	if err != nil {
		return core.Scope{}, fmt.Errorf("engine: fork: source: %w", err)
	}
	ns := core.Scope{
		ID:         core.NewID(),
		Parent:     src.Parent,
		Role:       src.Role,
		Title:      title,
		Version:    src.Version,
		ForkedFrom: source,
		CreatedAt:  time.Now(),
	}
	if err := e.meta.PutScope(ns); err != nil {
		return core.Scope{}, err
	}

	arts, err := e.meta.ArtifactsInScope(source)
	if err != nil {
		return core.Scope{}, err
	}
	for _, a := range arts {
		if !a.Published {
			continue
		}
		copyArt := core.Artifact{
			ID:          core.NewID(),
			Scope:       ns.ID,
			Kind:        a.Kind,
			Tier:        core.TierWorkspace,
			Summary:     a.Summary,
			EmbRef:      a.EmbRef, // shared embedding ref (same vector store)
			Content:     a.Content,
			DerivedFrom: []core.ID{a.ID},
			Published:   false,
			CreatedAt:   time.Now(),
		}
		if err := e.meta.PutArtifact(copyArt); err != nil {
			return core.Scope{}, err
		}
	}
	return ns, nil
}

// Consolidate promotes a single consolidated summary (written by the external
// LLM) into the parent scope's worktree tier — a branch-transition boundary.
// If the scope has no parent, the summary is stored in the scope itself.
func (e *Engine) Consolidate(ctx context.Context, scope core.ID, summary string) (core.Artifact, error) {
	if summary == "" {
		return core.Artifact{}, fmt.Errorf("engine: consolidate: %w: empty summary", core.ErrInvalidInput)
	}
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return core.Artifact{}, err
	}
	target := s.Parent
	if target.IsZero() {
		target = scope
	}
	ref, err := e.storeSummaryEmbedding(ctx, summary)
	if err != nil {
		return core.Artifact{}, err
	}
	a := core.Artifact{
		ID:        core.NewID(),
		Scope:     target,
		Kind:      core.KindSummary,
		Tier:      core.TierWorktree,
		Summary:   summary,
		EmbRef:    ref,
		Published: true,
		CreatedAt: time.Now(),
	}
	if err := e.meta.PutArtifact(a); err != nil {
		return core.Artifact{}, err
	}
	return a, nil
}

// CrossVersion archives the current scope version and opens vN+1 seeded with a
// single KindSeed artifact carrying distilled constraints/lessons.
func (e *Engine) CrossVersion(ctx context.Context, scope core.ID, seed core.Seed) (core.Scope, error) {
	s, err := e.meta.GetScope(scope)
	if err != nil {
		return core.Scope{}, err
	}
	s.Archived = true
	if err := e.meta.PutScope(s); err != nil {
		return core.Scope{}, err
	}

	ns := core.Scope{
		ID:         core.NewID(),
		Parent:     s.Parent,
		Role:       s.Role,
		Title:      s.Title,
		Version:    s.Version + 1,
		ForkedFrom: scope,
		CreatedAt:  time.Now(),
	}
	if err := e.meta.PutScope(ns); err != nil {
		return core.Scope{}, err
	}

	seedText := fmt.Sprintf("Constraints: %s\nLessons: %s", seed.Constraints, seed.Lessons)
	ref, err := e.storeSummaryEmbedding(ctx, seedText)
	if err != nil {
		return core.Scope{}, err
	}
	seedArt := core.Artifact{
		ID:        core.NewID(),
		Scope:     ns.ID,
		Kind:      core.KindSeed,
		Tier:      core.TierWorktree,
		Summary:   seedText,
		EmbRef:    ref,
		Published: true,
		CreatedAt: time.Now(),
	}
	if err := e.meta.PutArtifact(seedArt); err != nil {
		return core.Scope{}, err
	}
	ns.SeedFrom = seedArt.ID
	if err := e.meta.PutScope(ns); err != nil {
		return core.Scope{}, err
	}
	return ns, nil
}
