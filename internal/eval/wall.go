package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// realWallSeed is the hand-authored reasoning-wall corpus (the 28 real distilled
// decisions + 27 blind questions + currency pair). gen-wall reuses it as the
// "real" seed and pads it with synthetic distractors to reach scale, so the
// questions/gold stay genuine while the corpus grows.
//
//go:embed scenarios/wall.json
var realWallSeed []byte

// RealWallSpec returns the embedded hand-authored wall corpus.
func RealWallSpec() (*WallSpec, error) {
	var s WallSpec
	if err := json.Unmarshal(realWallSeed, &s); err != nil {
		return nil, fmt.Errorf("eval: parse embedded wall seed: %w", err)
	}
	return &s, nil
}

// The reasoning-wall experiment (docs/WALL_EXPERIMENT.md).
//
// The scenario harness (Run) measures "overview-sufficiency" by checking whether
// an author-chosen keyword appears in an author-written summary — circular, and
// gameable. The wall harness measures the real question instead: given ONLY the
// overview summaries IOC retrieves, can an *external* LLM answer a blind question,
// and does retrieval surface the CURRENT truth over a SUPERSEDED one?
//
// IOC never calls an LLM, so the judging is split out of this process: WallRun
// builds the corpus, runs overview-only retrieval per question, and emits two
// artifacts — a judging packet (question + retrieved summaries, gold WITHHELD)
// for a blind external judge, and a gold sidecar (reference answer + objective,
// code-measured retrieval facts: gold-ref ranks and current-vs-superseded rank).
// Answer-grounded sufficiency is scored by the external judge against the gold;
// the retrieval/currency facts here need no judge.
//
// Judging rule (learned the hard way, see docs/WALL_EXPERIMENT.md): feed the blind
// judge ONE packet per invocation. Batching several packets into one judge call
// lets it borrow another question's retrieved notes and answer a question whose own
// retrieval missed — inflating the score.

// WallSpec is a reasoning-wall corpus plus blind questions.
type WallSpec struct {
	Name      string         `json:"name"`
	TopK      int            `json:"topk"`
	Build     []Turn         `json:"build"`     // ops that construct the corpus (no "query")
	Questions []WallQuestion `json:"questions"` // blind questions + gold
}

// WallQuestion is one blind question. The judge sees Question + the retrieved
// summaries only; Gold and the *Refs are withheld and used for scoring/measuring.
type WallQuestion struct {
	ID       string    `json:"id"`
	Scope    string    `json:"scope"`              // viewpoint scope name (visibility)
	Question string    `json:"question"`           // what the blind judge is asked
	Gold     string    `json:"gold"`               // reference answer (withheld)
	GoldRefs []string  `json:"goldRefs,omitempty"` // artifact names that hold the answer (withheld)
	Currency *Currency `json:"currency,omitempty"` // optional superseded-vs-current probe
}

// Currency names the two competing artifacts of a supersession: the answer
// should reflect Current; Superseded is the stale truth that must NOT win.
type Currency struct {
	Current    string `json:"current"`
	Superseded string `json:"superseded"`
}

// JudgePacket is all (and only) what a blind judge receives.
type JudgePacket struct {
	ID        string   `json:"id"`
	Question  string   `json:"question"`
	Retrieved []string `json:"retrieved"` // ordered overview summaries (rank 1 first)
}

// WallGold pairs the reference answer with objective, code-measured retrieval
// facts for one question — no LLM needed for these.
type WallGold struct {
	ID           string         `json:"id"`
	Gold         string         `json:"gold"`
	GoldRefRanks map[string]int `json:"gold_ref_ranks,omitempty"` // name -> 1-based rank (0 = not in topK)
	GoldFound    bool           `json:"gold_found"`               // any gold ref in topK
	CurrentRank  int            `json:"current_rank,omitempty"`
	StaleRank    int            `json:"superseded_rank,omitempty"`
	CurrencyOK   *bool          `json:"currency_ok,omitempty"` // current retrieved above superseded
}

// WallReport is the run-level retrieval summary (the judge fills in the
// answer-grounded sufficiency separately).
type WallReport struct {
	Name       string  `json:"name"`
	EmbModel   string  `json:"emb_model"`
	TopK       int     `json:"topk"`
	Questions  int     `json:"questions"`
	GoldRecall float64 `json:"gold_ref_recall_at_topk"` // fraction with a gold ref in topK
	CurrencyN  int     `json:"currency_probes"`
	CurrencyOK int     `json:"currency_ok"` // probes where current outranked superseded
}

