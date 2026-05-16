package model

import (
	"errors"
	"time"
)

// Sentinel errors for temporal/historical operations.
var (
	ErrNoHistoricalState = errors.New("no historical state available at given time")
)

// EdgeValidAt checks if an edge was valid at time t.
// Edge is valid if Valid==true, t >= ValidFrom, and (ValidTo==nil or t < ValidTo).
func EdgeValidAt(e *Edge, t time.Time) bool {
	if !e.Valid {
		return false
	}
	if t.Before(e.ValidFrom) {
		return false
	}
	if e.ValidTo != nil && !t.Before(*e.ValidTo) {
		return false
	}
	return true
}

// ProjectionValidAt checks if a projection was valid at time t.
// Projection is valid if t >= ValidFrom and (ValidTo==nil or t < ValidTo).
func ProjectionValidAt(p *ArtifactProjection, t time.Time) bool {
	if t.Before(p.ValidFrom) {
		return false
	}
	if p.ValidTo != nil && !t.Before(*p.ValidTo) {
		return false
	}
	return true
}

// HistoricalScope is a reconstructed scope state at a point in time.
type HistoricalScope struct {
	ScopeID     ScopeID
	At          time.Time
	Artifacts   []*Artifact
	Projections []*ArtifactProjection
	Edges       []*Edge
	FromAnchor  AnchorID
}
