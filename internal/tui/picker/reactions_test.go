package picker

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

func TestWrapListBreaksOnlyAfterCommas(t *testing.T) {
	got := wrapList("Ann Lee, Bob, Cat, Dan", 12)
	want := []string{"Ann Lee,", "Bob, Cat,", "Dan"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("wrapList = %q, want %q", got, want)
	}
	if got := wrapList("Solo", 3); len(got) != 1 || got[0] != "Solo" {
		t.Fatalf("an item wider than the width stays whole, got %q", got)
	}
	if got := wrapList("", 10); len(got) != 0 {
		t.Fatalf("empty text wraps to nothing (the renderer adds a blank line), got %q", got)
	}
}

func TestHandleReactionsKeys(t *testing.T) {
	p := New("Reactions", []Item{{Key: "a"}, {Key: "b"}, {Key: "c"}})
	p.Open("")
	for _, k := range []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("q")}} {
		if !p.HandleReactions(k) {
			t.Fatalf("%v should close the list", k)
		}
	}
	if p.HandleReactions(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) {
		t.Fatal("other letters must not close it")
	}
	p.HandleReactions(tea.KeyMsg{Type: tea.KeyUp})
	if p.Idx != 0 {
		t.Fatalf("Up at the top stays at 0, got %d", p.Idx)
	}
	p.HandleReactions(tea.KeyMsg{Type: tea.KeyDown})
	p.HandleReactions(tea.KeyMsg{Type: tea.KeyDown})
	p.HandleReactions(tea.KeyMsg{Type: tea.KeyDown})
	if p.Idx != 2 {
		t.Fatalf("Down stops at the last entry, got %d", p.Idx)
	}
}

func TestRenderReactionsShowsEntriesAndWraps(t *testing.T) {
	p := New("Reactions", []Item{
		{Key: "fire 4", Desc: "Ann Lee, Bob Stone, Cat Field, Dan Marsh, Eve Rose"},
		{Key: "heart 1", Desc: "Fay"},
	})
	p.Open("")
	out := ansi.ReplaceAllString(p.RenderReactionsBox(Style{}, 80, 30), "")
	for _, want := range []string{"Reactions", "fire 4", "Ann Lee,", "Eve Rose", "heart 1", "Fay", "Esc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "more below") || strings.Contains(out, "more above") {
		t.Fatalf("everything fits, so no scroll hints expected:\n%s", out)
	}
}

func TestRenderReactionsScrollHints(t *testing.T) {
	items := make([]Item, 12)
	for i := range items {
		items[i] = Item{Key: "e" + string(rune('a'+i)) + " 1", Desc: "Person" + string(rune('A'+i))}
	}
	p := New("Reactions", items)
	p.Open("")
	top := ansi.ReplaceAllString(p.RenderReactionsBox(Style{}, 80, 16), "")
	if !strings.Contains(top, "more below") || strings.Contains(top, "more above") {
		t.Fatalf("top of a long list should hint only below:\n%s", top)
	}
	p.Idx = 3
	mid := ansi.ReplaceAllString(p.RenderReactionsBox(Style{}, 80, 16), "")
	if !strings.Contains(mid, "more above") || strings.Contains(mid, "PersonA") {
		t.Fatalf("scrolled list should hint above and drop the first entry:\n%s", mid)
	}
}

func TestPanelPickerRendersWithZeroStyleInOneAndTwoColumns(t *testing.T) {
	var s Style
	short := New("Short", []Item{{Key: "a", Label: "alpha"}, {Key: "b", Label: "beta"}})
	short.Panel = true
	long := New("Long", make([]Item, 9))
	for i := range long.Items {
		long.Items[i] = Item{Key: string(rune('a' + i)), Label: "a fairly long label number " + string(rune('a'+i))}
	}
	long.Panel = true
	for name, p := range map[string]*Picker{"short": &short, "long": &long} {
		out := ansi.ReplaceAllString(p.RenderBox(s, 80, 30), "")
		if !strings.Contains(out, p.Title) || !strings.Contains(out, "Enter") {
			t.Errorf("%s: panel missing title or hint:\n%s", name, out)
		}
	}
	if long.isSingleCol() {
		t.Error("a long panel list must use two columns even with wide labels")
	}
	if !short.isSingleCol() {
		t.Error("a short panel list stays one column")
	}
}
