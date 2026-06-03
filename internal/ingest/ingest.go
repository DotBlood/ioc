package ingest

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DotBlood/ioc/internal/core"
	"github.com/DotBlood/ioc/internal/engine"
)

// MaxFileBytes caps the size of a file we will ingest; larger files are skipped.
const MaxFileBytes = 512 * 1024

// skipDirs are directory names never descended into.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"bin": true, "dist": true, "build": true,
	".venv": true, "venv": true, "__pycache__": true,
	".idea": true, ".vscode": true,
}

// Options tunes an ingest run.
type Options struct {
	MaxChars int // chunk window size in chars (0 => DefaultMaxChars)
	Overlap  int // chunk overlap in chars (<0 => DefaultOverlap)
}

// Stats summarizes an ingest run.
type Stats struct {
	Files   int `json:"files"`
	Chunks  int `json:"chunks"`
	Skipped int `json:"skipped"`
	Scopes  int `json:"scopes"`
}

// Ingest walks root, mirrors its directory tree into nested scopes under
// rootScope, and writes each text file as line-aligned KindDocument chunks
// (the raw chunk text is embedded; Summary is a short path:lines label). Each
// directory scope gets a mechanical rollup listing its files so hierarchical
// retrieval can route by the file tree.
func Ingest(ctx context.Context, e *engine.Engine, root string, rootScope core.ID, opt Options) (Stats, error) {
	var st Stats
	root = filepath.Clean(root)
	dirScopes := map[string]core.ID{".": rootScope}
	dirFiles := map[string][]string{}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".ioc")) {
				return filepath.SkipDir
			}
			return nil
		}
		relDir := filepath.Dir(rel)
		scopeID, scErr := ensureDirScope(ctx, e, dirScopes, relDir)
		if scErr != nil {
			return scErr
		}
		n, ferr := ingestFile(ctx, e, path, filepath.ToSlash(rel), scopeID, opt)
		if ferr != nil {
			return ferr
		}
		if n == 0 {
			st.Skipped++
			return nil
		}
		st.Files++
		st.Chunks += n
		dirFiles[relDir] = append(dirFiles[relDir], d.Name())
		return nil
	})
	if walkErr != nil {
		return st, walkErr
	}
	st.Scopes = len(dirScopes)

	// Mechanical per-directory rollups (filenames) so coarse→fine routing works.
	for relDir, files := range dirFiles {
		sort.Strings(files)
		label := relDir
		if label == "." {
			label = filepath.Base(root)
		}
		summary := fmt.Sprintf("directory %s; files: %s", filepath.ToSlash(label), strings.Join(files, ", "))
		if err := e.RollupScope(ctx, dirScopes[relDir], summary); err != nil {
			return st, err
		}
	}
	return st, nil
}

// ensureDirScope returns the scope mirroring relDir, creating the missing
// ancestor chain under the cached root. relDir is in OS form ("." = root).
func ensureDirScope(ctx context.Context, e *engine.Engine, cache map[string]core.ID, relDir string) (core.ID, error) {
	if id, ok := cache[relDir]; ok {
		return id, nil
	}
	parent := filepath.Dir(relDir)
	parentID, err := ensureDirScope(ctx, e, cache, parent)
	if err != nil {
		return core.NilID, err
	}
	s, err := e.CreateScope(ctx, parentID, core.RoleWorkspace, filepath.Base(relDir))
	if err != nil {
		return core.NilID, err
	}
	cache[relDir] = s.ID
	return s.ID, nil
}

// ingestFile reads, filters, chunks, and pushes one file. Returns the number of
// chunks pushed (0 => skipped: too large or binary or empty).
func ingestFile(ctx context.Context, e *engine.Engine, path, relSlash string, scope core.ID, opt Options) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.Size() > MaxFileBytes {
		return 0, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if isBinary(data) {
		return 0, nil
	}
	chunks := Split(string(data), opt.MaxChars, opt.Overlap)
	for i, c := range chunks {
		lines := fmt.Sprintf("%d-%d", c.StartLine, c.EndLine)
		summary := fmt.Sprintf("%s:%s — %s", relSlash, lines, FirstLine(c.Text, 80))
		if _, err := e.Push(ctx, core.PushRequest{
			Scope:     scope,
			Kind:      core.KindDocument,
			Tier:      core.TierWorktree,
			Summary:   summary,
			EmbedText: c.Text,
			Content:   []byte(c.Text),
			Meta: map[string]string{
				"path":  relSlash,
				"lines": lines,
				"chunk": fmt.Sprintf("%d", i),
			},
		}); err != nil {
			return 0, err
		}
	}
	return len(chunks), nil
}

// isBinary reports whether data looks non-text: a NUL byte in the first 8KB.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
