package ingest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
)

// reconciler holds the in-memory index of the store (kept consistent with the
// backing store as we create/delete) so a sync runs without repeated full scans.
type reconciler struct {
	ctx       context.Context
	e         Store
	root      string
	rootScope core.ID
	opt       Options

	scopeByID map[core.ID]core.Scope
	childOf   map[core.ID][]core.Scope    // parent ID -> child scopes
	byScope   map[core.ID][]core.Artifact // scope ID -> artifacts
	dirScope  map[string]core.ID          // relDir -> scope ID (cache; "." = root)
	dirFiles  map[string][]string         // relDir -> filenames present this run
	dirty     map[string]bool             // relDirs whose rollup must be recomputed
	wantPaths map[string]bool             // relSlash paths seen this run
	st        Stats
}

// buildIndex loads all scopes and artifacts once into the in-memory index.
func (r *reconciler) buildIndex() error {
	scopes, err := r.e.ListScopes(r.ctx)
	if err != nil {
		return err
	}
	r.scopeByID = make(map[core.ID]core.Scope, len(scopes))
	r.childOf = make(map[core.ID][]core.Scope)
	for _, s := range scopes {
		r.scopeByID[s.ID] = s
		r.childOf[s.Parent] = append(r.childOf[s.Parent], s)
	}
	arts, err := r.e.ListArtifacts(r.ctx)
	if err != nil {
		return err
	}
	r.byScope = make(map[core.ID][]core.Artifact)
	for _, a := range arts {
		r.byScope[a.Scope] = append(r.byScope[a.Scope], a)
	}
	return nil
}

// walk visits the tree, reconciling each text file against the store.
func (r *reconciler) walk() error {
	return filepath.WalkDir(r.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(r.root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".ioc")) {
				return filepath.SkipDir
			}
			return nil
		}
		return r.reconcileFile(path, rel)
	})
}

// reconcileFile syncs one file: unchanged (sig match) → skip; else delete its
// stale chunks and push fresh ones.
func (r *reconciler) reconcileFile(path, rel string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > MaxFileBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if isBinary(data) {
		return nil
	}
	relSlash := filepath.ToSlash(rel)
	relDir := filepath.Dir(rel)
	dirScope, err := r.ensureDirScope(relDir)
	if err != nil {
		return err
	}
	r.wantPaths[relSlash] = true
	r.dirFiles[relDir] = append(r.dirFiles[relDir], filepath.Base(rel))

	lang := DetectLanguage(relSlash)
	sig := Sig(data, lang, r.opt.MaxChars, r.opt.Overlap)
	chunks := SplitLang(string(data), lang, r.opt.MaxChars, r.opt.Overlap)
	existing := r.chunksForPath(dirScope, relSlash)
	// Skip only if the stored chunk set is COMPLETE for this sig (right count,
	// every chunk's sig matches, indices 0..n-1 all present). Trusting a single
	// chunk's sig would mask a partial prior write (a mid-file push failure left
	// some chunks with the new sig) and the file would stay under-indexed forever.
	if chunksComplete(existing, sig, len(chunks)) {
		r.st.FilesUnchanged++
		return nil
	}
	wasUpdate := len(existing) > 0
	for _, a := range existing {
		if err := r.deleteArtifact(a); err != nil {
			return err
		}
	}
	for i, c := range chunks {
		if err := r.pushChunk(dirScope, relSlash, c, i, sig); err != nil {
			return err
		}
	}
	r.dirty[relDir] = true
	if wasUpdate {
		r.st.FilesUpdated++
	} else {
		r.st.FilesAdded++
	}
	return nil
}

// chunksComplete reports whether the existing chunk set is a COMPLETE, consistent
// representation of a file that produces n chunks under sig: exactly n chunks, all
// carrying sig, with chunk indices 0..n-1 each present once. A partial prior write
// (fewer chunks, a stale/leftover chunk, or a missing index) returns false so the
// file is fully re-chunked rather than wrongly skipped as "unchanged".
func chunksComplete(existing []core.Artifact, sig string, n int) bool {
	if len(existing) != n {
		return false
	}
	if n == 0 {
		return true
	}
	seen := make([]bool, n)
	for _, a := range existing {
		if a.Meta["sig"] != sig {
			return false
		}
		idx, err := strconv.Atoi(a.Meta["chunk"])
		if err != nil || idx < 0 || idx >= n || seen[idx] {
			return false
		}
		seen[idx] = true
	}
	return true
}

// chunksForPath returns the Document chunks in scope that belong to relSlash.
func (r *reconciler) chunksForPath(scope core.ID, relSlash string) []core.Artifact {
	var out []core.Artifact
	for _, a := range r.byScope[scope] {
		if a.Kind == core.KindDocument && a.Meta["path"] == relSlash {
			out = append(out, a)
		}
	}
	return out
}

// pushChunk writes one chunk artifact and updates the index.
func (r *reconciler) pushChunk(scope core.ID, relSlash string, c Chunk, idx int, sig string) error {
	lines := fmt.Sprintf("%d-%d", c.StartLine, c.EndLine)
	summary := fmt.Sprintf("%s:%s — %s", relSlash, lines, FirstLine(c.Text, 80))
	a, err := r.e.Push(r.ctx, core.PushRequest{
		Scope:     scope,
		Kind:      core.KindDocument,
		Tier:      core.TierWorktree,
		Summary:   summary,
		EmbedText: c.Text,
		Content:   []byte(c.Text),
		Meta: map[string]string{
			"path":  relSlash,
			"lines": lines,
			"chunk": fmt.Sprintf("%d", idx),
			"sig":   sig,
		},
	})
	if err != nil {
		return err
	}
	r.byScope[scope] = append(r.byScope[scope], a)
	r.st.ChunksAdded++
	return nil
}

// deleteArtifact removes an artifact from the store and the index.
func (r *reconciler) deleteArtifact(a core.Artifact) error {
	if err := r.e.DeleteArtifact(r.ctx, a.ID); err != nil {
		return err
	}
	r.byScope[a.Scope] = dropArtifact(r.byScope[a.Scope], a.ID)
	r.st.ChunksRemoved++
	return nil
}

// ensureDirScope returns the scope mirroring relDir, reusing an existing child
// scope by (parent, title) before creating a new one. relDir is OS-form.
func (r *reconciler) ensureDirScope(relDir string) (core.ID, error) {
	if id, ok := r.dirScope[relDir]; ok {
		return id, nil
	}
	parent, err := r.ensureDirScope(filepath.Dir(relDir))
	if err != nil {
		return core.NilID, err
	}
	base := filepath.Base(relDir)
	if s, ok := r.findChildScope(parent, base); ok {
		r.dirScope[relDir] = s.ID
		return s.ID, nil
	}
	s, err := r.e.CreateScope(r.ctx, parent, core.RoleWorkspace, base)
	if err != nil {
		return core.NilID, err
	}
	r.addScope(s)
	r.dirScope[relDir] = s.ID
	r.st.ScopesCreated++
	return s.ID, nil
}

// findChildScope returns the workspace child of parent named title, choosing the
// lowest ULID deterministically if legacy duplicates exist.
func (r *reconciler) findChildScope(parent core.ID, title string) (core.Scope, bool) {
	var best core.Scope
	found := false
	for _, s := range r.childOf[parent] {
		if s.Role == core.RoleWorkspace && s.Title == title {
			if !found || s.ID.String() < best.ID.String() {
				best, found = s, true
			}
		}
	}
	return best, found
}
