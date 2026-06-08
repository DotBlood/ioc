package runtime

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

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
	raw, err := readFrame(conn, maxDataFrame)
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
	raw, err := readFrame(conn, maxDataFrame)
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

// V6: an UNAUTHENTICATED connection announcing an oversize frame is dropped before
// the body is allocated (the header alone exceeds maxControlFrame).
func TestPreAuthFrameCapped(t *testing.T) {
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

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], maxControlFrame+1) // claim > pre-auth cap
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}
	// Server rejects the oversize frame and closes the conn → our read sees EOF.
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("expected the connection to be closed for an oversize pre-auth frame")
	}
}

// V6: after a small request authenticates the connection, a frame larger than the
// pre-auth cap (a big Push) is accepted.
func TestPostAuthLargeFrame(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	c, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	root, err := c.CreateScope(ctx, core.NilID, core.RoleWorktree, "root") // authenticates
	if err != nil {
		t.Fatalf("create scope: %v", err)
	}
	big := make([]byte, 2<<20) // 2 MiB > maxControlFrame(1 MiB)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := c.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "big doc", Content: big}); err != nil {
		t.Fatalf("post-auth large push failed: %v", err)
	}
}

// V6: concurrent connections are bounded — with the cap at 1, a second connection
// (opened while the first is held) is refused.
func TestMaxConns(t *testing.T) {
	old := maxConns
	maxConns = 1
	defer func() { maxConns = old }()

	ctx := context.Background()
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	c1, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial c1: %v", err)
	}
	defer c1.Close()
	// A completed round-trip guarantees c1 is registered in s.conns before c2 dials.
	if _, err := c1.CreateScope(ctx, core.NilID, core.RoleWorktree, "root"); err != nil {
		t.Fatalf("c1 create: %v", err)
	}

	c2, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial c2: %v", err)
	}
	defer c2.Close()
	if _, err := c2.CreateScope(ctx, core.NilID, core.RoleWorktree, "x"); err == nil {
		t.Fatal("expected the second connection to be refused at the conn cap")
	}
}

// V6: a connection that goes idle past the deadline is dropped.
func TestIdleTimeout(t *testing.T) {
	old := connIdleTimeout
	connIdleTimeout = 150 * time.Millisecond
	defer func() { connIdleTimeout = old }()

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

	time.Sleep(400 * time.Millisecond) // exceed the idle deadline without sending
	// The server has closed the idle conn; a request now fails to get a response.
	b, _ := json.Marshal(request{ID: 1, V: ProtoVersion, Method: mEmbModel, Token: info.Token})
	_ = writeFrame(conn, b)
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("expected the idle connection to have been closed")
	}
}

// V7: deeply nested params are rejected (codeInvalid) before json.Unmarshal, while
// a normal flat request decodes fine.
func TestJSONDepthLimit(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	info, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}

	deep := json.RawMessage(strings.Repeat("[", maxJSONDepth+5) + strings.Repeat("]", maxJSONDepth+5))
	resp := rawRequest(t, dir, request{ID: 1, V: ProtoVersion, Method: mPush, Token: info.Token, Params: deep})
	if resp.Error == nil || resp.Error.Code != codeInvalid {
		t.Fatalf("expected codeInvalid for deeply nested params, got %+v", resp.Error)
	}

	// A shallow (normal-depth) params value passes the depth check (it then fails on
	// the engine for an unknown scope, NOT with a depth/parse error).
	shallow := json.RawMessage(`{"req":{"scope":"x","summary":"s"}}`)
	resp2 := rawRequest(t, dir, request{ID: 2, V: ProtoVersion, Method: mPush, Token: info.Token, Params: shallow})
	if resp2.Error != nil && resp2.Error.Code == codeInvalid && strings.Contains(resp2.Error.Msg, "nested too deep") {
		t.Fatalf("shallow params wrongly rejected by depth check: %+v", resp2.Error)
	}
}

// --- bug #1: frame-tier unlocks ONLY on full-token auth (pre-auth bypass fix) ---

