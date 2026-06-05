package embed

import "testing"

// validateEndpoint enforces the V2 trust policy: unix/loopback allowed, remote
// plaintext always rejected, remote https only with allowRemote.
func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		endpoint    string
		allowRemote bool
		ok          bool
	}{
		{"", false, true},                      // mock
		{"unix:/tmp/ioc/e.sock", false, true},  // unix
		{"/tmp/ioc/e.sock", false, true},       // bare path
		{"http://127.0.0.1:8088", false, true}, // loopback v4
		{"http://[::1]:8088", false, true},     // loopback v6
		{"http://localhost:8088", false, true}, // loopback name
		{"http://10.0.0.5:8088", false, false}, // plaintext remote — always reject
		{"http://example.com", false, false},   // plaintext remote — always reject
		{"http://example.com", true, false},    // plaintext remote — reject even with allow
		{"https://example.com", false, false},  // https remote — needs allowRemote
		{"https://example.com", true, true},    // https remote — allowed with opt-in
		{"http://192.168.1.9:9", true, false},  // private but non-loopback plaintext — reject
		{"://bad", false, true},                // not an http(s) URL → treated as a local/unix path
		{"https://[::1", false, false},         // malformed https URL → rejected
	}
	for _, c := range cases {
		err := validateEndpoint(c.endpoint, c.allowRemote)
		if (err == nil) != c.ok {
			t.Errorf("validateEndpoint(%q, allowRemote=%v) err=%v, want ok=%v", c.endpoint, c.allowRemote, err, c.ok)
		}
	}
}
