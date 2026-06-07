package eval

import "github.com/DotBlood/ioc/internal/core"

// Probe is the minimal per-question signal the abstention metric needs: the top two
// scores on the in-force signal (cosine, or rerank when Reranked) and the hit count.
// Synthetic hits are rebuilt from these so the metric runs every probe through the SAME
// core.Decide as production — the metric and the live weak_match verdict cannot diverge.
type Probe struct {
	NumHits  int
	Top1     float64
	Top2     float64
	Reranked bool // Top1/Top2 are rerank scores (the rerank floor governs), not cosine
}

// toHits rebuilds the minimal hit list core.Decide inspects (it only reads hits[0] and
// hits[1]). For a reranked probe the scores go into RerankScore so Decide takes the
// rerank path (RerankFloor, margin gate disabled); otherwise into the cosine Score.
func (p Probe) toHits() []core.Hit {
	if p.NumHits == 0 {
		return nil
	}
	mk := func(s float64) core.Hit {
		if p.Reranked {
			rs := s
			return core.Hit{RerankScore: &rs}
		}
		return core.Hit{Score: s}
	}
	hits := []core.Hit{mk(p.Top1)}
	if p.NumHits > 1 {
		hits = append(hits, mk(p.Top2))
	}
	return hits
}

// AbstentionReport quantifies the two abstention error classes over a probe set split
// into PRESENT (the answer is in the corpus) and ABSENT (it is not), under one Confidence
// config. It is the number floors are tuned against.
type AbstentionReport struct {
	PresentN    int     `json:"present_n"`
	AbsentN     int     `json:"absent_n"`
	PresentWeak int     `json:"present_weak"` // present probes flagged weak_match
	AbsentWeak  int     `json:"absent_weak"`  // absent probes flagged weak_match (the desired outcome)
	// FalseNegRate = present probes WRONGLY flagged weak / PresentN — real answers suppressed.
	FalseNegRate float64 `json:"false_neg_rate"`
	// FalsePosRate = absent probes NOT flagged weak / AbsentN — a fake "answered" when the
	// system should have abstained. This is the error the user wants driven to ~0.
	FalsePosRate float64 `json:"false_pos_rate"`
	RankedBy     string  `json:"ranked_by"` // "cosine" | "rerank" — the signal in force
}

// Abstention scores PRESENT vs ABSENT probes through core.Decide under conf, so it
// reflects exactly what production would flag. Pass present/absent probes captured at the
// same signal (all cosine, or all rerank) and the matching conf.
func Abstention(present, absent []Probe, conf core.Confidence) AbstentionReport {
	r := AbstentionReport{PresentN: len(present), AbsentN: len(absent), RankedBy: "cosine"}
	for _, p := range present {
		d := core.Decide(p.toHits(), conf)
		if d.WeakMatch {
			r.PresentWeak++
		}
		r.RankedBy = d.RankedBy
	}
	for _, p := range absent {
		d := core.Decide(p.toHits(), conf)
		if d.WeakMatch {
			r.AbsentWeak++
		}
		r.RankedBy = d.RankedBy
	}
	if r.PresentN > 0 {
		r.FalseNegRate = float64(r.PresentWeak) / float64(r.PresentN)
	}
	if r.AbsentN > 0 {
		r.FalsePosRate = float64(r.AbsentN-r.AbsentWeak) / float64(r.AbsentN)
	}
	return r
}
