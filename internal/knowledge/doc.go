// Package knowledge implements the Knowledge Runtime Layer — cognition semantics
// that sit above the physical graph engine and below pipelines/runtime.
//
// This layer is responsible for:
//   - scope resolution and worktree hierarchy
//   - Revision DAG semantics and branching rules
//   - lifecycle transitions and invariant enforcement
//   - lineage rules and provenance tracking
//   - context assembly (workspace-aware retrieval orchestration)
//   - archive/retrieval policy enforcement
//
// knowledge/ does NOT store data — it reads/writes through the physical graph.
package knowledge
