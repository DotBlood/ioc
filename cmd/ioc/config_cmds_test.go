package main

import "testing"

// N4: `config set` may write only operator-tunable keys; safety-critical keys
// (enc / emb_model / emb_dims) and anything else are rejected.
func TestConfigSetAllowed(t *testing.T) {
	allow := []string{
		"ingest_root",
		"conf.floor.BAAI/bge-small-en-v1.5",
		"conf.margin.BAAI/bge-small-en-v1.5",
		"conf.rerank.BAAI/bge-small-en-v1.5",
	}
	deny := []string{"enc", "emb_model", "emb_dims", "conf.floor", "conf.other", "random", ""}
	for _, k := range allow {
		if !configSetAllowed(k) {
			t.Errorf("key %q should be allowed", k)
		}
	}
	for _, k := range deny {
		if configSetAllowed(k) {
			t.Errorf("key %q should be denied", k)
		}
	}
}
