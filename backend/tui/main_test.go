package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveSessionToken generates and persists a fresh token (0600) when no
// token file exists yet (S-1).
func TestResolveSessionTokenGeneratesAndWritesFile(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())

	token, err := resolveSessionToken()
	if err != nil {
		t.Fatalf("resolveSessionToken: %v", err)
	}
	if strings.TrimSpace(token) == "" {
		t.Fatalf("resolveSessionToken returned empty token")
	}

	path, err := sessionTokenPath()
	if err != nil {
		t.Fatalf("sessionTokenPath: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if strings.TrimSpace(string(b)) != token {
		t.Fatalf("token file contents = %q, want %q", string(b), token)
	}
}

// resolveSessionToken reuses an existing token file rather than generating
// a new one — restarting the TUI while the backend keeps running must not
// invalidate the existing session.
func TestResolveSessionTokenReusesExistingFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("WHATZAP_DATA_DIR", dataDir)

	tokenDir := filepath.Join(dataDir, "backend")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := "existing-session-token"
	if err := os.WriteFile(filepath.Join(tokenDir, "session.token"), []byte(want), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	got, err := resolveSessionToken()
	if err != nil {
		t.Fatalf("resolveSessionToken: %v", err)
	}
	if got != want {
		t.Fatalf("resolveSessionToken = %q, want %q (existing file)", got, want)
	}
}

// An empty (but present) token file is treated as absent — a fresh token
// is generated rather than handing the backend an empty Authorization
// header.
func TestResolveSessionTokenIgnoresEmptyFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("WHATZAP_DATA_DIR", dataDir)

	tokenDir := filepath.Join(dataDir, "backend")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tokenDir, "session.token"), []byte("   "), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	got, err := resolveSessionToken()
	if err != nil {
		t.Fatalf("resolveSessionToken: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("resolveSessionToken returned empty token for empty file")
	}
}
