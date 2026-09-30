package tui

import (
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpGroupsAreBuiltFromTheSections(t *testing.T) {
	p := newHelpPicker()
	total := 0
	for _, sec := range helpSections {
		if len(sec.entries) == 0 {
			t.Errorf("section %q is empty", sec.name)
		}
		total += len(sec.entries)
	}
	if len(p.Items) != total {
		t.Fatalf("picker has %d items, sections have %d entries", len(p.Items), total)
	}
	if len(p.Groups) != len(helpSections) {
		t.Fatalf("picker has %d groups, want %d", len(p.Groups), len(helpSections))
	}
	idx := 0
	for i, g := range p.Groups {
		if g.Name != helpSections[i].name || g.Count != len(helpSections[i].entries) {
			t.Fatalf("group %d = %+v, want %q with %d entries", i, g, helpSections[i].name, len(helpSections[i].entries))
		}
		if p.Items[idx].Key != helpSections[i].entries[0].key {
			t.Fatalf("group %q starts at %q, want %q", g.Name, p.Items[idx].Key, helpSections[i].entries[0].key)
		}
		idx += g.Count
	}
}

func TestHelpHasNoDuplicateKeys(t *testing.T) {
	seen := map[string]string{}
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			if prev, ok := seen[e.key]; ok {
				t.Errorf("%q is listed in both %q and %q", e.key, prev, sec.name)
			}
			seen[e.key] = sec.name
			if strings.TrimSpace(e.desc) == "" {
				t.Errorf("%q has no description", e.key)
			}
		}
	}
}

// Every real command is on the help screen, so a new command that is added to
// the command list cannot be forgotten. Theme shortcuts (/theme3linen) are
// covered by /theme, and /sound1 to /sound5 by one entry.
func TestHelpListsEveryCommand(t *testing.T) {
	listed := map[string]bool{}
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			listed[e.key] = true
		}
	}
	for _, c := range systemCommands {
		switch {
		case strings.HasPrefix(c, "/theme") && c != "/theme":
			continue
		case len(c) == len("/sound1") && strings.HasPrefix(c, "/sound") && unicode.IsDigit(rune(c[len(c)-1])):
			c = "/sound1 to 5"
		}
		if !listed[c] {
			t.Errorf("%s is a command but is missing from the help screen", c)
		}
	}
	for _, c := range chatCommands {
		if !listed[c] {
			t.Errorf("%s is a chat command but is missing from the help screen", c)
		}
	}
}

// Enter only fills the command box for entries that are commands.
func TestHelpRunTextOnlyForCommands(t *testing.T) {
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			isCommand := strings.HasPrefix(e.key, "/")
			if isCommand && !strings.HasPrefix(e.run, "/") {
				t.Errorf("%s should fill the command box, run=%q", e.key, e.run)
			}
			if !isCommand && e.run != "" {
				t.Errorf("%s is not a command but has run=%q", e.key, e.run)
			}
		}
	}
}

func helpPlain(x m, w, h int) string {
	return ansiStripRe.ReplaceAllString(x.helpPicker.RenderHelpBox(pickerStyle(), w, h), "")
}

// Whatever the pane height, the panel fits, keeps its title and hint row, and
// always shows the selected entry as it scrolls through the whole list.
func TestHelpScrollsAndAlwaysShowsTheSelection(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for _, h := range []int{24, 30, 40, 70} {
		x := m{helpPicker: newHelpPicker()}
		x.helpPicker.Open("")
		for i := range x.helpPicker.Items {
			x.helpPicker.Idx = i
			out := helpPlain(x, 110, h)
			lines := strings.Split(out, "\n")
			if len(lines) > h {
				t.Fatalf("h=%d idx=%d: panel is %d lines, taller than the pane", h, i, len(lines))
			}
			if !strings.Contains(out, "Help") || !strings.Contains(out, "Esc") {
				t.Fatalf("h=%d idx=%d: title or hint row missing:\n%s", h, i, out)
			}
			key := x.helpPicker.Items[i].Key
			if !strings.Contains(out, key) {
				t.Fatalf("h=%d: selected entry %q is not visible:\n%s", h, key, out)
			}
		}
	}
}

