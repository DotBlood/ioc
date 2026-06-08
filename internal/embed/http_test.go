package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubEmbedder serves a canned /embed body, so we can test how HTTPEmbedder
// validates a hostile/buggy response (V8).
func stubEmbedder(t *testing.T, body string) *HTTPEmbedder {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	e, err := NewHTTPEmbedder(srv.URL, false) // httptest is 127.0.0.1 → loopback, allowed
	if err != nil {
		t.Fatalf("new embedder: %v", err)
	}
	return e
}

func TestEmbed_ValidatesResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"valid", `{"dimension":2,"vectors":[[0.1,0.2]],"model":"m"}`, true},
		{"nan", `{"dimension":2,"vectors":[[0.1,NaN]],"model":"m"}`, false},          // json decode rejects bare NaN
		{"inf-string", `{"dimension":2,"vectors":[[1e999,0.2]],"model":"m"}`, false}, // 1e999 → +Inf, caught
		{"short-vector", `{"dimension":3,"vectors":[[0.1,0.2]],"model":"m"}`, false},
		{"count-mismatch", `{"dimension":2,"vectors":[[0.1,0.2],[0.3,0.4]],"model":"m"}`, false}, // 2 vecs for 1 text
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := stubEmbedder(t, c.body)
			_, err := e.Embed(context.Background(), []string{"one text"})
			if (err == nil) != c.ok {
				t.Fatalf("Embed err=%v, want ok=%v", err, c.ok)
			}
		})
	}
}

// An oversized streaming body is bounded by io.LimitReader (the decode fails
// rather than reading unboundedly).
func TestEmbed_BodyCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A valid prefix then a flood of bytes far exceeding the cap, never closing
		// the JSON — decode must fail (bounded), not hang/OOM.
		_, _ = w.Write([]byte(`{"dimension":2,"vectors":[[0.1,0.2`))
		flood := strings.Repeat(",0.0", 1<<20) // ~4 MiB of filler; cap is higher but unterminated → decode error
		for i := 0; i < 80; i++ {              // ~320 MiB if unbounded
			if _, err := w.Write([]byte(flood)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	e, err := NewHTTPEmbedder(srv.URL, false)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected a decode error on an unbounded/garbage body")
	}
}
