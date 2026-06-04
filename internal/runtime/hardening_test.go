package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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
