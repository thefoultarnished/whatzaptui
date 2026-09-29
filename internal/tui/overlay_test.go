package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func plainLines(s string) []string {
	return strings.Split(ansiStripRe.ReplaceAllString(s, ""), "\n")
}

func TestOverlayCenterKeepsBackgroundAroundBox(t *testing.T) {
	base := strings.Join([]string{
		"0123456789",
		"abcdefghij",
		"ABCDEFGHIJ",
		"klmnopqrst",
		"KLMNOPQRST",
	}, "\n")
	box := "[##]\n[##]"
	got := plainLines(overlayCenter(base, box, 10, 5))
	// The box is 4 wide, so it covers columns 3..6 of rows 1 and 2; the rest is untouched.
	want := []string{
		"0123456789",
		"abc[##]hij",
		"ABC[##]HIJ",
		"klmnopqrst",
		"KLMNOPQRST",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nall: %q", i, got[i], want[i], got)
		}
	}
	for i, l := range got {
		if ansi.StringWidth(l) != 10 {
			t.Fatalf("row %d width = %d, want 10: %q", i, ansi.StringWidth(l), l)
		}
	}
}

func TestOverlayCenterKeepsStylingRightOfBox(t *testing.T) {
	setTestTheme(t, TokyoNight)
	line := lipgloss.NewStyle().Background(lipgloss.Color("#123456")).Foreground(lipgloss.Color("#abcdef")).Render(strings.Repeat("x", 20))
	base := strings.Join([]string{line, line, line}, "\n")
	out := overlayCenter(base, "BOX", 20, 3)
	row := strings.Split(out, "\n")[1]
	if got := ansiStripRe.ReplaceAllString(row, ""); got != strings.Repeat("x", 8)+"BOX"+strings.Repeat("x", 9) {
		t.Fatalf("row text = %q", got)
	}
	rest := row[strings.Index(row, "BOX")+3:]
	if !strings.Contains(rest, "\x1b[") {
		t.Fatalf("the part right of the box lost its colors: %q", rest)
	}
	if ansi.StringWidth(row) != 20 {
		t.Fatalf("row width = %d, want 20", ansi.StringWidth(row))
	}
}

func TestOverlayCenterNeverSplitsWideCharacters(t *testing.T) {
	base := strings.Repeat("😀😀😀😀😀\n", 3)
	base = strings.TrimSuffix(base, "\n") // 10 cells per row
	for boxW := 1; boxW <= 6; boxW++ {
		box := strings.Repeat("#", boxW)
		out := overlayCenter(base, box, 10, 3)
		for i, l := range strings.Split(out, "\n") {
			if w := ansi.StringWidth(l); w != 10 {
				t.Fatalf("boxW=%d row %d width = %d, want 10: %q", boxW, i, w, l)
			}
		}
	}
}

func TestOverlayCenterHandlesShortBaseAndBigBox(t *testing.T) {
	out := overlayCenter("ab", "12345678\n12345678\n12345678", 6, 4)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected the base to grow to 4 rows, got %d: %q", len(lines), lines)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 6 {
			t.Fatalf("row %d is wider than the area (%d): %q", i, w, l)
		}
	}
	if overlayCenter("keep", "", 4, 1) != "keep" {
		t.Fatal("an empty box must leave the base alone")
	}
}

func TestReactionListLeavesChatVisibleBehindIt(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := reactModel(
		reactMsg("r1", "m1", "fire", "1", 101),
		reactMsg("r2", "m1", "fire", "2", 102),
	)
	x.w, x.h = 120, 40
	x.selectedMsgID = "m1"

	before := ansiStripRe.ReplaceAllString(x.renderRightMain(90, 30), "")
	if !strings.Contains(before, "lunch?") {
		t.Fatalf("test setup: the chat should show the message: %q", before)
	}

	x.openReactionList()
	after := ansiStripRe.ReplaceAllString(x.renderRightMain(90, 30), "")
	for _, want := range []string{"lunch?", "Reactions", "fire 2", "Ann Lee, Bob"} {
		if !strings.Contains(after, want) {
			t.Fatalf("expected %q with the chat still visible behind the panel:\n%s", want, after)
		}
	}
	if beforeLines, afterLines := strings.Count(before, "\n"), strings.Count(after, "\n"); beforeLines != afterLines {
		t.Fatalf("the pane height changed: %d vs %d lines", beforeLines+1, afterLines+1)
	}
	for i, l := range strings.Split(x.renderRightMain(90, 30), "\n") {
		if w := ansi.StringWidth(l); w != 90 {
			t.Fatalf("row %d width = %d, want 90", i, w)
		}
	}
}
