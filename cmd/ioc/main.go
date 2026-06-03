// Command ioc drives the IOC slice: run scripted eval scenarios, ping the
// embedder, and operate IOC as a persistent memory store from the shell (so an
// agent can use it via Bash). JSON output makes results machine-parseable.
//
// Embedder endpoint (-embed): empty = deterministic mock; "http://host:port" or
// "unix:/path" = external embedding service (py/embed_server.py).
//
// IMPORTANT: a given -dir must always be used with the SAME embedder; mock and
// real embeddings live in different vector spaces and must not be mixed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
	"github.com/DotBlood/ioc/internal/eval"
)

const defaultDataDir = ".ioc-data"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run-scenario":
		os.Exit(runScenario(args))
	case "embed-ping":
		err = embedPing(args)
	case "create-scope":
		err = createScope(args)
	case "push":
		err = push(args)
	case "query":
		err = query(args)
	case "drill":
		err = drill(args)
	case "publish":
		err = publish(args)
	case "siblings":
		err = siblings(args)
	case "ancestors":
		err = ancestors(args)
	case "fork":
		err = fork(args)
	case "consolidate":
		err = consolidate(args)
	case "crossversion":
		err = crossversion(args)
	case "trace":
		err = traceCmd(args)
	case "traces":
		err = traces(args)
	case "rollup":
		err = rollupCmd(args)
	case "gen-scenario":
		err = genScenario(args)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ioc <command> [flags]

commands:
  run-scenario <file.json> [-dir d] [-embed e]   run a scripted eval scenario
  embed-ping [-embed e]                          check the embedding service
  create-scope [-parent ID] -role R -title T     create a scope
  push -scope ID -summary S [-kind K] [-content X|-content-file F] [-publish]
  query -scope ID -text T [-detail overview|entry|raw] [-topk N] [-tier t]
  drill -artifact ID [-detail raw]
  publish -artifact ID
  siblings -scope ID
  ancestors -scope ID
  fork -scope ID -title T
  consolidate -scope ID -summary S
  crossversion -scope ID -constraints C -lessons L
  trace -query ID

common flags: -dir (default `+defaultDataDir+`)  -embed (empty=mock; http://host:port or unix:/path)`)
}

// --- embedder / engine wiring ---

func buildEmbedder(endpoint string) embed.Embedder {
	if endpoint == "" {
		return embed.NewMockEmbedder(384)
	}
	return embed.NewHTTPEmbedder(endpoint)
}

func openEngine(dir, endpoint string) (*engine.Engine, error) {
	return engine.Open(context.Background(), dir, buildEmbedder(endpoint))
}

// commonFlags registers -dir and -embed on a flag set.
func commonFlags(fs *flag.FlagSet) (*string, *string) {
	dir := fs.String("dir", defaultDataDir, "persistent data directory")
	em := fs.String("embed", "", "embedder endpoint (empty=mock; http://host:port or unix:/path)")
	return dir, em
}

func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// --- run-scenario (resets its own dir) ---

func runScenario(args []string) int {
	fs := flag.NewFlagSet("run-scenario", flag.ExitOnError)
	dir := fs.String("dir", filepath.Join(os.TempDir(), "ioc-run"), "run data directory (reset each run)")
	em := fs.String("embed", "", "embedder endpoint (empty=mock)")
	mode := fs.String("mode", "vector", "retrieval mode: vector|hybrid|hierarchical")
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
	ctx := context.Background()
	e, err := openEngine(*dir, *em)
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

	qm, hier := parseModeSpec(*mode)
	rep, err := eval.Run(ctx, e, sc, tf, qm, hier)
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

// --- embed-ping ---

func embedPing(args []string) error {
	fs := flag.NewFlagSet("embed-ping", flag.ExitOnError)
	em := fs.String("embed", "", "embedder endpoint")
	_ = fs.Parse(args)
	if *em == "" {
		m := embed.NewMockEmbedder(384)
		return printJSON(map[string]any{"model": m.Model(), "dims": m.Dims(), "transport": "mock"})
	}
	h := embed.NewHTTPEmbedder(*em)
	model, dims, err := h.Health(context.Background())
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"model": model, "dims": dims, "endpoint": *em})
}

// --- memory subcommands ---

func createScope(args []string) error {
	fs := flag.NewFlagSet("create-scope", flag.ExitOnError)
	dir, em := commonFlags(fs)
	parent := fs.String("parent", "", "parent scope ID (empty=root)")
	role := fs.String("role", "session", "worktree|workspace|session")
	title := fs.String("title", "", "scope title")
	_ = fs.Parse(args)

	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()

	parentID, err := parseScopeID(*parent)
	if err != nil {
		return err
	}
	s, err := e.CreateScope(context.Background(), parentID, parseRole(*role), *title)
	if err != nil {
		return err
	}
	return printJSON(scopeOut(s))
}

