package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DotBlood/ioc/internal/engine"
)

// compile-time check: the embedded engine satisfies Service.
var _ Service = (*engine.Engine)(nil)

// Server is the runtime daemon: it owns one *engine.Engine and serves the
// framed-JSON protocol to many client connections. All engine calls are
// serialized by a single mutex (S1; upgraded to RWMutex in S3).
type Server struct {
	eng *engine.Engine
	dir string

	// mu guards engine access: writes take Lock (exclusive), reads take RLock
	// (concurrent). Reads are safe to run in parallel because the engine mutates
	// no shared field on a read path (the embedding store is opened eagerly at
	// engine.Open) and storage is internally concurrency-safe (bbolt serializes
	// its own write txns — including the trace append a Query makes).
	mu sync.RWMutex

	token     string
	ln        net.Listener
	ready     chan struct{}
	stopped   chan struct{} // closed when Stop has fully finished cleanup
	startedAt string
	reqCount  atomic.Uint64

	connsMu sync.Mutex
	conns   map[net.Conn]struct{}
	closing bool

	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewServer wraps an open engine; the daemon takes ownership (Stop closes it).
func NewServer(eng *engine.Engine, dir string) *Server {
	return &Server{eng: eng, dir: dir, conns: map[net.Conn]struct{}{}, ready: make(chan struct{}), stopped: make(chan struct{})}
}

// Ready is closed once the daemon is listening and runtime.json is published.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the bound address (valid after Ready is closed).
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

func (s *Server) connCount() int {
	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	return len(s.conns)
}

// Serve binds a loopback listener, publishes runtime.json, then accepts
// connections until Stop. It blocks.
func (s *Server) Serve() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("runtime: listen: %w", err)
	}
	s.ln = ln

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return err
	}
	s.token = hex.EncodeToString(tok)
	s.startedAt = time.Now().UTC().Format(time.RFC3339)

	info := RuntimeInfo{
		PID: os.Getpid(), Net: "tcp", Addr: ln.Addr().String(), Token: s.token,
		StartedAt: s.startedAt, DataDir: s.dir,
		EmbedModel: s.eng.EmbModel(),
	}
	if err := writeRuntimeInfo(s.dir, info); err != nil {
		ln.Close()
		return fmt.Errorf("runtime: write runtime.json: %w", err)
	}
	close(s.ready)

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.connsMu.Lock()
			closing := s.closing
			s.connsMu.Unlock()
			if closing {
				<-s.stopped // wait for Stop to finish cleanup before returning
				return nil
			}
			return fmt.Errorf("runtime: accept: %w", err)
		}
		s.connsMu.Lock()
		s.conns[conn] = struct{}{}
		s.connsMu.Unlock()
		s.wg.Add(1)
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, conn)
		s.connsMu.Unlock()
		conn.Close()
	}()
	for {
		raw, err := readFrame(conn)
		if err != nil {
			return // EOF or connection closed
		}
		var req request
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		resp := s.handle(context.Background(), req)
		out, err := json.Marshal(resp)
		if err != nil {
			return
		}
		if err := writeFrame(conn, out); err != nil {
			return
		}
		if req.Method == mShutdown && resp.Error == nil {
			// Reply is sent; stop the daemon asynchronously (Stop closes this
			// conn + drains, so it must not run on this goroutine).
			go func() { _ = s.Stop() }()
			return
		}
	}
}

// Stop gracefully shuts down: stop accepting, close active connections, drain,
// flush embeddings, close the engine, and remove runtime.json. Idempotent.
func (s *Server) Stop() error {
	var ret error
	s.stopOnce.Do(func() {
		s.connsMu.Lock()
		s.closing = true
		if s.ln != nil {
			s.ln.Close()
		}
		for c := range s.conns {
			c.Close()
		}
		s.connsMu.Unlock()

		s.wg.Wait()

		if err := s.eng.Sync(); err != nil {
			ret = err
		}
		if err := s.eng.Close(); err != nil && ret == nil {
			ret = err
		}
		if err := removeRuntimeInfo(s.dir); err != nil && ret == nil {
			ret = err
		}
		close(s.stopped) // unblocks Serve so the process exits only after cleanup
	})
	return ret
}
