package main

import (
	"strings"
	"testing"
)

func TestTopBorderJunctionAlignsWithHeaderDivider(t *testing.T) {
	currentConfig.Borderless = false

	for _, w := range []int{70, 80, 100, 120} {
		model := m{
			w:         w,
			h:         30,
			status:    "ready",
			mode:      "chat",
			active:    "12345@s.whatsapp.net",
			mainCache: &renderCache{},
			sidebarCache: &sidebarCache{},
			chats: []chat{
				{ID: "12345@s.whatsapp.net", Name: "Alice"},
			},
			contacts: map[string]contact{},
			whitelist: map[string]string{},
			names:     map[string]string{},
			drafts:    map[string]string{},
			groupPreviews: map[string]groupPreview{},
		}

		view := model.View()
		lines := strings.Split(view, "\n")
		if len(lines) < 2 {
			t.Fatalf("width %d: view had %d lines, want >= 2", w, len(lines))
		}

		topRow := ansiStripRe.ReplaceAllString(lines[0], "")
		headRow := ansiStripRe.ReplaceAllString(lines[1], "")

		topRunes := []rune(topRow)
		headRunes := []rune(headRow)

		topJunctionCol := -1
		for i, r := range topRunes {
			if r == '┬' {
				topJunctionCol = i
				break
			}
		}
		if topJunctionCol == -1 {
			t.Fatalf("width %d: top border row missing '┬' junction: %q", w, topRow)
		}

		// In headRow, col 0 is the left frame border "│".
		// The divider between sidebar and chat is the second "│".
		if len(headRunes) == 0 || headRunes[0] != '│' {
			t.Fatalf("width %d: head row missing left frame border at col 0: %q", w, headRow)
		}
		dividerCol := -1
		for i := 1; i < len(headRunes); i++ {
			if headRunes[i] == '│' {
				dividerCol = i
				break
			}
		}
		if dividerCol == -1 {
			t.Fatalf("width %d: head row missing vertical divider '│': %q", w, headRow)
		}

		if topJunctionCol != dividerCol {
			t.Errorf("width %d: top row '┬' at col %d does not match header divider '│' at col %d",
				w, topJunctionCol, dividerCol)
		}
	}
}