func push(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	kind := fs.String("kind", "insight", "answer|insight|summary|document|reasoning|seed")
	summary := fs.String("summary", "", "mini-summary (required)")
	content := fs.String("content", "", "inline full content ('-' = stdin)")
	contentFile := fs.String("content-file", "", "file with full content")
	pub := fs.Bool("publish", false, "publish to siblings")
	_ = fs.Parse(args)

	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	body, err := readContent(*content, *contentFile)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()

	a, err := e.Push(context.Background(), core.PushRequest{
		Scope:   scopeID,
		Kind:    parseKind(*kind),
		Summary: *summary,
		Content: body,
		Publish: *pub,
	})
	if err != nil {
		return err
	}
	return printJSON(artifactOut(a))
}

func query(args []string) error {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "viewpoint scope ID")
	text := fs.String("text", "", "query text")
	detail := fs.String("detail", "overview", "overview|entry|raw")
	topk := fs.Int("topk", 5, "top-K")
	tier := fs.String("tier", "", "worktree|workspace (empty=both)")
	mode := fs.String("mode", "vector", "vector|hybrid|hierarchical")
	minScore := fs.Float64("min-score", 0, "drop hits with cosine score below this")
	_ = fs.Parse(args)

	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()

	qm, hier := parseModeSpec(*mode)
	qid, hits, err := e.Query(context.Background(), core.Query{
		Scope:        scopeID,
		Text:         *text,
		Detail:       parseDetail(*detail),
		TopK:         *topk,
		Tier:         parseTier(*tier),
		Mode:         qm,
		Hierarchical: hier,
		MinScore:     *minScore,
	})
	if err != nil {
		return err
	}
	return printJSON(queryOut(qid, hits, e.EmbModel()))
}

// traces lists recent query traces (newest first) so a query_id can be inspected.
func traces(args []string) error {
	fs := flag.NewFlagSet("traces", flag.ExitOnError)
	dir, em := commonFlags(fs)
	n := fs.Int("n", 10, "max recent traces")
	_ = fs.Parse(args)
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	trs, err := e.RecentTraces(*n)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(trs))
	for i, t := range trs {
		out[i] = map[string]any{
			"query_id": t.QueryID.String(),
			"scope":    t.Scope.String(),
			"text":     t.Text,
			"hits":     len(t.Hits),
		}
	}
	return printJSON(out)
}

