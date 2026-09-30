package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func chatItems(labels ...string) []Item {
	items := make([]Item, len(labels))
	for i, l := range labels {
		items[i] = Item{Key: "id-" + l, Label: l}
	}
	return items
}

func typeRunes(p *Picker, s string) {
	p.HandleFilterList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestFilterListNarrowsByTypingIgnoringCase(t *testing.T) {
	p := New("Forward to", chatItems("Alice", "Bob Stone", "Cara", "Bobby"))
	p.Open("")
	typeRunes(&p, "BO")
	vis := p.VisibleItems()
	if len(vis) != 2 || vis[0].Label != "Bob Stone" || vis[1].Label != "Bobby" {
		t.Fatalf("visible = %+v, want the two Bob entries", vis)
	}
	typeRunes(&p, "b") // "BOb": still both
	if vis := p.VisibleItems(); len(vis) != 2 {
		t.Fatalf("Bob still matches both, got %+v", vis)
	}
	typeRunes(&p, "b") // "BObb": only Bobby
	if vis := p.VisibleItems(); len(vis) != 1 || vis[0].Label != "Bobby" {
		t.Fatalf("Bobby only, got %+v", vis)
	}
	p.HandleFilterList(tea.KeyMsg{Type: tea.KeyBackspace})
	if p.Query != "BOb" || len(p.VisibleItems()) != 2 {
		t.Fatalf("Backspace should remove the last letter and widen the list, query = %q", p.Query)
	}
}

func TestFilterListSelectionMovesAndWrapsInsideTheVisibleList(t *testing.T) {
	p := New("Forward to", chatItems("Alice", "Bob", "Cara"))
	p.Open("")
	down := tea.KeyMsg{Type: tea.KeyDown}
	up := tea.KeyMsg{Type: tea.KeyUp}
	p.HandleFilterList(down)
	p.HandleFilterList(down)
	if p.Idx != 2 {
		t.Fatalf("Idx = %d, want 2", p.Idx)
	}
	p.HandleFilterList(down)
	if p.Idx != 0 {
		t.Fatalf("Down at the end wraps to the top, got %d", p.Idx)
	}
	p.HandleFilterList(up)
	if p.Idx != 2 {
		t.Fatalf("Up at the top wraps to the end, got %d", p.Idx)
	}
	// Typing resets the selection to the first match.
	typeRunes(&p, "a")
	if p.Idx != 0 {
		t.Fatalf("typing should reset the selection, got %d", p.Idx)
	}
}

func TestFilterListEnterEscAndEmptyList(t *testing.T) {
	p := New("Forward to", chatItems("Alice"))
	p.Open("")
	if a, done := p.HandleFilterList(tea.KeyMsg{Type: tea.KeyEnter}); a != "confirm" || !done {
		t.Fatalf("Enter = (%q,%v)", a, done)
	}
	if a, done := p.HandleFilterList(tea.KeyMsg{Type: tea.KeyEsc}); a != "cancel" || !done {
		t.Fatalf("Esc = (%q,%v)", a, done)
	}
	typeRunes(&p, "zzz")
	if len(p.VisibleItems()) != 0 {
		t.Fatal("nothing should match zzz")
	}
	// Moving in an empty list must not panic.
	p.HandleFilterList(tea.KeyMsg{Type: tea.KeyDown})
	p.HandleFilterList(tea.KeyMsg{Type: tea.KeyUp})
	if out := ansi.ReplaceAllString(p.RenderFilterListBox(Style{}, 80, 30), ""); !strings.Contains(out, "no matching chats") {
		t.Fatalf("empty result should say so:\n%s", out)
	}
}

func TestFilterListAltKeysDoNotType(t *testing.T) {
	p := New("Forward to", chatItems("Alice"))
	p.Open("")
	p.HandleFilterList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t"), Alt: true})
	if p.Query != "" {
		t.Fatalf("Alt+key must not be typed into the filter, got %q", p.Query)
	}
}

func TestRenderFilterListShowsSearchNotesAndSelection(t *testing.T) {
	items := chatItems("Alice", "Bob")
	items[1].Dim = true
	items[1].Desc = "not allowed"
	p := New("Forward to", items)
	p.Open("")
	out := ansi.ReplaceAllString(p.RenderFilterListBox(Style{}, 80, 30), "")
	for _, want := range []string{"Forward to", "type to filter", "Alice", "Bob", "not allowed", "Enter", "Esc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel missing %q:\n%s", want, out)
		}
	}
	typeRunes(&p, "ali")
	out = ansi.ReplaceAllString(p.RenderFilterListBox(Style{}, 80, 30), "")
	if !strings.Contains(out, "ali") || strings.Contains(out, "Bob") || strings.Contains(out, "type to filter") {
		t.Fatalf("typed text should replace the placeholder and hide non-matches:\n%s", out)
	}
}

func TestRenderFilterListScrollsAroundTheSelection(t *testing.T) {
	labels := make([]string, 40)
	for i := range labels {
		labels[i] = "Chat" + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	p := New("Forward to", chatItems(labels...))
	p.Open("")
	for _, h := range []int{22, 30, 60} {
		for i := range p.Items {
			p.Idx = i
			out := ansi.ReplaceAllString(p.RenderFilterListBox(Style{}, 80, h), "")
			if n := len(strings.Split(out, "\n")); n > h {
				t.Fatalf("h=%d idx=%d: panel is %d lines, taller than the pane", h, i, n)
			}
			if !strings.Contains(out, p.Items[i].Label) {
				t.Fatalf("h=%d: selected %q not visible:\n%s", h, p.Items[i].Label, out)
			}
			if !strings.Contains(out, "Esc") {
				t.Fatalf("h=%d idx=%d: hint row missing", h, i)
			}
		}
	}
	p.Idx = 0
	if top := ansi.ReplaceAllString(p.RenderFilterListBox(Style{}, 80, 24), ""); !strings.Contains(top, "more below") || strings.Contains(top, "more above") {
		t.Fatalf("top of a long list hints only below:\n%s", top)
	}
}
