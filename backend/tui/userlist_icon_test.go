package main

import (
	"fmt"
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

func TestBuildUserlistIconPickerItems(t *testing.T) {
	items := buildUserlistIconPickerItems()

	var sparkleIcons []string
	for _, a := range typingAnimationList {
		if a.key == "sparkle" {
			sparkleIcons = a.icons
		}
	}
	want := 1 + len(sparkleIcons)
	if len(items) != want {
		t.Fatalf("got %d items, want %d", len(items), want)
	}
	if items[0].key != "numbers" {
		t.Errorf("first item key = %q, want %q", items[0].key, "numbers")
	}
	for i, icon := range sparkleIcons {
		wantKey := fmt.Sprintf("sparkle-%d", i)
		var found *pickerItem
		for j := range items {
			if items[j].key == wantKey {
				found = &items[j]
				break
			}
		}
		if found == nil {
			t.Errorf("no item with key %q", wantKey)
			continue
		}
		if found.label != icon {
			t.Errorf("item %q label = %q, want %q", wantKey, found.label, icon)
		}
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
