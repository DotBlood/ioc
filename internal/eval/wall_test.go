package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

func decodeJSONL[T any](t *testing.T, b []byte) []T {
	t.Helper()
	var out []T
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, v)
	}
	return out
}

// WallRun must (a) emit one packet and one gold line per question, (b) withhold
// the gold answer from the packet, and (c) compute the objective retrieval facts
// — gold-ref ranks and the current-vs-superseded currency rank.
func TestWallRun(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer e.Close()

	spec := &WallSpec{
		Name: "wall-test",
		TopK: 5,
		Build: []Turn{
			{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "ioc"},
			{Op: "push", Scope: "wt", As: "cur", Kind: "reasoning",
				Summary: "current design uses a thin slice of mini-summary plus embedding, no knowledge graph"},
			{Op: "push", Scope: "wt", As: "old", Kind: "reasoning",
				Summary: "old design used a knowledge graph engine with nodes and edges"},
			{Op: "push", Scope: "wt", As: "filler", Kind: "reasoning",
				Summary: "embeddings are stored in an append-only file with an fsync on flush"},
		},
		Questions: []WallQuestion{
			{ID: "q-durable", Scope: "wt", GoldRefs: []string{"filler"},
				Question: "how are embeddings made durable on disk", Gold: "append-only file, fsync on flush"},
			{ID: "q-currency", Scope: "wt", GoldRefs: []string{"cur"},
				Question: "does the current design use a knowledge graph", Gold: "no, thin slice of summary plus embedding",
				Currency: &Currency{Current: "cur", Superseded: "old"}},
		},
	}

	var packets, gold bytes.Buffer
	rep, err := WallRun(ctx, e, spec, &packets, &gold, core.ModeVector, false, 0, false, 0)
	if err != nil {
		t.Fatalf("WallRun: %v", err)
	}

	pkts := decodeJSONL[JudgePacket](t, packets.Bytes())
	golds := decodeJSONL[WallGold](t, gold.Bytes())
	if len(pkts) != 2 || len(golds) != 2 {
		t.Fatalf("expected 2 packets and 2 gold lines, got %d and %d", len(pkts), len(golds))
	}

	// (b) the packet must NOT carry the gold answer field (retrieved summaries are
	// expected — they ARE the corpus summaries; only the gold answer is withheld).
	for _, p := range pkts {
		raw, _ := json.Marshal(p)
		if strings.Contains(string(raw), "\"gold\"") {
			t.Fatalf("packet must not carry a gold field: %s", raw)
		}
		if len(p.Retrieved) == 0 {
			t.Fatalf("packet %q has no retrieved summaries", p.ID)
		}
	}

	// (c) objective retrieval facts.
	byID := map[string]WallGold{}
	for _, g := range golds {
		byID[g.ID] = g
	}
	if g := byID["q-durable"]; !g.GoldFound || g.GoldRefRanks["filler"] < 1 {
		t.Fatalf("q-durable: gold ref should be retrieved, got %+v", g)
	}
	cg := byID["q-currency"]
	if cg.CurrencyOK == nil {
		t.Fatalf("q-currency: currency_ok not computed")
	}
	if cg.CurrentRank < 1 {
		t.Fatalf("q-currency: current artifact should be retrieved, got rank %d", cg.CurrentRank)
	}
	// currency_ok must agree with the ranks it was derived from.
	wantOK := cg.CurrentRank > 0 && (cg.StaleRank == 0 || cg.CurrentRank < cg.StaleRank)
	if *cg.CurrencyOK != wantOK {
		t.Fatalf("q-currency: currency_ok=%v inconsistent with ranks cur=%d stale=%d", *cg.CurrencyOK, cg.CurrentRank, cg.StaleRank)
	}

	if rep.Questions != 2 || rep.CurrencyN != 1 {
		t.Fatalf("report mismatch: %+v", rep)
	}
}

// A "query" op is not allowed in a wall build (questions are a separate block).
func TestWallRun_RejectsQueryInBuild(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer e.Close()

	spec := &WallSpec{
		Name:  "bad",
		Build: []Turn{{Op: "query", Scope: "root", Text: "x"}},
	}
	var p, g bytes.Buffer
	if _, err := WallRun(ctx, e, spec, &p, &g, core.ModeVector, false, 0, false, 0); err == nil {
		t.Fatal("expected an error for a query op in the build")
	}
}

// WallRun must honor hierarchical retrieval: with rollups on sub-scopes and the
// query viewpoint at the parent, a gold artifact living in a descendant session
// (invisible to a flat bottom-up query) is found via coarse→fine routing.
func TestWallRun_Hierarchical(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, t.TempDir(), embed.NewMockEmbedder(64))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer e.Close()

	spec := &WallSpec{
		Name: "wall-hier-test",
		TopK: 5,
		Build: []Turn{
			{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "ioc"},
			{Op: "create_scope", ID: "ws", Parent: "wt", Role: "workspace", Title: "decisions"},
			{Op: "create_scope", ID: "s0", Parent: "ws", Role: "session", Title: "storage"},
			{Op: "push", Scope: "s0", As: "durable", Kind: "reasoning",
				Summary: "embeddings are durable via an append-only file with an fsync on flush"},
			{Op: "rollup", Scope: "s0", Summary: "storage and durability of embeddings and content"},
			{Op: "create_scope", ID: "s1", Parent: "ws", Role: "session", Title: "runtime"},
			{Op: "push", Scope: "s1", As: "daemon", Kind: "reasoning",
				Summary: "a single daemon owns the store and serves clients over a loopback protocol"},
			{Op: "rollup", Scope: "s1", Summary: "runtime daemon and client protocol"},
		},
		Questions: []WallQuestion{
			{ID: "q-durable", Scope: "ws", GoldRefs: []string{"durable"},
				Question: "how are embeddings made durable", Gold: "append-only file, fsync on flush"},
		},
	}
	var packets, gold bytes.Buffer
	rep, err := WallRun(ctx, e, spec, &packets, &gold, core.ModeHybrid, true, 0, false, 0)
	if err != nil {
		t.Fatalf("WallRun hierarchical: %v", err)
	}
	golds := decodeJSONL[WallGold](t, gold.Bytes())
	if len(golds) != 1 || !golds[0].GoldFound {
		t.Fatalf("hierarchical retrieval should find the gold artifact in a descendant session: %+v", golds)
	}
	if rep.GoldRecall != 1.0 {
		t.Fatalf("expected gold recall 1.0, got %v", rep.GoldRecall)
	}
}
