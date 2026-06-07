package core

import (
	"errors"
	"strconv"
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
	Version    int       `json:"version"`     // vN
	SeedFrom   ID        `json:"seed_from"`   // KindSeed artifact this version started from
	Archived   bool      `json:"archived"`    // superseded by a newer version
	ForkedFrom ID        `json:"forked_from"` // scope this was forked from (idea-evolution graph)
	CreatedAt  time.Time `json:"created_at"`

	// Rollup: a representative summary+embedding of the scope's contents, set by
	// RollupScope. Used by hierarchical (coarse→fine) retrieval to rank scopes.
	RollupSummary string       `json:"rollup_summary,omitempty"`
	RollupEmbRef  EmbeddingRef `json:"rollup_emb_ref,omitempty"`
}

// Artifact is a leaf result/insight. Full content lives in CAS (cold); the
// working layer holds only a mini-summary + an embedding OF THE SUMMARY.
type Artifact struct {
	ID          ID                `json:"id"`
	Scope       ID                `json:"scope"`
	Kind        ArtifactKind      `json:"kind"`
	Tier        Tier              `json:"tier"`
	Summary     string            `json:"summary"`        // the cheap representation
	EmbRef      EmbeddingRef      `json:"emb_ref"`        // embedding of Summary
	Content     ContentHash       `json:"content"`        // full content in CAS; zero if summary-only
	DerivedFrom []ID              `json:"derived_from"`   // idea-evolution lineage
	Published   bool              `json:"published"`      // visible to siblings via parent blackboard
	Meta        map[string]string `json:"meta,omitempty"` // e.g. file path, line range, chunk index
	CreatedAt   time.Time         `json:"created_at"`

	// SupersededBy, when non-zero, is the artifact that replaced this one. A
	// superseded artifact is no longer "current": default retrieval excludes it
	// (Query.IncludeSuperseded surfaces it with this back-link). Append-only — the
	// atom is never deleted, so a wrong supersession is reversible and history is
	// always reachable. See docs/SUPERSESSION.md.
	SupersededBy ID `json:"superseded_by,omitempty"`
}

// PushRequest is how an external LLM writes content + a summary into IOC.
type PushRequest struct {
	Scope       ID
	Kind        ArtifactKind
	Summary     string // REQUIRED — the display label / mini-summary
	Content     []byte // OPTIONAL — full content; nil => summary-only
	DerivedFrom []ID
	Tier        Tier // defaults to TierWorkspace
	Publish     bool // make visible to siblings immediately
	// EmbedText, if set, is what gets embedded instead of Summary — used for file
	// chunks (embed the raw chunk text; keep Summary as a short display label).
	EmbedText string
	Meta      map[string]string // optional metadata (file path, line range, ...)
	// Supersedes lists prior artifacts this push replaces (agent-declared at write,
	// the cheapest reliable currency signal — the authoring LLM just reasoned about
	// the change). Push marks each as SupersededBy this new artifact and records the
	// lineage in DerivedFrom. See docs/SUPERSESSION.md.
	Supersedes []ID
	// Trust is engine-asserted provenance written into Meta["trust"] (e.g.
	// TrustIngested for untrusted file content). It is a dedicated field, NOT a Meta
	// key, so an external caller cannot forge it via Meta — Push strips any
	// caller-supplied Meta["trust"] and sets it only from here. Empty = authored
	// (trusted), the default. See V17 in docs/SECURITY_AND_VULNERABILITIES.md.
	Trust string
	// Relations are author-declared typed edges FROM the new artifact TO existing
	// artifacts (e.g. depends_on, contradicts, answers). Like Supersedes, the
	// authoring LLM declares them at write time — IOC never infers edges (no LLM in
	// the core). Each Target must already exist; Push validates before writing.
	Relations []EdgeSpec
}

// TrustIngested marks an artifact whose content came from ingested files — it is
// UNTRUSTED data (potential indirect prompt injection), not authored reasoning.
const TrustIngested = "ingested"

// RelationKind is the type of an author-declared directed edge between artifacts.
// The vocabulary is open (any non-empty string); these are the conventional kinds.
type RelationKind string

const (
	RelDependsOn   RelationKind = "depends_on"  // From depends on To
	RelContradicts RelationKind = "contradicts" // From contradicts To
	RelAnswers     RelationKind = "answers"     // From answers To (e.g. a question artifact)
	RelRefines     RelationKind = "refines"     // From refines/elaborates To
	RelRelatesTo   RelationKind = "relates_to"  // generic association
)

