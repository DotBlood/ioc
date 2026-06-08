package engine

import (
	"fmt"
	"log/slog"
	"strconv"

	"github.com/DotBlood/ioc/internal/core"
)

// CurrentSchemaVersion is the store schema version that this build understands.
// Increment this constant and register a migration in migrations whenever a
// format change requires an upgrade step (or a refuse-to-open guard).
const CurrentSchemaVersion = 1

// cfgSchemaVersion is the config-bucket key that records the persisted schema
// version. It does NOT need to be in plaintextConfigKeys: checkSchemaVersion
// runs after checkEncryptionSentinel has validated the key, so GetConfig will
// decrypt it correctly on encrypted stores.
const cfgSchemaVersion = "schema_version"

// migration upgrades a store from version (to-1) to version to.
type migration struct {
	to  int
	run func(e *Engine) error
}

// migrations is the ordered list of upgrade steps applied in ascending to order.
// Empty for v1 — schema versioning is established now so future format changes
// register here instead of silently corrupting an existing store.
var migrations []migration

// migrate upgrades the store one version at a time from `from` to `to`, running
// the registered migration for EACH intermediate version and persisting the new
// version after each step. The chain must be CONTIGUOUS: a missing step (a gap
// in the registry) is a fail-closed error rather than a silent jump to `to` —
// otherwise a build with an incomplete registry could mark a store as current
// while skipping a required transform. With from == to (e.g. an empty registry
// at v1) the loop body never runs, so it is a clean no-op.
func (e *Engine) migrate(from, to int) error {
	for v := from + 1; v <= to; v++ {
		m, ok := migrationTo(v)
		if !ok {
			return fmt.Errorf("engine: %w: no migration registered for schema v%d (this build is v%d) — cannot upgrade store from v%d",
				core.ErrInvalidInput, v, CurrentSchemaVersion, from)
		}
		slog.Info("migrating store schema", "dir", e.dir, "to", v)
		if err := m.run(e); err != nil {
			return fmt.Errorf("engine: migrate to v%d: %w", v, err)
		}
		if err := e.meta.PutConfig(cfgSchemaVersion, strconv.Itoa(v)); err != nil {
			return fmt.Errorf("engine: migrate to v%d: persist version: %w", v, err)
		}
	}
	return nil
}

// migrationTo returns the registered migration that produces version v.
func migrationTo(v int) (migration, bool) {
	for _, m := range migrations {
		if m.to == v {
			return m, true
		}
	}
	return migration{}, false
}

// SchemaVersion returns the schema version recorded in the store config, or
// CurrentSchemaVersion if the key is absent or non-integer (which is the case
// for a correctly opened store — Open stamps the version on every start).
func (e *Engine) SchemaVersion() int {
	v, ok := e.meta.GetConfig(cfgSchemaVersion)
	if !ok {
		return CurrentSchemaVersion
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return CurrentSchemaVersion
	}
	return n
}

// checkSchemaVersion enforces the schema version contract at Open time, mirroring
// the style of checkEncryptionSentinel:
//
//   - Brand-new store (no version recorded, IsEmpty) → stamp current version.
//   - Legacy store (no version recorded, non-empty) → stamp baseline v1, then
//     migrate up to current. No data is wiped; the store is just annotated.
//   - Stored == current → ok, nothing to do.
//   - Stored < current → migrate step-by-step (migrate persists each version).
//   - Stored > current → refuse (fail-closed: the data may use a format this
//     build does not understand; upgrading ioc is the only safe path).
//   - Corrupt (non-integer) version → return an explicit error.
func (e *Engine) checkSchemaVersion() error {
	v, ok := e.meta.GetConfig(cfgSchemaVersion)
	if !ok {
		// No version recorded yet.
		if e.meta.IsEmpty() {
			// Brand-new store: stamp current and return — nothing to migrate.
			return e.meta.PutConfig(cfgSchemaVersion, strconv.Itoa(CurrentSchemaVersion))
		}
		// Legacy store that pre-dates schema versioning: stamp baseline v1 (no
		// wipe), then migrate up to current. When CurrentSchemaVersion == 1 the
		// baseline stamp already equals current and migrate is a no-op; when it is
		// higher, migrate persists each step (and fails closed on a registry gap).
		slog.Info("stamped baseline schema on legacy store", "dir", e.dir, "version", 1)
		if err := e.meta.PutConfig(cfgSchemaVersion, "1"); err != nil {
			return err
		}
		return e.migrate(1, CurrentSchemaVersion)
	}

	stored, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("engine: open %s: %w: corrupt schema_version %q", e.dir, core.ErrInvalidInput, v)
	}

	switch {
	case stored == CurrentSchemaVersion:
		return nil
	case stored < CurrentSchemaVersion:
		// migrate persists each intermediate version and the final one.
		return e.migrate(stored, CurrentSchemaVersion)
	default: // stored > CurrentSchemaVersion
		return fmt.Errorf("engine: open %s: %w: store schema v%d is newer than this build (v%d) — upgrade ioc",
			e.dir, core.ErrInvalidInput, stored, CurrentSchemaVersion)
	}
}
