package main

import (
	"strings"
	"testing"
)

func TestHeaderShowsPhoneNumberForNamedIndividualChat(t *testing.T) {
	setTestTheme(t, TokyoNight)

	model := m{
		active: "15551230001@s.whatsapp.net",
		chats: []chat{
			{ID: "15551230001@s.whatsapp.net", Name: "Alice"},
		},
	}

	header := model.renderHeaderContainer(120, 30)
	visible := ansiStripRe.ReplaceAllString(header, "")
	if !strings.Contains(visible, "Alice") {
		t.Fatalf("header missing contact name; got %q", visible)
	}
	if !strings.Contains(visible, "+15551230001") {
		t.Fatalf("header missing phone number; got %q", visible)
	}
}

func TestHeaderOmitsDuplicatePhoneNumberWhenNameIsNumber(t *testing.T) {
	setTestTheme(t, TokyoNight)

	model := m{
		active: "15551230001@s.whatsapp.net",
		chats: []chat{
			{ID: "15551230001@s.whatsapp.net"},
		},
	}

	header := model.renderHeaderContainer(120, 30)
	visible := ansiStripRe.ReplaceAllString(header, "")
	if strings.Contains(visible, "+15551230001") {
		t.Fatalf("header should not repeat the number as both name and phone; got %q", visible)
	}
}

func TestHeaderOmitsPhoneNumberWhenSettingDisabled(t *testing.T) {
	setTestTheme(t, TokyoNight)

	saved := currentConfig.HidePhoneNumber
	t.Cleanup(func() { currentConfig.HidePhoneNumber = saved })
	currentConfig.HidePhoneNumber = true

	model := m{
		active: "15551230001@s.whatsapp.net",
		chats: []chat{
			{ID: "15551230001@s.whatsapp.net", Name: "Alice"},
		},
	}

	header := model.renderHeaderContainer(120, 30)
	visible := ansiStripRe.ReplaceAllString(header, "")
	if !strings.Contains(visible, "Alice") {
		t.Fatalf("header missing contact name; got %q", visible)
	}
	if strings.Contains(visible, "+15551230001") {
		t.Fatalf("phone number should be hidden when setting disabled; got %q", visible)
	}
}

func TestHeaderOmitsPhoneNumberForGroupChat(t *testing.T) {
	setTestTheme(t, TokyoNight)

	model := m{
		active: "111@g.us",
		chats: []chat{
			{ID: "111@g.us", Name: "Hiking Group"},
		},
	}

	header := model.renderHeaderContainer(120, 30)
	visible := ansiStripRe.ReplaceAllString(header, "")
	if !strings.Contains(visible, "Hiking Group") {
		t.Fatalf("header missing group name; got %q", visible)
	}
	if strings.Contains(visible, "+111") {
		t.Fatalf("group header should not show a phone number; got %q", visible)
	}
}
