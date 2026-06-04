package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

// A second OpenMeta on the same path (while the first holds the bbolt lock)
// must fail with a clear "busy/locked" message rather than a bare timeout.
func TestOpenMetaBusyIsClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")

	m1, err := OpenMeta(path)
	if err != nil {
		t.Fatalf("first OpenMeta: %v", err)
	}

	_, err = OpenMeta(path)
	if err == nil {
		t.Fatal("second OpenMeta on a locked dir should fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "busy") && !strings.Contains(msg, "locked") {
		t.Fatalf("error message not user-clear about a busy/locked dir: %q", msg)
	}

	// After closing the first, the path is openable again.
	if err := m1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	m2, err := OpenMeta(path)
	if err != nil {
		t.Fatalf("re-open after close: %v", err)
	}
	_ = m2.Close()
}