// EdgeDir selects which edges a traversal follows relative to a viewpoint artifact.
type EdgeDir uint8

const (
	DirOut  EdgeDir = iota // edges FROM the artifact (its declared targets)
	DirIn                  // edges TO the artifact (who points at it — e.g. "what depends on X")
	DirBoth                // both directions
)

// Edge is a directed, author-declared relation From one artifact To another. The
// triple (From, To, Kind) is unique — re-declaring the same edge is idempotent.
type Edge struct {
	From      ID           `json:"from"`
	To        ID           `json:"to"`
	Kind      RelationKind `json:"kind"`
	CreatedAt time.Time    `json:"created_at"`
}

// EdgeSpec declares an edge to create at Push time: an edge of Kind from the
// artifact being pushed TO Target.
type EdgeSpec struct {
	Kind   RelationKind `json:"kind"`
	Target ID           `json:"target"`
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
	Mode     QueryMode // vector (default) or hybrid
	MinScore float64   // cosine gate: drop candidates with cosine < MinScore before rerank (0 = keep all)

	Hierarchical bool           // coarse→fine: rank scope rollups, then search within top scopes
	CoarseK      int            // # of scopes to keep in the coarse stage (default 6)
	// Collapsed searches the visible set ∪ ALL descendant-scope artifacts in one flat
	// pass (no coarse→fine routing) — it fixes flat retrieval's blindness to descendant
	// scopes AND hierarchical's routing-drop (RAPTOR's "collapsed tree" beats top-down
	// traversal; see the R1 collapsed-tree experiment in docs/WALL_EXPERIMENT.md). Mutually exclusive with Hierarchical
	// (Hierarchical wins if both set). Only changes the candidate SET; the ranking
	// pipeline (cosine/hybrid/graph-boost/rerank) and Hit.Score are unchanged.
	Collapsed    bool
	Kinds        []ArtifactKind // restrict results to these kinds (empty = all)
	Rerank       bool           // cross-encoder rerank the top RerankN candidates (needs a reranker)
	RerankN      int            // # of candidates to rerank (default 50)
	// AutoRerank, when true and a reranker is attached, reranks ONLY when the cosine
	// result is borderline (top below the confidence floor, or the top-two cosine margin
	// below the margin floor) — the dense near-duplicate / weak-top region where cosine
	// alone cannot separate present from absent. Confident cosine results skip the
	// cross-encoder (no latency). Rerank=true always reranks regardless. A no-op without
	// a reranker (pure-cosine fallback). See docs/WALL_EXPERIMENT.md (R5).
	AutoRerank bool

	// IncludeSuperseded surfaces non-current memory: by default retrieval returns
	// only the current view (drops artifacts with SupersededBy set and artifacts in
	// Archived version scopes). Set true for a "show history" drill.
	IncludeSuperseded bool

	// RecencyHalfLifeDays, when > 0, blends a small recency term into the cosine
	// ranking as a TIE-BREAKER among current atoms: score = α·cosine + (1−α)·decay,
	// decay = 0.5^(ageDays/halfLife). 0 (default) = OFF, pure cosine. CAVEAT: this
	// down-weights stable canonical truths just because they are old, so it is
	// opt-in — supersession (not age) is the real currency signal. Ignored when
	// reranking (the cross-encoder already orders). See docs/SUPERSESSION.md §8.
	RecencyHalfLifeDays float64

	// GraphBoost, when > 0 (0 = OFF, default), blends the STRUCTURAL axis into the
	// semantic ranking: a candidate that is edge-connected (author-declared edges) to
	// high-cosine candidates is lifted — score = (1−w)·cosine + w·g, where g is the
	// candidate's normalized connectivity to strong neighbours within the candidate
	// set (1-hop, undirected, all kinds). It only REORDERS the visible candidate set
	// (no recall change); Hit.Score stays cosine. A no-op when there are no edges, and
	// ignored when reranking. Opt-in so the proven pure-semantic default is untouched.
	// See docs/FSD.md §5.
	GraphBoost float64

	// ImportanceWeight, when > 0 (0 = OFF, default), blends an AUTHOR-DECLARED importance
	// term into the cosine ranking as a TIE-BREAKER: score = (1−w)·cosine + w·importance,
	// where importance is derived from Tier (TierWorktree = canonical truth = 1.0,
	// TierWorkspace = mutable = 0.0) — so a canonical artifact outranks a workspace
	// near-duplicate of similar cosine. Importance is declared by the authoring agent
	// (the Tier it pushed at), never LLM-scored — the Generative Agents
	// relevance+recency+importance signal, kept no-LLM. It only REORDERS (Hit.Score stays
	// cosine); a no-op when all candidates share one Tier; vector mode, ignored when
	// reranking. Opt-in so the proven default ranking is untouched.
	ImportanceWeight float64
}

