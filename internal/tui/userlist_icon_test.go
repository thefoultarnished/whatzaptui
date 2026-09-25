package tui

import (
	"strings"
	"testing"
)

func TestUserlistIconPrefixNumbers(t *testing.T) {
	for _, style := range []string{"", "numbers", "unknown"} {
		if got := userlistIconPrefix(style); got != "" {
			t.Errorf("userlistIconPrefix(%q) = %q, want empty (falls back to numbers)", style, got)
		}
	}
}

func TestUserlistIconPrefixSparkleIndex(t *testing.T) {
	cases := map[string]string{
		"sparkle-0": "✿ ",
		"sparkle-1": "✦ ",
		"sparkle-4": "⌘ ",
	}
	for style, want := range cases {
		if got := userlistIconPrefix(style); got != want {
			t.Errorf("userlistIconPrefix(%q) = %q, want %q", style, got, want)
		}
	}
	if got := userlistIconPrefix("sparkle-99"); got != "" {
		t.Errorf("userlistIconPrefix(%q) = %q, want empty for out-of-range index", "sparkle-99", got)
	}
}

func TestRenderUserListUsesIconPrefixWhenConfigured(t *testing.T) {
	saved := currentConfig.UserlistIconStyle
	t.Cleanup(func() { currentConfig.UserlistIconStyle = saved })

	currentConfig.UserlistIconStyle = "sparkle-0"
	x := m{sel: -1, mode: "nav", active: ""}
	chats := []chat{{ID: "111@g.us", Name: "Alpha"}}

	lines := x.renderUserList(chats, 0, len(chats), 40)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	visible := ansiStripRe.ReplaceAllString(lines[0], "")
	if !strings.Contains(visible, "✿ Alpha") {
		t.Errorf("expected sparkle icon prefix, got %q", visible)
	}
	if strings.Contains(visible, "01.") {
		t.Errorf("expected number prefix to be replaced, got %q", visible)
	}
}

func TestRenderUserListDefaultsToNumbers(t *testing.T) {
	saved := currentConfig.UserlistIconStyle
	t.Cleanup(func() { currentConfig.UserlistIconStyle = saved })

	currentConfig.UserlistIconStyle = ""
	x := m{sel: -1, mode: "nav", active: ""}
	chats := []chat{{ID: "111@g.us", Name: "Alpha"}}

	lines := x.renderUserList(chats, 0, len(chats), 40)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	visible := ansiStripRe.ReplaceAllString(lines[0], "")
	if !strings.Contains(visible, "01. Alpha") {
		t.Errorf("expected number prefix, got %q", visible)
	}
}
