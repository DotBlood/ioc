package ingest

import (
	"fmt"
	"strings"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	cases := map[string]Language{
		"a.go": Go, "b.py": Python, "r.md": Markdown, "x.markdown": Markdown,
		"c.ts": JSTS, "d.JSX": JSTS, "e.mjs": JSTS, "f.txt": Generic, "noext": Generic,
		"dir/g.go": Go,
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSplitLangGoBoundaries(t *testing.T) {
	src := `package x

import "fmt"

// Greet says hi.
// Second doc line.
func Greet() {
	fmt.Println("hi")
}

type Thing struct {
	Name string
}
`
	// maxChars small enough to force splits, large enough to keep each unit whole.
	chunks := SplitLang(src, Go, 90, 20)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple semantic chunks, got %d", len(chunks))
	}
	// Some chunk must start at the func and carry its doc comment.
	var funcChunk string
	for _, c := range chunks {
		if strings.HasPrefix(c.Text, "// Greet says hi.") {
			funcChunk = c.Text
		}
	}
	if funcChunk == "" {
		t.Fatalf("no chunk started at the Greet doc-comment; chunks=%v", texts(chunks))
	}
	if !strings.Contains(funcChunk, "func Greet()") {
		t.Fatalf("doc comment not attached to its func: %q", funcChunk)
	}
	assertCoversNonBlank(t, src, chunks)
}

func TestSplitLangMarkdownHeadings(t *testing.T) {
	src := "# Title\n\nintro paragraph\n\n## Section A\n\nbody a\n\n## Section B\n\nbody b\n"
	chunks := SplitLang(src, Markdown, 30, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected heading-aligned chunks, got %d", len(chunks))
	}
	// Every chunk after the first should begin at a heading.
	for i, c := range chunks {
		first := strings.SplitN(c.Text, "\n", 2)[0]
		if i > 0 && !strings.HasPrefix(first, "#") {
			t.Fatalf("chunk %d does not start at a heading: %q", i, first)
		}
	}
	assertCoversNonBlank(t, src, chunks)
}

func TestSplitLangOversizeUnitFallback(t *testing.T) {
	var b strings.Builder
	b.WriteString("func Big() {\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "\tx%d := compute(%d) // a reasonably long line of code here\n", i, i)
	}
	b.WriteString("}\n")
	chunks := SplitLang(b.String(), Go, 500, 100)
	if len(chunks) < 2 {
		t.Fatalf("oversize unit should split into multiple chunks, got %d", len(chunks))
	}
	assertCoversNonBlank(t, b.String(), chunks)
}

func TestSplitLangGenericParagraphs(t *testing.T) {
	src := "para one line a\npara one line b\n\npara two\n\npara three\n"
	chunks := SplitLang(src, Generic, 40, 5)
	if len(chunks) < 2 {
		t.Fatalf("expected paragraph chunks, got %d", len(chunks))
	}
	assertCoversNonBlank(t, src, chunks)
}

func texts(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Text
	}
	return out
}

// assertCoversNonBlank checks every non-blank source line falls within some
// chunk's [StartLine, EndLine] range (no content lost during chunking).
func assertCoversNonBlank(t *testing.T, src string, chunks []Chunk) {
	t.Helper()
	covered := map[int]bool{}
	for _, c := range chunks {
		for ln := c.StartLine; ln <= c.EndLine; ln++ {
			covered[ln] = true
		}
	}
	for i, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !covered[i+1] {
			t.Fatalf("line %d (%q) not covered by any chunk", i+1, line)
		}
	}
}
