// Package engine wires the storage, embedding, and search layers into the
// IOC public API. It is the only package that depends on all the others.
package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/embed"
	"github.com/DotBlood/ioc/internal/storage"
)

// Engine is the embeddable IOC runtime. The external LLM writes summaries via
// Push and reads via Query/Drill; IOC stores, embeds, and serves them.
type Engine struct {
	dir      string
	meta     *storage.Meta
	cas      *storage.CAS
	emb      *storage.EmbeddingStore // opened lazily once the embedding dim is known
	embedder embed.Embedder
	reranker embed.Reranker // optional cross-encoder for last-mile rerank (nil = off)

	box *storage.Box // at-rest encryption (nil = off)

	// modelMu guards the one-time reconciliation of the embedder model against
	// the persisted emb_model (so reads, which run concurrently, don't race).
	modelMu sync.Mutex
	modelOK bool
}

// Option configures an Engine at Open time.
type Option func(*openCfg)

// openCfg accumulates Open-time options before the Engine is constructed.
type openCfg struct {
	reranker    embed.Reranker
	key         []byte
	keyExplicit bool
}

// WithReranker attaches a cross-encoder reranker (used when Query.Rerank is set).
func WithReranker(r embed.Reranker) Option { return func(c *openCfg) { c.reranker = r } }

// WithEncryptionKey enables at-rest encryption with a raw 32-byte key, overriding
// the environment (IOC_ENCRYPTION_KEY / _KEYFILE). A nil key means "off" explicitly.
func WithEncryptionKey(key []byte) Option {
	return func(c *openCfg) { c.key = key; c.keyExplicit = true }
}

// Open opens (creating if needed) an IOC repository at dir, using embedder for
// summary embeddings. Options can attach extras like a reranker.
func Open(_ context.Context, dir string, embedder embed.Embedder, opts ...Option) (*Engine, error) {
	if embedder == nil {
		return nil, fmt.Errorf("engine: nil embedder")
	}
	// 0o700: the data dir holds the whole memory (meta.db, CAS, emb.dat). Owner-only
	// is the strongest single confidentiality lever — even if an inner file were
	// world-readable, a 0o700 parent blocks other local users from traversing in.
	// Tighten a pre-existing dir too (best-effort; no-op on Windows).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("engine: mkdir: %w", err)
	}
	_ = os.Chmod(dir, 0o700)

	// Resolve the at-rest encryption key (explicit option beats env). Build the box
	// BEFORE opening storage so meta/cas/emb get it.
	cfg := openCfg{}
	for _, opt := range opts {
		opt(&cfg)
	}
	key := cfg.key
	if !cfg.keyExplicit {
		var lerr error
		if key, lerr = storage.LoadKey(); lerr != nil {
			return nil, lerr
		}
	}
	var box *storage.Box
	if len(key) > 0 {
		var berr error
		if box, berr = storage.NewBox(key); berr != nil {
			return nil, berr
		}
	}

	meta, err := storage.OpenMeta(filepath.Join(dir, "meta.db"), box)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		dir:      dir,
		meta:     meta,
		cas:      storage.NewCAS(filepath.Join(dir, "cas"), box),
		embedder: embedder,
		reranker: cfg.reranker,
		box:      box,
	}

	// Encryption sentinel matrix (fail-closed, no silent mixing). The "enc" config
	// value is always plaintext so it is readable without the key.
	if err := e.checkEncryptionSentinel(); err != nil {
		e.Close()
		return nil, err
	}
	if box.Enabled() {
		fmt.Fprintln(os.Stderr, "ioc: at-rest encryption ENABLED (AES-256-GCM) — losing the key means losing the data; there is no recovery")
	}

	// Refuse to open a store with an incompatible embedder. A store records its
	// embedding dimension (emb_dims) and, once written, its embedder model
	// (emb_model). Mixing embedders silently compares vectors across different
	// spaces and returns garbage — the worst case is two embedders that share a
	// dimension (e.g. the 384-dim mock vs bge-small), which the dim check alone
	// would miss; the model check catches it.
	if v, ok := meta.GetConfig("emb_dims"); ok {
		dims, convErr := strconv.Atoi(v)
		if convErr == nil {
			if d := embedder.Dims(); d > 0 && d != dims {
				e.Close()
				return nil, fmt.Errorf("engine: open %s: %w: store is %d-dim but embedder is %d-dim (one -dir = one embedder)", dir, core.ErrInvalidInput, dims, d)
			}
			if err := e.openEmb(dims); err != nil {
				e.Close()
				return nil, err
			}
		}
	} else if d := embedder.Dims(); d > 0 {
		if err := e.openEmb(d); err != nil {
			e.Close()
			return nil, err
		}
	}
	// Eager model check when the embedder reports its model up front (e.g. mock);
	// HTTP embedders report "" until the first call, so they are checked lazily in
	// reconcileModel (first embed). e.Close() (not meta.Close()) so the now-open
	// embedding-store file handle is released on the refuse path.
	if m := embedder.Model(); m != "" {
		if stored, ok := meta.GetConfig("emb_model"); ok && stored != m {
			e.Close()
			return nil, fmt.Errorf("engine: open %s: %w: store built with embedder %q, got %q (one -dir = one embedder)", dir, core.ErrInvalidInput, stored, m)
		}
	}
	return e, nil
}

