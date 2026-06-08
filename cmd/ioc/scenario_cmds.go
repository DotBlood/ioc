package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/eval"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// runScenario resets its own data dir and plays a scripted scenario.
func runScenario(args []string) int {
	fs := flag.NewFlagSet("run-scenario", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(os.TempDir(), "ioc-run"), "run data directory (reset each run)")
	em := fs.String("embed", "", "embedder endpoint (empty=mock)")
	mode := fs.String("mode", "vector", "retrieval mode: vector|hybrid|hierarchical")
	coarseK := fs.Int("coarsek", 0, "hierarchical coarse stage: # scopes to keep (0=engine default)")
	rerank := fs.Bool("rerank", false, "cross-encoder rerank the top candidates (needs a real -embed)")
	// Allow the scenario path before or after flags (Go's flag pkg otherwise
	// stops at the first positional, silently dropping trailing -embed/-dir).
	scenarioPath, rest := splitPositional(args)
	_ = fs.Parse(rest)
	if scenarioPath == "" {
		usage()
		return 2
	}
	sc, err := eval.LoadScenario(scenarioPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := os.RemoveAll(*dir); err != nil {
		fmt.Fprintln(os.Stderr, "error: reset dir:", err)
		return 1
	}
	e, err := openEngineEmbedded(*dir, *em, *rerank)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: open engine:", err)
		return 1
	}
	defer e.Close()

	tracePath := filepath.Join(*dir, "trace.jsonl")
	tf, err := os.Create(tracePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: trace file:", err)
		return 1
	}
	defer tf.Close()

	qm, hier, _ := iocfmt.ParseModeSpec(*mode) // scenario runner has no collapsed mode
	rep, err := eval.Run(context.Background(), e, sc, tf, qm, hier, *coarseK, *rerank)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: run:", err)
		return 1
	}
	fmt.Println(rep.String())
	fmt.Printf("per-turn trace: %s\n", tracePath)
	if !rep.Pass() {
		return 1
	}
	return 0
}

func embedPing(args []string) error {
	fs := flag.NewFlagSet("embed-ping", flag.ExitOnError)
	em := fs.String("embed", "", "embedder endpoint")
	_ = fs.Parse(args)
	if *em == "" {
		m := embed.NewMockEmbedder(384)
		return printJSON(map[string]any{"model": m.Model(), "dims": m.Dims(), "transport": "mock"})
	}
	h, err := embed.NewHTTPEmbedder(*em, allowRemoteEmbed())
	if err != nil {
		return err
	}
	model, dims, err := h.Health(context.Background())
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"model": model, "dims": dims, "endpoint": *em})
}

// genScenario writes a deterministic scale scenario. -shape flat puts all
// artifacts in one session; tree splits them across one session per cluster with
// a rollup each (for hierarchical retrieval). Recall queries are identical in both.
func genScenario(args []string) error {
	fs := flag.NewFlagSet("gen-scenario", flag.ExitOnError)
	n := fs.Int("n", 180, "total artifacts")
	clusters := fs.Int("clusters", 18, "topic clusters")
	shape := fs.String("shape", "tree", "flat|tree")
	out := fs.String("out", "", "output JSON path (required)")
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("gen-scenario: -out required")
	}

	topics := []string{
		"outbound request timeouts", "retry and backoff policy", "authentication and tokens",
		"rate limiting", "database storage engine", "event delivery semantics",
		"consumer idempotency", "structured logging", "distributed tracing", "service metrics",
		"deployment rollout strategy", "caching layer", "API pagination", "data encryption at rest",
		"backup and restore", "feature flag rollout", "configuration management", "HTTP error status codes",
	}
	// Distinctive per-topic decision (shared boilerplate defeats sentence embeddings).
	decisions := []string{
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
	aspects := []string{"latency", "cost", "security", "testing", "rollback", "monitoring", "capacity", "compatibility", "migration"}
	c := *clusters
	if c > len(topics) {
		c = len(topics)
	}
	per := *n / c
	if per < 2 {
		per = 2
	}
	tree := *shape == "tree"

	sc := eval.Scenario{Name: "scale-" + *shape, TopK: 5}
	add := func(t eval.Turn) { sc.Turns = append(sc.Turns, t) }

	add(eval.Turn{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "platform"})
	add(eval.Turn{Op: "create_scope", ID: "ws", Parent: "wt", Role: "workspace", Title: "decisions"})
	if !tree {
		add(eval.Turn{Op: "create_scope", ID: "s", Parent: "ws", Role: "session", Title: "log"})
	}

	scopeOf := func(i int) string {
		if tree {
			return fmt.Sprintf("s%d", i)
		}
		return "s"
	}
	for i := 0; i < c; i++ {
		topic := topics[i]
		if tree {
			add(eval.Turn{Op: "create_scope", ID: fmt.Sprintf("s%d", i), Parent: "ws", Role: "session", Title: topic})
		}
		add(eval.Turn{Op: "push", Scope: scopeOf(i), As: fmt.Sprintf("t%d", i), Publish: true,
			Summary: fmt.Sprintf("%s — we decided to %s", topic, decisions[i])})
		for j := 1; j < per; j++ {
			add(eval.Turn{Op: "push", Scope: scopeOf(i), As: fmt.Sprintf("v%d_%d", i, j),
				Summary: fmt.Sprintf("%s — %s considerations, note %d", topic, aspects[(j-1)%len(aspects)], j)})
		}
		if tree {
			// Rich, distinctive rollup: topic + decision + a few aspect keywords —
			// coarse retrieval can only disambiguate scopes if rollups are distinctive.
			a1, a2, a3 := aspects[0], aspects[1%len(aspects)], aspects[2%len(aspects)]
			add(eval.Turn{Op: "rollup", Scope: fmt.Sprintf("s%d", i),
				Summary: fmt.Sprintf("%s — decided to %s; covers %s, %s, %s", topic, decisions[i], a1, a2, a3)})
		}
	}

	qScope := "s"
	if tree {
		qScope = "ws"
	}
	for i := 0; i < c; i++ {
		topic := topics[i]
		add(eval.Turn{Op: "query", Scope: qScope,
			Text: fmt.Sprintf("what did we decide about %s?", topic),
			Expect: &eval.Expectation{
				MustContain:    fmt.Sprintf("t%d", i),
				MustMentionAny: []string{"we decided"},
				MaxDrills:      0,
				RawBaseline:    []string{fmt.Sprintf("t%d", i)},
			}})
	}

	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %d artifacts, %d clusters, shape=%s\n", *out, c*per, c, *shape)
	return nil
}

// splitPositional pulls the first bare (non-flag) token out as a positional,
// returning remaining args (flags, original order) so flags may appear before or
// after the positional. Handles "-dir/-embed/-mode/-coarsek value".
func splitPositional(args []string) (pos string, rest []string) {
	valueFlags := map[string]bool{
		"-dir": true, "-embed": true, "-mode": true, "-coarsek": true,
		"-scope": true, "-title": true, "-maxchars": true, "-overlap": true,
	}
	skip := false
	for _, a := range args {
		if skip {
			rest = append(rest, a)
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
			if valueFlags[a] {
				skip = true
			}
			continue
		}
		if pos == "" {
			pos = a
		} else {
			rest = append(rest, a)
		}
	}
	return pos, rest
}
