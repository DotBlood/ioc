package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// runtimeFile is written inside a store's data dir while a daemon owns it.
const runtimeFile = "runtime.json"

// RuntimeInfo is the published descriptor of a running daemon (one per data dir).
type RuntimeInfo struct {
	PID           int    `json:"pid"`
	Net           string `json:"net"`  // "tcp"
	Addr          string `json:"addr"` // e.g. 127.0.0.1:54321
	Token         string `json:"token"`
	StartedAt     string `json:"started_at"` // RFC3339
	DataDir       string `json:"data_dir"`
	EmbedModel    string `json:"embed_model"`
	TLS           bool   `json:"tls,omitempty"`             // daemon requires mTLS (off by default)
	TLSServerName string `json:"tls_server_name,omitempty"` // SNI / cert name to verify
}

func runtimePath(dir string) string { return filepath.Join(dir, runtimeFile) }

func writeRuntimeInfo(dir string, info RuntimeInfo) error {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	// 0o600 (V3): runtime.json carries the daemon's bearer token. World-readable
	// (0o644) let any local user on a shared host read it and drive the full RPC
	// (shutdown, delete_scope, ingest, drill any content). Keep it owner-only.
	return os.WriteFile(runtimePath(dir), append(b, '\n'), 0o600)
}

// readRuntimeInfo loads the descriptor; os.IsNotExist(err) means "no daemon".
func readRuntimeInfo(dir string) (RuntimeInfo, error) {
	var info RuntimeInfo
	b, err := os.ReadFile(runtimePath(dir))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

func removeRuntimeInfo(dir string) error {
	if err := os.Remove(runtimePath(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
