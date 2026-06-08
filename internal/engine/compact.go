package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/storage"
)

const (
	defaultEmbFile = "emb.dat"  // live embedding file for stores predating compaction
	cfgEmbFile     = "emb_file" // config key: current live embedding filename
	cfgEmbGen      = "emb_gen"  // config key: monotonic compaction generation counter
)

// currentEmbFile returns the live embedding-store filename. Stores created
// before compaction existed have no emb_file config and default to emb.dat.
func (e *Engine) currentEmbFile() string {
	if v, ok := e.meta.GetConfig(cfgEmbFile); ok && v != "" {
		return v
	}
	return defaultEmbFile
}

// isEmbFile reports whether name is an embedding-store file (emb.dat or
// emb.<n>.dat) — the set compaction creates and rotates.
func isEmbFile(name string) bool {
	return strings.HasPrefix(name, "emb.") && strings.HasSuffix(name, ".dat")
}

// gcOrphanEmbFiles removes stale embedding-store files left by a previous or
// crashed compaction — any emb.dat / emb.<n>.dat that is not the current live
// file. Orphans are dead weight (never read; the live file is named in config),
// so a removal failure is logged, not fatal. Safe because the store is single-
// owner (bbolt's exclusive lock) for the duration it is open.
func (e *Engine) gcOrphanEmbFiles(current string) {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if name == current || !isEmbFile(name) {
			continue
		}
		if err := os.Remove(filepath.Join(e.dir, name)); err != nil {
			slog.Warn("compact: could not remove orphan embedding file", "file", name, "err", err)
		}
	}
}

// Compact reclaims orphaned embeddings. The embedding store is append-only, so a
// vector whose artifact was deleted or re-chunked (e.g. by re-ingesting changed
// files) lingers as dead weight. Compact writes a fresh embedding file holding
// ONLY live vectors (those still referenced by an artifact's EmbRef or a scope's
// RollupEmbRef), then atomically switches the store to it.
//
// Crash-safety: the new file is fully written and fsynced FIRST; then a SINGLE
// bbolt transaction both remaps every EmbRef/RollupEmbRef and flips the emb_file
// pointer (Meta.RemapEmbeddings). bbolt's commit is the only switch point, so a
// crash leaves either the old file authoritative (commit didn't land) or the new
// one (it did) — never a torn mix. A half-written new file is an orphan, swept by
// gcOrphanEmbFiles on the next Open.
//
// Compact requires exclusive access (no concurrent writes): the runtime serves it
// as a write-tier op under its write lock, and an embedded caller is single-
// process. It is a no-op when nothing is embedded or the store is already dense.
func (e *Engine) Compact(_ context.Context) (core.CompactStats, error) {
	var stats core.CompactStats
	if e.emb == nil {
		return stats, nil // nothing embedded in this store
	}

	dims := e.emb.Dims()
	oldPath := e.emb.Path()
	oldCount := e.emb.Len()
	stats.OldRecords = oldCount
	if fi, serr := os.Stat(oldPath); serr == nil {
		stats.OldBytes = fi.Size()
	}

	// Collect the set of live refs (artifacts' EmbRef + scopes' RollupEmbRef).
	live := map[core.EmbeddingRef]struct{}{}
	arts, err := e.meta.ListArtifacts()
	if err != nil {
		return stats, err
	}
	for _, a := range arts {
		if a.EmbRef != 0 {
			live[a.EmbRef] = struct{}{}
		}
	}
	scopes, err := e.meta.ListScopes()
	if err != nil {
		return stats, err
	}
	for _, s := range scopes {
		if s.RollupEmbRef != 0 {
			live[s.RollupEmbRef] = struct{}{}
		}
	}
	stats.LiveRecords = len(live)

	// Already dense: every stored record is referenced, so refs are 1..N with no
	// gaps. Nothing to reclaim.
	if len(live) == oldCount {
		stats.NewBytes = stats.OldBytes
		return stats, nil
	}

	// Deterministic order: ascending old ref → sequential new refs 1..N.
	sorted := make([]core.EmbeddingRef, 0, len(live))
	for r := range live {
		sorted = append(sorted, r)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	// Next generation filename (monotonic counter persisted in config).
	gen := 0
	if v, ok := e.meta.GetConfig(cfgEmbGen); ok {
		if n, cerr := strconv.Atoi(v); cerr == nil {
			gen = n
		}
	}
	gen++
	newName := fmt.Sprintf("emb.%d.dat", gen)
	newPath := filepath.Join(e.dir, newName)

	// Build the new file: copy each live vector across (decrypt-old → encrypt-new
	// happens inside Get/Put, which re-seal under the new ref's AAD).
	newStore, err := storage.OpenEmbeddingStore(newPath, dims, e.box)
	if err != nil {
		return stats, err
	}
	abort := func(cause error) (core.CompactStats, error) {
		_ = newStore.Close()
		_ = os.Remove(newPath)
		return stats, cause
	}
	remap := make(map[core.EmbeddingRef]core.EmbeddingRef, len(sorted))
	for _, oldRef := range sorted {
		vec, gerr := e.emb.Get(oldRef)
		if gerr != nil {
			return abort(fmt.Errorf("engine: compact: read ref %d: %w", oldRef, gerr))
		}
		newRef, perr := newStore.Put(vec)
		if perr != nil {
			return abort(fmt.Errorf("engine: compact: write ref %d: %w", oldRef, perr))
		}
		remap[oldRef] = newRef
	}
	if serr := newStore.Sync(); serr != nil {
		return abort(fmt.Errorf("engine: compact: sync new file: %w", serr))
	}

	// Atomic switch: remap all refs + flip emb_file/emb_gen in one bbolt txn.
	nA, nS, rerr := e.meta.RemapEmbeddings(remap, map[string]string{
		cfgEmbFile: newName,
		cfgEmbGen:  strconv.Itoa(gen),
	})
	if rerr != nil {
		return abort(fmt.Errorf("engine: compact: remap: %w", rerr))
	}
	stats.Artifacts = nA
	stats.Scopes = nS

	// Commit landed: the new file is now authoritative. Swap the in-memory store
	// and drop the old file. Errors past this point are non-fatal (the store is
	// already consistent on disk; a leftover old file is swept on next Open).
	if cerr := e.emb.Close(); cerr != nil {
		slog.Warn("compact: closing old embedding store failed", "err", cerr)
	}
	e.emb = newStore
	if rmErr := os.Remove(oldPath); rmErr != nil {
		slog.Warn("compact: could not remove old embedding file", "file", oldPath, "err", rmErr)
	}

	if fi, serr := os.Stat(newPath); serr == nil {
		stats.NewBytes = fi.Size()
	}
	stats.Reclaimed = oldCount - len(live)
	stats.ReclaimedBytes = stats.OldBytes - stats.NewBytes
	slog.Info("compacted embedding store",
		"old_records", stats.OldRecords, "live_records", stats.LiveRecords,
		"reclaimed", stats.Reclaimed, "artifacts_rewritten", nA, "scopes_rewritten", nS)
	return stats, nil
}
