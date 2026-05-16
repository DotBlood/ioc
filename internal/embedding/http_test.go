//go:build integration

package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPEmbedder_DimensionDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"dimension":384,"vectors":[[0.1,0.2]],"model":"test-model"}`))
	}))
	defer server.Close()

	emb := &HTTPEmbedder{client: &http.Client{Timeout: 5 * time.Second}}

	batch, err := emb.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if batch.Dimension != 384 {
		t.Errorf("Dimension = %d, want 384", batch.Dimension)
	}
	if emb.Dims() != 384 {
		t.Errorf("Dims() = %d, want 384", emb.Dims())
	}
	if emb.Model() != "test-model" {
		t.Errorf("Model() = %q, want %q", emb.Model(), "test-model")
	}
}

func TestHTTPEmbedder_EmptyInput(t *testing.T) {
	emb := &HTTPEmbedder{client: &http.Client{}}
	_, err := emb.Embed(context.Background(), []string{})
	if err != ErrInvalidInput {
		t.Errorf("empty batch: want ErrInvalidInput, got %v", err)
	}
}

func TestHTTPEmbedder_VectorCountMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"dimension":384,"vectors":[[0.1]],"model":"test"}`))
	}))
	defer server.Close()

	emb := &HTTPEmbedder{client: &http.Client{Timeout: 5 * time.Second}}
	_, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != ErrVectorCountMismatch {
		t.Errorf("want ErrVectorCountMismatch, got %v", err)
	}
}

func TestHTTPEmbedder_DimensionMismatch(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			w.Write([]byte(`{"dimension":384,"vectors":[[0.1]],"model":"test"}`))
		} else {
			w.Write([]byte(`{"dimension":768,"vectors":[[0.1]],"model":"test"}`))
		}
	}))
	defer server.Close()

	emb := &HTTPEmbedder{client: &http.Client{Timeout: 5 * time.Second}}

	_, err := emb.Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}

	_, err = emb.Embed(context.Background(), []string{"b"})
	if err != ErrDimensionMismatch {
		t.Errorf("want ErrDimensionMismatch, got %v", err)
	}
}

func TestHTTPEmbedder_ModelEmptyBeforeFirstCall(t *testing.T) {
	emb := &HTTPEmbedder{}
	if emb.Model() != "" {
		t.Errorf("Model() before first call: want empty, got %q", emb.Model())
	}
}

func TestHTTPEmbedder_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	emb := &HTTPEmbedder{client: &http.Client{Timeout: 5 * time.Second}}
	_, err := emb.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for 503")
	}
}
