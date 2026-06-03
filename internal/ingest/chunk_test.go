package ingest

import (
	"strings"
	"testing"
)

func TestSplitWindowsAndOverlap(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 20))
		b.WriteByte('\n')
	}
	chunks := Split(b.String(), 200, 50)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.StartLine < 1 || c.EndLine < c.StartLine {
			t.Fatalf("chunk %d bad line range %d-%d", i, c.StartLine, c.EndLine)
		}
		if len(c.Text) > 200 && !strings.Contains(c.Text, "\n") {
			t.Fatalf("chunk %d over max with no line break", i)
		}
	}
	// Consecutive chunks overlap (next starts at or before prev end+1).
	for i := 1; i < len(chunks); i++ {
		if chunks[i].StartLine > chunks[i-1].EndLine+1 {
			t.Fatalf("gap between chunk %d (end %d) and %d (start %d)",
				i-1, chunks[i-1].EndLine, i, chunks[i].StartLine)
		}
	}
}

func TestSplitOverlongSingleLine(t *testing.T) {
	long := strings.Repeat("z", 5000)
	chunks := Split(long, 200, 50)
	if len(chunks) != 1 {
		t.Fatalf("over-long single line should be one chunk, got %d", len(chunks))
	}
	if chunks[0].StartLine != 1 || chunks[0].EndLine != 1 {
		t.Fatalf("unexpected line range %d-%d", chunks[0].StartLine, chunks[0].EndLine)
	}
}

func TestSplitSkipsBlankOnly(t *testing.T) {
	if got := Split("\n\n  \n\t\n", 200, 50); len(got) != 0 {
		t.Fatalf("blank-only input should yield no chunks, got %d", len(got))
	}
}

func TestFirstLine(t *testing.T) {
	if got := FirstLine("\n\n  hello world  \nmore", 80); got != "hello world" {
		t.Fatalf("FirstLine = %q", got)
	}
	if got := FirstLine(strings.Repeat("a", 100), 10); got != strings.Repeat("a", 10)+"…" {
		t.Fatalf("truncation failed: %q", got)
	}
}
