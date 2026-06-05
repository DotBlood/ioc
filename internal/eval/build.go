package eval

import (
	"context"
	"fmt"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// resolveScopeName maps a symbolic scope name to a real ID. "" / "root" / "-"
// resolve to the zero (root) scope.
func resolveScopeName(scopes map[string]core.ID, name string) (core.ID, error) {
	if name == "" || name == "root" || name == "-" {
		return core.NilID, nil
	}
	id, ok := scopes[name]
	if !ok {
		return core.NilID, fmt.Errorf("unknown scope %q", name)
	}
	return id, nil
}

// applyStructureTurn executes a corpus-construction turn (everything except
// "query") against the engine, mutating the scopes/arts symbol tables. It
// returns handled=false for a "query" turn so the caller can score it. This is
// the single source of truth for turn semantics, shared by the scenario harness
// (Run) and the wall harness (WallRun).
func applyStructureTurn(ctx context.Context, e *engine.Engine, i int, t Turn, scopes, arts map[string]core.ID) (handled bool, err error) {
	switch t.Op {
	case "create_scope":
		parent, err := resolveScopeName(scopes, t.Parent)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		s, err := e.CreateScope(ctx, parent, iocfmt.ParseRole(t.Role), t.Title)
		if err != nil {
			return true, fmt.Errorf("turn %d create_scope: %w", i, err)
		}
		scopes[t.ID] = s.ID

	case "push":
		scope, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		var content []byte
		if t.Content != "" {
			content = []byte(t.Content)
		}
		var supersedes []core.ID
		for _, name := range t.Supersedes {
			id, ok := arts[name]
			if !ok {
				return true, fmt.Errorf("turn %d push: unknown supersedes artifact %q", i, name)
			}
			supersedes = append(supersedes, id)
		}
		a, err := e.Push(ctx, core.PushRequest{
			Scope:      scope,
			Kind:       iocfmt.ParseKind(t.Kind),
			Summary:    t.Summary,
			Content:    content,
			Publish:    t.Publish,
			Supersedes: supersedes,
		})
		if err != nil {
			return true, fmt.Errorf("turn %d push: %w", i, err)
		}
		if t.As != "" {
			arts[t.As] = a.ID
		}

	case "publish":
		id, ok := arts[t.Ref]
		if !ok {
			return true, fmt.Errorf("turn %d publish: unknown artifact %q", i, t.Ref)
		}
		if err := e.Publish(ctx, id); err != nil {
			return true, fmt.Errorf("turn %d publish: %w", i, err)
		}

	case "fork":
		src, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		ns, err := e.Fork(ctx, src, t.Title)
		if err != nil {
			return true, fmt.Errorf("turn %d fork: %w", i, err)
		}
		scopes[t.ID] = ns.ID

	case "consolidate":
		scope, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		var supersedes []core.ID
		for _, name := range t.Supersedes {
			id, ok := arts[name]
			if !ok {
				return true, fmt.Errorf("turn %d consolidate: unknown supersedes artifact %q", i, name)
			}
			supersedes = append(supersedes, id)
		}
		if _, err := e.Consolidate(ctx, scope, t.Summary, supersedes); err != nil {
			return true, fmt.Errorf("turn %d consolidate: %w", i, err)
		}

	case "crossversion":
		scope, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		ns, err := e.CrossVersion(ctx, scope, core.Seed{Constraints: t.Constraints, Lessons: t.Lessons})
		if err != nil {
			return true, fmt.Errorf("turn %d crossversion: %w", i, err)
		}
		scopes[t.ID] = ns.ID

	case "rollup":
		scope, err := resolveScopeName(scopes, t.Scope)
		if err != nil {
			return true, fmt.Errorf("turn %d: %w", i, err)
		}
		if err := e.RollupScope(ctx, scope, t.Summary); err != nil {
			return true, fmt.Errorf("turn %d rollup: %w", i, err)
		}

	case "query":
		return false, nil // caller scores it

	default:
		return true, fmt.Errorf("turn %d: unknown op %q", i, t.Op)
	}
	return true, nil
}
