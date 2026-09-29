package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func items(keys ...string) []Item {
	out := make([]Item, len(keys))
	for i, k := range keys {
		out[i] = Item{Key: k, Label: k}
	}
	return out
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// Positive: Open selects the current key; Close(true) returns the new pick,
// Close(false) restores the original.
func TestOpenCloseConfirmAndCancel(t *testing.T) {
	p := New("T", items("a", "b", "c"))
	p.Open("b")
	if !p.IsOpen || p.Idx != 1 {
		t.Fatalf("Open(b): IsOpen=%v Idx=%d, want true 1", p.IsOpen, p.Idx)
	}
	p.Handle(key(tea.KeyDown))
	if got := p.Close(true); got != "c" || p.IsOpen {
		t.Fatalf("Close(true) = %q open=%v, want c false", got, p.IsOpen)
	}
	p.Open("b")
	p.Handle(key(tea.KeyDown))
	if got := p.Close(false); got != "b" {
		t.Fatalf("Close(false) = %q, want original b", got)
	}
}

// Edge: an unknown current key falls back to the first item.
func TestOpenUnknownKeySelectsFirst(t *testing.T) {
	p := New("T", items("a", "b"))
	p.Open("zzz")
	if p.Idx != 0 {
		t.Fatalf("Idx = %d, want 0", p.Idx)
	}
}

// Edge: single-column lists wrap around at both ends.
func TestSingleColumnWraps(t *testing.T) {
	p := New("T", items("a", "b", "c"))
	p.Open("a")
	p.Handle(key(tea.KeyUp))
	if p.SelectedKey() != "c" {
		t.Fatalf("Up from first = %q, want c", p.SelectedKey())
	}
	p.Handle(key(tea.KeyDown))
	if p.SelectedKey() != "a" {
		t.Fatalf("Down from last = %q, want a", p.SelectedKey())
	}
}

// Edge: a long label forces single-column even with many items, so labels
// never wrap in the two-column layout.
func TestLongLabelForcesSingleColumn(t *testing.T) {
	its := items("a", "b", "c", "d", "e", "f")
	if p := New("T", its); p.isSingleCol() {
		t.Fatal("6 short items should use two columns")
	}
	its[2].Label = strings.Repeat("x", 25)
	if p := New("T", its); !p.isSingleCol() {
		t.Fatal("a label over 20 wide should force a single column")
	}
}

// Positive: grouped navigation crosses into the next group keeping the
// column, and clamps when the next group is narrower.
func TestGroupedNavigationCrossesGroups(t *testing.T) {
	p := New("T", items("a1", "a2", "a3", "a4", "b1"))
	p.Groups = []Group{{Name: "A", Count: 4}, {Name: "B", Count: 1}}
	p.Open("a4") // group A, row 1, col 1
	p.HandleTheme(key(tea.KeyDown))
	if p.SelectedKey() != "b1" {
		t.Fatalf("Down from a4 = %q, want b1 (clamped to the only column)", p.SelectedKey())
	}
	p.HandleTheme(key(tea.KeyUp))
	if p.SelectedKey() != "a3" {
		t.Fatalf("Up from b1 = %q, want a3 (last row, same column)", p.SelectedKey())
	}
	if a, done := p.HandleTheme(key(tea.KeyEnter)); a != "confirm" || !done {
		t.Fatalf("Enter = (%q,%v), want (confirm,true)", a, done)
	}
}

// Negative: Enter on a settings selector asks to open its sub-picker, on a
// toggle it confirms, and Esc cancels.
func TestHandleSettingsActions(t *testing.T) {
	p := New("S", []Item{{Key: "t1", Value: "ON"}, {Key: "s1", Selector: true, Value: "x"}})
	p.Open("t1")
	if a, _ := p.HandleSettings(key(tea.KeyEnter)); a != "confirm" {
		t.Fatalf("Enter on toggle = %q, want confirm", a)
	}
	p.Idx = 1
	if a, _ := p.HandleSettings(key(tea.KeyEnter)); a != "selector" {
		t.Fatalf("Enter on selector = %q, want selector", a)
	}
	if a, done := p.HandleSettings(key(tea.KeyEsc)); a != "cancel" || !done {
		t.Fatalf("Esc = (%q,%v), want (cancel,true)", a, done)
	}
}

// Negative: every layout renders with a zero Style (no Outline/Shine hooks)
// without panicking and still shows its title.
func TestRenderWithZeroStyle(t *testing.T) {
	var s Style
	grouped := New("Grouped", items("a", "b", "c"))
	grouped.Groups = []Group{{Name: "G", Count: 3}}
	typing := New("Typing", []Item{{Key: SquaresKey, Name: "Squares", Icons: []string{"◼"}}, {Key: "dots", Name: "Dots", Icons: []string{"."}}})
	settings := New("Settings", []Item{{Key: "t", Value: "ON"}, {Key: "s", Selector: true, Value: "v"}})

	for name, out := range map[string]string{
		"plain":    func() string { p := New("Plain", items("a", "b")); return p.Render(s, 80, 20) }(),
		"theme":    grouped.RenderThemeBox(s, 80, 30),
		"help":     grouped.RenderHelp(s, 100, 30),
		"typing":   typing.RenderTypingAnimation(s, 80, 20, 1),
		"settings": settings.RenderSettings(s, 100, 30),
	} {
		if out == "" {
			t.Errorf("%s: empty render", name)
		}
	}
	if out := grouped.RenderThemeBox(s, 80, 30); !strings.Contains(out, "Grouped") {
		t.Error("theme render missing title")
	}
}

// Edge: the squares animation is always 8 cells and cycles every 6 frames,
// including negative frame numbers.
func TestSquaresIconWidthAndCycle(t *testing.T) {
	for f := -6; f < 12; f++ {
		plain := SquaresIcon(f, "", false)
		if n := strings.Count(plain, "◼") + strings.Count(plain, "·"); n != 8 {
			t.Fatalf("frame %d has %d cells, want 8", f, n)
		}
	}
	if SquaresIcon(0, "", false) != SquaresIcon(6, "", false) {
		t.Fatal("frame 6 should repeat frame 0")
	}
	if SquaresIcon(0, "", true) == "" {
		t.Fatal("light-background render is empty")
	}
}
