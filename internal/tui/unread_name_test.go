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

	// Active chat should NOT contain unread dot
	if strings.Contains(lines[0], "\u25cf") {
		t.Fatalf("active chat should not display unread dot, got: %q", lines[0])
	}

	// Inactive chat with unread count SHOULD contain unread dot
	if !strings.Contains(lines[1], "\u25cf") {
		t.Fatalf("inactive chat should display unread dot, got: %q", lines[1])
	}
}