// WallRun builds the corpus, runs overview-only retrieval for each question, and
// writes the judging packets and gold sidecar. It returns the retrieval-level
// report (answer scoring happens externally against the gold file). mode/
// hierarchical/coarseK/rerank select the retrieval configuration so the same
// corpus+questions can be measured flat-vector vs hierarchical vs +rerank at scale.
func WallRun(ctx context.Context, e *engine.Engine, spec *WallSpec, packetsW, goldW io.Writer, mode core.QueryMode, hierarchical bool, coarseK int, rerank bool) (*WallReport, error) {
	topK := spec.TopK
	if topK <= 0 {
		topK = 5
	}
	scopes := map[string]core.ID{}
	arts := map[string]core.ID{}

	for i, t := range spec.Build {
		handled, err := applyStructureTurn(ctx, e, i, t, scopes, arts)
		if err != nil {
			return nil, err
		}
		if !handled {
			return nil, fmt.Errorf("build turn %d: %q is not allowed in a wall build (questions are separate)", i, t.Op)
		}
	}

	rep := &WallReport{Name: spec.Name, EmbModel: e.EmbModel(), TopK: topK, Questions: len(spec.Questions)}
	var goldFoundCount int

	for _, q := range spec.Questions {
		scope, err := resolveScopeName(scopes, q.Scope)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}
		_, hits, err := e.Query(ctx, core.Query{
			Scope: scope, Text: q.Question, Detail: core.DetailOverview, TopK: topK,
			Mode: mode, Hierarchical: hierarchical, CoarseK: coarseK, Rerank: rerank,
		})
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.ID, err)
		}

		// rank lookup: artifact ID -> 1-based position in the retrieved set.
		rankByID := make(map[core.ID]int, len(hits))
		summaries := make([]string, len(hits))
		for i, h := range hits {
			rankByID[h.Artifact] = i + 1
			summaries[i] = h.Summary
		}

		packet := JudgePacket{ID: q.ID, Question: q.Question, Retrieved: summaries}
		if err := writeJSONLine(packetsW, packet); err != nil {
			return nil, err
		}

		gold := WallGold{ID: q.ID, Gold: q.Gold}
		if len(q.GoldRefs) > 0 {
			gold.GoldRefRanks = make(map[string]int, len(q.GoldRefs))
			for _, name := range q.GoldRefs {
				id, ok := arts[name]
				if !ok {
					return nil, fmt.Errorf("question %q: unknown goldRef %q", q.ID, name)
				}
				r := rankByID[id] // 0 if absent
				gold.GoldRefRanks[name] = r
				if r > 0 {
					gold.GoldFound = true
				}
			}
		}
		if gold.GoldFound {
			goldFoundCount++
		}
		if q.Currency != nil {
			rep.CurrencyN++
			cur, ok1 := arts[q.Currency.Current]
			stale, ok2 := arts[q.Currency.Superseded]
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("question %q: unknown currency artifact(s)", q.ID)
			}
			gold.CurrentRank = rankByID[cur]
			gold.StaleRank = rankByID[stale]
			// Current "wins" if it is retrieved and either the stale one is absent
			// from topK or ranks strictly below the current one.
			ok := gold.CurrentRank > 0 && (gold.StaleRank == 0 || gold.CurrentRank < gold.StaleRank)
			gold.CurrencyOK = &ok
			if ok {
				rep.CurrencyOK++
			}
		}
		if err := writeJSONLine(goldW, gold); err != nil {
			return nil, err
		}
	}

	if rep.Questions > 0 {
		rep.GoldRecall = float64(goldFoundCount) / float64(rep.Questions)
	}
	return rep, nil
}

// String renders the retrieval-level wall report. Answer-grounded sufficiency is
// NOT here — it is scored by the external blind judge against the gold file.
func (r *WallReport) String() string {
	s := fmt.Sprintf("Wall: %s   (embedder: %s, topK: %d)\n", r.Name, r.EmbModel, r.TopK)
	s += fmt.Sprintf("questions: %d\n", r.Questions)
	s += fmt.Sprintf("gold-ref recall@topK: %.2f  (a gold-bearing artifact was retrieved)\n", r.GoldRecall)
	if r.CurrencyN > 0 {
		s += fmt.Sprintf("currency probes: %d/%d current outranked superseded (retrieval only)\n", r.CurrencyOK, r.CurrencyN)
	}
	s += "NOTE: answer-grounded overview-sufficiency is scored by the blind judge over the\n"
	s += "packets file vs the gold file; these numbers are the objective retrieval layer only.\n"
	if r.EmbModel == "mock-bow" {
		s += "WARN: mock-bow is a lexical proxy — run with the real embedder for wall numbers.\n"
	}
	return s
}

// LoadWallSpec reads a wall spec from JSON.
func LoadWallSpec(path string) (*WallSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read wall spec: %w", err)
	}
	var s WallSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("eval: parse wall spec: %w", err)
	}
	return &s, nil
}

func writeJSONLine(w io.Writer, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(line))
	return err
}
