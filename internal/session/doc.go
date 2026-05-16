// Package session manages ephemeral conversational state.
//
// Session state is process-local, non-persistent, and outside
// the deterministic graph model. Session data MUST NOT affect:
//   - graph correctness
//   - revision semantics
//   - retrieval determinism
//   - archival or snapshot behavior
//
// Session values are NOT serialized and MUST NOT contain
// graph-owned mutable state (*Artifact, *StatefulGraph, etc.).
package session