// dialRaw opens a raw connection to the daemon owning dir (no auto-auth).
func dialRaw(t *testing.T, dir string) (net.Conn, RuntimeInfo) {
	t.Helper()
	info, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	conn, err := net.Dial(info.Net, info.Addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, info
}

// readResp reads and decodes one framed response (fatal on transport error).
func readResp(t *testing.T, conn net.Conn) response {
	t.Helper()
	raw, err := readFrame(conn, maxDataFrame)
	if err != nil {
		t.Fatalf("read resp: %v", err)
	}
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	return r
}

// oversizeReadFrame builds a valid-if-read request padded just past maxControlFrame
// (and well under maxDataFrame). A server that has NOT lifted the frame cap rejects
// it as too large and closes the connection; a server that wrongly lifted it would
// read and process it and reply. The "pad" field is ignored by request's Unmarshal.
func oversizeReadFrame(method, token string) []byte {
	pad := strings.Repeat("x", maxControlFrame)
	b, _ := json.Marshal(map[string]any{
		"id": 2, "v": ProtoVersion, "method": method, "token": token, "pad": pad,
	})
	return b
}

// TestBadVersionDoesNotUnlockFrames is the core regression for the V6 bypass: an
// UNAUTHENTICATED client sends a bogus-version frame (→ codeInvalid, returned before
// the auth check) and must NOT thereby earn data-size frames.
func TestBadVersionDoesNotUnlockFrames(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	conn, info := dialRaw(t, dir)
	defer conn.Close()

	// Frame 1: bogus version, NO token. Pre-auth → codeInvalid, no unlock.
	b, _ := json.Marshal(request{ID: 1, V: 999, Method: mEmbModel, Token: ""})
	if err := writeFrame(conn, b); err != nil {
		t.Fatalf("write f1: %v", err)
	}
	if resp := readResp(t, conn); resp.Error == nil || resp.Error.Code != codeInvalid {
		t.Fatalf("frame1: want codeInvalid, got %+v", resp.Error)
	}

	// Frame 2: oversize-for-control. With the fix the tier is still locked → the
	// server rejects it and closes; our read then errors. (A write error is fine —
	// the server may close mid-write.)
	_ = writeFrame(conn, oversizeReadFrame(mEmbModel, info.Token))
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("frame tier unlocked after a pre-auth version error (V6 bypass regressed)")
	}
}

// TestReadTokenDoesNotUnlockFrames: a read-only token authenticates a read (ACL ok)
// but must never lift the frame cap — reads always fit the control-size frame.
func TestReadTokenDoesNotUnlockFrames(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	full, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	rt, err := full.MintReadToken()
	if err != nil {
		t.Fatalf("mint read token: %v", err)
	}
	_ = full.Close()

	conn, _ := dialRaw(t, dir)
	defer conn.Close()

	// Frame 1: a read with the read token succeeds (tierRead accepts it)...
	b, _ := json.Marshal(request{ID: 1, V: ProtoVersion, Method: mEmbModel, Token: rt})
	if err := writeFrame(conn, b); err != nil {
		t.Fatalf("write f1: %v", err)
	}
	if resp := readResp(t, conn); resp.Error != nil {
		t.Fatalf("read-token read should succeed, got %+v", resp.Error)
	}

	// Frame 2: ...but does NOT unlock data-size frames.
	_ = writeFrame(conn, oversizeReadFrame(mEmbModel, rt))
	if _, err := readFrame(conn, maxDataFrame); err == nil {
		t.Fatal("read-only token unlocked data-size frames")
	}
}

// TestFullTokenReadUnlocksFrames: the positive companion — a READ with the FULL
// token authenticates the owner and lifts the cap (read-first, complementing the
// write-first TestPostAuthLargeFrame).
func TestFullTokenReadUnlocksFrames(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()
	conn, info := dialRaw(t, dir)
	defer conn.Close()

	b, _ := json.Marshal(request{ID: 1, V: ProtoVersion, Method: mEmbModel, Token: info.Token})
	if err := writeFrame(conn, b); err != nil {
		t.Fatalf("write f1: %v", err)
	}
	if resp := readResp(t, conn); resp.Error != nil {
		t.Fatalf("full-token read failed: %+v", resp.Error)
	}

	// An oversize-for-control frame is now accepted and processed.
	if err := writeFrame(conn, oversizeReadFrame(mEmbModel, info.Token)); err != nil {
		t.Fatalf("write f2: %v", err)
	}
	if resp := readResp(t, conn); resp.Error != nil {
		t.Fatalf("post-full-auth large frame should be processed, got %+v", resp.Error)
	}
}

// --- bug #2: runtime.json is written atomically (no torn reads under rotation) ---

// TestRuntimeInfoAtomicWriteNoTornRead hammers readRuntimeInfo concurrently with
// repeated writeRuntimeInfo (the token-rotation rewrite path). Every read must
// decode a complete descriptor — never a half-written, unmarshal-failing file.
func TestRuntimeInfoAtomicWriteNoTornRead(t *testing.T) {
	dir := t.TempDir()
	srv := startDaemon(t, dir)
	defer srv.Stop()

	base, err := readRuntimeInfo(dir)
	if err != nil {
		t.Fatalf("read base: %v", err)
	}

	errs := make(chan error, 16)
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			info := base
			info.Token = fmt.Sprintf("rotated-token-%08d", i)
			if err := writeRuntimeInfo(dir, info); err != nil {
				errs <- fmt.Errorf("write: %w", err)
				return
			}
		}
	}()

	const readers, iters = 8, 300
	var rg sync.WaitGroup
	for r := 0; r < readers; r++ {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for i := 0; i < iters; i++ {
				if _, err := readRuntimeInfo(dir); err != nil {
					errs <- fmt.Errorf("torn read: %w", err)
					return
				}
			}
		}()
	}
	rg.Wait()
	close(stop)
	writer.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
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