func TestHelpShowsMoreMarkersOnlyWhenScrolled(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{helpPicker: newHelpPicker()}
	x.helpPicker.Open("")

	tall := helpPlain(x, 110, 200)
	if strings.Contains(tall, "more above") || strings.Contains(tall, "more below") {
		t.Fatalf("a tall pane shows everything, no markers expected:\n%s", tall)
	}
	short := helpPlain(x, 110, 24)
	if !strings.Contains(short, "more below") || strings.Contains(short, "more above") {
		t.Fatalf("top of a scrolled list should hint only below:\n%s", short)
	}
	x.helpPicker.Idx = len(x.helpPicker.Items) - 1
	end := helpPlain(x, 110, 24)
	if !strings.Contains(end, "more above") || strings.Contains(end, "more below") {
		t.Fatalf("end of a scrolled list should hint only above:\n%s", end)
	}
}

func TestHelpShowsSectionsAndKeyboardShortcuts(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{helpPicker: newHelpPicker()}
	x.helpPicker.Open("")
	out := helpPlain(x, 110, 200)
	for _, want := range []string{"Chats", "Access", "Sync", "Look and sound", "Shortcuts: navigate", "Shortcuts: messages", "/pin", "Alt+R", "Alt+G", "Ctrl+K", "Reply to a message"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q", want)
		}
	}
}

func helpKey(t *testing.T, x m, k tea.KeyMsg) m {
	t.Helper()
	next, _ := x.key(k)
	return next.(m)
}

func TestEnterOnAHelpCommandFillsTheCommandBox(t *testing.T) {
	x := m{status: "ready", helpPicker: newHelpPicker()}
	x.helpPicker.Open("")
	for i, it := range x.helpPicker.Items {
		if it.Key == "/pin" {
			x.helpPicker.Idx = i
		}
	}
	x = helpKey(t, x, tea.KeyMsg{Type: tea.KeyEnter})
	if x.helpPicker.IsOpen {
		t.Fatal("Enter should close the help")
	}
	if x.leftInput != "/pin" || !x.leftInputFocused {
		t.Fatalf("command box = %q (focused=%v), want /pin focused", x.leftInput, x.leftInputFocused)
	}
}

func TestEnterOnAHelpCommandWithArgumentsLeavesRoomToType(t *testing.T) {
	x := m{status: "ready", helpPicker: newHelpPicker()}
	x.helpPicker.Open("")
	for i, it := range x.helpPicker.Items {
		if it.Key == "/send" {
			x.helpPicker.Idx = i
		}
	}
	x = helpKey(t, x, tea.KeyMsg{Type: tea.KeyEnter})
	if x.leftInput != "/send " {
		t.Fatalf("command box = %q, want %q", x.leftInput, "/send ")
	}
}

func TestEnterOnAShortcutOnlyClosesTheHelp(t *testing.T) {
	x := m{status: "ready", helpPicker: newHelpPicker()}
	x.helpPicker.Open("")
	for i, it := range x.helpPicker.Items {
		if it.Key == "Alt+R" {
			x.helpPicker.Idx = i
		}
	}
	x = helpKey(t, x, tea.KeyMsg{Type: tea.KeyEnter})
	if x.helpPicker.IsOpen || x.leftInput != "" || x.leftInputFocused {
		t.Fatalf("a shortcut entry should just close: open=%v input=%q focused=%v", x.helpPicker.IsOpen, x.leftInput, x.leftInputFocused)
	}
}

func TestEscClosesHelpWithoutTouchingTheCommandBox(t *testing.T) {
	x := m{status: "ready", helpPicker: newHelpPicker(), leftInput: "typed"}
	x.helpPicker.Open("")
	x = helpKey(t, x, tea.KeyMsg{Type: tea.KeyEsc})
	if x.helpPicker.IsOpen || x.leftInput != "typed" {
		t.Fatalf("Esc: open=%v input=%q", x.helpPicker.IsOpen, x.leftInput)
	}
}

// At a normal pane width no description is cut short with "...".
func TestHelpDescriptionsFitAtNormalWidth(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{helpPicker: newHelpPicker()}
	x.helpPicker.Open("")
	out := helpPlain(x, 90, 300)
	if strings.Contains(out, "...") {
		t.Fatalf("a description is cut short:\n%s", out)
	}
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			if !strings.Contains(out, e.desc) {
				t.Errorf("%s: description %q is not shown in full", e.key, e.desc)
			}
		}
	}
}
