package knowledge

import (
	"context"
	"sort"
	"time"

	"github.com/DotBlood/ioc/internal/model"
)

// ContextAssembler constructs a retrieval context from a user query.
// It orchestrates scope resolution → collection → filtering → assembly.
//
// This is NOT a search engine. It delegates search to retrieval/ and
// focuses on: where to look, what to include, how to order, and
// what to filter out based on scope/branch/lifecycle semantics.
type ContextAssembler struct {
	scope    *ScopeResolver
	revision *RevisionManager
	lifecycle *LifecycleManager
	reader   ContextStoreReader
}

// NewContextAssembler creates a new context assembler.
func NewContextAssembler(
	scope *ScopeResolver,
	revision *RevisionManager,
	lifecycle *LifecycleManager,
	reader ContextStoreReader,
) *ContextAssembler {
	return &ContextAssembler{
		scope:    scope,
		revision: revision,
		lifecycle: lifecycle,
		reader:   reader,
	}
}

// AssemblyPlan describes where and how to search for context.
type AssemblyPlan struct {
	PrimaryScope   model.ScopeID   // the main scope to search
	SecondaryScopes []model.ScopeID // parent scopes (for broader context)
	InheritFrom     []model.ScopeID // worktree-level conclusions
	MaxDepth        int
	TimeWindow      *TimeWindow
	TokenBudget     int
}

// TimeWindow restricts context to a temporal range.
type TimeWindow struct {
	From time.Time
	To   time.Time
}

// AssemblyResult is the assembled context ready for LLM consumption.
type AssemblyResult struct {
	PrimaryBlocks  []ContextBlock
	SecondaryBlocks []ContextBlock
	SummaryBlocks  []ContextBlock
	TokenCount     int
	AssembledAt    time.Time
}

// ContextBlock is a single unit of context for the LLM.
type ContextBlock struct {
	SourceID    model.ID
	Scope       model.ScopeID
	Summary     string
	RawContent  string
	TokenCount  int
	Priority    float64
	BlockType   BlockType
}

// BlockType categorises context blocks.
type BlockType uint8

const (
	BlockArtifact      BlockType = 1
	BlockSummary       BlockType = 2
	BlockWorktreeState BlockType = 3
	BlockConversation  BlockType = 4
)

// Plan creates a search plan from a query and active scope.
func (a *ContextAssembler) Plan(ctx context.Context, query string, activeScope model.ScopeID, opts model.RetrievalOpts) (*AssemblyPlan, error) {
	plan := &AssemblyPlan{
		PrimaryScope:   activeScope,
		SecondaryScopes: nil,
		MaxDepth:        opts.ScopeFilter.MaxDepth,
		TokenBudget:     opts.TokenBudget,
	}

	// If scope is a session, include its parent workspace for broader context.
	if a.isSessionScope(activeScope) {
		parent := a.parentScope(activeScope)
		if parent != "" {
			plan.SecondaryScopes = append(plan.SecondaryScopes, parent)
		}
	}

	return plan, nil
}

// Assemble runs the assembly plan and produces the final context.
func (a *ContextAssembler) Assemble(ctx context.Context, plan *AssemblyPlan, candidates []model.ScoredNode) (*AssemblyResult, error) {
	result := &AssemblyResult{
		AssembledAt: time.Now(),
	}

	// Split candidates by scope.
	for _, c := range candidates {
		scope, err := a.reader.NodeScope(ctx, c.ID)
		if err != nil {
			continue
		}
		isPrimary, _ := a.scope.IsVisibleFrom(ctx, scope, plan.PrimaryScope)

		block := ContextBlock{
			SourceID:   c.ID,
			Scope:      scope,
			Priority:   c.Score.Final,
			TokenCount: estimateTokens(256), // placeholder
			BlockType:  BlockArtifact,
		}

		if isPrimary {
			result.PrimaryBlocks = append(result.PrimaryBlocks, block)
		} else {
			result.SecondaryBlocks = append(result.SecondaryBlocks, block)
		}
	}

	// Sort by priority.
	sort.Slice(result.PrimaryBlocks, func(i, j int) bool {
		return result.PrimaryBlocks[i].Priority > result.PrimaryBlocks[j].Priority
	})
	sort.Slice(result.SecondaryBlocks, func(i, j int) bool {
		return result.SecondaryBlocks[i].Priority > result.SecondaryBlocks[j].Priority
	})

	// Apply token budget: trim from the end if over budget.
	result.TokenCount = countTokens(result.PrimaryBlocks) + countTokens(result.SecondaryBlocks)
	if plan.TokenBudget > 0 && result.TokenCount > plan.TokenBudget {
		result = a.trimToBudget(result, plan.TokenBudget)
	}

	return result, nil
}

func (a *ContextAssembler) isSessionScope(scope model.ScopeID) bool {
	// Heuristic: session scopes have 3+ parts (worktree:workspace:session).
	parts := countParts(string(scope))
	return parts >= 3
}

func (a *ContextAssembler) parentScope(scope model.ScopeID) model.ScopeID {
	s := string(scope)
	idx := lastColon(s)
	if idx < 0 {
		return ""
	}
	return model.ScopeID(s[:idx])
}

func (a *ContextAssembler) trimToBudget(result *AssemblyResult, budget int) *AssemblyResult {
	var trimmed AssemblyResult
	trimmed.AssembledAt = result.AssembledAt

	for _, block := range result.PrimaryBlocks {
		if trimmed.TokenCount+block.TokenCount > budget {
			break
		}
		trimmed.PrimaryBlocks = append(trimmed.PrimaryBlocks, block)
		trimmed.TokenCount += block.TokenCount
	}

	remaining := budget - trimmed.TokenCount
	for _, block := range result.SecondaryBlocks {
		if trimmed.TokenCount+block.TokenCount > remaining {
			break
		}
		trimmed.SecondaryBlocks = append(trimmed.SecondaryBlocks, block)
		trimmed.TokenCount += block.TokenCount
	}

	return &trimmed
}

func estimateTokens(words int) int {
	return words * 2 // rough estimate
}

func countTokens(blocks []ContextBlock) int {
	total := 0
	for _, b := range blocks {
		total += b.TokenCount
	}
	return total
}

func countParts(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, c := range s {
		if c == ':' {
			n++
		}
	}
	return n
}

// ContextStoreReader is the minimal interface knowledge/ uses for assembly.
type ContextStoreReader interface {
	NodeScope(ctx context.Context, id model.ID) (model.ScopeID, error)
}
