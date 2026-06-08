package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// N1: the embedder/reranker client must NOT follow redirects. validateEndpoint vets the
// CONFIGURED URL, but following a 3xx could redirect a validated https endpoint to
// http:// (downgrade) or another host and exfiltrate all POSTed text. A redirect must
// surface as an error, and the final (redirect-target) server must never be reached.
func TestEmbed_DoesNotFollowRedirect(t *testing.T) {
	finalHit := false
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		finalHit = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dimension":2,"vectors":[[0.1,0.2]],"model":"m"}`))
	}))
	defer final.Close()

	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/embed", http.StatusFound) // 302 to the "other host"
	}))
	defer redir.Close()

	e, err := NewHTTPEmbedder(redir.URL, false) // loopback → allowed
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("expected an error — the redirect must not be followed")
	}
	if finalHit {
		t.Fatal("client followed the redirect to the target server — redirects must be disabled (N1)")
	}
}
