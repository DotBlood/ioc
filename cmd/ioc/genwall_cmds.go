package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DotBlood/ioc/internal/eval"
)

// distractorTopics/Decisions are generic infra decisions used as synthetic
// distractor reasoning artifacts — semantically plausible neighbours that crowd
// the real answers, creating the recall pressure the scale test needs. They are
// never gold/currency, just noise (mirrors gen-scenario's scale corpus).
var distractorTopics = []string{
	"outbound request timeouts", "retry and backoff policy", "authentication and tokens",
	"rate limiting", "database storage engine", "event delivery semantics",
	"consumer idempotency", "structured logging", "distributed tracing", "service metrics",
	"deployment rollout strategy", "caching layer", "API pagination", "data encryption at rest",
	"backup and restore", "feature flag rollout", "configuration management", "HTTP error status codes",
}

var distractorDecisions = []string{
	"time out outbound calls after 2 seconds", "retry with exponential backoff, max 3 attempts",
	"use RS256 JWT access tokens with a 15-minute TTL", "rate-limit with a token bucket per API key",
	"store data in PostgreSQL 16 using JSONB columns", "guarantee at-least-once event delivery",
	"deduplicate events by a stable dedup key", "emit structured JSON logs",
	"propagate the W3C traceparent header for tracing", "record RED metrics per endpoint",
	"roll out with a canary then blue-green", "cache hot reads in Redis with a 60-second TTL",
	"paginate with opaque cursors instead of offsets", "encrypt data at rest with AES-256",
	"back up nightly with 90-day retention", "gate features behind LaunchDarkly flags",
	"manage configuration via env vars and Vault", "return RFC 7807 problem+json error bodies",
}

var distractorAspects = []string{"latency", "cost", "security", "testing", "rollback", "monitoring", "capacity", "compatibility", "migration"}

// realTopicSession groups a real artifact (by its As-name prefix) into a topical
// session so hierarchical retrieval can route to it. RT* must be checked before R*.
func realTopicSession(as string) string {
	switch {
	case strings.HasPrefix(as, "RT"):
		return "runtime"
	case strings.HasPrefix(as, "R"):
		return "retrieval"
	case strings.HasPrefix(as, "S"):
		return "storage"
	default:
		return "direction" // M* and anything else
	}
}

