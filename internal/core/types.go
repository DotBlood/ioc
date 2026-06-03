package core

import (
	"errors"
	"time"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("not found")
	ErrInvalidInput = errors.New("invalid input")
)

// Role is the role a Scope plays. Roles are conventions on a single recursive
// Scope type — NOT fixed hierarchy levels. A RoleSession scope can be promoted
// to RoleWorkspace and nest its own scopes.
type Role uint8

const (
	RoleWorktree  Role = iota + 1 // project / tenant root; long-lived
	RoleWorkspace                 // an area of study; mutable working memory
	RoleSession                   // a direction of reasoning (node in the idea-evolution graph)
)

func (r Role) String() string {
	switch r {
	case RoleWorktree:
		return "worktree"
	case RoleWorkspace:
		return "workspace"
	case RoleSession:
		return "session"
	default:
		return "unknown"
	}
}

// Tier distinguishes the two memory tiers. Memory is a view over artifacts:
// an artifact's Tier says whether it is mutable working memory or a canonical truth.
type Tier uint8

const (
	TierWorkspace Tier = iota + 1 // mutable, short-lived working memory (default)
	TierWorktree                  // canonical, finished truths; long-lived
)

func (t Tier) String() string {
	switch t {
	case TierWorkspace:
		return "workspace"
	case TierWorktree:
		return "worktree"
	default:
		return "unknown"
	}
}

// ArtifactKind classifies a leaf result/insight.
type ArtifactKind uint8

const (
	KindAnswer    ArtifactKind = iota + 1 // a produced answer
	KindInsight                           // a distilled conclusion (first-class reasoning)
	KindSummary                           // a memory summary (incl. summaries-of-summaries)
	KindDocument                          // a document
	KindReasoning                         // a distilled reasoning step (raw transcript stays cold)
	KindSeed                              // version-boundary seed: distilled constraints/lessons
)

func (k ArtifactKind) String() string {
	switch k {
	case KindAnswer:
		return "answer"
	case KindInsight:
		return "insight"
	case KindSummary:
		return "summary"
	case KindDocument:
		return "document"
	case KindReasoning:
		return "reasoning"
	case KindSeed:
		return "seed"
	default:
		return "unknown"
	}
}

// Detail is the disclosure resolution requested from a query/drill.
type Detail uint8

const (
	DetailOverview Detail = iota + 1 // summary + score only (cheap, default)
	DetailEntry                      // + metadata / lineage / scope path
	DetailRaw                        // + full content from CAS (expensive)
)

// Scope is the single recursive organizing unit.
type Scope struct {
	ID         ID        `json:"id"`
	Parent     ID        `json:"parent"`      // zero => root
	Role       Role      `json:"role"`        // worktree/workspace/session (a label)
	Title      string    `json:"title"`       //
	Version    int        `json:"version"`    // vN
	SeedFrom   ID        `json:"seed_from"`   // KindSeed artifact this version started from
	Archived   bool      `json:"archived"`    // superseded by a newer version
	ForkedFrom ID        `json:"forked_from"` // scope this was forked from (idea-evolution graph)
	CreatedAt  time.Time `json:"created_at"`
}

// Artifact is a leaf result/insight. Full content lives in CAS (cold); the
// working layer holds only a mini-summary + an embedding OF THE SUMMARY.
type Artifact struct {
	ID          ID           `json:"id"`
	Scope       ID           `json:"scope"`
	Kind        ArtifactKind `json:"kind"`
	Tier        Tier         `json:"tier"`
	Summary     string       `json:"summary"`      // the cheap representation
	EmbRef      EmbeddingRef `json:"emb_ref"`      // embedding of Summary
	Content     ContentHash  `json:"content"`      // full content in CAS; zero if summary-only
	DerivedFrom []ID         `json:"derived_from"` // idea-evolution lineage
	Published   bool         `json:"published"`    // visible to siblings via parent blackboard
	CreatedAt   time.Time    `json:"created_at"`
}

// PushRequest is how an external LLM writes content + a summary into IOC.
type PushRequest struct {
	Scope       ID
	Kind        ArtifactKind
	Summary     string // REQUIRED — the mini-summary the LLM wrote
	Content     []byte // OPTIONAL — full content; nil => summary-only
	DerivedFrom []ID
	Tier        Tier // defaults to TierWorkspace
	Publish     bool // make visible to siblings immediately
}

// QueryMode selects the retrieval strategy.
type QueryMode uint8

const (
	// ModeVector (default) — cosine only. Strongest at tested scale: bge-small
	// disambiguates near-duplicate clusters well on its own.
	ModeVector QueryMode = iota
	// ModeHybrid — vector + BM25 fused with RRF. EXPERIMENTAL: naive RRF over
	// short, term-overlapping summaries DEGRADED ranking in scale tests
	// (recall 0.75 vs vector's 1.00). Kept opt-in; needs better fusion to help.
	ModeHybrid
)

// Query is a progressive-disclosure retrieval request.
type Query struct {
	Scope    ID        // viewpoint scope (governs visibility)
	Text     string    // natural-language query; embedded via the embedder
	Detail   Detail    // start cheap (DetailOverview)
	TopK     int       //
	Tier     Tier      // 0 = both tiers
	Mode     QueryMode // hybrid (default) or vector-only
	MinScore float64   // drop hits whose cosine score < MinScore (0 = keep all)
}

// Hit is one retrieval result. Content is populated only at DetailRaw.
type Hit struct {
	Artifact  ID           `json:"artifact"`
	Scope     ID           `json:"scope"`
	ScopePath string       `json:"scope_path"`
	Kind      ArtifactKind `json:"kind"`
	Tier      Tier         `json:"tier"`
	Summary   string       `json:"summary"`
	Score     float64      `json:"score"`
	Content   []byte       `json:"content,omitempty"`
}

// Seed carries distilled constraints/lessons across a version boundary.
type Seed struct {
	Constraints string
	Lessons     string
}

// TraceRecord records the exact context a query saw (inspectability, not determinism).
type TraceRecord struct {
	QueryID    ID        `json:"query_id"`
	Scope      ID        `json:"scope"`
	Text       string    `json:"text"`
	EmbModel   string    `json:"emb_model"`
	VisibleSet []ID      `json:"visible_set"`
	Hits       []Hit     `json:"hits"`
	Drills     []ID      `json:"drills"`
	CreatedAt  time.Time `json:"created_at"`
}
