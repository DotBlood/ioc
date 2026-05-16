package session

import (
	"sync"
	"testing"
	"time"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/retrieval"
)

func TestSessionCache_TTL(t *testing.T) {
	cache := NewSessionCache(50 * time.Millisecond)
	defer cache.Close()

	id := model.NewID()
	cache.CachePut(id, "test-value", 30*time.Millisecond)

	// Immediate get succeeds.
	val, ok := cache.CacheGet(id)
	if !ok {
		t.Fatal("expected immediate get to succeed")
	}
	if val != "test-value" {
		t.Errorf("expected 'test-value', got %v", val)
	}

	// Wait for TTL + sweep.
	time.Sleep(100 * time.Millisecond)

	val, ok = cache.CacheGet(id)
	if ok {
		t.Error("expected expired entry to return false")
	}
	if val != nil {
		t.Errorf("expected nil value for expired entry, got %v", val)
	}
}

func TestSessionCache_Close(t *testing.T) {
	cache := NewSessionCache(50 * time.Millisecond)

	// Close is idempotent.
	cache.Close()
	cache.Close()

	// Put after Close is no-op.
	id := model.NewID()
	cache.CachePut(id, "value", time.Hour)
	_, ok := cache.CacheGet(id)
	if ok {
		t.Error("cache should reject puts after close")
	}
}

func TestSessionCache_ConcurrentAccess(t *testing.T) {
	cache := NewSessionCache(time.Hour) // no sweep during test
	defer cache.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := model.NewID()
			cache.CachePut(id, n, time.Hour)
			val, ok := cache.CacheGet(id)
			if ok {
				_ = val.(int)
			}
		}(i)
	}
	wg.Wait()
}

func TestSessionState_ReasoningStack(t *testing.T) {
	s := NewSessionState()

	// Empty stack.
	step, ok := s.PopReasoning()
	if ok {
		t.Error("expected false from empty stack")
	}
	if step != "" {
		t.Errorf("expected empty string, got %q", step)
	}

	// LIFO order.
	s.PushReasoning("first")
	s.PushReasoning("second")
	s.PushReasoning("third")

	step, ok = s.PopReasoning()
	if !ok || step != "third" {
		t.Errorf("expected 'third', got %q (ok=%v)", step, ok)
	}

	step, ok = s.PopReasoning()
	if !ok || step != "second" {
		t.Errorf("expected 'second', got %q (ok=%v)", step, ok)
	}

	step, ok = s.PopReasoning()
	if !ok || step != "first" {
		t.Errorf("expected 'first', got %q (ok=%v)", step, ok)
	}

	// Empty again after pops.
	_, ok = s.PopReasoning()
	if ok {
		t.Error("expected false after draining stack")
	}

	// Empty strings are ignored.
	s.PushReasoning("")
	s.PushReasoning("   ")
	_, ok = s.PopReasoning()
	if ok {
		t.Error("expected false after pushing empty strings")
	}
}

func TestSessionState_Clear(t *testing.T) {
	s := NewSessionState()

	s.ActivePrompt = "test-query"
	s.PromptHistory = []string{"q1", "q2"}
	s.LastResults = []retrieval.SearchResult{
		{ID: model.NewID(), Score: 0.9},
	}
	s.ReasoningStack = []string{"step1", "step2"}

	oldCache := s.SessionCache
	id := model.NewID()
	oldCache.CachePut(id, "pre-clear", time.Hour)

	s.Clear()

	// All fields reset.
	if s.ActivePrompt != "" {
		t.Errorf("expected empty ActivePrompt, got %q", s.ActivePrompt)
	}
	if s.PromptHistory != nil {
		t.Errorf("expected nil PromptHistory, got %v", s.PromptHistory)
	}
	if s.LastResults != nil {
		t.Errorf("expected nil LastResults, got %v", s.LastResults)
	}
	if s.ReasoningStack != nil {
		t.Errorf("expected nil ReasoningStack, got %v", s.ReasoningStack)
	}

	// New cache is functional.
	if s.SessionCache == nil {
		t.Fatal("SessionCache must not be nil after Clear")
	}
	if s.SessionCache == oldCache {
		t.Error("SessionCache must be replaced, not the same instance")
	}

	newID := model.NewID()
	s.SessionCache.CachePut(newID, "post-clear", time.Hour)
	val, ok := s.SessionCache.CacheGet(newID)
	if !ok {
		t.Error("new cache should accept puts")
	}
	if val != "post-clear" {
		t.Errorf("expected 'post-clear', got %v", val)
	}

	// Old cache is closed — puts are no-ops.
	orphanID := model.NewID()
	oldCache.CachePut(orphanID, "after-close", time.Hour)
	_, ok = oldCache.CacheGet(orphanID)
	if ok {
		t.Error("old cache must reject puts after Close")
	}
}
