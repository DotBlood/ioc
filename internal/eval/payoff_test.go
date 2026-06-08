package eval

// Scope-advise PAYOFF experiment (docs/SCOPE_POLICY.md §8).
//
// Question: if an agent takes a FLAT, multi-topic dump and SPLITS it per
// engine.ScopeStats' mechanical clusters, does descendant-aware retrieval
// (hierarchical / collapsed) on the resulting tree BEAT the flat dump — and how
// close does the mechanical partition get to a hand-authored topical tree?
//
// This needs a REAL embedder (mock-bow is lexical and its ConfidenceFloor is 0).
// Gated on IOC_PAYOFF_EMBED, e.g.:
//
//	IOC_PAYOFF_EMBED=http://127.0.0.1:8088 go test ./internal/eval/ \
//	    -run TestScopeAdvisePayoff -v -count=1 -timeout 600s
//
// It LOGS recall@topK for each layout (it is an experiment, not a pass/fail gate);
// the only hard assertion is that the harness ran and produced numbers.

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/engine"
)

// --- compact distractor corpus (mirrors cmd/ioc gen-wall) so the flat dump is
// large enough that flat retrieval degrades and the tree payoff is visible. ---

var payoffTopics = []string{
	"outbound request timeouts", "retry and backoff policy", "authentication and tokens",
	"rate limiting", "database storage engine", "event delivery semantics",
	"consumer idempotency", "structured logging", "distributed tracing", "service metrics",
	"deployment rollout strategy", "caching layer", "API pagination", "data encryption at rest",
	"backup and restore", "feature flag rollout", "configuration management", "HTTP error status codes",
}

