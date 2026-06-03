// Package iocfmt holds parsing and JSON-shaping helpers shared by the ioc and
// ioc-mcp commands. It depends only on internal/core (no engine/mcp) to stay
// dependency-cycle-free.
package iocfmt

import (
	"strings"

	"github.com/DotBlood/ioc/internal/core"
)

// ParseScopeID parses a scope ID; "" or "root" => NilID (a root scope).
func ParseScopeID(s string) (core.ID, error) {
	if s == "" || s == "root" {
		return core.NilID, nil
	}
	return core.ParseID(s)
}

// ParseRole maps a string to a Role (default session).
func ParseRole(s string) core.Role {
	switch strings.ToLower(s) {
	case "worktree":
		return core.RoleWorktree
	case "workspace":
		return core.RoleWorkspace
	default:
		return core.RoleSession
	}
}

// ParseKind maps a string to an ArtifactKind (default insight).
func ParseKind(s string) core.ArtifactKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "answer":
		return core.KindAnswer
	case "summary":
		return core.KindSummary
	case "document", "file":
		return core.KindDocument
	case "reasoning":
		return core.KindReasoning
	case "seed":
		return core.KindSeed
	default:
		return core.KindInsight
	}
}

// ParseKinds parses a comma-separated kind list into a set (empty => all kinds).
func ParseKinds(s string) []core.ArtifactKind {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []core.ArtifactKind
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, ParseKind(p))
		}
	}
	return out
}

// ParseDetail maps a string to a Detail level (default overview).
func ParseDetail(s string) core.Detail {
	switch strings.ToLower(s) {
	case "raw":
		return core.DetailRaw
	case "entry":
		return core.DetailEntry
	default:
		return core.DetailOverview
	}
}

// ParseTier maps a string to a Tier (empty => 0 = both).
func ParseTier(s string) core.Tier {
	switch strings.ToLower(s) {
	case "worktree":
		return core.TierWorktree
	case "workspace":
		return core.TierWorkspace
	default:
		return 0
	}
}

// ParseModeSpec maps a -mode string to (retrieval mode, hierarchical?).
//
//	vector (default) | hybrid (experimental) | hierarchical (coarse→fine over rollups)
func ParseModeSpec(s string) (core.QueryMode, bool) {
	switch strings.ToLower(s) {
	case "hybrid":
		return core.ModeHybrid, false
	case "hierarchical":
		return core.ModeVector, true
	default:
		return core.ModeVector, false
	}
}
