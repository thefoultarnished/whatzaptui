package buildid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentMatchesExecutable(t *testing.T) {
	id := Current()
	if id == "" {
		t.Fatal("Current() returned empty build ID")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	expected, err := OfFile(exe)
	if err != nil {
		t.Fatalf("OfFile(%s): %v", exe, err)
	}

	if id != expected {
		t.Fatalf("Current() = %q, want %q", id, expected)
	}
}

func TestOfFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "sample.bin")
	if err := os.WriteFile(p, []byte("hello buildid"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := OfFile(p)
	if err != nil {
		t.Fatalf("OfFile failed: %v", err)
	}
	want := "1182373f944dd5ec7962df1de012da824519d89111b1c0ffde6e0e7a8a75656d"
	if got != want {
		t.Fatalf("OfFile = %q, want %q", got, want)
	}
}
