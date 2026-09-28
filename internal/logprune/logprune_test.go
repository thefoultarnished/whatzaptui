package logprune

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestKeepNewestDeletesOldestOnly(t *testing.T) {
	dir := t.TempDir()
	for d := 1; d <= 5; d++ {
		touch(t, dir, fmt.Sprintf("backend-2026090%dT120000Z.log", d))
	}
	if err := KeepNewest(dir, "backend-", ".log", 3); err != nil {
		t.Fatal(err)
	}
	for d := 1; d <= 5; d++ {
		name := fmt.Sprintf("backend-2026090%dT120000Z.log", d)
		if want := d > 2; exists(dir, name) != want {
			t.Fatalf("%s exists=%v, want %v", name, !want, want)
		}
	}
}

func TestKeepNewestLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "backend-20260901T000000Z.log")
	touch(t, dir, "backend-20260902T000000Z.log")
	touch(t, dir, "notes.txt")
	touch(t, dir, "other-20260101T000000Z.log")
	touch(t, dir, "backend-20250101T000000Z.txt")
	if err := KeepNewest(dir, "backend-", ".log", 1); err != nil {
		t.Fatal(err)
	}
	if exists(dir, "backend-20260901T000000Z.log") || !exists(dir, "backend-20260902T000000Z.log") {
		t.Fatal("expected only the newest backend log to remain")
	}
	for _, n := range []string{"notes.txt", "other-20260101T000000Z.log", "backend-20250101T000000Z.txt"} {
		if !exists(dir, n) {
			t.Fatalf("%s should not be touched", n)
		}
	}
}

func TestKeepNewestUnderLimitAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "tui-20260901T000000Z.log")
	if err := KeepNewest(dir, "tui-", ".log", 10); err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "tui-20260901T000000Z.log") {
		t.Fatal("file under the limit was deleted")
	}
	if err := KeepNewest(filepath.Join(dir, "missing"), "tui-", ".log", 10); err == nil {
		t.Fatal("expected error for missing dir")
	}
}
