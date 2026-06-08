package ingest

import (
	"context"

	"github.com/DotBlood/ioc/internal/core"
)

// Store is the subset of engine operations ingestion needs. Both *engine.Engine
// and a runtime client satisfy it, so ingestion runs embedded or through the
// daemon without ingest depending on either package.
type Store interface {
	CreateScope(ctx context.Context, parent core.ID, role core.Role, title string) (core.Scope, error)
	Push(ctx context.Context, r core.PushRequest) (core.Artifact, error)
	RollupScope(ctx context.Context, scope core.ID, summary string) error
	DeleteArtifact(ctx context.Context, id core.ID) error
	DeleteScope(ctx context.Context, id core.ID) error
	ListScopes(ctx context.Context) ([]core.Scope, error)
	ListArtifacts(ctx context.Context) ([]core.Artifact, error)
	GetScope(ctx context.Context, id core.ID) (core.Scope, error)
	Config(key string) (string, bool)
	SetConfig(key, val string) error
}