var payoffDecisions = []string{
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

var payoffAspects = []string{"latency", "cost", "security", "testing", "rollback", "monitoring", "capacity", "compatibility", "migration"}

// payoffDistractors produces n synthetic reasoning pushes spread across the topic
// list, with As-names "d<topic>_<j>" (never gold).
func payoffDistractors(n int) []Turn {
	var out []Turn
	c := len(payoffTopics)
	per := (n + c - 1) / c
	emitted := 0
	for i := 0; i < c && emitted < n; i++ {
		topic, decision := payoffTopics[i], payoffDecisions[i]
		for j := 0; j < per && emitted < n; j++ {
			var summary string
			if j == 0 {
				summary = fmt.Sprintf("%s — we decided to %s", topic, decision)
			} else {
				asp := payoffAspects[(j-1)%len(payoffAspects)]
				summary = fmt.Sprintf("%s — %s considerations, note %d", topic, asp, j)
			}
			out = append(out, Turn{Op: "push", As: fmt.Sprintf("d%d_%d", i, j), Kind: "reasoning", Summary: summary})
			emitted++
		}
	}
	return out
}

// payoffRealTopic maps a real artifact's As-name to its hand-authored topical
// session (mirrors cmd/ioc gen-wall's realTopicSession). RT* before R*.
func payoffRealTopic(as string) string {
	switch {
	case strings.HasPrefix(as, "RT"):
		return "runtime"
	case strings.HasPrefix(as, "R"):
		return "retrieval"
	case strings.HasPrefix(as, "S"):
		return "storage"
	default:
		return "direction"
	}
}

// synthRollup builds a mechanical (no-LLM) rollup for a cluster. The MODE is a
// control for the rollup-leak confound: "join" concatenates ALL member summaries
// (so the rollup literally contains the gold summary text — inflates coarse
// routing, NOT realistic); "first" uses one representative member summary (a
// realistic short rollup); "label" uses a content-free placeholder (no routing
// signal — pessimistic bound). Selected by IOC_PAYOFF_ROLLUP (default "first",
// the honest one). Held constant across the mechanical and hand-authored trees so
// the only variable is the PARTITION.
func synthRollup(sess string, summaries []string) string {
	switch os.Getenv("IOC_PAYOFF_ROLLUP") {
	case "join":
		s := strings.Join(summaries, "; ")
		if len(s) > 600 {
			s = s[:600]
		}
		return s
	case "label":
		return "topic cluster " + sess
	default: // "first" — one representative summary, the realistic case
		if len(summaries) > 0 {
			return summaries[0]
		}
		return "topic cluster " + sess
	}
}

func retarget(qs []WallQuestion, scope string) []WallQuestion {
	out := make([]WallQuestion, len(qs))
	for i, q := range qs {
		q.Scope = scope
		out[i] = q
	}
	return out
}

func payoffEngine(t *testing.T, endpoint string) *engine.Engine {
	t.Helper()
	emb, err := embed.NewHTTPEmbedder(endpoint, false)
	require.NoError(t, err)
	e, err := engine.Open(context.Background(), t.TempDir(), emb)
	require.NoError(t, err)
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func runRecall(t *testing.T, endpoint string, spec *WallSpec, hierarchical, collapsed bool, coarseK int) float64 {
	t.Helper()
	e := payoffEngine(t, endpoint)
	rep, err := WallRun(context.Background(), e, spec, io.Discard, io.Discard,
		core.ModeVector, hierarchical, collapsed, coarseK, false, 0, 0)
	require.NoError(t, err)
	return rep.GoldRecall
}

func TestScopeAdvisePayoff(t *testing.T) {
	endpoint := os.Getenv("IOC_PAYOFF_EMBED")
	if endpoint == "" {
		t.Skip("set IOC_PAYOFF_EMBED to a real bge endpoint (e.g. http://127.0.0.1:8088)")
	}
	ctx := context.Background()

	real, err := RealWallSpec()
	require.NoError(t, err)
	var realPushes []Turn
	for _, tn := range real.Build {
		if tn.Op == "push" {
			realPushes = append(realPushes, tn)
		}
	}
	nDist := 90
	if v := os.Getenv("IOC_PAYOFF_DISTRACTORS"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			nDist = n
		}
	}
	pushes := append(append([]Turn{}, realPushes...), payoffDistractors(nDist)...)
	total := len(pushes)
	t.Logf("corpus: %d real + %d distractor = %d artifacts, %d questions", len(realPushes), total-len(realPushes), total, len(real.Questions))

	// --- Layout FLAT: everything in one session, queried from it (vector). ---
	flat := &WallSpec{Name: "payoff-flat", TopK: real.TopK}
	flat.Build = append(flat.Build,
		Turn{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "ioc"},
		Turn{Op: "create_scope", ID: "all", Parent: "wt", Role: "session", Title: "all"})
	for _, p := range pushes {
		p.Scope = "all"
		flat.Build = append(flat.Build, p)
	}
	flat.Questions = retarget(real.Questions, "all")
	recFlat := runRecall(t, endpoint, flat, false, false, 0)

	// --- Reference CEILING: hand-authored topical tree (hierarchical). ---
	hand := buildTreeSpec(t, pushes, real.Questions, func(p Turn) string {
		if strings.HasPrefix(p.As, "d") && len(p.As) > 1 && p.As[1] >= '0' && p.As[1] <= '9' {
			// distractor "dI_J" → session by its topic index I
			return "topic_" + strings.SplitN(p.As[1:], "_", 2)[0]
		}
		return payoffRealTopic(p.As)
	}, real.TopK)
	recHand := runRecall(t, endpoint, hand, true, false, 6)

	// --- Build a flat store ONCE and run the SHIPPED engine.ScopeStats on it to
	// get the mechanical clusters (by artifact ID → As-name). ---
	cl := payoffEngine(t, endpoint)
	scopes := map[string]core.ID{}
	arts := map[string]core.ID{}
	for i, tn := range flat.Build {
		_, berr := applyStructureTurn(ctx, cl, i, tn, scopes, arts)
		require.NoError(t, berr)
	}
	idToName := make(map[core.ID]string, len(arts))
	for name, id := range arts {
		idToName[id] = name
	}

	// Sweep a few thresholds (the FRD §10 open question on tau).
	t.Logf("RESULT  flat(vector)=%.2f   hand-tree(hier)=%.2f", recFlat, recHand)
	for _, tau := range []float64{0.60, 0.68, 0.72, 0.75} {
		st, serr := cl.ScopeStats(ctx, scopes["all"], tau, 2)
		require.NoError(t, serr)

		// Map each cluster (artifact IDs) → a session of As-names.
		assign := func(p Turn) string { return "" }
		clusterOf := map[string]int{}
		for ci, comp := range st.Clusters {
			for _, id := range comp {
				clusterOf[idToName[id]] = ci
			}
		}
		assign = func(p Turn) string { return fmt.Sprintf("c%d", clusterOf[p.As]) }

		mech := buildTreeSpec(t, pushes, real.Questions, assign, real.TopK)
		recMechHier := runRecall(t, endpoint, mech, true, false, 6)
		recMechColl := runRecall(t, endpoint, mech, false, true, 0)
		t.Logf("RESULT  tau=%.2f  clusters=%-3d dispersion=%.2f  mech-tree(hier)=%.2f  mech-tree(collapsed)=%.2f",
			tau, st.ClusterCount, st.Dispersion, recMechHier, recMechColl)
	}

	// Log the DEFAULT-tau path (tau=0 → the shipped ConfidenceFloor default) for
	// reference. NOTE: the hierarchical numbers here are dominated by rollup quality
	// (IOC_PAYOFF_ROLLUP), not the partition — see the rollup-leak control below.
	stDef, derr := cl.ScopeStats(ctx, scopes["all"], 0, 2)
	require.NoError(t, derr)
	defClusterOf := map[string]int{}
	for ci, comp := range stDef.Clusters {
		for _, id := range comp {
			defClusterOf[idToName[id]] = ci
		}
	}
	mechDef := buildTreeSpec(t, pushes, real.Questions, func(p Turn) string { return fmt.Sprintf("c%d", defClusterOf[p.As]) }, real.TopK)
	recDefHier := runRecall(t, endpoint, mechDef, true, false, 6)
	t.Logf("RESULT  DEFAULT-tau=%.2f clusters=%d  mech-tree(hier)=%.2f  (flat=%.2f, hand=%.2f)", stDef.Tau, stDef.ClusterCount, recDefHier, recFlat, recHand)

	require.Greater(t, recFlat, -1.0) // harness produced numbers; verdict is read from the logs
}

// buildTreeSpec constructs a workspace→sessions tree: each push is placed in the
// session named by assign(push); every session gets a synthesized rollup; all
// questions are asked from the workspace viewpoint "ws".
func buildTreeSpec(t *testing.T, pushes []Turn, questions []WallQuestion, assign func(Turn) string, topK int) *WallSpec {
	t.Helper()
	spec := &WallSpec{Name: "payoff-tree", TopK: topK}
	spec.Build = append(spec.Build,
		Turn{Op: "create_scope", ID: "wt", Parent: "root", Role: "worktree", Title: "ioc"},
		Turn{Op: "create_scope", ID: "ws", Parent: "wt", Role: "workspace", Title: "decisions"})

	order := []string{}              // session creation order (deterministic)
	members := map[string][]string{} // session → member summaries
	seen := map[string]bool{}
	for _, p := range pushes {
		sess := assign(p)
		if !seen[sess] {
			spec.Build = append(spec.Build, Turn{Op: "create_scope", ID: sess, Parent: "ws", Role: "session", Title: sess})
			seen[sess] = true
			order = append(order, sess)
		}
		p.Scope = sess
		spec.Build = append(spec.Build, p)
		members[sess] = append(members[sess], p.Summary)
	}
	sort.Strings(order)
	for _, sess := range order {
		spec.Build = append(spec.Build, Turn{Op: "rollup", Scope: sess, Summary: synthRollup(sess, members[sess])})
	}
	spec.Questions = retarget(questions, "ws")
	return spec
}
