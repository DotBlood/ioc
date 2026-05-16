# METHODOLOGY

## IOC Development Process

---

## 1. Version Control

### 1.1 Branch Strategy

- **Trunk-based development** — single `main` branch
- Feature flags for incomplete features
- No long-lived feature branches
- Short-lived branches: max 2 days

```text
main ───●────●────●────●────●────●────●
         \    /
          ●──●   (short-lived, <48h)
```

### 1.2 Commit Convention

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

**Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`

**Scope:** `graph`, `store`, `retrieval`, `pipeline`, `model`, `docs`

**Examples:**
```
feat(graph): add BFS traversal with temporal filter
fix(store): handle CAS collision on concurrent write
docs(fsd): add edge revision semantics
```

### 1.3 Commit Granularity

- One logical change per commit
- Each commit compiles (`go build ./...`)
- Each commit passes lint (`golangci-lint run ./...`)

---

## 2. Code Review

### 2.1 Process

- Every PR requires 1 approval
- No force push to `main`
- `go build`, `go vet`, `go test -race` must pass
- `golangci-lint` must pass

### 2.2 PR Size

- Target: <400 lines per PR
- Max: 800 lines (exception with justification)
- Larger changes: split into stacked PRs

### 2.3 Review Focus

1. **Correctness** — does this match the FSD?
2. **Concurrency** — race conditions? Deadlocks?
3. **Error handling** — errors checked? Wrapped?
4. **API ergonomics** — does the API make sense?
5. **Performance** — allocations? Lock contention?

---

## 3. Versioning

### 3.1 Semantic Versioning

```
v0.1.0
  │││
  ││└── patch (bug fixes, no breaking changes)
  │└── minor (features, no breaking changes)
  └── major (breaking changes)
```

### 3.2 Pre-1.0 Rules

- `v0.x.0`: breaking changes allowed
- `v0.x.y`: bug fixes only, no breaking
- All pre-1.0 versions considered unstable
- Breaking changes documented in release notes

### 3.3 Release Process

```text
1. All tests pass on main
2. Update version in go.mod
3. Tag: git tag v0.1.0 && git push --tags
4. Generate release notes
5. Build binaries
```

---

## 4. CI/CD

### 4.1 CI Pipeline (GitHub Actions)

```yaml
name: CI

on: [push, pull_request]

jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - run: golangci-lint run ./...

  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - run: go test -race -count=1 ./...

  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - run: go build ./cmd/iocctl
```

### 4.2 Quality Gates

| Gate | Blocking? | Tool |
|------|-----------|------|
| Compilation | ✅ | `go build` |
| Lint | ✅ | `golangci-lint` |
| Unit tests | ✅ | `go test -race` |
| Integration tests | ❌ (v0.1) | `go test -tags=integration` |
| Benchmarks | ❌ | `go test -bench=.` |

---

## 5. Documentation

### 5.1 Document Structure

```
docs/
├── PDR.md           Product Definition — why, for whom
├── FRD.md           Functional Requirements — what it must do
├── FSD.md           Functional Specification — formal guarantees
├── PAD.md           Platform Architecture — how it works
├── CODE-STYLE.md    Go conventions
├── METHODOLOGY.md   Development process
└── ROADMAP.md       Implementation plan
```

### 5.2 Update Rules

- **FSD changes** → update all referencing documents
- **Implementation divergence** → document deviation in Open Questions
- **FRD changes** → update ROADMAP scope definitions

### 5.3 Accuracy Target

- FSD: 95% accurate (formal specification)
- PAD: 85% accurate (architecture evolves)
- ROADMAP: 70% accurate (estimates adjust)

---

## 6. Scope Definition

Each development scope in ROADMAP follows this template:

```
### Scope X.Y: <name>

Description: <one paragraph>

Deliverable:
- <concrete deliverable>
- <tests passing>

Dependencies:
- <depends on>

Effort estimate: <days>
```

### 6.1 Definition of Done

- [ ] Code compiles
- [ ] Tests pass
- [ ] Lint passes
- [ ] No `FIXME` / `TODO` in new code
- [ ] Documentation updated (if public API changed)
- [ ] Changelog updated

---

## 7. Communication

- Issue tracking: GitHub Issues
- Design discussions: in code review or design docs
- No Slack/Teams — everything in writing
- Decision records: inline or Design Decisions Log (FSD §16)