const encSentinelValue = "aes256gcm"

// checkEncryptionSentinel enforces the fail-closed key/format matrix: an encrypted
// store needs the key; a non-empty plaintext store must NOT be opened with a key
// (no in-place migration); a brand-new store with a key is marked encrypted.
func (e *Engine) checkEncryptionSentinel() error {
	_, hasEnc := e.meta.GetConfig("enc") // "enc" is an always-plaintext config key
	enabled := e.box.Enabled()
	switch {
	case hasEnc && !enabled:
		return fmt.Errorf("engine: open %s: %w: store is encrypted; set %s to open it", e.dir, core.ErrInvalidInput, storage.EncryptionKeyEnv)
	case !hasEnc && enabled:
		if !e.meta.IsEmpty() {
			return fmt.Errorf("engine: open %s: %w: store is plaintext but an encryption key is set (no in-place migration)", e.dir, core.ErrInvalidInput)
		}
		if err := e.meta.PutConfig("enc", encSentinelValue); err != nil {
			return err
		}
	}
	return nil
}

// reconcileModel persists the embedder's model on first use, or refuses if it
// disagrees with the model the store was built with. It is safe to call from
// concurrent read paths (guarded by modelMu; a no-op after the first success).
func (e *Engine) reconcileModel() error {
	e.modelMu.Lock()
	defer e.modelMu.Unlock()
	if e.modelOK {
		return nil
	}
	m := e.embedder.Model()
	if m == "" {
		return nil // model not known yet (HTTP embedder before its first response)
	}
	stored, ok := e.meta.GetConfig("emb_model")
	if !ok {
		if err := e.meta.PutConfig("emb_model", m); err != nil {
			return err
		}
		e.modelOK = true
		return nil
	}
	if stored != m {
		return fmt.Errorf("engine: %w: store built with embedder %q but %q is in use (one -dir = one embedder)", core.ErrInvalidInput, stored, m)
	}
	e.modelOK = true
	return nil
}

// Close releases resources.
func (e *Engine) Close() error {
	if e.emb != nil {
		if err := e.emb.Close(); err != nil {
			return err
		}
	}
	return e.meta.Close()
}

func (e *Engine) openEmb(dims int) error {
	es, err := storage.OpenEmbeddingStore(filepath.Join(e.dir, "emb.dat"), dims, e.box)
	if err != nil {
		return err
	}
	e.emb = es
	return e.meta.PutConfig("emb_dims", strconv.Itoa(dims))
}

func (e *Engine) ensureEmb(dims int) error {
	if e.emb == nil {
		return e.openEmb(dims)
	}
	if e.emb.Dims() != dims {
		return fmt.Errorf("engine: embedding dim mismatch: store=%d new=%d", e.emb.Dims(), dims)
	}
	return nil
}