// matchesKind reports whether k is in the (possibly empty=all) filter set.
func MatchesKinds(kinds []ArtifactKind, k ArtifactKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, want := range kinds {
		if want == k {
			return true
		}
	}
	return false
}

// ConfidenceFloor returns the cosine score below which a top hit should be
// treated as "no specific match" (weak_match) for a given embedder. Calibration
// is approximate and embedder-specific — not a solved problem.
func ConfidenceFloor(model string) float64 {
	switch model {
	case "BAAI/bge-small-en-v1.5":
		return 0.68 // in-domain floor ~0.6; real hits 0.78–0.87
	case "mock-bow":
		return 0.0 // lexical toy — don't flag weak
	default:
		return 0.5
	}
}

// RerankFloor returns the cross-encoder relevance score (sigmoid-normalized to
// [0,1]) below which a top reranked hit is treated as weak_match. A bge-reranker
// logit crosses 0 — sigmoid 0.5 — at the relevant/not-relevant boundary, so 0.5
// is the natural floor regardless of which cross-encoder produced the score.
// When results are reranked, weak_match/margin must use THIS signal, not the
// cosine ConfidenceFloor: rerank logits and cosine live on different scales.
// model is reserved for future per-reranker calibration.
func RerankFloor(model string) float64 {
	_ = model
	return 0.5
}

// MarginFloor returns the top1−top2 cosine margin below which the top hit is treated
// as "present but not decisive" (margin_ambiguous) for a given embedder — a SECOND,
// independent weak_match condition beside the absolute ConfidenceFloor. A large gap
// means one artifact clearly dominates (answer present and specific); a small gap means
// two hits are nearly tied (ambiguous or absent). Validated direction: a top1-vs-top2
// margin is a more robust abstention gate than an absolute threshold (TARG,
// arXiv:2511.09803), and absolute floors are not portable across embedders
// (arXiv:2403.05440) — so the margin is the more stable signal. CAVEAT: the threshold
// is NOT transferable (it lives on the embedder's cosine distribution) — calibrate per
// embedder on a probe set / the wall; the value below is a wall-calibrated start, not a
// guarantee. Applies to the COSINE path only (rerank scores are sigmoid-saturated, so
// their margin is unreliable — rerank stays floor-only). See the R4 confidence/abstention work in docs/WALL_EXPERIMENT.md.
func MarginFloor(model string) float64 {
	switch model {
	case "BAAI/bge-small-en-v1.5":
		return 0.05
	case "mock-bow":
		return 0.0 // lexical toy — don't flag on margin
	default:
		return 0.03
	}
}

// Confidence holds the per-embedder thresholds the confidence signals read. It is
// resolved by the CALLER (hardcoded defaults or per-embedder calibration) and passed to
// iocfmt.QueryOut, which therefore stays storage-free. Calibrated reports whether the
// floor came from per-embedder calibration (R4b) rather than a transferred constant —
// absolute cosine floors are NOT portable across embedders (arXiv:2403.05440).
type Confidence struct {
	Floor       float64 // cosine floor below which the top hit is weak (floor_miss)
	MarginFloor float64 // top1−top2 cosine margin below which the result is ambiguous
	RerankFloor float64 // rerank-score floor (the cosine path uses Floor)
	Calibrated  bool    // floors came from calibration, not a default constant
}

// DefaultConfidence returns the hardcoded per-embedder defaults (not calibrated).
func DefaultConfidence(model string) Confidence {
	return Confidence{
		Floor:       ConfidenceFloor(model),
		MarginFloor: MarginFloor(model),
		RerankFloor: RerankFloor(model),
		Calibrated:  false,
	}
}

