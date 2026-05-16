package session

import (
	"strings"
	"sync"
	"time"

	"github.com/DotBlood/ioc/internal/model"
	"github.com/DotBlood/ioc/internal/retrieval"
)

const defaultMaxHistory = 100

// SessionState is ephemeral conversational state for a single session.
//
// SessionState is NOT persisted, NOT part of the graph, and MUST NOT
// affect graph correctness, revision semantics, retrieval determinism,
// or archival behavior.
type SessionState struct {
	mu sync.RWMutex

	ActivePrompt   string
	PromptHistory  []string // bounded by maxHistory, oldest dropped first
	LastResults    []retrieval.SearchResult
	RetrievalTrace *model.RetrievalTrace
	ReasoningStack []string
	SessionCache   *SessionCache

	maxHistory int
}

// NewSessionState creates a SessionState with default settings.
func NewSessionState() *SessionState {
	return &SessionState{
		SessionCache: NewSessionCache(5 * time.Minute),
		maxHistory:   defaultMaxHistory,
	}
}

// NewSessionStateWithCache creates a SessionState with a specific cache.
// This is a DI hook for tests (fake ticker, disabled sweep, etc.).
func NewSessionStateWithCache(cache *SessionCache) *SessionState {
	return &SessionState{
		SessionCache: cache,
		maxHistory:   defaultMaxHistory,
	}
}

// PushReasoning appends a reasoning step to the stack.
// Empty strings are silently ignored.
func (s *SessionState) PushReasoning(step string) {
	if strings.TrimSpace(step) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReasoningStack = append(s.ReasoningStack, step)
}

// PopReasoning returns the most recent reasoning step (LIFO).
// Returns false if the stack is empty.
func (s *SessionState) PopReasoning() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ReasoningStack) == 0 {
		return "", false
	}
	last := s.ReasoningStack[len(s.ReasoningStack)-1]
	s.ReasoningStack = s.ReasoningStack[:len(s.ReasoningStack)-1]
	return last, true
}

// Clear resets all session state and replaces the cache.
// The old cache is closed after the new one is installed — no window
// with a closed cache.
func (s *SessionState) Clear() {
	s.mu.Lock()

	old := s.SessionCache
	s.SessionCache = NewSessionCache(5 * time.Minute)

	s.ActivePrompt = ""
	s.PromptHistory = nil
	s.LastResults = nil
	s.RetrievalTrace = nil
	s.ReasoningStack = nil

	s.mu.Unlock()

	old.Close()
}

// addToHistory appends a prompt to the history, dropping oldest entries
// when maxHistory is exceeded.
func (s *SessionState) addToHistory(prompt string) {
	s.PromptHistory = append(s.PromptHistory, prompt)
	if len(s.PromptHistory) > s.maxHistory {
		s.PromptHistory = s.PromptHistory[len(s.PromptHistory)-s.maxHistory:]
	}
}
