// Package pipeline orchestrates ingestion and archive workflows.
//
// Pipeline layer coordinates capabilities (CAS, artifact store, embedding,
// text indexing, scope resolution) without owning storage or graph internals.
// Pipelines are synchronous, deterministic, and content-idempotent.
package pipeline
