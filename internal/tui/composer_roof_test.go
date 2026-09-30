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

// raisedRows returns the three rows of the raised part with colours stripped:
// the roof edge, the row of shortcuts under it, and the rule at the base.
func raisedRows(model m, w int, focused, locked bool) (edge, text, base string) {
	rows := strings.Split(model.renderComposerRoof(w, focused, locked), "\n")
	return stripGraphicsSeqs(rows[0]), stripGraphicsSeqs(rows[1]), stripGraphicsSeqs(model.renderComposerTopBorder(w, focused, locked))
}

// The roof edge, the wall on the shortcut row and the corner where the rule
// rises are all in one column. The edge runs to the right side and ends in a
// rule character so the frame draws a junction there.
func TestRaisedPartWallsLineUpAtEveryWidth(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	for w := 40; w <= 140; w++ {
		edge, text, base := raisedRows(model, w, true, false)
		for name, row := range map[string]string{"edge": edge, "text": text, "base": base} {
			if runeDisplayWidth(row) != w {
				t.Fatalf("width %d: the %s row is %d cells wide", w, name, runeDisplayWidth(row))
			}
		}
		corner, wall, rise := runeIndex(edge, '╭'), runeIndex(text, '│'), runeIndex(base, '╯')
		if corner < 0 || corner != wall || wall != rise {
			t.Fatalf("width %d: roof corner %d, wall %d and rule corner %d do not line up:\n%s\n%s\n%s", w, corner, wall, rise, edge, text, base)
		}
		if !strings.HasSuffix(edge, "─") {
			t.Fatalf("width %d: the roof edge must end in a rule so the frame connects: %q", w, edge)
		}
		if strings.Trim(edge, " ╭─") != "" {
			t.Fatalf("width %d: the roof edge is only spaces, a corner and a rule: %q", w, edge)
		}
		if !strings.HasSuffix(text, " ") || !strings.HasSuffix(base, " ") {
			t.Fatalf("width %d: the text and base rows are open on the right, so the frame draws a wall", w)
		}
		if !strings.Contains(string([]rune(text)[wall+1:]), "emoji") {
			t.Fatalf("width %d: shortcuts are not inside the raised part: %q", w, text)
		}
	}
}

// The shortcuts have a row of their own, between the roof edge and the rule.
func TestShortcutsSitOnTheirOwnRow(t *testing.T) {
	setTestTheme(t, TokyoNight)
	edge, text, base := raisedRows(roofModel(), 120, true, false)
	if strings.Contains(edge, "emoji") || strings.Contains(base, "emoji") {
		t.Fatalf("the shortcuts must not share a row with the roof edge or the rule:\n%s\n%s\n%s", edge, text, base)
	}
	if !strings.Contains(text, "emoji [alt+e]") || !strings.Contains(text, "edit [alt+a]") {
		t.Fatalf("the shortcut row is missing shortcuts: %q", text)
	}
}

func TestRaisedPartSpansFromTheWallToTheFrame(t *testing.T) {
	setTestTheme(t, TokyoNight)
	const w = 120
	edge, text, _ := raisedRows(roofModel(), w, true, false)
	shortcuts := "emoji [alt+e]  ·  file [alt+f]  ·  reply [alt+r]  ·  edit [alt+a]"
	// Wall, a space, the shortcuts and a space: that is the raised part.
	want := runeDisplayWidth(shortcuts) + 3
	if got := w - runeIndex(edge, '╭'); got != want {
		t.Fatalf("the raised part is %d wide, want %d:\n%s\n%s", got, want, edge, text)
	}
}

func TestRoofIsBlankWithoutShortcuts(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := roofModel()
	noChat := roofModel()
	noChat.active = ""
	cases := map[string][2]string{}
	edge, text, _ := raisedRows(model, 80, false, false)
	cases["sidebar focused"] = [2]string{edge, text}
	edge, text, _ = raisedRows(model, 20, true, false)
	cases["too narrow"] = [2]string{edge, text}
	edge, text, _ = raisedRows(noChat, 80, true, false)
	cases["no chat"] = [2]string{edge, text}
	for name, rows := range cases {
		for i, row := range rows {
			if strings.TrimSpace(row) != "" {
				t.Errorf("%s: roof row %d should be blank, got %q", name, i, row)
			}
		}
	}
	edge, text, _ = raisedRows(model, 80, false, false)
	if runeDisplayWidth(edge) != 80 || runeDisplayWidth(text) != 80 {
		t.Errorf("blank roof rows keep the width: %d and %d", runeDisplayWidth(edge), runeDisplayWidth(text))
	}
}

