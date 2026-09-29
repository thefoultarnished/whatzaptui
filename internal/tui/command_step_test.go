package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCommandStepFollowsTheSuggestionInSteps(t *testing.T) {
	cases := []struct{ typed, want string }{
		// sync family: three commands split after "/sync".
		{"/s", "/sync"},
		{"/sync", "/synccontacts"},
		{"/syncg", "/syncgroups"},
		{"/synch", "/synchistory"},
		// blacklist, blacklistall and block.
		{"/b", "/bl"},
		{"/bl", "/blacklist"},
		{"/blacklist", "/blacklistall"},
		{"/blo", "/block"},
		// rename and restart.
		{"/r", "/re"},
		{"/re", "/rename"},
		{"/res", "/restart"},
		// mouseon and mouseoff.
		{"/m", "/mouseo"},
		{"/mouseo", "/mouseon"},
		{"/mouseof", "/mouseoff"},
		// one command on the branch: Tab completes it straight away.
		{"/a", "/allcontacts"},
		{"/ar", "/archive"},
		// sound1..5, soundon, soundoff.
		{"/sou", "/sound"},
		{"/sound", "/sound1"},
		{"/soundo", "/soundon"},
		{"/soundof", "/soundoff"},
		// a full command that is also the start of a longer one steps on to it.
		{"/w", "/whitelist"},
		{"/whitelist", "/whitelistall"},
		// theme shortcuts.
		{"/t", "/theme"},
		{"/theme", "/theme1"},
		{"/theme1", "/theme1halo"},
		// only the start of the input matters, and case does not.
		{"/SYNC", "/synccontacts"},
		{"  /sync  ", "/synccontacts"},
		// the bare slash follows the existing ghost (/theme), so the first step is /t.
		{"/", "/t"},
	}
	for _, c := range cases {
		if got := commandStep(c.typed); got != c.want {
			t.Errorf("commandStep(%q) = %q, want %q", c.typed, got, c.want)
		}
	}
}

func TestCommandStepHasNothingToDoWithoutASuggestion(t *testing.T) {
	for _, typed := range []string{"", "hello", "/zzz", "/synccontacts", "/exit", "/soundoff", "sync"} {
		if got := commandStep(typed); got != "" {
			t.Errorf("commandStep(%q) = %q, want no step", typed, got)
		}
	}
}

// Whatever the user has typed so far, repeated Tab presses must always end on
// a real command without skipping a choice or looping.
func TestCommandStepReachesARealCommandFromAnyPrefix(t *testing.T) {
	isCommand := map[string]bool{}
	for _, c := range systemCommands {
		isCommand[c] = true
	}
	for _, cmd := range systemCommands {
		for n := 1; n <= len(cmd); n++ {
			typed := cmd[:n]
			if commandBestMatch(typed) == "" {
				continue // the full command itself has nothing left to complete
			}
			cur := typed
			for i := 0; ; i++ {
				if i > len(cmd)+3 {
					t.Fatalf("%q: Tab keeps going without finishing (stuck at %q)", typed, cur)
				}
				next := commandStep(cur)
				if next == "" {
					break
				}
				if len(next) <= len(cur) || !strings.HasPrefix(next, cur) {
					t.Fatalf("%q: step %q -> %q does not move forward along the same text", typed, cur, next)
				}
				// A step never skips a choice: every command that continues the
				// suggestion's way must still start with the step.
				best := commandBestMatch(cur)
				branch := cur + best[len(cur):len(cur)+1]
				for _, c := range systemCommands {
					if strings.HasPrefix(c, branch) && !strings.HasPrefix(c, next) {
						t.Fatalf("%q: step %q skipped past %q, which shares %q", cur, next, c, branch)
					}
				}
				cur = next
			}
			if !isCommand[cur] && commandBestMatch(cur) == "" {
				t.Fatalf("%q ended on %q, which is not a command", typed, cur)
			}
		}
	}
}

func tabIn(x m) m {
	next, _ := x.handleLeftInput(tea.KeyMsg{Type: tea.KeyTab})
	return next.(m)
}

func typeIn(x m, s string) m {
	next, _ := x.handleLeftInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(m)
}

func TestTabInTheCommandBoxMovesInSteps(t *testing.T) {
	x := m{leftInputFocused: true, leftInput: "/s"}

	x = tabIn(x)
	if x.leftInput != "/sync" {
		t.Fatalf("first Tab = %q, want /sync", x.leftInput)
	}
	if got := commandBestMatch(x.leftInput); got != "/synccontacts" {
		t.Fatalf("the ghost should keep showing /synccontacts, got %q", got)
	}
	x = tabIn(x)
	if x.leftInput != "/synccontacts" || !x.leftInputFocused {
		t.Fatalf("second Tab = %q (focused=%v), want /synccontacts still focused", x.leftInput, x.leftInputFocused)
	}
}

func TestTypingAfterAStepChangesTheSuggestion(t *testing.T) {
	x := m{leftInputFocused: true, leftInput: "/s"}
	x = tabIn(x) // /sync
	x = typeIn(x, "g")
	if got := commandBestMatch(x.leftInput); got != "/syncgroups" {
		t.Fatalf("after typing g the ghost should be /syncgroups, got %q", got)
	}
	x = tabIn(x)
	if x.leftInput != "/syncgroups" {
		t.Fatalf("Tab = %q, want /syncgroups", x.leftInput)
	}
}

func TestTabWithNoSuggestionLeavesTheCommandBox(t *testing.T) {
	x := m{leftInputFocused: true, leftInput: "/zzz"}
	x = tabIn(x)
	if x.leftInputFocused || x.leftInput != "/zzz" {
		t.Fatalf("Tab with nothing to complete should leave the box unchanged: focused=%v input=%q", x.leftInputFocused, x.leftInput)
	}
}

func TestCommandBoxShowsTheGhostAfterAStep(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{leftInputFocused: true, leftInput: "/sync"}
	box := ansiStripRe.ReplaceAllString(x.renderCommandBox(40), "")
	if !strings.Contains(box, "/synccontacts") {
		t.Fatalf("the command box should still show the suggestion /synccontacts: %q", box)
	}
}

func TestEnterRunsWhatIsTypedNotTheGhost(t *testing.T) {
	// "/whitelist" is a command of its own even though "/whitelistall" continues it.
	if !strings.HasPrefix("/whitelistall", "/whitelist") {
		t.Skip("test data changed")
	}
	x := m{leftInputFocused: true, leftInput: "/whitelist", status: "ready"}
	if got := commandStep(x.leftInput); got != "/whitelistall" {
		t.Fatalf("Tab should offer the longer command, got %q", got)
	}
	if x.leftInput != "/whitelist" {
		t.Fatalf("the typed text must stay as typed until Tab is pressed, got %q", x.leftInput)
	}
}
