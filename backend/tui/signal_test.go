package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCleanupRemovesMediaFiles(t *testing.T) {
	dir := t.TempDir()
	paths := map[string]string{}
	for _, id := range []string{"a", "b"} {
		p := filepath.Join(dir, id+".tmp")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths[id] = p
	}
	x := m{downloadedMedia: paths} // not demo, backend never started: removes files, skips kill
	x.cleanup()
	for id, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("media %s not removed", id)
		}
	}
}

func TestCleanupNilSafe(t *testing.T) {
	var x m
	x.cleanup() // must not panic: nil media, nil ws, nil backend, nil cancel
}

func TestCtrlCQuitsAndCancelsRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x := m{status: "ready", apiCtx: ctx, apiCancel: cancel}
	_, cmd := x.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must return a quit cmd")
	}
	if ctx.Err() == nil {
		t.Fatal("ctrl+c must cancel in-flight requests")
	}
}

func TestExitCommandCancelsRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x := m{status: "ready", apiCtx: ctx, apiCancel: cancel}
	cmd, ok := x.runCommand("/exit", true)
	if !ok || cmd == nil {
		t.Fatal("/exit must return a quit cmd")
	}
	if ctx.Err() == nil {
		t.Fatal("/exit must cancel in-flight requests")
	}
}
