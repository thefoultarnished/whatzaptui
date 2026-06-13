package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A-6: confirmDialog is the "are you sure?" modal shown before /logout,
// /whitelistall, /blacklistall.

func TestConfirmDialogOpenDefaultsToNo(t *testing.T) {
	var d confirmDialog
	d.Open("Log out?", "Ends your session.", "logout")
	if !d.open {
		t.Fatalf("Open() did not set open = true")
	}
	if d.idx != 1 {
		t.Fatalf("Open() idx = %d, want 1 (No)", d.idx)
	}
	if d.action != "logout" {
		t.Fatalf("Open() action = %q, want %q", d.action, "logout")
	}
}

func TestConfirmDialogHandleYesNo(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key        tea.KeyMsg
		startIdx   int
		wantAction string
	}{
		{name: "y confirms regardless of selection", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, startIdx: 1, wantAction: "confirm"},
		{name: "Y confirms", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")}, startIdx: 1, wantAction: "confirm"},
		{name: "n cancels regardless of selection", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, startIdx: 0, wantAction: "cancel"},
		{name: "N cancels", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")}, startIdx: 0, wantAction: "cancel"},
		{name: "Enter on Yes confirms", key: tea.KeyMsg{Type: tea.KeyEnter}, startIdx: 0, wantAction: "confirm"},
		{name: "Enter on No (default) cancels", key: tea.KeyMsg{Type: tea.KeyEnter}, startIdx: 1, wantAction: "cancel"},
		{name: "Esc cancels", key: tea.KeyMsg{Type: tea.KeyEsc}, startIdx: 0, wantAction: "cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := confirmDialog{open: true, action: "logout", idx: tc.startIdx}
			action, done := d.Handle(tc.key)
			if !done {
				t.Fatalf("Handle(%v) done = false, want true", tc.key)
			}
			if action != tc.wantAction {
				t.Fatalf("Handle(%v) action = %q, want %q", tc.key, action, tc.wantAction)
			}
		})
	}
}

func TestConfirmDialogHandleArrowTogglesSelection(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
		{Type: tea.KeyTab},
	} {
		d := confirmDialog{open: true, idx: 1}
		action, done := d.Handle(key)
		if done {
			t.Fatalf("Handle(%v) done = true, want false (just toggled selection)", key)
		}
		if action != "" {
			t.Fatalf("Handle(%v) action = %q, want empty", key, action)
		}
		if d.idx != 0 {
			t.Fatalf("Handle(%v) idx = %d, want 0 after toggling from 1", key, d.idx)
		}

		// toggling again flips back
		d.Handle(key)
		if d.idx != 1 {
			t.Fatalf("Handle(%v) idx = %d, want 1 after toggling back", key, d.idx)
		}
	}
}

func TestConfirmDialogClose(t *testing.T) {
	d := confirmDialog{open: true}
	d.Close()
	if d.open {
		t.Fatalf("Close() left open = true")
	}
}
