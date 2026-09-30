package tui

import (
	"strings"
	"testing"
)

func roofModel() m {
	return m{
		w:            110,
		h:            30,
		status:       "ready",
		mode:         "chat",
		active:       "12345@s.whatsapp.net",
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
		chats:        []chat{{ID: "12345@s.whatsapp.net", Name: "Alice"}},
		contacts:     map[string]contact{},
		whitelist:    map[string]string{"12345": "Alice"},
		names:        map[string]string{},
		drafts:       map[string]string{},

		groupPreviews: map[string]groupPreview{},
	}
}

// The roof starts exactly above the wall where the rule rises and runs all the
// way to the right side, ending in a rule character so the frame draws a
// junction there.
func TestComposerRoofSitsOverTheShortcuts(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	for w := 40; w <= 140; w++ {
		line := stripGraphicsSeqs(model.renderComposerTopBorder(w, true, false))
		roof := stripGraphicsSeqs(model.renderComposerRoof(w, true, false))
		if runeDisplayWidth(roof) != w || runeDisplayWidth(line) != w {
			t.Fatalf("width %d: roof is %d cells and rule %d cells wide", w, runeDisplayWidth(roof), runeDisplayWidth(line))
		}
		wall := runeIndex(line, '╯')
		if wall < 0 {
			t.Fatalf("width %d: rule has no wall: %q", w, line)
		}
		if runeIndex(roof, '╭') != wall {
			t.Fatalf("width %d: the roof corner is not over the wall (%d vs %d):\n%s\n%s", w, runeIndex(roof, '╭'), wall, roof, line)
		}
		if !strings.HasSuffix(roof, "─") {
			t.Fatalf("width %d: the roof must end in a rule so the frame connects: %q", w, roof)
		}
		if strings.Trim(roof, " ╭─") != "" {
			t.Fatalf("width %d: the roof is only spaces, a corner and a rule: %q", w, roof)
		}
		inside := string([]rune(line)[wall+1:])
		if !strings.Contains(inside, "emoji") {
			t.Fatalf("width %d: shortcuts are not under the roof: %q", w, line)
		}
	}
}

func TestComposerRaisedPartSpansFromTheWallToTheFrame(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	const w = 120
	line := stripGraphicsSeqs(model.renderComposerTopBorder(w, true, false))
	roof := stripGraphicsSeqs(model.renderComposerRoof(w, true, false))
	shortcuts := "emoji [alt+e]  ·  file [alt+f]  ·  reply [alt+r]  ·  edit [alt+a]"
	// Wall, a space, the shortcuts and a space: that is the raised part.
	want := runeDisplayWidth(shortcuts) + 3
	if got := w - runeIndex(roof, '╭'); got != want {
		t.Fatalf("the raised part is %d wide, want %d:\n%s\n%s", got, want, roof, line)
	}
}

func TestComposerRoofIsBlankWithoutShortcuts(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	cases := map[string]string{
		"sidebar focused": stripGraphicsSeqs(model.renderComposerRoof(80, false, false)),
		"too narrow":      stripGraphicsSeqs(model.renderComposerRoof(20, true, false)),
	}
	for name, roof := range cases {
		if strings.TrimSpace(roof) != "" {
			t.Errorf("%s: the roof should be blank, got %q", name, roof)
		}
	}
	if w := runeDisplayWidth(cases["sidebar focused"]); w != 80 {
		t.Errorf("a blank roof keeps the width: %d", w)
	}
	noChat := roofModel()
	noChat.active = ""
	if strings.TrimSpace(stripGraphicsSeqs(noChat.renderComposerRoof(80, true, false))) != "" {
		t.Error("no chat open: the roof should be blank")
	}
}