// ResolveConfidence builds the thresholds for a model: per-embedder calibration written
// to config (keys conf.floor/conf.margin/conf.rerank "."+model, R4b) overrides the
// hardcoded DefaultConfidence. get is a config reader (engine.Config / runtime
// Client.Config), so this works over both the embedded engine and the daemon.
func ResolveConfidence(model string, get func(string) (string, bool)) Confidence {
	c := DefaultConfidence(model)
	if get == nil {
		return c
	}
	if v, ok := get("conf.floor." + model); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Floor = f
			c.Calibrated = true
		}
	}
	if v, ok := get("conf.margin." + model); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.MarginFloor = f
		}
	}
	if v, ok := get("conf.rerank." + model); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.RerankFloor = f
		}
	}
	return c
}

// Decision is the confidence verdict over a ranked hit list: which signal ordered the
// hits, the top score and top1−top2 margin on that signal, the confidence code, and the
// weak_match convenience flag. It is the single source of truth shared by the JSON
// formatter (iocfmt.QueryOut) and the eval/calibration harness, so production and the
// abstention metric judge a hit list IDENTICALLY.
type Decision struct {
	Confidence string  // "ok" | "floor_miss" | "margin_ambiguous" | "empty"
	WeakMatch  bool    // Confidence != "ok"
	RankedBy   string  // "cosine" | "rerank" — which signal is in force
	TopScore   float64 // top hit's score on the in-force signal
	Margin     float64 // top1−top2 on the in-force signal (0 if <2 comparable hits)
}

// Decide classifies a ranked hit list against the per-embedder Confidence thresholds.
// It reads the signal that ACTUALLY ordered the hits — the cross-encoder rerank score
// (vs RerankFloor) when present on the top hit, else cosine (vs Floor). weak_match has
// two independent causes surfaced via the confidence code: floor_miss = no confident
// match (→ do not answer); margin_ambiguous = a match exists but the top two are nearly
// tied (→ answer with stated uncertainty). The margin gate is COSINE-path only — rerank
// scores are sigmoid-saturated, so their margin is unreliable; floor_miss takes priority.
// Mixing signals (cosine margin over rerank-ordered hits) produces false confidence (the
// H3 bug), so RankedBy names the signal in force. See the R4 confidence/abstention work
// in docs/WALL_EXPERIMENT.md.
func Decide(hits []Hit, conf Confidence) Decision {
	reranked := len(hits) > 0 && hits[0].RerankScore != nil
	score := func(h Hit) float64 {
		if reranked && h.RerankScore != nil {
			return *h.RerankScore
		}
		return h.Score
	}
	var top, margin float64
	if len(hits) > 0 {
		top = score(hits[0])
	}
	// Only a margin between two hits ranked by the SAME signal is meaningful.
	marginValid := len(hits) > 1 && (!reranked || hits[1].RerankScore != nil)
	if marginValid {
		margin = score(hits[0]) - score(hits[1])
	}

	floor := conf.Floor
	rankedBy := "cosine"
	if reranked {
		floor = conf.RerankFloor
		rankedBy = "rerank"
	}

	marginAmbiguous := !reranked && marginValid && margin < conf.MarginFloor
	confidence := "ok"
	switch {
	case len(hits) == 0:
		confidence = "empty"
	case top < floor:
		confidence = "floor_miss"
	case marginAmbiguous:
		confidence = "margin_ambiguous"
	}
	return Decision{
		Confidence: confidence,
		WeakMatch:  confidence != "ok",
		RankedBy:   rankedBy,
		TopScore:   top,
		Margin:     margin,
	}
}

// Hit is one retrieval result. Content is populated only at DetailRaw.
type Hit struct {
	Artifact  ID           `json:"artifact"`
	Scope     ID           `json:"scope"`
	ScopePath string       `json:"scope_path"`
	Kind      ArtifactKind `json:"kind"`
	Tier      Tier         `json:"tier"`
	Summary   string       `json:"summary"`
	Score     float64      `json:"score"` // cosine similarity (the semantic-similarity signal), always
	// RerankScore is the cross-encoder relevance (sigmoid-normalized to [0,1]),
	// set only when the query was reranked. When present it — not Score — is the
	// signal that ordered the hits, so confidence (weak_match/margin) reads from it.
	RerankScore *float64          `json:"rerank_score,omitempty"`
	Meta        map[string]string `json:"meta,omitempty"`
	Content     []byte            `json:"content,omitempty"`
	// SupersededBy is set (non-zero) only on hits surfaced via IncludeSuperseded,
	// so the caller can see the result is stale and which artifact replaced it.
	SupersededBy ID `json:"superseded_by,omitempty"`
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
