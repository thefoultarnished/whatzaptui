package picker

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func testStyle() Style {
	return Style{
		V2: true, Text: "#ffffff", TextSecondary: "#cccccc", Muted: "#888888", Accent: "#00aaff",
		Purple: "#aa00ff", BorderSubtle: "#444444", BgSelected: "#223344", PanelBg: "#101010", BadgeInk: "#000000",
	}
}

func plainPanel(s string) []string { return strings.Split(ansi.ReplaceAllString(s, ""), "\n") }

func TestPanelHasTheSameFrameAsAPicker(t *testing.T) {
	p := testStyle().NewPanel("New poll", 100, 30, 60)
	p.Section("Question")
	p.Row(RowOpts{Label: "Lunch?", Selected: true})
	p.Blank()
	p.Muted("a quiet line")
	p.Hint("Esc", "close")
	lines := plainPanel(p.Render())
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"New poll", "esc", "QUESTION", "Lunch?", "a quiet line", "Esc close", "────"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in:\n%s", want, joined)
		}
	}
	if strings.Count(joined, "─") < 2*p.InnerW {
		t.Errorf("a divider under the title and one above the hint:\n%s", joined)
	}
	if !strings.Contains(joined, "▌ Lunch?") {
		t.Errorf("the selected row carries the bar marker like a picker:\n%s", joined)
	}
}

func TestPanelRowsAreAllTheSameWidth(t *testing.T) {
	p := testStyle().NewPanel("Vote", 100, 30, 60)
	p.Text("Lunch on Friday?", true)
	p.Row(RowOpts{Label: "Pizza", Note: "2", Selected: true})
	p.Row(RowOpts{Label: "Sushi", Note: "1", Color: "#00aaff", Bold: true})
	p.Row(RowOpts{Label: "Withdraw my vote", Dim: true})
	p.Note("a warning", "#ff0000")
	p.Hint("↑↓", "move", "Enter", "vote")
	lines := plainPanel(p.Render())
	want := lipgloss.Width(lines[0])
	for i, l := range lines {
		if lipgloss.Width(l) != want {
			t.Errorf("line %d is %d cells wide, the first is %d: %q", i, lipgloss.Width(l), want, l)
		}
	}
}

func TestPanelRowStyles(t *testing.T) {
	v2 := testStyle()
	legacy := testStyle()
	legacy.V2 = false
	for name, s := range map[string]Style{"v2": v2, "legacy": legacy} {
		p := s.NewPanel("T", 100, 30, 60)
		p.Row(RowOpts{Label: "one", Selected: true})
		p.Row(RowOpts{Label: "two"})
		lines := plainPanel(p.Render())
		var one, two string
		for _, l := range lines {
			if strings.Contains(l, "one") {
				one = l
			}
			if strings.Contains(l, "two") {
				two = l
			}
		}
		if one == "" || two == "" {
			t.Fatalf("%s: rows missing:\n%s", name, strings.Join(lines, "\n"))
		}
		if lipgloss.Width(one) != lipgloss.Width(two) {
			t.Errorf("%s: selected and normal rows differ in width", name)
		}
	}
}

func TestPanelCutsLongText(t *testing.T) {
	Truncate = func(s string, n int) string {
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		return string(r[:n-1]) + "…"
	}
	t.Cleanup(func() { Truncate = func(s string, n int) string { return s } })
	p := testStyle().NewPanel("T", 100, 30, 60)
	long := strings.Repeat("x", 300)
	p.Text(long, false)
	p.Muted(long)
	p.Section(long)
	p.Note(long, "#ff0000")
	p.Row(RowOpts{Label: long, Note: long})
	p.Hint("Esc", "close")
	lines := plainPanel(p.Render())
	want := lipgloss.Width(lines[0])
	for i, l := range lines {
		if lipgloss.Width(l) != want {
			t.Errorf("line %d is %d wide, want %d", i, lipgloss.Width(l), want)
		}
	}
}

func TestPanelWidthFollowsTheArea(t *testing.T) {
	wide := testStyle().NewPanel("T", 200, 40, 60)
	tiny := testStyle().NewPanel("T", 30, 40, 60)
	if wide.InnerW != 54 {
		t.Errorf("a wide area is capped at maxW, inner = %d", wide.InnerW)
	}
	if tiny.InnerW > 30 {
		t.Errorf("a tiny area never gets a panel wider than itself, inner = %d", tiny.InnerW)
	}
}
