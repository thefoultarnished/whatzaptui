package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The selected row's highlight must be padded to the longest option's width
// so the background rectangle is the same size no matter which row is
// selected, instead of shrinking to fit a short name.
func TestEmojiPickerSelectionPadMatchesLongestLabel(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	results := filterEmojiCatalog("")
	shortIdx, longIdx := -1, -1
	shortest, longest := 1<<30, -1
	for i, r := range results {
		w := lipgloss.Width(r.Char + "  " + r.Name)
		if w < shortest {
			shortest = w
			shortIdx = i
		}
		if w > longest {
			longest = w
			longIdx = i
		}
	}
	if shortIdx < 0 || longIdx < 0 || shortIdx == longIdx {
		t.Skip("catalog too uniform to exercise padding")
	}

	x := m{w: 100, h: 30}
	check := func(sel int) {
		t.Helper()
		x.emojiSel = sel
		x.clampEmojiSelection()
		out := ansiStripRe.ReplaceAllString(x.renderEmojiPicker(), "")
		item := results[sel]
		label := item.Char + "  " + item.Name
		want := label + strings.Repeat(" ", longest-lipgloss.Width(label))
		if !strings.Contains(out, want) {
			t.Errorf("sel=%d: rendered output missing label padded to longest width (%d); label=%q", sel, longest, label)
		}
	}
	check(shortIdx)
	check(longIdx)
}
