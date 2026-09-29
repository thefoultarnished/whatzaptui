package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
)

func TestMediaViewPickerClosesAndSwitchesToSettings(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()
	currentConfig.MediaViewStyle = "full"
	x := m{status: "ready"}
	x.mediaViewPicker = newMediaViewPicker()
	x.mediaViewPicker.Open(currentConfig.MediaViewStyle)
	if !x.mediaViewPicker.IsOpen {
		t.Fatal("expected mediaViewPicker to be open")
	}

	// 1. Arrow down
	resModel, _ := x.key(tea.KeyMsg{Type: tea.KeyDown})
	resM := resModel.(m)
	if !resM.mediaViewPicker.IsOpen {
		t.Fatal("mediaViewPicker should remain open while navigating")
	}

	// 2. Press Enter to confirm
	resModel2, _ := resM.key(tea.KeyMsg{Type: tea.KeyEnter})
	resM2 := resModel2.(m)
	if resM2.mediaViewPicker.IsOpen {
		t.Fatal("mediaViewPicker should be closed after Enter confirm")
	}
	if !resM2.settingsPicker.IsOpen {
		t.Fatal("settingsPicker should be open after confirming sub-picker")
	}

	// 3. Test Esc cancel restores original
	currentConfig.MediaViewStyle = "text"
	y := m{status: "ready"}
	y.mediaViewPicker = newMediaViewPicker()
	y.mediaViewPicker.Open(currentConfig.MediaViewStyle)
	// Arrow to another option
	resY, _ := y.key(tea.KeyMsg{Type: tea.KeyDown})
	resYM := resY.(m)
	// Cancel with Esc
	resY2, _ := resYM.key(tea.KeyMsg{Type: tea.KeyEsc})
	resYM2 := resY2.(m)
	if resYM2.mediaViewPicker.IsOpen {
		t.Fatal("mediaViewPicker should be closed after Esc")
	}
	if currentConfig.MediaViewStyle != "text" {
		t.Fatalf("MediaViewStyle = %q, want restored original 'text'", currentConfig.MediaViewStyle)
	}
}

func TestMediaIconPickerRenderAndNavigation(t *testing.T) {
	p := newMediaIconPicker()
	p.Open("text")

	rendered := p.Render(pickerStyle(), 80, 20)
	if !strings.Contains(rendered, "Media Icon Style") {
		t.Fatalf("missing title in rendered picker: %q", rendered)
	}
	if !strings.Contains(rendered, "Text") || !strings.Contains(rendered, "Nerd") {
		t.Fatalf("missing Text or Nerd option in rendered picker: %q", rendered)
	}

	// Navigation: KeyDown should move from item 0 ("text") to item 1 ("nerd")
	action, done := p.Handle(tea.KeyMsg{Type: tea.KeyDown})
	if done || action != "" {
		t.Fatalf("KeyDown should just navigate, got action=%q, done=%v", action, done)
	}
	if p.SelectedKey() != "nerd" {
		t.Fatalf("SelectedKey after KeyDown = %q, want 'nerd'", p.SelectedKey())
	}

	// Navigation: KeyUp should move back to item 0 ("text")
	action, done = p.Handle(tea.KeyMsg{Type: tea.KeyUp})
	if done || action != "" {
		t.Fatalf("KeyUp should just navigate, got action=%q, done=%v", action, done)
	}
	if p.SelectedKey() != "text" {
		t.Fatalf("SelectedKey after KeyUp = %q, want 'text'", p.SelectedKey())
	}
}

