package runtime

import (
	"context"
	"crypto/rand"
	"crypto/tls"
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

// Connection-DoS bounds (V6). Package vars so tests can lower them.
var (
	// maxConns caps concurrent client connections — half-open/idle sockets can't
	// pin unbounded goroutines+memory.
	maxConns = 128
	// connIdleTimeout bounds inter-frame inactivity; a stalled connection is dropped.
	connIdleTimeout = 60 * time.Second
)

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

	tokens    atomic.Pointer[tokenSet]    // full (owner) + optional read-only; swapped on rotation
	info      atomic.Pointer[RuntimeInfo] // base descriptor, re-published on rotate
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

	tlsConfig *tls.Config // non-nil => mTLS listener (opt-in; off by default)
}

// tokenSet is the daemon's auth credentials. It is immutable once stored; rotation
// swaps the whole pointer so concurrent readers never see a torn value.
type tokenSet struct {
	full string // owner: all methods
	read string // read-only ("" = none minted)
}

// NewServer wraps an open engine; the daemon takes ownership (Stop closes it).
func NewServer(eng *engine.Engine, dir string) *Server {
	return &Server{eng: eng, dir: dir, conns: map[net.Conn]struct{}{}, ready: make(chan struct{}), stopped: make(chan struct{})}
}

// NewServerTLS is NewServer with mTLS enabled (the listener requires+verifies
// client certs). A nil cfg is equivalent to NewServer (plaintext).
func NewServerTLS(eng *engine.Engine, dir string, cfg *tls.Config) *Server {
	s := NewServer(eng, dir)
	s.tlsConfig = cfg
	return s
}

// newToken returns a fresh random hex token.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// rotateTokens regenerates the full token (and the read token if one exists) and
// republishes runtime.json with the new full token. Old tokens stop authenticating.
func (s *Server) rotateTokens() (tokenSet, error) {
	cur := s.tokens.Load()
	full, err := newToken()
	if err != nil {
		return tokenSet{}, err
	}
	ns := tokenSet{full: full}
	if cur != nil && cur.read != "" {
		if ns.read, err = newToken(); err != nil {
			return tokenSet{}, err
		}
	}
	s.tokens.Store(&ns)
	if err := s.publishInfo(ns.full); err != nil {
		return tokenSet{}, err
	}
	return ns, nil
}

// mintReadToken mints a fresh read-only token, replacing (revoking) any prior one.
// The full token is unchanged; runtime.json is not rewritten (it carries the full
// token only).
func (s *Server) mintReadToken() (string, error) {
	cur := s.tokens.Load()
	read, err := newToken()
	if err != nil {
		return "", err
	}
	ns := tokenSet{full: cur.full, read: read}
	s.tokens.Store(&ns)
	return read, nil
}

// publishInfo rewrites runtime.json with the given full token (other fields from
// the stored base descriptor).
func (s *Server) publishInfo(full string) error {
	base := s.info.Load()
	info := *base
	info.Token = full
	return writeRuntimeInfo(s.dir, info)
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
	if s.tlsConfig != nil {
		// tls.Conn satisfies net.Conn, so the accept loop / framing / idle deadline
		// downstream are unchanged.
		ln = tls.NewListener(ln, s.tlsConfig)
	}
	s.ln = ln

	full, err := newToken()
	if err != nil {
		_ = ln.Close()
		return err
	}
	s.tokens.Store(&tokenSet{full: full}) // read token is opt-in (mint_read_token)
	s.startedAt = time.Now().UTC().Format(time.RFC3339)

	base := RuntimeInfo{
		PID: os.Getpid(), Net: "tcp", Addr: ln.Addr().String(), Token: full,
		StartedAt: s.startedAt, DataDir: s.dir,
		EmbedModel: s.eng.EmbModel(),
	}
	if s.tlsConfig != nil {
		base.TLS = true
		base.TLSServerName = "localhost"
	}
	s.info.Store(&base)
	if err := writeRuntimeInfo(s.dir, base); err != nil {
		_ = ln.Close()
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
		if s.closing {
			// Stop won the race: it already set closing (and is about to / has
			// begun wg.Wait). Don't Add — a wg.Add concurrent with wg.Wait is a
			// data race / WaitGroup misuse. Drop this late connection.
			s.connsMu.Unlock()
			_ = conn.Close()
			continue
		}
		// Max-conns (V6): bound concurrent connections so half-open/idle sockets
		// can't pin unbounded goroutines+memory. Checked under the same lock that
		// gates closing, so it composes with the shutdown race fix.
		if len(s.conns) >= maxConns {
			s.connsMu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1) // under connsMu, gated by !closing → ordered before Stop's wg.Wait
		s.connsMu.Unlock()
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, conn)
		s.connsMu.Unlock()
		_ = conn.Close()
	}()
	// An unauthenticated connection may send only a control-size frame; only once a
	// request authenticates with the FULL (owner) token does it earn data-size
	// frames (a large Push rides inside). Gating on genuine full-token auth — not on
	// "the response wasn't codeAuth" — closes the V6 bypass where a pre-auth error
	// (e.g. a protocol-version mismatch → codeInvalid) used to lift the cap without
	// any valid token. A read-only token never lifts it: reads fit the control cap.
	fullAuthed := false
	for {
		// Idle read deadline (V6): a half-open / slow-loris connection that stops
		// sending is dropped instead of pinning a goroutine forever. Refreshed each
		// frame, so it bounds inter-frame inactivity, not total session length.
		_ = conn.SetReadDeadline(time.Now().Add(connIdleTimeout))
		max := uint32(maxControlFrame)
		if fullAuthed {
			max = maxDataFrame
		}
		raw, err := readFrame(conn, max)
		if err != nil {
			return // EOF, deadline, oversize frame, or connection closed
		}
		var req request
		if err := json.Unmarshal(raw, &req); err != nil {
			return
		}
		resp, full := s.handle(context.Background(), req)
		if full {
			fullAuthed = true // full-token request → allow larger frames henceforth (latch)
		}
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
			_ = s.ln.Close()
		}
		for c := range s.conns {
			_ = c.Close()
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
