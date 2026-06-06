package main

import (
	"context"
	"flag"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

func query(args []string) error {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "viewpoint scope ID")
	text := fs.String("text", "", "query text")
	detail := fs.String("detail", "overview", "overview|entry|raw")
	topk := fs.Int("topk", 5, "top-K")
	tier := fs.String("tier", "", "worktree|workspace (empty=both)")
	kind := fs.String("kind", "", "restrict to kinds (comma list: document,reasoning,insight,...)")
	mode := fs.String("mode", "collapsed", "collapsed (default: flat over visible ∪ all descendants)|vector (flat, visible only)|hybrid|hierarchical")
	coarseK := fs.Int("coarsek", 0, "hierarchical coarse stage: # scopes to keep (0=default)")
	minScore := fs.Float64("min-score", 0, "drop hits with cosine score below this")
	rerank := fs.Bool("rerank", false, "cross-encoder rerank the top candidates (needs a real -embed)")
	includeSuperseded := fs.Bool("include-superseded", false, "include superseded/archived (history) — default current view only")
	recencyHalfLife := fs.Float64("recency-halflife-days", 0, "opt-in recency tie-breaker half-life in days (0=off; vector mode only)")
	graphBoost := fs.Float64("graph-boost", 0, "opt-in graph-aware boost weight 0..1 (0=off): lift candidates edge-connected to strong hits")
	importanceWeight := fs.Float64("importance-weight", 0, "opt-in author-declared importance weight 0..1 (0=off): lift canonical (worktree-tier) artifacts over workspace near-duplicates")
	_ = fs.Parse(args)

	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, *rerank)
	if err != nil {
		return err
	}
	defer e.Close()

	qm, hier, coll := iocfmt.ParseModeSpec(*mode)
	qid, hits, err := e.Query(context.Background(), core.Query{
		Scope:               scopeID,
		Text:                *text,
		Detail:              iocfmt.ParseDetail(*detail),
		TopK:                *topk,
		Tier:                iocfmt.ParseTier(*tier),
		Kinds:               iocfmt.ParseKinds(*kind),
		Mode:                qm,
		Hierarchical:        hier,
		Collapsed:           coll,
		CoarseK:             *coarseK,
		MinScore:            *minScore,
		Rerank:              *rerank,
		IncludeSuperseded:   *includeSuperseded,
		RecencyHalfLifeDays: *recencyHalfLife,
		GraphBoost:          *graphBoost,
		ImportanceWeight:    *importanceWeight,
	})
	if err != nil {
		return err
	}
	return printJSON(iocfmt.QueryOut(qid, hits, e.EmbModel()))
}

// neighbors finds the most similar CURRENT artifacts to text — the dedup/supersede
// lookup to run before pushing a new conclusion.
func neighbors(args []string) error {
	fs := flag.NewFlagSet("neighbors", flag.ExitOnError)
	dir, em := commonFlags(fs)
	scope := fs.String("scope", "", "viewpoint scope ID")
	text := fs.String("text", "", "the conclusion you're about to write")
	topk := fs.Int("topk", 5, "max neighbors")
	_ = fs.Parse(args)

	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	hits, err := e.Neighbors(context.Background(), scopeID, *text, *topk)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"neighbors": iocfmt.HitsOut(hits)})
}

// traces lists recent query traces (newest first) so a query_id can be inspected.
func traces(args []string) error {
	fs := flag.NewFlagSet("traces", flag.ExitOnError)
	dir, em := commonFlags(fs)
	n := fs.Int("n", 10, "max recent traces")
	_ = fs.Parse(args)
	e, err := openService(*dir, *em, false)
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

func traceCmd(args []string) error {
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	dir, em := commonFlags(fs)
	q := fs.String("query", "", "query ID")
	_ = fs.Parse(args)
	id, err := core.ParseID(*q)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
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
