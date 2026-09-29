package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

// useTokyoNight selects a named theme and restores the config afterwards.
func useTokyoNight(t *testing.T) {
	t.Helper()
	setTestTheme(t, TokyoNight)
	old := currentConfig
	t.Cleanup(func() { currentConfig = old })
	currentConfig.ThemeName = "tokyonight"
}

func TestThemePickerLeavesChatVisibleBehindIt(t *testing.T) {
	useTokyoNight(t)
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.w, x.h = 120, 40
	x.themePicker = newThemePicker()
	x.themePicker.Open(currentConfig.ThemeName)

	out := x.renderRightMain(90, 60)
	plain := ansiStripRe.ReplaceAllString(out, "")
	for _, want := range []string{"lunch?", "Select Theme", "Tokyo Night"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected %q with the chat visible behind the theme picker:\n%s", want, plain)
		}
	}
	for i, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w != 90 {
			t.Fatalf("row %d width = %d, want 90", i, w)
		}
	}
	if got := len(strings.Split(plain, "\n")); got != 60 {
		t.Fatalf("pane height = %d, want 60", got)
	}
}

func TestThemePickerPreviewsThemeOnTheChatBehindIt(t *testing.T) {
	useTokyoNight(t)
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.w, x.h = 120, 40
	x.themePicker = newThemePicker()
	x.themePicker.Open(currentConfig.ThemeName)
	original := currentConfig.ThemeName
	before := x.renderRightMain(90, 60)

	next, _ := x.key(tea.KeyMsg{Type: tea.KeyDown})
	moved := next.(m)
	if currentConfig.ThemeName == original {
		t.Fatal("moving the selection should apply the highlighted theme")
	}
	after := moved.renderRightMain(90, 60)
	if before == after {
		t.Fatal("the chat behind the picker should be redrawn in the previewed theme")
	}
	if !strings.Contains(ansiStripRe.ReplaceAllString(after, ""), "lunch?") {
		t.Fatal("the chat must stay visible while previewing")
	}

	next, _ = moved.key(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(m).themePicker.IsOpen || currentConfig.ThemeName != original {
		t.Fatalf("Esc should close and restore %q, got open=%v theme=%q", original, next.(m).themePicker.IsOpen, currentConfig.ThemeName)
	}
}

// Every popup is drawn over the chat, not instead of it, and keeps the pane
// exactly w x h.
func TestEveryPopupLeavesChatVisibleBehindIt(t *testing.T) {
	useTokyoNight(t)
	const w, h = 100, 60
	popups := []struct {
		name  string
		open  func(x *m)
		title string
	}{
		{"theme", func(x *m) { x.themePicker = newThemePicker(); x.themePicker.Open("tokyonight") }, "Select Theme"},
		{"pointer", func(x *m) { x.pointerPicker = newPointerPicker(); x.pointerPicker.Open("") }, newPointerPicker().Title},
		{"typing style", func(x *m) { x.typingAnimationPicker = newTypingAnimationPicker(); x.typingAnimationPicker.Open("") }, newTypingAnimationPicker().Title},
		{"media icons", func(x *m) { x.mediaIconPicker = newMediaIconPicker(); x.mediaIconPicker.Open("") }, newMediaIconPicker().Title},
		{"media preview", func(x *m) { x.mediaViewPicker = newMediaViewPicker(); x.mediaViewPicker.Open("") }, newMediaViewPicker().Title},
		{"chat list icons", func(x *m) { x.userlistIconPicker = newUserlistIconPicker(); x.userlistIconPicker.Open("") }, newUserlistIconPicker().Title},
		{"startup speed", func(x *m) { x.splashSpeedPicker = newSplashSpeedPicker(); x.splashSpeedPicker.Open("") }, newSplashSpeedPicker().Title},
		{"help", func(x *m) { x.helpPicker = newHelpPicker(); x.helpPicker.Open("") }, "Commands"},
		{"settings", func(x *m) { x.settingsPicker = newSettingsPicker(); x.settingsPicker.Open("") }, "Settings"},
		{"confirm", func(x *m) { x.confirmDialog.Open("Log out?", "Are you sure?", "logout") }, "Log out?"},
		{"font test", func(x *m) { x.fontTestOpen = true }, "Nerd Font glyph"},
	}
	for _, c := range popups {
		x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
		x.w, x.h = 120, 80
		c.open(&x)

		out := x.renderRightMain(w, h)
		plain := ansiStripRe.ReplaceAllString(out, "")
		if !strings.Contains(plain, c.title) {
			t.Errorf("%s: popup title %q missing:\n%s", c.name, c.title, plain)
		}
		if !strings.Contains(plain, "lunch?") {
			t.Errorf("%s: the chat is not visible behind the popup:\n%s", c.name, plain)
		}
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Errorf("%s: pane height = %d, want %d", c.name, len(lines), h)
		}
		for i, l := range lines {
			if lw := ansi.StringWidth(l); lw != w {
				t.Errorf("%s: row %d width = %d, want %d", c.name, i, lw, w)
				break
			}
		}
	}
}

func TestEmojiPickerLeavesChatVisibleBehindIt(t *testing.T) {
	useTokyoNight(t)
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.w, x.h = 120, 80
	x.openEmojiPicker()
	out := x.renderRightMain(100, 60)
	if !strings.Contains(ansiStripRe.ReplaceAllString(out, ""), "lunch?") {
		t.Fatalf("the chat should stay visible behind the emoji picker:\n%s", ansiStripRe.ReplaceAllString(out, ""))
	}
	if len(strings.Split(out, "\n")) != 60 {
		t.Fatalf("pane height changed: %d", len(strings.Split(out, "\n")))
	}
}

func TestFileBrowserStillFillsThePane(t *testing.T) {
	useTokyoNight(t)
	x := reactModel()
	x.w, x.h = 120, 40
	x.fileBrowserOpen = true
	if box := x.floatingPanel(100, 40); box != "" {
		t.Fatalf("the file browser is a full screen and must not float, got a panel")
	}
}
