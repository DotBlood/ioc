package model

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors.
var (
	ErrNotFound         = errors.New("entity not found")
	ErrInvalidState     = errors.New("invalid lifecycle state")
	ErrCycleDetected    = errors.New("cycle detected in lineage")
	ErrReadOnly         = errors.New("entity is read-only")
	ErrDanglingRef      = errors.New("reference target not found")
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrDuplicate        = errors.New("entity already exists")
	ErrInvalidTransition = errors.New("invalid lifecycle transition")
	ErrNotImplemented   = errors.New("not implemented")
)

// RetrieveError wraps a non-fatal error during retrieval.
type RetrieveError struct {
	Stage   string
	Message string
	Err     error
}

func (e *RetrieveError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Stage, e.Message, e.Err)
}

func (e *RetrieveError) Unwrap() error {
	return e.Err
}

// ScoredNode is a graph node with a retrieval score.
type ScoredNode struct {
	ID    ID
	Score Score
}

// Score combines multiple factors into a final relevance score.
type Score struct {
	CosineSimilarity float64
	ScopePriority    float64
	Recency          float64
	IsActive         float64
	LineageDepth     float64
	DanglingPenalty  float64
	Final            float64
}

// Compute computes the final weighted score.
func (s *Score) Compute() {
	s.Final = 0.5*s.CosineSimilarity +
		0.2*s.ScopePriority +
		0.15*s.Recency +
		0.1*s.IsActive +
		0.05*s.LineageDepth -
		s.DanglingPenalty
}

// TraversalOpts controls how graph traversal behaves.
type TraversalOpts struct {
	ActiveOnly  bool
	EdgeTypes   []EdgeType
	MaxDepth    int
	ScopeFilter *ScopeFilter
	TimeFilter  *TimeFilter
	CyclePolicy CyclePolicy
}

// TimeFilter restricts traversal to a temporal state.
type TimeFilter struct {
	At       *time.Time
	From     *time.Time
	To       *time.Time
	Revision *RevisionNumber
}

// ScopeFilter controls which scopes are included.
type ScopeFilter struct {
	IncludeArchived bool
	IncludeDetached bool
	MaxDepth        int
}

// DanglingConfig controls behavior when dangling references are encountered.
type DanglingConfig struct {
	Penalty         float64
	TraversalAction TraversalAction
}

// TraversalAction specifies what to do on a dangling reference.
type TraversalAction uint8

const (
	TraversalSkip TraversalAction = 1
	TraversalWarn TraversalAction = 2
)

// RetrievalTrace logs the full retrieval pipeline for debugging.
type RetrievalTrace struct {
	QueryHash      ContentHash
	GraphSnapshot  GraphSnapshotID
	ScopesSearched []ScopeID
	Candidates     []ScoredNode
	EdgesTraversed []EdgeID
	RerankScores   map[ID]Score
	FinalSelection []ID
	Stages         []RetrievalStage
	Duration       time.Duration
	Errors         []string
}

// RetrievalStage logs a single stage of the retrieval pipeline.
type RetrievalStage struct {
	Name       string
	InputSize  int
	OutputSize int
	Duration   time.Duration
}

// GraphSnapshotID uniquely identifies a consistent graph snapshot.
type GraphSnapshotID struct {
	ProjectionMaxRev RevisionNumber
	EdgeHash         ContentHash
	ScopeStateHash   ContentHash
	Timestamp        time.Time
}

// ModelVersion identifies an embedding model.
type ModelVersion struct {
	Name    string
	Version string
	Dims    int
}

// DeterminismBoundary defines the context within which retrieval is deterministic.
type DeterminismBoundary struct {
	GraphSnapshot   GraphSnapshotID
	EmbeddingModel  ModelVersion
	RetrievalConfig RetrievalConfigHash
}

// RetrievalConfigHash is a content hash of the retrieval configuration.
type RetrievalConfigHash ContentHash

// RetrievalOpts configures a retrieval query.
type RetrievalOpts struct {
	TopKCoarse     int
	TopKFine       int
	TopKChunk      int
	CoarseEFSearch int
	FineEFSearch   int
	IncludeTrace   bool
	ScopeFilter    *ScopeFilter
	DanglingConfig *DanglingConfig
	TokenBudget    int
}
