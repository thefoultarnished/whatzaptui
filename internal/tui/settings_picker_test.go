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
		trimmed := strings.TrimLeft(rest, " ")
		col := lipgloss.Width(line[:end]) + len(rest) - len(trimmed)
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

	// Down from the right column of the last toggle row enters OPTIONS.
	p.idx = settingsIndex(t, "Show phone number")
	press(tea.KeyDown)
	if got := settingsDefs[p.idx].name; got != "Media icon style" {
		t.Fatalf("Down from last toggle row = %q, want Media icon style", got)
	}
	// Down into the half-empty last row lands on its only cell.
	p.idx = settingsIndex(t, "Chat list icons")
	press(tea.KeyDown)
	if got := settingsDefs[p.idx].name; got != "Startup speed" {
		t.Fatalf("Down into short row = %q, want Startup speed", got)
	}
	// Right from a lone cell stays put.
	press(tea.KeyRight)
	if got := settingsDefs[p.idx].name; got != "Startup speed" {
		t.Fatalf("Right from lone cell moved to %q", got)
	}
	// Up from OPTIONS returns to the last toggle row.
	p.idx = settingsIndex(t, "Typing style")
	press(tea.KeyUp)
	if got := settingsDefs[p.idx].name; got != "Hide borders" {
		t.Fatalf("Up from first option = %q, want Hide borders", got)
	}
}

// With an odd number of toggles the first selector must still start its own
// row instead of filling the empty cell next to the last toggle.
func TestSettingsOptionsStartNewRowAfterOddToggles(t *testing.T) {
	orig := settingsDefs
	defer func() { settingsDefs = orig }()
	settingsDefs = append(append(orig[:0:0], orig[:6]...), orig[7:]...) // drop one toggle → 7

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
