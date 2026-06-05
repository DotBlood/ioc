// Package runtime is the IOC runtime layer: a long-lived daemon that owns a
// single store (one *engine.Engine) and serves multiple clients/agents over an
// internal framed-JSON RPC, plus a client that speaks the same protocol. CLI,
// MCP, and sub-agents become thin clients of one owner instead of fighting
// bbolt's exclusive lock. See docs/RUNTIME_ROADMAP.md.
package runtime

import (
	"context"

	"github.com/DotBlood/ioc/internal/core"
)

// Service is the operation set a caller needs, satisfied by BOTH the embedded
// *engine.Engine and the remote *Client — so cmd/ioc and cmd/ioc-mcp can depend
// on this interface and route to a daemon when one is running, else open the
// engine directly. Signatures mirror *engine.Engine exactly (ctx-less methods
// stay ctx-less) so the engine satisfies it without an adapter.
type Service interface {
	CreateScope(ctx context.Context, parent core.ID, role core.Role, title string) (core.Scope, error)
	Push(ctx context.Context, r core.PushRequest) (core.Artifact, error)
	Query(ctx context.Context, q core.Query) (core.ID, []core.Hit, error)
	Neighbors(ctx context.Context, scope core.ID, text string, k int) ([]core.Hit, error)
	Drill(ctx context.Context, artifactID core.ID, to core.Detail) (core.Hit, error)
	Publish(ctx context.Context, artifactID core.ID) error
	SiblingOverview(ctx context.Context, scope core.ID) ([]core.Hit, error)
	Ancestors(ctx context.Context, scope core.ID) ([]core.Scope, error)
	Fork(ctx context.Context, source core.ID, title string) (core.Scope, error)
	Consolidate(ctx context.Context, scope core.ID, summary string, supersedes []core.ID) (core.Artifact, error)
	CrossVersion(ctx context.Context, scope core.ID, seed core.Seed) (core.Scope, error)
	Supersede(ctx context.Context, old, replacement core.ID) error
	RollupScope(ctx context.Context, scope core.ID, summary string) error
	Trace(ctx context.Context, queryID core.ID) (core.TraceRecord, error)
	RecentTraces(n int) ([]core.TraceRecord, error)
	ListScopes(ctx context.Context) ([]core.Scope, error)
	GetScope(ctx context.Context, id core.ID) (core.Scope, error)
	ListArtifacts(ctx context.Context) ([]core.Artifact, error)
	DeleteArtifact(ctx context.Context, id core.ID) error
	DeleteScope(ctx context.Context, id core.ID) error
	Config(key string) (string, bool)
	SetConfig(key, val string) error
	EmbModel() string
	Close() error
}