// embedText embeds a single document/passage into a vector.
func (e *Engine) embedText(ctx context.Context, text string) ([]float32, error) {
	vec, err := one(e.embedder.Embed(ctx, []string{text}))
	if err != nil {
		return nil, err
	}
	if err := e.reconcileModel(); err != nil {
		return nil, err
	}
	return vec, nil
}

// embedQuery embeds a single search query (instruction-prefixed for bge).
func (e *Engine) embedQuery(ctx context.Context, text string) ([]float32, error) {
	vec, err := one(e.embedder.EmbedQuery(ctx, []string{text}))
	if err != nil {
		return nil, err
	}
	if err := e.reconcileModel(); err != nil {
		return nil, err
	}
	return vec, nil
}

func one(batch [][]float32, err error) ([]float32, error) {
	if err != nil {
		return nil, err
	}
	if len(batch) != 1 {
		return nil, fmt.Errorf("engine: embedder returned %d vectors for 1 text", len(batch))
	}
	return batch[0], nil
}

// storeSummaryEmbedding embeds summary text and appends it to the vector store.
func (e *Engine) storeSummaryEmbedding(ctx context.Context, summary string) (core.EmbeddingRef, error) {
	vec, err := e.embedText(ctx, summary)
	if err != nil {
		return 0, err
	}
	// Defense-in-depth (V8): never persist a non-finite vector — a NaN/Inf would
	// silently corrupt cosine for every future query. The HTTP embedder already
	// validates its responses; this guards any Embedder implementation.
	for i, c := range vec {
		if math.IsNaN(float64(c)) || math.IsInf(float64(c), 0) {
			return 0, fmt.Errorf("engine: %w: embedding component %d is non-finite", core.ErrInvalidInput, i)
		}
	}
	if err := e.ensureEmb(len(vec)); err != nil {
		return 0, err
	}
	return e.emb.Put(vec)
}

// CreateScope creates a scope under parent (zero parent => root).
func (e *Engine) CreateScope(_ context.Context, parent core.ID, role core.Role, title string) (core.Scope, error) {
	if !parent.IsZero() {
		if _, err := e.meta.GetScope(parent); err != nil {
			return core.Scope{}, fmt.Errorf("engine: parent scope: %w", err)
		}
	}
	s := core.Scope{
		ID:        core.NewID(),
		Parent:    parent,
		Role:      role,
		Title:     title,
		Version:   1,
		CreatedAt: time.Now(),
	}
	if err := e.meta.PutScope(s); err != nil {
		return core.Scope{}, err
	}
	return s, nil
}

