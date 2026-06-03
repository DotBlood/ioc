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
	mode := fs.String("mode", "vector", "retrieval mode: vector|hybrid (hybrid is experimental)")
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

	rep, err := eval.Run(ctx, e, sc, tf, parseMode(*mode))
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
	mode := fs.String("mode", "vector", "vector|hybrid (hybrid is experimental)")
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

	qid, hits, err := e.Query(context.Background(), core.Query{
		Scope:    scopeID,
		Text:     *text,
		Detail:   parseDetail(*detail),
		TopK:     *topk,
		Tier:     parseTier(*tier),
		Mode:     parseMode(*mode),
		MinScore: *minScore,
	})
	if err != nil {
		return err
	}
	return printJSON(queryOut(qid, hits))
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

func parseMode(s string) core.QueryMode {
	if strings.ToLower(s) == "hybrid" {
		return core.ModeHybrid
	}
	return core.ModeVector
}

// weakThreshold: a top cosine below this means "no strong match" for bge-small.
const weakThreshold = 0.45

func queryOut(qid core.ID, hits []core.Hit) map[string]any {
	weak := len(hits) == 0 || hits[0].Score < weakThreshold
	return map[string]any{
		"query_id":   qid.String(),
		"weak_match": weak,
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
