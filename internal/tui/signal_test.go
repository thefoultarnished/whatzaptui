package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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


// Ctrl+L asks Bubble Tea to clear and repaint the whole screen, from any
// mode, without touching what the user is typing.
func TestCtrlLClearsScreenInEveryMode(t *testing.T) {
	cases := map[string]m{
		"nav":          {status: "ready", mode: "nav"},
		"chat+typing":  {status: "ready", mode: "chat", active: "1@s.whatsapp.net", input: "half a message"},
		"left input":   {status: "ready", mode: "nav", leftInputFocused: true, leftInput: "/he"},
		"file browser": {status: "ready", mode: "chat", fileBrowserOpen: true},
		"loading":      {status: "Connecting..."},
	}
	for name, x := range cases {
		before := x.revision
		next, cmd := x.key(tea.KeyMsg{Type: tea.KeyCtrlL})
		if cmd == nil {
			t.Fatalf("%s: ctrl+l must return a command", name)
		}
		if cmd() != tea.ClearScreen() {
			t.Fatalf("%s: ctrl+l must return tea.ClearScreen", name)
		}
		got := next.(m)
		if got.revision == before {
			t.Fatalf("%s: ctrl+l must invalidate the render cache", name)
		}
		if got.input != x.input || got.leftInput != x.leftInput {
			t.Fatalf("%s: ctrl+l must not change typed text", name)
		}
	}
}

func questionKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
}

// "?" on the chat list opens the same help screen as /help.
func TestQuestionMarkOpensHelpOnChatList(t *testing.T) {
	x := m{status: "ready", mode: "nav", sidebarFocused: true}
	next, _ := x.key(questionKey())
	if !next.(m).helpPicker.IsOpen {
		t.Fatal("? on the chat list should open the help picker")
	}
}

// While the user is typing, "?" must never open help. The composer and
// command box keep it as a character; search only takes letters and numbers,
// so it drops it (as it always has).
func TestQuestionMarkDoesNotOpenHelpWhileTyping(t *testing.T) {
	cases := map[string]struct {
		x         m
		wantTyped bool
	}{
		"chat composer": {m{status: "ready", mode: "chat", active: "1@s.whatsapp.net", defaultAllowed: true}, true},
		"command box":   {m{status: "ready", mode: "nav", leftInputFocused: true}, true},
		"search":        {m{status: "ready", mode: "search", sidebarFocused: true}, false},
	}
	for name, c := range cases {
		next, _ := c.x.key(questionKey())
		got := next.(m)
		if got.helpPicker.IsOpen {
			t.Fatalf("%s: ? must not open help while typing", name)
		}
		typed := got.input + got.inputBuf + got.leftInput + got.searchInput
		if c.wantTyped && !strings.Contains(typed, "?") {
			t.Fatalf("%s: ? was not typed (input=%q inputBuf=%q leftInput=%q)", name, got.input, got.inputBuf, got.leftInput)
		}
	}
}
