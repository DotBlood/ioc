package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
)

func TestProtoVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	info, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	conn, err := net.Dial(info.Net, info.Addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	reqBytes, _ := json.Marshal(request{ID: 1, V: 999, Method: mEmbModel, Token: info.Token})
	if err := writeFrame(conn, reqBytes); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != codeInvalid {
		t.Fatalf("expected invalid (version) error, got %+v", resp.Error)
	}
}

// rawRequest sends one framed request over a fresh connection and returns the response.
func rawRequest(t *testing.T, dir string, req request) response {
	t.Helper()
	info, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	conn, err := net.Dial(info.Net, info.Addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	b, _ := json.Marshal(req)
	if err := writeFrame(conn, b); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := readFrame(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return resp
}

// V3: the token is checked with a constant-time compare; bad/missing/wrong-length
// tokens are rejected with codeAuth, a correct token passes.
func TestTokenAuth(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	info, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}

	cases := []struct {
		name string
		tok  string
		auth bool // true => expect codeAuth rejection
	}{
		{"correct", info.Token, false},
		{"empty", "", true},
		{"wrong-same-length", strings.Repeat("0", len(info.Token)), true},
		{"wrong-different-length", "short", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawRequest(t, dir, request{ID: 1, V: ProtoVersion, Method: mEmbModel, Token: tc.tok})
			gotAuth := resp.Error != nil && resp.Error.Code == codeAuth
			if gotAuth != tc.auth {
				t.Fatalf("token %q: got error %+v, want codeAuth=%v", tc.name, resp.Error, tc.auth)
			}
		})
	}
}

// V3: runtime.json (which holds the bearer token) is owner-only (POSIX-gated).
func TestRuntimeInfoMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes not enforced on Windows")
	}
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	fi, err := os.Stat(runtimePath(dir))
	if err != nil {
		t.Fatalf("stat runtime.json: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("runtime.json mode = %o, want 0600", fi.Mode().Perm())
	}
}

func TestStatsControl(t *testing.T) {
	srv := startDaemon(t, t.TempDir())
	defer srv.Stop()
	dir := srv.dir

	c, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	st, err := c.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.ProtoVersion != ProtoVersion {
		t.Fatalf("proto version = %d, want %d", st.ProtoVersion, ProtoVersion)
	}
	if st.Conns < 1 {
		t.Fatalf("conns = %d, want >= 1", st.Conns)
	}
}

func TestConcurrentReadsConsistent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	w, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	root, err := w.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	art, err := w.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "needle in the haystack"})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	_ = w.Close()

	// Many concurrent readers (RLock path) must all see the artifact.
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := Dial(dir)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			_, hits, err := c.Query(ctx, core.Query{Scope: root.ID, Text: "needle haystack", TopK: 5})
			if err != nil {
				errs <- err
				return
			}
			if len(hits) == 0 || hits[0].Artifact != art.ID {
				errs <- fmt.Errorf("concurrent read returned wrong/no artifact")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read failed: %v", err)
	}
}