// Push writes content + a summary into IOC. The summary is REQUIRED and is the
// text that gets embedded; full content (if any) goes to CAS.
func (e *Engine) Push(ctx context.Context, r core.PushRequest) (core.Artifact, error) {
	if r.Summary == "" {
		return core.Artifact{}, fmt.Errorf("engine: push: %w: empty summary", core.ErrInvalidInput)
	}
	if _, err := e.meta.GetScope(r.Scope); err != nil {
		return core.Artifact{}, fmt.Errorf("engine: push: scope: %w", err)
	}
	tier := r.Tier
	if tier == 0 {
		tier = core.TierWorkspace
	}
	kind := r.Kind
	if kind == 0 {
		kind = core.KindInsight
	}

	// Provenance is engine-asserted, not caller-asserted (V17): strip any
	// Meta["trust"] a caller tried to set, then write it ONLY from the dedicated
	// PushRequest.Trust field — so an external ioc_push cannot forge "ingested"
	// (or launder ingested content as authored). Only allocate when trust is
	// involved, and never mutate the caller's map.
	meta := r.Meta
	if _, forged := meta["trust"]; forged || r.Trust != "" {
		m := make(map[string]string, len(r.Meta)+1)
		for k, v := range r.Meta {
			if k != "trust" {
				m[k] = v
			}
		}
		if r.Trust != "" {
			m["trust"] = r.Trust
		}
		meta = m
		if len(meta) == 0 {
			meta = nil
		}
	}

	var content core.ContentHash
	if r.Content != nil {
		h, err := e.cas.StoreBytes(ctx, r.Content)
		if err != nil {
			return core.Artifact{}, err
		}
		content = h
	}
	// Embed EmbedText if provided (file chunks embed raw chunk text, keeping
	// Summary as a short display label); otherwise embed the Summary itself.
	embedTarget := r.Summary
	if r.EmbedText != "" {
		embedTarget = r.EmbedText
	}
	ref, err := e.storeSummaryEmbedding(ctx, embedTarget)
	if err != nil {
		return core.Artifact{}, err
	}
	// Validate the superseded targets BEFORE writing the new artifact, so a bad id
	// fails the whole push rather than leaving a half-applied supersession.
	superseded, err := e.loadSupersedeTargets(r.Supersedes)
	if err != nil {
		return core.Artifact{}, fmt.Errorf("engine: push: %w", err)
	}
	// Validate edge targets BEFORE writing too, so a bad relation aborts the whole
	// push rather than leaving a half-applied artifact.
	if err := e.validateRelationTargets(r.Relations); err != nil {
		return core.Artifact{}, fmt.Errorf("engine: push: %w", err)
	}

	a := core.Artifact{
		ID:          core.NewID(),
		Scope:       r.Scope,
		Kind:        kind,
		Tier:        tier,
		Summary:     r.Summary,
		EmbRef:      ref,
		Content:     content,
		DerivedFrom: unionIDs(r.DerivedFrom, r.Supersedes), // lineage records what it replaced
		Published:   r.Publish,
		Meta:        meta,
		CreatedAt:   time.Now(),
	}
	if err := e.meta.PutArtifact(a); err != nil {
		return core.Artifact{}, err
	}
	if err := e.markSupersededBy(superseded, a.ID); err != nil {
		return core.Artifact{}, err
	}
	if err := e.createPushEdges(a.ID, r.Relations); err != nil {
		return core.Artifact{}, err
	}
	return a, nil
}

// loadSupersedeTargets validates that every id exists, returning the artifacts (so
// a bad id aborts the whole write before anything is persisted).
func (e *Engine) loadSupersedeTargets(ids []core.ID) ([]core.Artifact, error) {
	out := make([]core.Artifact, 0, len(ids))
	for _, sid := range ids {
		a, err := e.meta.GetArtifact(sid)
		if err != nil {
			return nil, fmt.Errorf("supersedes %s: %w", sid, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// markSupersededBy sets SupersededBy=newID on each target and persists it
// (append-only: targets stay in the store, just excluded from the current view).
func (e *Engine) markSupersededBy(targets []core.Artifact, newID core.ID) error {
	for _, old := range targets {
		old.SupersededBy = newID
		if err := e.meta.PutArtifact(old); err != nil {
			return err
		}
	}
	return nil
}

// Supersede marks old as replaced by replacement (post-hoc supersession, the
// batch/assist path; Push handles the at-write path via PushRequest.Supersedes).
// Append-only: old is kept and merely excluded from the default current view.
func (e *Engine) Supersede(_ context.Context, old, replacement core.ID) error {
	if old == replacement {
		return fmt.Errorf("engine: supersede: %w: an artifact cannot supersede itself", core.ErrInvalidInput)
	}
	if _, err := e.meta.GetArtifact(replacement); err != nil {
		return fmt.Errorf("engine: supersede: replacement %s: %w", replacement, err)
	}
	a, err := e.meta.GetArtifact(old)
	if err != nil {
		return fmt.Errorf("engine: supersede: %s: %w", old, err)
	}
	a.SupersededBy = replacement
	return e.meta.PutArtifact(a)
}

// unionIDs concatenates two ID slices, dropping zero and duplicate IDs, order-stable.
func unionIDs(a, b []core.ID) []core.ID {
	seen := make(map[core.ID]bool, len(a)+len(b))
	var out []core.ID
	for _, id := range a {
		if id.IsZero() || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, id := range b {
		if id.IsZero() || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// Trace returns the recorded context a query saw.
func (e *Engine) Trace(_ context.Context, queryID core.ID) (core.TraceRecord, error) {
	return e.meta.GetTrace(queryID)
}

// EmbModel returns the embedder's model identifier.
func (e *Engine) EmbModel() string { return e.embedder.Model() }
