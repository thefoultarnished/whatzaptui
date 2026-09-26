package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func settingsIndex(t *testing.T, name string) int {
	t.Helper()
	for i, s := range settingsDefs {
		if s.name == name {
			return i
		}
	}
	t.Fatalf("setting %q not found", name)
	return -1
}

func TestSettingsTogglesComeBeforeSelectors(t *testing.T) {
	seenSelector := false
	for _, s := range settingsDefs {
		if s.isSelector {
			seenSelector = true
		} else if seenSelector {
			t.Fatalf("toggle %q is listed after a selector", s.name)
		}
	}
}

// Selector routing in input.go matches on setting names; a rename that misses
// input.go would silently open the wrong sub-picker.
func TestSettingsSelectorsOpenTheirOwnPicker(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()

	cases := []struct {
		name   string
		isOpen func(m) bool
	}{
		{"Typing style", func(x m) bool { return x.typingAnimationPicker.open }},
		{"Media icon style", func(x m) bool { return x.mediaIconPicker.open }},
		{"Media preview", func(x m) bool { return x.mediaViewPicker.open }},
		{"Chat list icons", func(x m) bool { return x.userlistIconPicker.open }},
		{"Startup speed", func(x m) bool { return x.splashSpeedPicker.open }},
		{"Theme", func(x m) bool { return x.themePicker.open }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := m{status: "ready"}
			x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
			x.settingsPicker.Open("")
			x.settingsPicker.idx = settingsIndex(t, tc.name)

			res, _ := x.key(tea.KeyMsg{Type: tea.KeyEnter})
			got := res.(m)
			if !tc.isOpen(got) {
				t.Fatalf("Enter on %q did not open its sub-picker", tc.name)
			}
		})
	}
}

func TestSettingsMouseToggleAppliesMouseMode(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()
	currentConfig.MouseEnabled = false

	x := m{status: "ready"}
	x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
	x.settingsPicker.Open("")
	x.settingsPicker.idx = settingsIndex(t, "Mouse support")

	res, _ := x.key(tea.KeyMsg{Type: tea.KeyEnter})
	if got := res.(m); !got.mouseEnabled {
		t.Fatal("toggling Mouse support should enable mouse mode on the model")
	}
}

func TestSettingsValuesStartAtSameColumn(t *testing.T) {
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()
	currentConfig.SplashStageSpeed = "extremely_slow"

	p := picker{title: "Settings", items: buildSettingsPickerItems()}
	p.Open("")
	lines := strings.Split(ansiStripRe.ReplaceAllString(p.RenderSettings(120, 40), ""), "\n")

	valueCols := map[int]map[int]string{}
	for i, item := range p.items {
		var line string
		for _, l := range lines {
			if strings.Contains(l, item.key) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("setting %q not rendered", item.key)
		}
		end := strings.Index(line, item.key) + len(item.key)
		rest := line[end:]
		// Skip the padding/separator spaces, and an optional status dot
		// (toggles only) which is decoration, not the value itself.
		skip := 0
		for skip < len(rest) && rest[skip] == ' ' {
			skip++
		}
		if strings.HasPrefix(rest[skip:], "●") {
			skip += len("●")
			for skip < len(rest) && rest[skip] == ' ' {
				skip++
			}
		}
		col := lipgloss.Width(line[:end]) + lipgloss.Width(rest[:skip])
		_, gridCol := settingsVisualPos(i)
		if valueCols[gridCol] == nil {
			valueCols[gridCol] = map[int]string{}
		}
		valueCols[gridCol][col] = item.key
	}
	for gridCol, cols := range valueCols {
		if len(cols) != 1 {
			t.Errorf("grid column %d has values starting at %d different positions: %v", gridCol, len(cols), cols)
		}
	}
}

func TestSettingsNavigationAcrossSections(t *testing.T) {
	p := picker{title: "Settings", items: buildSettingsPickerItems()}
	p.Open("")
	press := func(k tea.KeyType) { p.HandleSettings(tea.KeyMsg{Type: k}) }

	// Down from the lone last toggle (odd count leaves it alone in col 0)
	// enters OPTIONS at col 0.
	p.idx = settingsIndex(t, "Show phone number")
	press(tea.KeyDown)
	if got := settingsDefs[p.idx].name; got != "Typing style" {
		t.Fatalf("Down from last toggle row = %q, want Typing style", got)
	}
	// Down into the last row lands in the same column.
	p.idx = settingsIndex(t, "Chat list icons")
	press(tea.KeyDown)
	if got := settingsDefs[p.idx].name; got != "Theme" {
		t.Fatalf("Down into last row = %q, want Theme", got)
	}
	p.idx = settingsIndex(t, "Media preview")
	press(tea.KeyDown)
	if got := settingsDefs[p.idx].name; got != "Startup speed" {
		t.Fatalf("Down into last row = %q, want Startup speed", got)
	}
	// Right from the last cell stays put.
	p.idx = settingsIndex(t, "Theme")
	press(tea.KeyRight)
	if got := settingsDefs[p.idx].name; got != "Theme" {
		t.Fatalf("Right from last cell moved to %q", got)
	}
	// Up from OPTIONS returns to the last toggle row.
	p.idx = settingsIndex(t, "Typing style")
	press(tea.KeyUp)
	if got := settingsDefs[p.idx].name; got != "Show phone number" {
		t.Fatalf("Up from first option = %q, want Show phone number", got)
	}
}

