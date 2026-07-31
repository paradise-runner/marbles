package main

import (
	"path/filepath"
	"testing"
)

// TestResolveIdentityWrongTokenReturnsNonNilInfo locks in the fix for the
// nil-pointer panic. Mutating command handlers do:
//
//	info, _ := ResolveIdentity(db, "")
//	agent := MustGetAgent(info)     // <-- deref panic if info == nil
//
// When MB_AGENT_TOKEN is present but matches no agent, ResolveIdentity used to
// return (nil, error). The error was discarded, so MustGetAgent dereferenced a
// nil *IdentityInfo and crashed the process. It must now return a non-nil info
// (with Method "none") so callers degrade to a clean error instead of panicking.
func TestResolveIdentityWrongTokenReturnsNonNilInfo(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := registerAgent(db, "pi", HashToken(tok), "pi"); err != nil {
		t.Fatalf("registerAgent: %v", err)
	}

	info, err := ResolveIdentity(db, "definitely-not-the-right-token")
	if err == nil {
		t.Fatal("expected an error for a non-matching token")
	}
	if info == nil {
		t.Fatal("expected non-nil info on token mismatch; nil caused a panic in callers")
	}
	if info.Agent != "" {
		t.Errorf("expected empty Agent on mismatch, got %q", info.Agent)
	}
	if info.Method != "none" {
		t.Errorf(`expected Method "none" on mismatch so MustGetAgent emits a helpful error, got %q`, info.Method)
	}
	if !info.TokenPresent {
		t.Error("expected TokenPresent=true (a token was supplied)")
	}
}

// The happy path must still assert the agent via the token.
func TestResolveIdentityCorrectTokenAsserts(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := registerAgent(db, "pi", HashToken(tok), "pi"); err != nil {
		t.Fatalf("registerAgent: %v", err)
	}

	info, err := ResolveIdentity(db, tok)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if info == nil || info.Agent != "pi" || info.Method != "token" {
		t.Fatalf("unexpected identity: %+v", info)
	}
}
