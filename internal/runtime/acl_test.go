package runtime

import (
	"context"
	"testing"

	"github.com/DotBlood/ioc/internal/core"
)

// Tiered ACL: the full token passes every tier; a read-only token passes reads but
// is rejected for writes and control ops; a bad token is rejected everywhere.
func TestTierAuth(t *testing.T) {
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	full, err := Info(dir)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	// Mint a read-only token (control op, needs the full token).
	c, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readTok, err := c.MintReadToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	_ = c.Close()

	cases := []struct {
		name   string
		token  string
		method string
		auth   bool // true => expect codeAuth
	}{
		{"full-read", full.Token, mListScopes, false},
		{"full-write", full.Token, mCreateScope, false},
		{"full-control", full.Token, mStats, false},
		{"read-read", readTok, mListScopes, false},
		{"read-write", readTok, mCreateScope, true},
		{"read-control", readTok, mStats, true},
		{"bad-read", "deadbeef", mListScopes, true},
		{"empty-read", "", mListScopes, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawRequest(t, dir, request{ID: 1, V: ProtoVersion, Method: tc.method, Token: tc.token})
			gotAuth := resp.Error != nil && resp.Error.Code == codeAuth
			if gotAuth != tc.auth {
				t.Fatalf("%s: error %+v, want codeAuth=%v", tc.name, resp.Error, tc.auth)
			}
		})
	}
}

// A read-only client can query but not write; re-minting revokes the prior read token.
func TestMintReadToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	owner, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer owner.Close()
	root, err := owner.CreateScope(ctx, core.NilID, core.RoleWorktree, "root")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	readTok, err := owner.MintReadToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	rc, err := DialWithToken(dir, readTok)
	if err != nil {
		t.Fatalf("dial read: %v", err)
	}
	defer rc.Close()
	if _, _, err := rc.Query(ctx, core.Query{Scope: root.ID, Text: "x", TopK: 5}); err != nil {
		t.Fatalf("read client query should work: %v", err)
	}
	if _, err := rc.Push(ctx, core.PushRequest{Scope: root.ID, Summary: "nope"}); err == nil {
		t.Fatal("read client must NOT be able to push")
	}

	// Re-mint revokes the old read token.
	if _, err := owner.MintReadToken(); err != nil {
		t.Fatalf("re-mint: %v", err)
	}
	resp := rawRequest(t, dir, request{ID: 1, V: ProtoVersion, Method: mListScopes, Token: readTok})
	if resp.Error == nil || resp.Error.Code != codeAuth {
		t.Fatalf("old read token should be revoked, got %+v", resp.Error)
	}
}

// Rotation invalidates the old full token, updates runtime.json, and a re-Dial
// (reading the new token) works.
func TestRotateInvalidatesOld(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	defer startDaemon(t, dir).Stop()

	oldInfo, _ := Info(dir)
	c, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	newFull, _, err := c.RotateToken()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// The same client kept working (token updated in-place).
	if _, err := c.CreateScope(ctx, core.NilID, core.RoleWorktree, "after-rotate"); err != nil {
		t.Fatalf("client after rotate: %v", err)
	}
	_ = c.Close()

	// runtime.json now holds the new token.
	updated, _ := Info(dir)
	if updated.Token == oldInfo.Token {
		t.Fatal("runtime.json token was not updated by rotate")
	}
	if updated.Token != newFull {
		t.Fatalf("runtime.json token %q != rotate result %q", updated.Token, newFull)
	}
	if updated.Token == "" || oldInfo.Token == "" {
		t.Fatal("empty token")
	}

	// The OLD token is rejected on a fresh connection.
	resp := rawRequest(t, dir, request{ID: 1, V: ProtoVersion, Method: mListScopes, Token: oldInfo.Token})
	if resp.Error == nil || resp.Error.Code != codeAuth {
		t.Fatalf("old token should be rejected after rotate, got %+v", resp.Error)
	}
	// A fresh Dial (reads the new token) works.
	c2, err := Dial(dir)
	if err != nil {
		t.Fatalf("re-dial: %v", err)
	}
	defer c2.Close()
	if _, err := c2.ListScopes(ctx); err != nil {
		t.Fatalf("re-dial list: %v", err)
	}
}
