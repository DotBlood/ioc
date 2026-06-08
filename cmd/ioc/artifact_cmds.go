package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/iocfmt"
)

// parseRelations parses "kind:targetID,kind:targetID" into edge specs (empty => nil).
func parseRelations(s string) ([]core.EdgeSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.EdgeSpec
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("bad relation %q (want kind:targetID)", part)
		}
		id, err := core.ParseID(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, err
		}
		out = append(out, core.EdgeSpec{Kind: core.RelationKind(strings.TrimSpace(kv[0])), Target: id})
	}
	return out, nil
}

// parseEdgeDir maps an "out|in|both" string to core.EdgeDir (default out).
func parseEdgeDir(s string) core.EdgeDir {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "in":
		return core.DirIn
	case "both":
		return core.DirBoth
	default:
		return core.DirOut
	}
}

// parseKindList parses a comma-separated relation-kind list (empty => nil = all).
func parseKindList(s string) []core.RelationKind {
	var out []core.RelationKind
	for _, k := range strings.Split(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, core.RelationKind(k))
		}
	}
	return out
}

// parseIDList parses a comma-separated list of artifact IDs (empty => nil).
func parseIDList(s string) ([]core.ID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.ID
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := core.ParseID(part)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
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
	supersedes := fs.String("supersedes", "", "comma-separated artifact IDs this push replaces")
	relations := fs.String("relations", "", "comma-separated kind:targetID edges, e.g. depends_on:ID,answers:ID")
	_ = fs.Parse(args)

	scopeID, err := iocfmt.ParseScopeID(*scope)
	if err != nil {
		return err
	}
	body, err := readContent(*content, *contentFile)
	if err != nil {
		return err
	}
	sup, err := parseIDList(*supersedes)
	if err != nil {
		return err
	}
	rels, err := parseRelations(*relations)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()

	a, err := e.Push(context.Background(), core.PushRequest{
		Scope:      scopeID,
		Kind:       iocfmt.ParseKind(*kind),
		Summary:    *summary,
		Content:    body,
		Publish:    *pub,
		Supersedes: sup,
		Relations:  rels,
	})
	if err != nil {
		return err
	}
	return printJSON(iocfmt.ArtifactOut(a))
}

// supersede marks an existing artifact as replaced by another (post-hoc currency).
func supersede(args []string) error {
	fs := flag.NewFlagSet("supersede", flag.ExitOnError)
	dir, em := commonFlags(fs)
	old := fs.String("old", "", "artifact ID being superseded")
	by := fs.String("by", "", "artifact ID that replaces it")
	_ = fs.Parse(args)
	oldID, err := core.ParseID(*old)
	if err != nil {
		return err
	}
	newID, err := core.ParseID(*by)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Supersede(context.Background(), oldID, newID); err != nil {
		return err
	}
	return printJSON(map[string]any{"superseded": oldID.String(), "by": newID.String()})
}

// relate creates an author-declared edge from one artifact to another.
func relate(args []string) error {
	fs := flag.NewFlagSet("relate", flag.ExitOnError)
	dir, em := commonFlags(fs)
	from := fs.String("from", "", "source artifact ID")
	to := fs.String("to", "", "target artifact ID")
	kind := fs.String("kind", "relates_to", "relation kind (depends_on|contradicts|answers|refines|relates_to|...)")
	_ = fs.Parse(args)
	fromID, err := core.ParseID(*from)
	if err != nil {
		return err
	}
	toID, err := core.ParseID(*to)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Relate(context.Background(), fromID, toID, core.RelationKind(*kind)); err != nil {
		return err
	}
	return printJSON(map[string]any{"from": fromID.String(), "to": toID.String(), "kind": *kind})
}

// related walks the edge graph from an artifact (structural retrieval).
func related(args []string) error {
	fs := flag.NewFlagSet("related", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	kinds := fs.String("kind", "", "comma-separated relation kinds to follow (empty = all)")
	dirFlag := fs.String("direction", "out", "edge direction: out (its targets) | in (who points at it) | both")
	depth := fs.Int("depth", 1, "traversal depth")
	_ = fs.Parse(args)
	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	hits, err := e.Related(context.Background(), id, parseKindList(*kinds), parseEdgeDir(*dirFlag), *depth)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"related": iocfmt.HitsOut(hits)})
}

// history walks the supersession chain of an artifact (what it replaced, and what
// replaced it), newest-last. Implemented client-side over ListArtifacts so it
// needs no extra RPC method.
func history(args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	dir, em := commonFlags(fs)
	artifact := fs.String("artifact", "", "artifact ID")
	_ = fs.Parse(args)
	id, err := core.ParseID(*artifact)
	if err != nil {
		return err
	}
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()

	arts, err := e.ListArtifacts(context.Background())
	if err != nil {
		return err
	}
	byID := make(map[core.ID]core.Artifact, len(arts))
	supersededBy := make(map[core.ID]core.ID, len(arts)) // old -> new
	for _, a := range arts {
		byID[a.ID] = a
		if !a.SupersededBy.IsZero() {
			supersededBy[a.ID] = a.SupersededBy
		}
	}
	if _, ok := byID[id]; !ok {
		return core.ErrNotFound
	}
	// Walk back to the oldest ancestor in the chain, then forward to the newest.
	start := id
	for {
		prev, ok := priorOf(byID, supersededBy, start)
		if !ok {
			break
		}
		start = prev
	}
	var chain []map[string]any
	cur := start
	seen := map[core.ID]bool{}
	for !cur.IsZero() && !seen[cur] {
		seen[cur] = true
		a := byID[cur]
		entry := map[string]any{"artifact": a.ID.String(), "summary": a.Summary, "current": a.SupersededBy.IsZero()}
		if !a.SupersededBy.IsZero() {
			entry["superseded_by"] = a.SupersededBy.String()
		}
		chain = append(chain, entry)
		cur = a.SupersededBy
	}
	return printJSON(map[string]any{"artifact": id.String(), "chain": chain})
}

// priorOf finds the artifact that this one supersedes (the back-link), if any.
func priorOf(byID map[core.ID]core.Artifact, supersededBy map[core.ID]core.ID, id core.ID) (core.ID, bool) {
	for old, neu := range supersededBy {
		if neu == id {
			return old, true
		}
	}
	return core.NilID, false
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
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()

	h, err := e.Drill(context.Background(), id, iocfmt.ParseDetail(*detail))
	if err != nil {
		return err
	}
	return printJSON(iocfmt.HitOut(h))
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
	e, err := openService(*dir, *em, false)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Publish(context.Background(), id); err != nil {
		return err
	}
	return printJSON(map[string]any{"published": id.String()})
}
