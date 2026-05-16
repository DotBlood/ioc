// Package store implements persistent storage for IOC.
//
// It provides four storage backends:
//   - DiskStore (bbolt) for graph state (nodes, edges, projections, indexes)
//   - CAS for content-addressable file storage (SHA-256, zstd, chunked)
//   - EmbeddingStore (mmap) for fixed-size embedding vectors
//   - ArtifactStore / ProjectionStore / RevisionDAG on top of DiskStore
package store