func TestComposerRoofRowsOnlyWithAnOpenChatAndRoom(t *testing.T) {
	model := roofModel()
	if model.composerRoofRows(60) != 1 {
		t.Error("an open chat with room should reserve the roof row")
	}
	if model.composerRoofRows(24) != 0 {
		t.Error("a box too narrow for shortcuts reserves no row")
	}
	model.active = ""
	if model.composerRoofRows(60) != 0 {
		t.Error("no open chat reserves no row")
	}
}

func TestComposerRoofDoesNotChangeTheTotalHeight(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for _, size := range [][2]int{{80, 20}, {100, 30}, {120, 40}, {160, 50}} {
		for _, focused := range []bool{true, false} {
			model := roofModel()
			model.w, model.h = size[0], size[1]
			model.sidebarFocused = !focused
			view := model.View()
			lines := strings.Split(view, "\n")
			if len(lines) != model.h {
				t.Fatalf("%dx%d focused=%v: view is %d lines, want %d", size[0], size[1], focused, len(lines), model.h)
			}
		}
	}
}

func TestComposerRoofTakesOneRowFromTheMessagePane(t *testing.T) {
	model := roofModel()
	with := model.chatPaneGeometry().mainH
	model.active = ""
	without := model.chatPaneGeometry().mainH
	if with != without-1 {
		t.Fatalf("message pane is %d rows with a chat open and %d without: the roof should cost exactly one", with, without)
	}
}

// In the whole view the roof runs to the right frame and joins it, and the
// text row under it ends at the frame wall.
func TestComposerRoofIsDrawnJustAboveTheRuleAndJoinsTheFrame(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	lines := strings.Split(ansiStripRe.ReplaceAllString(model.View(), ""), "\n")
	rule := -1
	for i, l := range lines {
		if strings.Contains(l, "╯ emoji") {
			rule = i
		}
	}
	if rule < 1 {
		t.Fatalf("no shortcut rule found in the view:\n%s", strings.Join(lines, "\n"))
	}
	roofRow := strings.TrimRight(lines[rule-1], " ")
	if !strings.Contains(roofRow, "╭") || !strings.HasSuffix(roofRow, "┤") {
		t.Fatalf("the roof should run to the right frame and connect with a junction: %q", lines[rule-1])
	}
	if !strings.HasSuffix(strings.TrimRight(lines[rule], " "), "│") {
		t.Fatalf("the raised part should end at the frame wall: %q", lines[rule])
	}
	// The rule is on the same row as the left divider (the cross junction).
	if !strings.Contains(lines[rule], "┼") {
		t.Fatalf("the rule should sit on the sidebar divider row: %q", lines[rule])
	}
	// The left column keeps its own layout: no roof characters there.
	left := []rune(lines[rule-1])[:30]
	if strings.ContainsRune(string(left), '╭') {
		t.Fatalf("the roof must stay in the right column: %q", string(left))
	}
}

func TestComposerModeHeadersGetARoofToo(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for name, model := range map[string]m{
		"edit":       func() m { x := roofModel(); x.editPickMode = true; return x }(),
		"reply":      func() m { x := roofModel(); x.replyPickMode = true; return x }(),
		"attachment": func() m { x := roofModel(); x.pendingAttachmentPath = "photo.png"; return x }(),
	} {
		line := stripGraphicsSeqs(model.renderComposerTopBorder(90, true, false))
		roof := stripGraphicsSeqs(model.renderComposerRoof(90, true, false))
		if runeIndex(roof, '╭') != runeIndex(line, '╯') || runeIndex(roof, '╭') < 0 {
			t.Errorf("%s: roof does not start over the header:\n%s\n%s", name, roof, line)
		}
	}
}

func TestBlacklistedNoticeGetsARoofToo(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	line := stripGraphicsSeqs(model.renderComposerTopBorder(90, true, true))
	roof := stripGraphicsSeqs(model.renderComposerRoof(90, true, true))
	if !strings.Contains(line, "blacklisted") || runeIndex(roof, '╭') != runeIndex(line, '╯') {
		t.Fatalf("the blacklisted notice should sit under a roof:\n%s\n%s", roof, line)
	}
}
