package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// ansiBgRun is one contiguous stretch of text sharing the same background
// colour code (as its raw "48;2;r;g;b" SGR parameter, or "" for none/reset).
type ansiBgRun struct {
	text string
	bg   string
}

// splitAnsiBgRuns walks a string's SGR codes and groups its visible text
// into runs of constant background colour, tracking resets (0/49).
func splitAnsiBgRuns(s string) []ansiBgRun {
	var runs []ansiBgRun
	var cur strings.Builder
	curBG := ""
	flush := func() {
		if cur.Len() > 0 {
			runs = append(runs, ansiBgRun{cur.String(), curBG})
			cur.Reset()
		}
	}
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := strings.IndexByte(s[i:], 'm')
			if j < 0 {
				break
			}
			flush()
			parts := strings.Split(s[i+2:i+j], ";")
			for k := 0; k < len(parts); k++ {
				switch parts[k] {
				case "", "0":
					curBG = ""
				case "49":
					curBG = ""
				case "48":
					if k+4 < len(parts) && parts[k+1] == "2" {
						curBG = strings.Join(parts[k+2:k+5], ";")
						k += 4
					}
				}
			}
			i += j + 1
			continue
		}
		cur.WriteByte(s[i])
		i++
	}
	flush()
	return runs
}

// bgRunWidth returns the total visible width of every run in s whose
// background matches hex.
func bgRunWidth(s, hex string) int {
	want := strings.TrimPrefix(bgANSI(hex), "48;2;")
	total := 0
	for _, r := range splitAnsiBgRuns(s) {
		if r.bg == want {
			total += lipgloss.Width(r.text)
		}
	}
	return total
}

// The selected settings row's highlighted background must span the full
// column width regardless of how short or long that option's value text is
// (a toggle's "OFF" vs. a selector's longer value like "Extra slow").
func TestSettingsSelectionHighlightWidthConstant(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)
	t.Cleanup(func() { rehashStyles() })

	origCfg := currentConfig
	t.Cleanup(func() { currentConfig = origCfg })
	currentConfig.MouseEnabled = false          // "Mouse support" -> "OFF" (short)
	currentConfig.SplashStageSpeed = "extremely_slow" // "Startup speed" -> "Extra slow" (long)

	p := picker{title: "Settings", items: buildSettingsPickerItems()}
	p.Open("")

	highlightWidth := func(idxName string) int {
		t.Helper()
		p.idx = settingsIndex(t, idxName)
		out := p.RenderSettings(120, 40)
		var best int
		for _, line := range strings.Split(out, "\n") {
			stripped := ansiStripRe.ReplaceAllString(line, "")
			if !strings.Contains(stripped, idxName) {
				continue
			}
			if w := bgRunWidth(line, currentTheme.ShortcutActive); w > best {
				best = w
			}
		}
		return best
	}

	shortW := highlightWidth("Mouse support")
	longW := highlightWidth("Startup speed")
	if shortW == 0 || longW == 0 {
		t.Fatalf("expected a highlighted background run for both rows, got short=%d long=%d", shortW, longW)
	}
	if shortW != longW {
		t.Errorf("selection highlight width varies with value length: %q=%d %q=%d", "Mouse support", shortW, "Startup speed", longW)
	}
}
