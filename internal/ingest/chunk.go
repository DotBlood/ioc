// Package ingest mechanically splits files into chunks and writes them into IOC
// as KindDocument artifacts (no LLM in the loop — the raw chunk text is embedded).
package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Defaults for chunking, in characters. A chunk is a window of whole lines whose
// combined length stays under maxChars; consecutive chunks overlap by ~overlap
// characters (carried as trailing whole lines) so a match near a boundary is not
// split across two chunks.
const (
	DefaultMaxChars = 1500
	DefaultOverlap  = 200
)

// Chunk is a contiguous line-aligned window of a file. StartLine/EndLine are
// 1-based, inclusive.
type Chunk struct {
	Text      string
	StartLine int
	EndLine   int
}

// Split breaks text into line-aligned windows. Each window holds as many whole
// lines as fit under maxChars (a single over-long line becomes its own chunk),
// and the next window backs up by ~overlap characters of trailing lines so
// content straddling a boundary stays retrievable from at least one chunk.
func Split(text string, maxChars, overlap int) []Chunk {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	if overlap < 0 || overlap >= maxChars {
		overlap = DefaultOverlap
	}
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	i := 0
	for i < len(lines) {
		var b strings.Builder
		start := i
		for i < len(lines) {
			ln := lines[i]
			// +1 for the newline we re-join with. Always take at least one line
			// so an over-long line still forms a (single) chunk.
			if b.Len() > 0 && b.Len()+len(ln)+1 > maxChars {
				break
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(ln)
			i++
		}
		txt := strings.TrimRight(b.String(), "\n")
		if strings.TrimSpace(txt) != "" {
			chunks = append(chunks, Chunk{Text: txt, StartLine: start + 1, EndLine: i})
		}
		if i >= len(lines) {
			break
		}
		// Back up by whole trailing lines worth ~overlap chars for the next window.
		i = backupForOverlap(lines, start, i, overlap)
	}
	return chunks
}

// backupForOverlap returns the next start index: from end, step back over whole
// lines until ~overlap characters are covered, without going past start+1 (so we
// always make forward progress).
func backupForOverlap(lines []string, start, end, overlap int) int {
	if overlap <= 0 {
		return end
	}
	acc := 0
	j := end
	for j > start+1 {
		acc += len(lines[j-1]) + 1
		if acc >= overlap {
			break
		}
		j--
	}
	if j <= start {
		j = start + 1
	}
	return j
}

// Sig is a per-file change signature: the content hash plus the chunking
// parameters. Two ingests produce the same Sig iff the bytes AND the chunk
// windowing are identical — so an unchanged file (same params) can be skipped,
// while changing the file or the maxChars/overlap forces a re-chunk.
func Sig(data []byte, maxChars, overlap int) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%s:%d:%d", hex.EncodeToString(h[:]), maxChars, overlap)
}

// FirstLine returns a trimmed one-line label for a chunk (its first non-empty
// line), truncated to n runes — used as the display Summary.
func FirstLine(text string, n int) string {
	for _, ln := range strings.Split(text, "\n") {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		r := []rune(s)
		if len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	return ""
}
