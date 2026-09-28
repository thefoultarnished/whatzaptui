package tui

import (
	"strings"
	"testing"
	"time"
)

// clickTestModel builds a 1:1 chat with one message on each of three
// different days, so the pane draws a date separator above every message.
func clickTestModel() m {
	jid := "15551230001@s.whatsapp.net"
	mk := func(id, text string, ts int64) wireMsg {
		msg := wireMsg{Message: map[string]any{"conversation": text}}
		msg.Key.ID = id
		msg.Key.RemoteJID = jid
		msg.MessageTimestamp = ts
		return msg
	}
	return m{
		active: jid,
		msgs: map[string][]wireMsg{jid: {
			mk("m1", "alpha", 1710000000),
			mk("m2", "bravo", 1710100000),
			mk("m3", "charlie", 1710200000),
		}},
		typingChats: map[string]time.Time{},
	}
}

// assertClicksMatchDrawing checks every row of the rendered pane: a row
// showing a message's text must hit-test to that message, and a date row
// must hit-test to nothing.
func assertClicksMatchDrawing(t *testing.T, model m, w, h int) {
	t.Helper()
	texts := map[string]string{"alpha": "m1", "bravo": "m2", "charlie": "m3"}
	rows := strings.Split(ansiStripRe.ReplaceAllString(model.renderMain(w, h), ""), "\n")
	checked := 0
	for row, line := range rows {
		got := model.msgIDAtLine(row, w, h)
		want := ""
		for text, id := range texts {
			if strings.Contains(line, text) {
				want = id
			}
		}
		if want == "" && got != "" && strings.Contains(line, "─") {
			t.Fatalf("row %d is a date line %q but click selects %q", row, line, got)
		}
		if want != "" {
			checked++
			if got != want {
				t.Fatalf("row %d shows %q but click selects %q, want %q", row, line, got, want)
			}
		}
	}
	if checked == 0 {
		t.Fatalf("no message rows found in render:\n%s", strings.Join(rows, "\n"))
	}
}

func TestMsgIDAtLineCountsDateSeparators(t *testing.T) {
	setTestTheme(t, TokyoNight)
	assertClicksMatchDrawing(t, clickTestModel(), 60, 20)
}

func TestMsgIDAtLineWithTypingIndicator(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := clickTestModel()
	model.typingChats[model.active] = time.Now()
	// Short pane so the typing row pushes messages and the window shifts.
	assertClicksMatchDrawing(t, model, 60, 6)
	if id := model.msgIDAtLine(5, 60, 6); id != "" {
		t.Fatalf("typing indicator row should select nothing, got %q", id)
	}
}

func TestMsgIDAtLineWhenScrolled(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := clickTestModel()
	model.scroll = 2
	assertClicksMatchDrawing(t, model, 60, 4)
	// The top row is a pinned date line while scrolled.
	if id := model.msgIDAtLine(0, 60, 4); id != "" {
		t.Fatalf("pinned date row should select nothing, got %q", id)
	}
}

// The mouse handler converts screen rows with chatPaneGeometry; make sure
// that lines up with where the full screen actually draws each message.
func TestChatPaneGeometryMatchesFullScreen(t *testing.T) {
	setTestTheme(t, TokyoNight)
	for _, borderless := range []bool{false, true} {
		saved := currentConfig.Borderless
		currentConfig.Borderless = borderless
		model := clickTestModel()
		model.status = "ready"
		model.mode = "chat"
		model.w, model.h = 110, 30
		g := model.chatPaneGeometry()
		rows := strings.Split(ansiStripRe.ReplaceAllString(model.viewInner(), ""), "\n")
		found := false
		for y, line := range rows {
			if !strings.Contains(line, "charlie") {
				continue
			}
			found = true
			if id := model.msgIDAtLine(y-g.paneY, g.rightW, g.mainH); id != "m3" {
				t.Fatalf("borderless=%v: screen row %d shows charlie but click selects %q", borderless, y, id)
			}
		}
		currentConfig.Borderless = saved
		if !found {
			t.Fatalf("borderless=%v: charlie not drawn:\n%s", borderless, strings.Join(rows, "\n"))
		}
	}
}

func TestMsgIDAtLineOutOfRange(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := clickTestModel()
	for _, row := range []int{-1, 20, 99} {
		if id := model.msgIDAtLine(row, 60, 20); id != "" {
			t.Fatalf("row %d should select nothing, got %q", row, id)
		}
	}
	model.active = ""
	if id := model.msgIDAtLine(0, 60, 20); id != "" {
		t.Fatalf("no active chat should select nothing, got %q", id)
	}
}