func TestRoofRowsOnlyWithAnOpenChatAndRoom(t *testing.T) {
	model := roofModel()
	if model.composerRoofRows(60) != composerRoofHeight || composerRoofHeight != 2 {
		t.Errorf("an open chat with room reserves %d rows, want the 2 of the roof", model.composerRoofRows(60))
	}
	if model.composerRoofRows(24) != 0 {
		t.Error("a box too narrow for shortcuts reserves no rows")
	}
	model.active = ""
	if model.composerRoofRows(60) != 0 {
		t.Error("no open chat reserves no rows")
	}
}

func TestRoofDoesNotChangeTheTotalHeight(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for _, size := range [][2]int{{80, 20}, {100, 30}, {120, 40}, {160, 50}} {
		for _, focused := range []bool{true, false} {
			model := roofModel()
			model.w, model.h = size[0], size[1]
			model.sidebarFocused = !focused
			lines := strings.Split(model.View(), "\n")
			if len(lines) != model.h {
				t.Fatalf("%dx%d focused=%v: view is %d lines, want %d", size[0], size[1], focused, len(lines), model.h)
			}
		}
	}
}

func TestRoofTakesTwoRowsFromTheMessagePane(t *testing.T) {
	model := roofModel()
	with := model.chatPaneGeometry().mainH
	model.active = ""
	without := model.chatPaneGeometry().mainH
	if with != without-2 {
		t.Fatalf("message pane is %d rows with a chat open and %d without: the roof should cost exactly two", with, without)
	}
}

// In the whole view the roof edge runs to the right frame and joins it, and the
// rows under it end at the frame wall.
func TestRoofJoinsTheFrameInTheWholeView(t *testing.T) {
	setTestTheme(t, TokyoNight)
	lines := strings.Split(ansiStripRe.ReplaceAllString(roofModel().View(), ""), "\n")
	text := -1
	for i, l := range lines {
		if strings.Contains(l, "│ emoji") {
			text = i
		}
	}
	if text < 1 || text+1 >= len(lines) {
		t.Fatalf("no shortcut row found in the view:\n%s", strings.Join(lines, "\n"))
	}
	edge, base := lines[text-1], lines[text+1]
	if !strings.Contains(edge, "╭") || !strings.HasSuffix(strings.TrimRight(edge, " "), "┤") {
		t.Fatalf("the roof edge should run to the right frame and connect with a junction: %q", edge)
	}
	if !strings.HasSuffix(strings.TrimRight(lines[text], " "), "│") {
		t.Fatalf("the shortcut row should end at the frame wall: %q", lines[text])
	}
	if !strings.Contains(base, "┼") || !strings.HasSuffix(strings.TrimRight(base, " "), "│") {
		t.Fatalf("the rule row sits on the sidebar divider and ends at the frame wall: %q", base)
	}
	// The left column keeps its own layout: no roof characters there.
	for _, l := range lines[text-1 : text+2] {
		if strings.ContainsAny(string([]rune(l)[:30]), "╭") {
			t.Fatalf("the roof must stay in the right column: %q", l)
		}
	}
}

func TestModeHeadersGetTheSameRaisedPart(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for name, model := range map[string]m{
		"edit":       func() m { x := roofModel(); x.editPickMode = true; return x }(),
		"reply":      func() m { x := roofModel(); x.replyPickMode = true; return x }(),
		"attachment": func() m { x := roofModel(); x.pendingAttachmentPath = "photo.png"; return x }(),
	} {
		edge, text, base := raisedRows(model, 90, true, false)
		corner := runeIndex(edge, '╭')
		if corner < 0 || corner != runeIndex(text, '│') || corner != runeIndex(base, '╯') {
			t.Errorf("%s: the raised part does not line up:\n%s\n%s\n%s", name, edge, text, base)
		}
	}
}

func TestBlacklistedNoticeGetsTheSameRaisedPart(t *testing.T) {
	setTestTheme(t, TokyoNight)
	edge, text, base := raisedRows(roofModel(), 90, true, true)
	corner := runeIndex(edge, '╭')
	if !strings.Contains(text, "blacklisted") || corner < 0 || corner != runeIndex(text, '│') || corner != runeIndex(base, '╯') {
		t.Fatalf("the blacklisted notice should sit in the raised part:\n%s\n%s\n%s", edge, text, base)
	}
}
