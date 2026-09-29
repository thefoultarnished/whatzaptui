package tui

import (
	"regexp"
	"strings"
	"testing"
)

var ansiStripRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestUnreadRowKeepsName guards against the shine wave eating the username:
// renderShine wraps each rune in ANSI escapes, and padding measured on the
// styled string used to truncate the visible name away.
func TestUnreadRowKeepsName(t *testing.T) {
	x := m{
		sel:    0,
		mode:   "nav",
		active: "",
	}
	chats := []chat{
		{ID: "111@g.us", Name: "First Selected Group", UnreadCount: 2},
		{ID: "222@g.us", Name: "Tartu Hiking Group", UnreadCount: 5},
	}

	lines := x.renderUserList(chats, 0, len(chats), 40)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	// Row index 1 is unread, not selected -> goes through the shine path.
	visible := ansiStripRe.ReplaceAllString(lines[1], "")
	if !strings.Contains(visible, "Tartu Hiking Group") {
		t.Fatalf("unread row dropped the name; visible text = %q", visible)
	}

	// The selected row (index 0) is also unread: the shine must still play and
	// keep the name visible when highlighted.
	selected := ansiStripRe.ReplaceAllString(lines[0], "")
	if !strings.Contains(selected, "First Selected Group") {
		t.Fatalf("highlighted unread row dropped the name; visible text = %q", selected)
	}
}

func TestActiveChatSuppressesUnreadDot(t *testing.T) {
	x := m{
		sel:    0,
		mode:   "chat",
		active: "111@g.us",
	}
	chats := []chat{
		{ID: "111@g.us", Name: "Active Group", UnreadCount: 3},
		{ID: "222@g.us", Name: "Other Group", UnreadCount: 5},
	}

	lines := x.renderUserList(chats, 0, len(chats), 40)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	// Active chat should NOT show an unread count
	if strings.HasSuffix(strings.TrimRight(ansiStripRe.ReplaceAllString(lines[0], ""), " "), "3") {
		t.Fatalf("active chat should not display unread count, got: %q", lines[0])
	}

	// Inactive chat with unread count SHOULD show it at the end of the row
	if !strings.HasSuffix(ansiStripRe.ReplaceAllString(lines[1], ""), "5 ") {
		t.Fatalf("inactive chat should display unread count, got: %q", lines[1])
	}
}

func TestUnreadBadgeText(t *testing.T) {
	cases := map[int]string{1: "1", 9: "9", 10: "10", 99: "99", 100: "99+", 5000: "99+"}
	for n, want := range cases {
		if got := unreadBadgeText(n); got != want {
			t.Errorf("unreadBadgeText(%d) = %q, want %q", n, got, want)
		}
	}
}

// Rows must keep the same width whatever the badge width, and long names
// must still truncate around the badge.
func TestUnreadBadgeKeepsRowWidth(t *testing.T) {
	x := m{sel: 5, mode: "nav"}
	chats := []chat{
		{ID: "1@g.us", Name: "Read Chat"},
		{ID: "2@g.us", Name: "One", UnreadCount: 7},
		{ID: "3@g.us", Name: "Two", UnreadCount: 42},
		{ID: "4@g.us", Name: "Three", UnreadCount: 250},
		{ID: "5@g.us", Name: "A very long chat name that must be cut off somewhere", UnreadCount: 250},
	}
	lines := x.renderUserList(chats, 0, len(chats), 30)
	wants := []string{"", "7", "42", "99+", "99+"}
	for i, l := range lines {
		vis := ansiStripRe.ReplaceAllString(l, "")
		if w := runeDisplayWidth(vis); w != 30 {
			t.Errorf("row %d width = %d, want 30: %q", i, w, vis)
		}
		if wants[i] != "" && !strings.HasSuffix(vis, wants[i]+" ") {
			t.Errorf("row %d should end with %q and one space: %q", i, wants[i], vis)
		}
	}
}
