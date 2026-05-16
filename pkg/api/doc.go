// Package api provides an embeddable IOC runtime for programmatic use.
//
// Entry point:
//
//	rt, err := api.Open(ctx, api.Config{RootDir: "/path/to/.ioc"})
//	if err != nil { log.Fatal(err) }
//	defer rt.Close()
//
// Runtime exposes three operation groups:
//   - Retrieval: Query, Trace — concurrent-safe in v0.1
//   - Admin: CreateScope, ListScopes, ArchiveScope, RestoreScope, AddArtifact
//     — NOT concurrent-safe, callers must serialize mutations
//   - State: ScopeState, Close
//
// All public types are in the api package — no internal types leak.
package api
