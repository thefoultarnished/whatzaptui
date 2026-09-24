package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

// The trace log follows the load path (chats → messages → drawable) with
// redacted chat IDs, logs render state only on change, and never writes
// the raw phone number.
func TestTraceLogFollowsLoadPath(t *testing.T) {
	path := openTraceLog(t.TempDir())
	if path == "" {
		t.Fatal("openTraceLog failed")
	}
	t.Cleanup(closeTraceLog)

	chatID := "15551230001@s.whatsapp.net"
	x := m{
		status:       "ready",
		msgs:         map[string][]wireMsg{},
		flashUntil:   map[string]time.Time{},
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	res, _ := x.Update(chatsMsg{chats: []chat{{ID: chatID}}})
	x = res.(m)
	x.active = chatID
	res, _ = x.Update(msgsMsg{chatID: chatID, msgs: []wireMsg{{}, {}}})
	x = res.(m)
	_, _ = x.Update(cursorBlinkMsg{}) // no state change: must not re-log

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	logText := string(b)
	want := traceChat(chatID)
	for _, s := range []string{
		"session.start",
		"chats.loaded  count=1",
		"render.chatlist  chats=1",
		"messages.loaded  chat=" + want + " count=2",
		"render.chat  chat=" + want + " messages=2",
	} {
		if !strings.Contains(logText, s) {
			t.Fatalf("trace missing %q:\n%s", s, logText)
		}
	}
	if n := strings.Count(logText, "render.chat  "); n != 1 {
		t.Fatalf("render.chat logged %d times, want 1 (change-only):\n%s", n, logText)
	}
	if strings.Contains(logText, "15551230001") {
		t.Fatal("raw phone number leaked into the trace log")
	}
}

// traceChat must hash exactly like the backend's redactChatID so lines in
// the two logs can be joined.
func TestTraceChatMatchesBackendFormat(t *testing.T) {
	if got := traceChat("120363000000000000@g.us"); got != "chat:group" {
		t.Fatalf("group = %q", got)
	}
	// Backend: "chat:phone:" + hex(sha256(digits)[:3]).
	sum := sha256.Sum256([]byte("15551230001"))
	want := "chat:phone:" + hex.EncodeToString(sum[:3])
	if got := traceChat("15551230001@s.whatsapp.net"); got != want {
		t.Fatalf("phone = %q, want %q", got, want)
	}
}

// tlog must be a no-op when no trace file is open (all other tests).
func TestTlogNoopWithoutFile(t *testing.T) {
	closeTraceLog()
	tlog("anything", "k", "v")
}
