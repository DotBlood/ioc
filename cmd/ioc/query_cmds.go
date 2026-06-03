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
	mode := fs.String("mode", "vector", "vector|hybrid|hierarchical")
	coarseK := fs.Int("coarsek", 0, "hierarchical coarse stage: # scopes to keep (0=default)")
	minScore := fs.Float64("min-score", 0, "drop hits with cosine score below this")
	rerank := fs.Bool("rerank", false, "cross-encoder rerank the top candidates (needs a real -embed)")
	_ = fs.Parse(args)

	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	e, err := openEngineRerank(*dir, *em, *rerank)
	if err != nil {
		return err
	}
	defer e.Close()

	qm, hier := iocfmt.ParseModeSpec(*mode)
	qid, hits, err := e.Query(context.Background(), core.Query{
		Scope:        scopeID,
		Text:         *text,
		Detail:       iocfmt.ParseDetail(*detail),
		TopK:         *topk,
		Tier:         iocfmt.ParseTier(*tier),
		Kinds:        iocfmt.ParseKinds(*kind),
		Mode:         qm,
		Hierarchical: hier,
		CoarseK:      *coarseK,
		MinScore:     *minScore,
		Rerank:       *rerank,
	})
	if err != nil {
		return err
	}
	return printJSON(iocfmt.QueryOut(qid, hits, e.EmbModel()))
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
