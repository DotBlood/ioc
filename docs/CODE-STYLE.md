# CODE-STYLE

## IOC Go Implementation Guide

---

## 1. Language

- **Go 1.26+** (use `slog`, `maps`, `slices`, `cmp`, `math/rand/v2`)
- No CGO unless absolutely required (CAS/embedding store: no CGO)
- No reflection in hot paths

---

## 2. Naming Conventions

### 2.1 Packages
- Single word, lowercase: `graph`, `store`, `model`, `retrieval`
- No plural forms: `store`, not `stores`
- Avoid `util`, `common`, `misc`
- Package name matches directory name

### 2.2 Types
- PascalCase for exported: `StatefulGraph`, `ArtifactID`, `RevisionDAG`
- No stutter: `graph.StatefulGraph` ✓, not `graph.Graph`
- Interfaces: suffix `-er` or `-able`: `Embedder`, `Storer`

### 2.3 Functions/Methods
- PascalCase exported: `NewStatefulGraph`, `TraverseScope`
- camelCase private: `buildIndex`, `computeScore`
- No `Get` prefix for accessors (Go convention):
  - `graph.GetNode(id)` → `graph.Node(id)` (preferred)
  - Only `Get` if it's expensive/lazy

### 2.4 Variables
- camelCase for locals
- Short names for small scopes: `ctx`, `g`, `s`, `n`
- Meaningful names for wider scopes: `graph`, `store`, `node`

### 2.5 Constants
```go
type EdgeType uint8
const (
    EdgeLineage     EdgeType = 1
    EdgeOwnership   EdgeType = 2
)
```

---

## 3. Project Structure

```go
// Standard Go layout
internal/
├── model/     // types only — zero dependencies
├── graph/     // depends on model
├── store/     // depends on graph, model
├── retrieval/ // depends on graph, store, model
├── pipeline/  // depends on everything above
└── runtime/   // depends on nothing

// Package dependency direction:
// model ← graph ← store ← retrieval ← pipeline
// model ← runtime (standalone)
// NO circular dependencies
```

---

## 4. Error Handling

### 4.1 Sentinel Errors

```go
package model

var (
    ErrNotFound      = errors.New("entity not found")
    ErrInvalidState  = errors.New("invalid lifecycle state")
    ErrCycleDetected = errors.New("cycle detected in lineage")
    ErrReadOnly      = errors.New("entity is read-only")
    ErrDanglingRef   = errors.New("reference target not found")
)
```

### 4.2 Error Wrapping

```go
// Always wrap with context:
if err := store.Put(key, val); err != nil {
    return fmt.Errorf("store put %s: %w", key, err)
}

// No panic in public API:
func (g *StatefulGraph) MustAddNode(ctx context.Context, n *Node) {
    if err := g.AddNode(ctx, n); err != nil {
        panic(err) // ❌
    }
}
```

### 4.3 Sentinel Checks

```go
if errors.Is(err, model.ErrNotFound) { ... }
// NOT: err == model.ErrNotFound
```

---

## 5. Testing

### 5.1 Framework
- Standard `testing` package
- `testify/require` for assertions
- No `ginkgo` / `gomega`

### 5.2 Test Organization

```go
// Table-driven tests
func TestStatefulGraph_AddNode(t *testing.T) {
    tests := []struct {
        name     string
        node     *Node
        wantErr  error
    }{
        {name: "valid node", node: validNode(), wantErr: nil},
        {name: "nil node", node: nil, wantErr: model.ErrInvalidArgument},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            g := NewStatefulGraph()
            err := g.AddNode(context.Background(), tt.node)
            if tt.wantErr != nil {
                require.ErrorIs(t, err, tt.wantErr)
                return
            }
            require.NoError(t, err)
        })
    }
}
```

### 5.3 Test Data

```go
// testdata/ — test fixtures
graph_test.go
testdata/
├── artifact_test.json
└── graph_snapshot.bin
```

### 5.4 Coverage

```go
// v0.1: minimum — only core graph operations
// go test -race -count=1 ./internal/...
```

---

## 6. Concurrency

### 6.1 StatefulGraph

```go
type StatefulGraph struct {
    mu   sync.RWMutex
    nodes map[ID]*Node
    // ...
}

func (g *StatefulGraph) Node(id ID) (*Node, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    // ...
}

func (g *StatefulGraph) AddNode(ctx context.Context, n *Node) error {
    g.mu.Lock()
    defer g.mu.Unlock()
    // ...
}
```

### 6.2 Atomic Counters

```go
type Metrics struct {
    nodeCount atomic.Int64
    edgeCount atomic.Int64
}
```

### 6.3 Context Propagation

```go
// All public APIs accept context:
func (g *StatefulGraph) BFS(ctx context.Context, start ID, opts TraversalOpts) ([]*Node, error) {
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }
    // ...
}
```

### 6.4 Race Detection

```go
// CI step: go test -race ./...
```

---

## 7. Code Review Checklist

- [ ] All errors are checked (no `_ =` ignoring errors)
- [ ] Context is propagated through public API
- [ ] Mutex locking is correct (RLock vs Lock, no nested locks)
- [ ] No `panic` in public API
- [ ] No `reflect` in hot paths
- [ ] No CGO unless explicitly documented
- [ ] Tests are table-driven
- [ ] Test file is named `*_test.go`
- [ ] Benchmark tests for critical paths
- [ ] No circular imports
- [ ] `go fmt` passed
- [ ] `go vet` passed
- [ ] `go test -race` passed

---

## 8. Commits

```go
// Conventional commits:
feat:    new feature
fix:     bug fix
docs:    documentation
refactor: code change with no functional change
test:    tests only
chore:   build, CI, tooling
```

---

## 9. Serialization

- Use `github.com/tinylib/msgp` (MessagePack) for disk serialization
- Generate serialization code with `msgp` tags
- No Go `encoding/json` in hot paths

```go
//go:generate msgp
type Node struct {
    ID     string  `msg:"id"`
    Type   uint8   `msg:"type"`
    Active bool    `msg:"active"`
}
```
