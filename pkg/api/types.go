package api

import "time"

// QueryResult is a single artifact retrieval result.
// ArtifactID is a stable ULID, Score is the fusion score (higher = more relevant).
type QueryResult struct {
	ArtifactID string
	Score      float64
	Summary    string
}

// TraceResult contains the full retrieval pipeline trace.
type TraceResult struct {
	Results  []QueryResult
	Stages   []TraceStage
	Errors   []string
	Duration time.Duration
}

// TraceStage describes one stage of the retrieval pipeline.
// Kind values for v0.1: "dense_search", "sparse_search", "rerank".
type TraceStage struct {
	Kind       string
	InputSize  int
	OutputSize int
	Duration   time.Duration
}

// ScopeInfo describes a scope and its current lifecycle state.
// CreatedAt is populated from ScopeState.CreatedAt (v0.1+).
type ScopeInfo struct {
	ScopeID   string
	Type      string
	State     string
	ParentID  string
	CreatedAt time.Time
}

// CreateScopeRequest specifies the scope to create.
// For worktree — ParentID must be empty.
// For workspace/session — ParentID must reference a valid parent scope.
type CreateScopeRequest struct {
	Type     string
	ParentID string
}