// Every ON/OFF toggle must show a status dot in front of its name so the
// state is visible at a glance alongside the ON/OFF text.
func TestSettingsTogglesShowStatusDot(t *testing.T) {
	p := picker{title: "Settings", items: buildSettingsPickerItems()}
	p.Open("")
	out := ansiStripRe.ReplaceAllString(p.RenderSettings(120, 40), "")
	lines := strings.Split(out, "\n")
	for i, s := range settingsDefs {
		if s.isSelector {
			continue
		}
		var line string
		for _, l := range lines {
			if strings.Contains(l, p.items[i].key) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("toggle %q not rendered", s.name)
		}
		if !strings.Contains(line, "●") {
			t.Fatalf("toggle %q line missing status dot: %q", s.name, strings.TrimSpace(line))
		}
		if !strings.Contains(line, "ON") && !strings.Contains(line, "OFF") {
			t.Fatalf("toggle %q line missing ON/OFF text: %q", s.name, strings.TrimSpace(line))
		}
	}
}

// With an odd number of toggles the first selector must still start its own
// row instead of filling the empty cell next to the last toggle.
func TestSettingsOptionsStartNewRowAfterOddToggles(t *testing.T) {
	orig := settingsDefs
	defer func() { settingsDefs = orig }()
	settingsDefs = append(append(orig[:0:0], orig[:6]...), orig[8:]...) // drop two toggles → 7

	firstOpt := settingsToggleCount()
	lastTogRow, _ := settingsVisualPos(firstOpt - 1)
	row, col := settingsVisualPos(firstOpt)
	if row != lastTogRow+1 || col != 0 {
		t.Fatalf("first option at (%d,%d), want (%d,0)", row, col, lastTogRow+1)
	}
	if i := settingsIdxAt(lastTogRow, 1); i != -1 {
		t.Fatalf("cell beside last toggle holds %q, want empty", settingsDefs[i].name)
	}

	p := picker{title: "Settings", items: buildSettingsPickerItems()}
	p.Open("")
	p.idx = settingsIndex(t, "Mouse support") // row 1, col 0 above the lone toggle
	p.HandleSettings(tea.KeyMsg{Type: tea.KeyDown})
	p.HandleSettings(tea.KeyMsg{Type: tea.KeyDown})
	p.HandleSettings(tea.KeyMsg{Type: tea.KeyDown})
	if !settingsDefs[p.idx].isSelector {
		t.Fatalf("Down from last toggle row should reach OPTIONS, got %q", settingsDefs[p.idx].name)
	}

	out := ansiStripRe.ReplaceAllString(p.RenderSettings(120, 40), "")
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "●") && strings.Contains(l, "→") {
			t.Fatalf("toggle and option share a row: %q", strings.TrimSpace(l))
		}
	}
}

// Closing an option sub-picker must return to settings with the entered
// option still selected instead of jumping back to the first item.
func TestSettingsSubPickerReturnRestoresSelection(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	origCfg := currentConfig
	origTheme := currentTheme
	defer func() { currentConfig = origCfg; currentTheme = origTheme; rehashStyles() }()

	openSettingsAt := func(name string) m {
		x := m{status: "ready"}
		x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
		x.settingsPicker.Open("")
		x.settingsPicker.idx = settingsIndex(t, name)
		return x
	}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	esc := tea.KeyMsg{Type: tea.KeyEsc}

	// Generic sub-picker, cancel path: Media preview -> Esc.
	x := openSettingsAt("Media preview")
	res, _ := x.key(enter)
	x = res.(m)
	if !x.mediaViewPicker.open {
		t.Fatal("Enter on Media preview did not open its sub-picker")
	}
	res, _ = x.key(esc)
	x = res.(m)
	if !x.settingsPicker.open {
		t.Fatal("Esc in sub-picker did not return to settings")
	}
	if got := settingsDefs[x.settingsPicker.idx].name; got != "Media preview" {
		t.Fatalf("returned to %q, want Media preview", got)
	}

	// Generic sub-picker, confirm path: Startup speed -> Enter.
	x = openSettingsAt("Startup speed")
	res, _ = x.key(enter)
	x = res.(m)
	if !x.splashSpeedPicker.open {
		t.Fatal("Enter on Startup speed did not open its sub-picker")
	}
	res, _ = x.key(enter)
	x = res.(m)
	if !x.settingsPicker.open {
		t.Fatal("confirm in sub-picker did not return to settings")
	}
	if got := settingsDefs[x.settingsPicker.idx].name; got != "Startup speed" {
		t.Fatalf("returned to %q, want Startup speed", got)
	}

	// Theme picker, cancel path: Theme -> Esc returns to settings on Theme.
	x = openSettingsAt("Theme")
	res, _ = x.key(enter)
	x = res.(m)
	if !x.themePicker.open {
		t.Fatal("Enter on Theme did not open the theme picker")
	}
	res, _ = x.key(esc)
	x = res.(m)
	if x.themePicker.open {
		t.Fatal("theme picker still open after Esc")
	}
	if !x.settingsPicker.open {
		t.Fatal("Esc in theme picker did not return to settings")
	}
	if got := settingsDefs[x.settingsPicker.idx].name; got != "Theme" {
		t.Fatalf("returned to %q, want Theme", got)
	}
}
