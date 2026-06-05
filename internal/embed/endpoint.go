package embed

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Endpoint policy (V2). IOC POSTs every summary/chunk it embeds to the configured
// endpoint, so a tampered or misconfigured IOC_EMBED could exfiltrate the entire
// memory in plaintext to a remote host. validateEndpoint enforces:
//   - unix sockets / bare paths           → allowed (kernel-local, fs-permission-gated)
//   - http/https to a LOOPBACK literal     → allowed (127.0.0.0/8, ::1, localhost)
//   - https to a non-loopback host         → allowed ONLY with allowRemote
//   - http (plaintext) to a non-loopback   → ALWAYS rejected (no cleartext memory off-box)
//   - malformed                            → rejected
//
// Loopback is judged by the literal host/IP only (net.ParseIP / "localhost") — NO
// DNS resolution, so a name that resolves to 127.0.0.1 is treated as remote and a
// DNS-rebinding trick can't smuggle traffic off-box.
func validateEndpoint(endpoint string, allowRemote bool) error {
	if endpoint == "" {
		return nil // empty = mock embedder; nothing to validate
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return nil // unix:/path or bare path → loopback-equivalent
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	host := u.Hostname()
	if isLoopbackHost(host) {
		return nil
	}
	// Non-loopback host from here.
	if u.Scheme != "https" {
		return fmt.Errorf("embed: refusing to send memory in plaintext to non-loopback %q — use https or a loopback/unix endpoint", endpoint)
	}
	if !allowRemote {
		return fmt.Errorf("embed: refusing remote embedder %q — set --allow-remote-embed / IOC_ALLOW_REMOTE_EMBED=1 to permit it", endpoint)
	}
	return nil
}

// isLoopbackHost reports whether host is a loopback literal (no DNS lookup).
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
