package graph

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// WriteHandle is a scope-scoped write lease. Must be released via Done().
type WriteHandle struct {
	scopeID model.ScopeID
	g       *StatefulGraph
	done    bool
}

// Done releases the write lease. Safe to call multiple times.
func (h *WriteHandle) Done() {
	if h.done {
		return
	}
	h.done = true
	h.g.endWrite(h.scopeID)
}

// scopeLock serializes writes to a scope and supports freeze for archival.
type scopeLock struct {
	mu       sync.RWMutex
	freezing atomic.Bool
	pending  atomic.Int32
}

// freezeScope initiates a scope freeze:
//  1. Sets the freezing flag (blocks new writes)
//  2. Drains pending writes
//  3. Returns when scope is safe to snapshot
//
// Returns ErrScopeFrozen if the scope is already frozen.
// Must be paired with UnfreezeScope.
// TODO(v0.2): Add freeze lease / watchdog timeout to prevent permanent frozen scopes.
func (g *StatefulGraph) freezeScope(scopeID model.ScopeID) error {
	lock := g.getOrCreateLock(scopeID)
	lock.mu.Lock()

	if lock.freezing.Load() {
		lock.mu.Unlock()
		return model.ErrScopeFrozen
	}

	lock.freezing.Store(true)
	for lock.pending.Load() > 0 {
		lock.mu.Unlock()
		time.Sleep(time.Millisecond) // TODO(v0.2): replace with sync.Cond
		lock.mu.Lock()
	}
	lock.mu.Unlock()
	return nil
}

// unfreezeScope re-enables writes to a previously frozen scope.
func (g *StatefulGraph) unfreezeScope(scopeID model.ScopeID) {
	lock := g.getOrCreateLock(scopeID)
	lock.freezing.Store(false)
}

// beginWrite acquires a write lease for the given scope.
// Returns ErrReadOnly if the scope is being frozen.
func (g *StatefulGraph) beginWrite(scopeID model.ScopeID) (*WriteHandle, error) {
	lock := g.getOrCreateLock(scopeID)
	// ReadLock blocks during freeze — гарантирует отсутствие TOCTOU.
	lock.mu.RLock()
	if lock.freezing.Load() {
		lock.mu.RUnlock()
		return nil, model.ErrReadOnly
	}
	lock.pending.Add(1)
	lock.mu.RUnlock()
	return &WriteHandle{scopeID: scopeID, g: g}, nil
}

// endWrite releases a write lease. Called automatically by WriteHandle.Done().
func (g *StatefulGraph) endWrite(scopeID model.ScopeID) {
	lock, ok := g.scopeLocks[scopeID]
	if !ok {
		return
	}
	lock.pending.Add(-1)
}

// getOrCreateLock lazily creates a scope lock.
// Thread-safe — защищён отдельным scopeLocksMu.
func (g *StatefulGraph) getOrCreateLock(scopeID model.ScopeID) *scopeLock {
	g.scopeLocksMu.RLock()
	lock, ok := g.scopeLocks[scopeID]
	g.scopeLocksMu.RUnlock()
	if ok {
		return lock
	}

	g.scopeLocksMu.Lock()
	defer g.scopeLocksMu.Unlock()

	// Double-check после upgrade.
	if lock, ok := g.scopeLocks[scopeID]; ok {
		return lock
	}

	lock = &scopeLock{}
	g.scopeLocks[scopeID] = lock
	return lock
}
