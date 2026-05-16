package graph

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

func TestScopeLock_RaceFree(t *testing.T) {
	g := NewStatefulGraph()
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		scopeID := model.ScopeID(fmt.Sprintf("scope:%d", i))
		wg.Add(1)
		go func(sid model.ScopeID) {
			defer wg.Done()
			h, err := g.beginWrite(sid)
			if err != nil {
				return
			}
			h.Done()
		}(scopeID)
	}
	wg.Wait()
}

func TestScopeLock_FreezeBlocksWrites(t *testing.T) {
	g := NewStatefulGraph()
	scopeID := model.ScopeID("freeze-test")

	// Acquire two write leases.
	h1, _ := g.beginWrite(scopeID)
	h2, _ := g.beginWrite(scopeID)

	// Freeze in background. Use channel to confirm freeze has started.
	started := make(chan struct{})
	go func() {
		close(started)
		g.freezeScope(scopeID)
	}()
	<-started

	// Allow freeze goroutine to progress.
	time.Sleep(time.Millisecond)

	// beginWrite during freeze should fail.
	h, err := g.beginWrite(scopeID)
	if err == nil {
		h.Done()
		t.Error("beginWrite during freeze: expected ErrReadOnly")
	}

	// Release both leases — freeze completes.
	h1.Done()
	h2.Done()
}

func TestScopeLock_FreezeUnfreezeCycle(t *testing.T) {
	g := NewStatefulGraph()
	scopeID := model.ScopeID("cycle-test")

	g.freezeScope(scopeID)

	_, err := g.beginWrite(scopeID)
	if err == nil {
		t.Error("beginWrite during freeze: expected error")
	}

	g.unfreezeScope(scopeID)
	h, err := g.beginWrite(scopeID)
	if err != nil {
		t.Errorf("beginWrite after unfreeze: %v", err)
	}
	h.Done()
}

func TestScopeLock_ConcurrentFreezeAndWrite(t *testing.T) {
	g := NewStatefulGraph()
	scopeID := model.ScopeID("concurrent")
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				g.freezeScope(scopeID)
				g.unfreezeScope(scopeID)
			} else {
				h, err := g.beginWrite(scopeID)
				if err == nil {
					h.Done()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestWriteHandle_DoubleDone(t *testing.T) {
	g := NewStatefulGraph()
	h, err := g.beginWrite("safe-test")
	if err != nil {
		t.Fatalf("beginWrite: %v", err)
	}
	h.Done()
	h.Done()
}
