package runtime

import (
	"context"

	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// Open returns a Service for the store at dir: a *Client when a daemon currently
// owns dir (reachable via runtime.json), otherwise an embedded *engine.Engine
// opened with the given embedder/opts. The caller builds embedder/opts; when a
// daemon is reached they go unused (the daemon owns the real embedder). The
// returned Service must be Closed by the caller (Client.Close disconnects;
// engine.Close closes the store).
func Open(dir string, embedder embed.Embedder, opts ...engine.Option) (Service, error) {
	if c, err := Dial(dir); err == nil {
		return c, nil
	}
	return engine.Open(context.Background(), dir, embedder, opts...)
}

// Info returns the published descriptor of the daemon owning dir. An
// os.IsNotExist error means no daemon has claimed this dir.
func Info(dir string) (RuntimeInfo, error) { return readRuntimeInfo(dir) }

// Shutdown asks the daemon owning dir to stop gracefully (no-op-style error if
// no daemon is reachable).
func Shutdown(dir string) error {
	c, err := Dial(dir)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Shutdown()
}
