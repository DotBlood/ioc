package runtime

import (
	"context"
	"fmt"
	"os"

	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// Open returns a Service for the store at dir: a *Client when a daemon currently
// owns dir (reachable via runtime.json), otherwise an embedded *engine.Engine
// opened with the given embedder/opts. The caller builds embedder/opts; when a
// daemon is reached they go unused (the daemon owns the real embedder). The
// returned Service must be Closed by the caller (Client.Close disconnects;
// engine.Close closes the store).
//
// Dial errors are classified rather than uniformly treated as "no daemon": only a
// genuinely absent or dead daemon falls back to an embedded engine. A daemon that
// is reachable but rejected the dial for another reason (e.g. an mTLS handshake
// failure) is surfaced — silently falling back would open a SECOND engine on a dir
// a live daemon already owns and fail with an opaque bbolt lock error instead of
// the real cause.
func Open(dir string, embedder embed.Embedder, opts ...engine.Option) (Service, error) {
	c, err := Dial(dir)
	if err == nil {
		return c, nil
	}
	switch {
	case os.IsNotExist(err):
		// No runtime.json → no daemon claims this dir → embedded.
	case dialRefused(err):
		// runtime.json present but the socket is dead (stale file / daemon gone) →
		// embedded. The stale file self-heals: a new daemon overwrites it (atomically),
		// a clean Stop removes it; Open does not delete it as a read-path side effect.
	default:
		// Reached the daemon but the dial failed otherwise (mTLS handshake, or a
		// genuinely corrupt runtime.json now that writes are atomic). Do NOT fall
		// back onto the daemon's own dir; report the real cause.
		return nil, fmt.Errorf("runtime: cannot reach daemon for %s: %w", dir, err)
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
