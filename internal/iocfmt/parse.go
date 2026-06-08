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

// ParseModeSpec maps a -mode string to (retrieval mode, hierarchical?, collapsed?).
//
//	vector (default) | hybrid | hierarchical (coarse→fine over rollups) |
//	collapsed (flat over visible ∪ ALL descendants — no coarse→fine routing)
func ParseModeSpec(s string) (core.QueryMode, bool, bool) {
	switch strings.ToLower(s) {
	case "hybrid":
		return core.ModeHybrid, false, false
	case "hierarchical", "hierarchical-hybrid", "hybrid-hierarchical":
		// coarse(rollups) + HYBRID fine — the configuration that works at scale
		// (recall 0.94 vs 0.33 for vector-only at 180 artifacts).
		return core.ModeHybrid, true, false
	case "hierarchical-vector":
		return core.ModeVector, true, false // coarse + vector fine (weak; for comparison)
	case "collapsed", "collapsed-vector":
		return core.ModeVector, false, true // flat over the whole subtree (RAPTOR collapsed-tree)
	case "collapsed-hybrid", "hybrid-collapsed":
		return core.ModeHybrid, false, true
	default:
		return core.ModeVector, false, false
	}
}