// genWall emits a ~N-artifact reasoning-wall spec: the embedded real corpus (27
// questions + their artifacts + the currency pair) padded with synthetic
// distractors. -shape flat puts everything in one session (queried from it) to
// test flat vector; -shape tree groups artifacts into topical sessions with
// rollups (queried from the workspace) to test hierarchical retrieval.
func genWall(args []string) error {
	fs := flag.NewFlagSet("gen-wall", flag.ExitOnError)
	out := fs.String("out", "", "output WallSpec JSON path (required)")
	shape := fs.String("shape", "tree", "flat|tree")
	n := fs.Int("n", 180, "approximate total artifacts (real seed + synthetic distractors)")
	dClusters := fs.Int("distractor-clusters", 15, "# of distractor topic sessions (tree shape)")
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("gen-wall: -out required")
	}

	seed, err := eval.RealWallSpec()
	if err != nil {
		return err
	}
	// Extract the real push turns (As, Kind, Summary, Supersedes, Publish) from the
	// seed build; we re-place them into shape-specific scopes below.
	var realPushes []eval.Turn
	for _, t := range seed.Build {
		if t.Op == "push" {
			realPushes = append(realPushes, t)
		}
	}
	nDistract := *n - len(realPushes)
	if nDistract < 0 {
		nDistract = 0
	}

	tree := *shape == "tree"
	spec := eval.WallSpec{Name: "reasoning-wall-180-" + *shape, TopK: 5}
	add := func(t eval.Turn) { spec.Build = append(spec.Build, t) }

	add(eval.Turn{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "ioc"})

	var queryScope string
	if tree {
		add(eval.Turn{Op: "create_scope", ID: "ws", Parent: "wt", Role: "workspace", Title: "decisions"})
		queryScope = "ws"
		// Real artifacts → 4 topical sessions (retrieval/storage/runtime/direction),
		// each with a rollup so the coarse stage can route to it.
		realSessions := []string{"retrieval", "storage", "runtime", "direction"}
		rollups := map[string]string{
			"retrieval": "retrieval strategy: vector, hybrid and hierarchical search, reranking, confidence floors",
			"storage":   "storage and durability: embeddings, content store, integrity, embedder model guards",
			"runtime":   "runtime daemon: single store owner, client protocol, concurrency, shutdown",
			"direction": "project direction: memory model, two tiers, visibility, versioning, current vs old design",
		}
		seen := map[string]bool{}
		for _, t := range realPushes {
			sess := realTopicSession(t.As)
			if !seen[sess] {
				add(eval.Turn{Op: "create_scope", ID: sess, Parent: "ws", Role: "session", Title: sess})
				seen[sess] = true
			}
			t.Scope = sess
			add(t)
		}
		for _, sess := range realSessions {
			if seen[sess] {
				add(eval.Turn{Op: "rollup", Scope: sess, Summary: rollups[sess]})
			}
		}
		// Distractor sessions, each with a rollup.
		emitDistractors(add, nDistract, *dClusters, true)
	} else {
		add(eval.Turn{Op: "create_scope", ID: "all", Parent: "wt", Role: "session", Title: "all"})
		queryScope = "all"
		for _, t := range realPushes {
			t.Scope = "all"
			add(t)
		}
		emitDistractors(add, nDistract, *dClusters, false)
	}

	// Re-target the real questions at the shape's query viewpoint (gold/goldRefs/
	// currency unchanged — they reference artifacts by As-name, which we preserved).
	for _, q := range seed.Questions {
		q.Scope = queryScope
		spec.Questions = append(spec.Questions, q)
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d real + %d distractor = %d artifacts, %d questions, shape=%s\n",
		*out, len(realPushes), nDistract, len(realPushes)+nDistract, len(spec.Questions), *shape)
	return nil
}

// emitDistractors appends nDistract synthetic distractor reasoning artifacts. In
// tree shape they are spread across `clusters` topic sessions (each with a
// rollup); in flat shape they all go into the single "all" session.
func emitDistractors(add func(eval.Turn), nDistract, clusters int, tree bool) {
	if nDistract <= 0 {
		return
	}
	c := clusters
	if c > len(distractorTopics) {
		c = len(distractorTopics)
	}
	if c < 1 {
		c = 1
	}
	per := (nDistract + c - 1) / c // ceil
	emitted := 0
	for i := 0; i < c && emitted < nDistract; i++ {
		topic := distractorTopics[i%len(distractorTopics)]
		decision := distractorDecisions[i%len(distractorDecisions)]
		sess := fmt.Sprintf("d%d", i)
		scope := "all"
		if tree {
			scope = sess
			add(eval.Turn{Op: "create_scope", ID: sess, Parent: "ws", Role: "session", Title: topic})
		}
		for j := 0; j < per && emitted < nDistract; j++ {
			var summary string
			if j == 0 {
				summary = fmt.Sprintf("%s — we decided to %s", topic, decision)
			} else {
				asp := distractorAspects[(j-1)%len(distractorAspects)]
				summary = fmt.Sprintf("%s — %s considerations, note %d", topic, asp, j)
			}
			add(eval.Turn{Op: "push", Scope: scope, As: fmt.Sprintf("%s_%d", sess, j), Kind: "reasoning", Summary: summary})
			emitted++
		}
		if tree {
			a1, a2 := distractorAspects[0], distractorAspects[1]
			add(eval.Turn{Op: "rollup", Scope: sess, Summary: fmt.Sprintf("%s — decided to %s; covers %s, %s", topic, decision, a1, a2)})
		}
	}
}