func drill(args []string) error {
	fs := flag.NewFlagSet("drill", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	detail := fs.String("detail", "raw", "entry|raw")
	_ = fs.Parse(args)

	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()

	h, err := e.Drill(context.Background(), id, parseDetail(*detail))
	if err != nil {
		return err
	}
	return printJSON(hitOut(h))
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	_ = fs.Parse(args)
	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Publish(context.Background(), id); err != nil {
		return err
	}
	return printJSON(map[string]any{"published": id.String()})
}

func siblings(args []string) error {
	fs := flag.NewFlagSet("siblings", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	hits, err := e.SiblingOverview(context.Background(), scopeID)
	if err != nil {
		return err
	}
	return printJSON(hitsOut(hits))
}

func ancestors(args []string) error {
	fs := flag.NewFlagSet("ancestors", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	scs, err := e.Ancestors(context.Background(), scopeID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(scs))
	for i, s := range scs {
		out[i] = scopeOut(s)
	}
	return printJSON(out)
}

func fork(args []string) error {
	fs := flag.NewFlagSet("fork", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "source scope ID")
	title := fs.String("title", "", "new scope title")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	s, err := e.Fork(context.Background(), scopeID, *title)
	if err != nil {
		return err
	}
	return printJSON(scopeOut(s))
}

func consolidate(args []string) error {
	fs := flag.NewFlagSet("consolidate", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	summary := fs.String("summary", "", "consolidated summary")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	a, err := e.Consolidate(context.Background(), scopeID, *summary)
	if err != nil {
		return err
	}
	return printJSON(artifactOut(a))
}

func crossversion(args []string) error {
	fs := flag.NewFlagSet("crossversion", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	constraints := fs.String("constraints", "", "carried-forward constraints")
	lessons := fs.String("lessons", "", "carried-forward lessons")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	s, err := e.CrossVersion(context.Background(), scopeID, core.Seed{Constraints: *constraints, Lessons: *lessons})
	if err != nil {
		return err
	}
	return printJSON(scopeOut(s))
}

func traceCmd(args []string) error {
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	dir, em := commonFlags(fs)
	q := fs.String("query", "", "query ID")
	_ = fs.Parse(args)
	id, err := core.ParseID(*q)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	tr, err := e.Trace(context.Background(), id)
	if err != nil {
		return err
	}
	return printJSON(tr)
}

func rollupCmd(args []string) error {
	fs := flag.NewFlagSet("rollup", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "scope ID")
	summary := fs.String("summary", "", "rollup summary representing the scope")
	_ = fs.Parse(args)
	scopeID, err := parseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngine(*dir, *em)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.RollupScope(context.Background(), scopeID, *summary); err != nil {
		return err
	}
	return printJSON(map[string]any{"rolled_up": scopeID.String()})
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
	// Distinctive per-topic decision (avoids shared boilerplate that defeats
	// sentence embeddings — the target must be topically discriminable).
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
			// Distinctive rollup (topic + its decision) — coarse retrieval can
			// only disambiguate scopes if their rollups are distinctive.
			add(eval.Turn{Op: "rollup", Scope: fmt.Sprintf("s%d", i),
				Summary: fmt.Sprintf("%s — decided to %s", topic, decisions[i])})
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

// --- helpers: parsing + output shaping ---

// splitPositional pulls the first bare (non-flag) token out as a positional,
// returning the remaining args (flags, original order) so flags may appear
// before or after the positional. Handles "-dir value" / "-embed value".
func splitPositional(args []string) (pos string, rest []string) {
	valueFlags := map[string]bool{"-dir": true, "-embed": true, "-mode": true}
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

func parseScopeID(s string) (core.ID, error) {
	if s == "" || s == "root" {
		return core.NilID, nil
	}
	return core.ParseID(s)
}

func readContent(inline, file string) ([]byte, error) {
	switch {
	case file != "":
		return os.ReadFile(file)
	case inline == "-":
		return io.ReadAll(os.Stdin)
	case inline != "":
		return []byte(inline), nil
	default:
		return nil, nil
	}
}

func parseRole(s string) core.Role {
	switch strings.ToLower(s) {
	case "worktree":
		return core.RoleWorktree
	case "workspace":
		return core.RoleWorkspace
	default:
		return core.RoleSession
	}
}

func parseKind(s string) core.ArtifactKind {
	switch strings.ToLower(s) {
	case "answer":
		return core.KindAnswer
	case "summary":
		return core.KindSummary
	case "document":
		return core.KindDocument
	case "reasoning":
		return core.KindReasoning
	case "seed":
		return core.KindSeed
	default:
		return core.KindInsight
	}
}

func parseDetail(s string) core.Detail {
	switch strings.ToLower(s) {
	case "raw":
		return core.DetailRaw
	case "entry":
		return core.DetailEntry
	default:
		return core.DetailOverview
	}
}

func parseTier(s string) core.Tier {
	switch strings.ToLower(s) {
	case "worktree":
		return core.TierWorktree
	case "workspace":
		return core.TierWorkspace
	default:
		return 0
	}
}

// parseModeSpec maps a -mode string to (retrieval mode, hierarchical?).
//   vector (default) | hybrid (experimental) | hierarchical (coarse→fine over rollups)
func parseModeSpec(s string) (core.QueryMode, bool) {
	switch strings.ToLower(s) {
	case "hybrid":
		return core.ModeHybrid, false
	case "hierarchical":
		return core.ModeVector, true
	default:
		return core.ModeVector, false
	}
}

// queryOut builds the query response: weak_match uses a per-embedder confidence
// floor (not a fixed constant), plus top_score and margin (top1-top2) as a
// relative signal. Calibration is heuristic.
func queryOut(qid core.ID, hits []core.Hit, model string) map[string]any {
	var top, margin float64
	if len(hits) > 0 {
		top = hits[0].Score
	}
	if len(hits) > 1 {
		margin = hits[0].Score - hits[1].Score
	}
	weak := len(hits) == 0 || top < core.ConfidenceFloor(model)
	return map[string]any{
		"query_id":   qid.String(),
		"weak_match": weak,
		"top_score":  top,
		"margin":     margin,
		"hits":       hitsOut(hits),
	}
}

func scopeOut(s core.Scope) map[string]any {
	out := map[string]any{
		"id":       s.ID.String(),
		"role":     s.Role.String(),
		"title":    s.Title,
		"version":  s.Version,
		"archived": s.Archived,
	}
	if !s.Parent.IsZero() {
		out["parent"] = s.Parent.String()
	}
	if !s.ForkedFrom.IsZero() {
		out["forked_from"] = s.ForkedFrom.String()
	}
	return out
}

func artifactOut(a core.Artifact) map[string]any {
	return map[string]any{
		"id":        a.ID.String(),
		"scope":     a.Scope.String(),
		"kind":      a.Kind.String(),
		"tier":      a.Tier.String(),
		"published": a.Published,
	}
}

func hitOut(h core.Hit) map[string]any {
	out := map[string]any{
		"artifact":   h.Artifact.String(),
		"scope_path": h.ScopePath,
		"kind":       h.Kind.String(),
		"tier":       h.Tier.String(),
		"summary":    h.Summary,
		"score":      h.Score,
	}
	if len(h.Content) > 0 {
		out["content"] = string(h.Content)
	}
	return out
}

func hitsOut(hits []core.Hit) []map[string]any {
	out := make([]map[string]any, len(hits))
	for i, h := range hits {
		out[i] = hitOut(h)
	}
	return out
}