func TestPointerPickerIsATwoColumnPanel(t *testing.T) {
	setTestTheme(t, TokyoNight)
	p := newPointerPicker()
	p.Open("")
	out := ansiStripRe.ReplaceAllString(p.RenderBox(pickerStyle(), 100, 40), "")
	lines := strings.Split(out, "\n")

	if !strings.Contains(out, "Select Pointer Icon") || !strings.Contains(out, "esc") {
		t.Fatalf("panel should have the title and the esc hint like the theme picker:\n%s", out)
	}
	// Row by row, like the theme picker: the first two pointers share a row.
	first, second := pointerList[0].displayName, pointerList[1].displayName
	var row string
	for _, l := range lines {
		if strings.Contains(l, first) {
			row = l
			break
		}
	}
	if row == "" || !strings.Contains(row, second) {
		t.Fatalf("expected %q and %q on one row:\n%s", first, second, out)
	}
	if strings.Index(row, first) > strings.Index(row, second) {
		t.Fatalf("first pointer should be left of the second: %q", row)
	}
	// Every pointer shows up, and the long names fit whole.
	for _, e := range pointerList {
		if !strings.Contains(out, e.displayName) {
			t.Fatalf("missing %q in panel:\n%s", e.displayName, out)
		}
	}
	// Two columns means about half as many rows as pointers.
	if len(lines) >= len(pointerList)+6 {
		t.Fatalf("panel is %d lines tall, expected a two column layout", len(lines))
	}
}

func TestPointerPickerMovesLikeTheThemePicker(t *testing.T) {
	p := newPointerPicker()
	p.Open("")
	press := func(k tea.KeyType) { p.Handle(tea.KeyMsg{Type: k}) }

	press(tea.KeyRight)
	if p.Idx != 1 {
		t.Fatalf("Right should go to the next cell on the row (1), got %d", p.Idx)
	}
	press(tea.KeyRight)
	if p.Idx != 1 {
		t.Fatalf("Right at the end of a row stays put (1), got %d", p.Idx)
	}
	press(tea.KeyDown)
	if p.Idx != 3 {
		t.Fatalf("Down should go to the same column on the next row (3), got %d", p.Idx)
	}
	press(tea.KeyLeft)
	if p.Idx != 2 {
		t.Fatalf("Left should go to the other column on the row (2), got %d", p.Idx)
	}
	press(tea.KeyUp)
	if p.Idx != 0 {
		t.Fatalf("Up should go to the same column on the row above (0), got %d", p.Idx)
	}
}

func TestPointerPickerMovementMatchesThemePickerOnTheSameGrid(t *testing.T) {
	theme := newThemePicker()
	theme.Open("")
	pointer := newPointerPicker()
	pointer.Open("")
	moves := []tea.KeyType{tea.KeyRight, tea.KeyDown, tea.KeyDown, tea.KeyLeft, tea.KeyUp, tea.KeyRight, tea.KeyRight, tea.KeyDown}
	for i, k := range moves {
		theme.Handle(tea.KeyMsg{Type: k})
		pointer.Handle(tea.KeyMsg{Type: k})
		// The theme grid is split into groups, so compare inside the first group only.
		if theme.Idx >= themeGroupDefs[0].Count {
			break
		}
		if theme.Idx != pointer.Idx {
			t.Fatalf("after move %d (%v): theme at %d, pointer at %d", i+1, k, theme.Idx, pointer.Idx)
		}
	}
}

func TestPointerPickerHighlightsTheSelectedRow(t *testing.T) {
	setTestTheme(t, TokyoNight)
	p := newPointerPicker()
	p.Open("")
	first := p.RenderBox(pickerStyle(), 100, 40)
	p.Idx = 3
	moved := p.RenderBox(pickerStyle(), 100, 40)
	if first == moved {
		t.Fatal("moving the selection should change the drawing")
	}
	if !strings.Contains(ansiStripRe.ReplaceAllString(moved, ""), "▌") {
		t.Fatalf("V2 themes mark the selected row with the ▌ bar:\n%s", ansiStripRe.ReplaceAllString(moved, ""))
	}
}

func TestPointerPickerFloatsOverTheChat(t *testing.T) {
	useTokyoNight(t)
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.w, x.h = 120, 80
	x.pointerPicker = newPointerPicker()
	x.pointerPicker.Open("")
	plain := ansiStripRe.ReplaceAllString(x.renderRightMain(100, 60), "")
	if !strings.Contains(plain, "lunch?") || !strings.Contains(plain, "Select Pointer Icon") {
		t.Fatalf("chat and pointer panel should both show:\n%s", plain)
	}
}
