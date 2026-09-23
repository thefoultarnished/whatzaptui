package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackendBinStale(t *testing.T) {
	dir := t.TempDir()

	// Missing binary is stale.
	if !backendBinStale(dir) {
		t.Fatalf("missing binary should be stale")
	}

	bin := backendBinPath(dir)
	if filepath.Base(bin) != "backend.exe" && filepath.Base(bin) != "backend" {
		t.Fatalf("unexpected bin name %q", bin)
	}

	// Fresh binary newer than sources is not stale.
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.go"), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if backendBinStale(dir) {
		t.Fatalf("fresh binary should not be stale")
	}

	// Newer source makes it stale again.
	now := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.go"), now, now); err != nil {
		t.Fatal(err)
	}
	if !backendBinStale(dir) {
		t.Fatalf("binary older than source should be stale")
	}
}
