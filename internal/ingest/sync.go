package ingest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
)

// removeVanished deletes Document chunks whose file path was not seen this run.
func (r *reconciler) removeVanished() error {
	revDir := r.reverseDirCache()
	removed := map[string]bool{}
	for _, sid := range r.descendants(r.rootScope) {
		var del []core.Artifact
		for _, a := range r.byScope[sid] {
			if a.Kind != core.KindDocument {
				continue
			}
			if p := a.Meta["path"]; p != "" && !r.wantPaths[p] {
				del = append(del, a)
				removed[p] = true
			}
		}
		for _, a := range del {
			if err := r.deleteArtifact(a); err != nil {
				return err
			}
			if rd, ok := revDir[sid]; ok {
				r.dirty[rd] = true
			}
		}
	}
	r.st.FilesRemoved = len(removed)
	return nil
}

// pruneEmpty deletes scopes under root that hold no artifacts and no children,
// repeating until stable (a parent may empty after its last child is removed).
func (r *reconciler) pruneEmpty() error {
	for {
		changed := false
		for _, sid := range r.descendants(r.rootScope) {
			if sid == r.rootScope {
				continue
			}
			if len(r.byScope[sid]) > 0 || len(r.childOf[sid]) > 0 {
				continue
			}
			s, ok := r.scopeByID[sid]
			if !ok {
				continue
			}
			if err := r.e.DeleteScope(r.ctx, sid); err != nil {
				return err
			}
			r.removeScope(s)
			r.st.ScopesRemoved++
			changed = true
		}
		if !changed {
			return nil
		}
	}
}

// rollups recomputes the mechanical filename rollup for each changed directory
// scope that still exists (dirs with no direct files keep no rollup).
func (r *reconciler) rollups() error {
	for relDir := range r.dirty {
		sid, ok := r.dirScope[relDir]
		if !ok {
			continue
		}
		if _, alive := r.scopeByID[sid]; !alive {
			continue
		}
		files := append([]string(nil), r.dirFiles[relDir]...)
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		label := relDir
		if label == "." {
			label = filepath.Base(r.root)
		}
		summary := fmt.Sprintf("directory %s; files: %s", filepath.ToSlash(label), strings.Join(files, ", "))
		if err := r.e.RollupScope(r.ctx, sid, summary); err != nil {
			return err
		}
	}
	return nil
}

// --- index maintenance helpers ---

func (r *reconciler) addScope(s core.Scope) {
	r.scopeByID[s.ID] = s
	r.childOf[s.Parent] = append(r.childOf[s.Parent], s)
}

func (r *reconciler) removeScope(s core.Scope) {
	delete(r.scopeByID, s.ID)
	siblings := r.childOf[s.Parent]
	for i, c := range siblings {
		if c.ID == s.ID {
			r.childOf[s.Parent] = append(siblings[:i], siblings[i+1:]...)
			break
		}
	}
}

// descendants returns rootScope and all scope IDs beneath it (BFS).
func (r *reconciler) descendants(root core.ID) []core.ID {
	out := []core.ID{root}
	for i := 0; i < len(out); i++ {
		for _, ch := range r.childOf[out[i]] {
			out = append(out, ch.ID)
		}
	}
	return out
}

// reverseDirCache maps scope ID -> relDir from the dir-scope cache.
func (r *reconciler) reverseDirCache() map[core.ID]string {
	rev := make(map[core.ID]string, len(r.dirScope))
	for rel, id := range r.dirScope {
		rev[id] = rel
	}
	return rev
}

func dropArtifact(arts []core.Artifact, id core.ID) []core.Artifact {
	for i, a := range arts {
		if a.ID == id {
			return append(arts[:i], arts[i+1:]...)
		}
	}
	return arts
}
