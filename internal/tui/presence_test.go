package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

const presenceChat = "15551230001@s.whatsapp.net"

func presenceEvent(t *testing.T, chatID string, online bool, lastSeen int64) wsEvtMsg {
	t.Helper()
	b, err := json.Marshal(map[string]any{"chatId": chatID, "online": online, "lastSeen": lastSeen})
	if err != nil {
		t.Fatal(err)
	}
	return wsEvtMsg{ok: true, evt: env{Type: "presence", Payload: b}}
}

func TestPresenceLabel(t *testing.T) {
	x := m{presence: map[string]presenceInfo{
		presenceChat:       {online: true},
		"2@s.whatsapp.net": {online: false, lastSeen: 1710000000},
		"3@s.whatsapp.net": {online: false, lastSeen: 0},
		"4@g.us":           {online: true},
	}}
	cases := []struct {
		id         string
		wantLabel  string
		wantOnline bool
	}{
		{presenceChat, "online", true},
		{"2@s.whatsapp.net", "offline", false},
		{"3@s.whatsapp.net", "", false}, // offline and hides last seen: show nothing
		{"4@g.us", "", false},           // groups have no presence
		{"5@s.whatsapp.net", "", false}, // never heard from
	}
	for _, c := range cases {
		label, online := x.presenceLabel(c.id)
		if label != c.wantLabel || online != c.wantOnline {
			t.Errorf("presenceLabel(%q) = %q,%v want %q,%v", c.id, label, online, c.wantLabel, c.wantOnline)
		}
	}
}

func TestApplyPresenceStoresAndRejectsBadPayload(t *testing.T) {
	x := m{}
	if x.applyPresence([]byte("not json")) || x.applyPresence([]byte(`{"online":true}`)) {
		t.Fatal("bad payloads must be ignored")
	}
	if len(x.presence) != 0 {
		t.Fatalf("presence = %v, want empty", x.presence)
	}
	if !x.applyPresence([]byte(`{"chatId":"a@s.whatsapp.net","online":true,"lastSeen":0}`)) {
		t.Fatal("valid payload rejected")
	}
	if !x.presence["a@s.whatsapp.net"].online {
		t.Fatal("online not stored")
	}
	x.applyPresence([]byte(`{"chatId":"a@s.whatsapp.net","online":false,"lastSeen":99}`))
	if p := x.presence["a@s.whatsapp.net"]; p.online || p.lastSeen != 99 {
		t.Fatalf("update not applied: %+v", p)
	}
}

func TestWSPresenceEventUpdatesHeader(t *testing.T) {
	setTestTheme(t, TokyoNight)
	model := m{active: presenceChat, chats: []chat{{ID: presenceChat, Name: "Alice"}}}

	next, _ := model.updateInner(presenceEvent(t, presenceChat, true, 0))
	visible := ansiStripRe.ReplaceAllString(next.(m).renderHeaderContainer(120, 30), "")
	if !strings.Contains(visible, "● online") {
		t.Fatalf("header should show online, got %q", visible)
	}

	next, _ = next.(m).updateInner(presenceEvent(t, presenceChat, false, 1710000000))
	visible = ansiStripRe.ReplaceAllString(next.(m).renderHeaderContainer(120, 30), "")
	if !strings.Contains(visible, "○ offline") || strings.Contains(visible, "● online") {
		t.Fatalf("header should show offline, got %q", visible)
	}
}

func TestHeaderShowsNothingWhenContactHidesPresence(t *testing.T) {
	setTestTheme(t, TokyoNight)
	// Went offline with no visible last seen: same as hidden.
	model := m{
		active:   presenceChat,
		chats:    []chat{{ID: presenceChat, Name: "Alice"}},
		presence: map[string]presenceInfo{presenceChat: {online: false, lastSeen: 0}},
	}
	visible := ansiStripRe.ReplaceAllString(model.renderHeaderContainer(120, 30), "")
	if strings.Contains(visible, "online") || strings.Contains(visible, "offline") {
		t.Fatalf("hidden presence must show nothing, got %q", visible)
	}
}

func TestSettingsShowOnlineToggle(t *testing.T) {
	old := currentConfig
	t.Cleanup(func() { currentConfig = old })
	currentConfig.ShowOnline = true
	idx := -1
	for i, s := range settingsDefs {
		if s.name == "Show online status" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("Show online status missing from settings")
	}
	settingsDefs[idx].set(false)
	if currentConfig.ShowOnline || settingsDefs[idx].get() {
		t.Fatal("toggle did not turn ShowOnline off")
	}
	settingsDefs[idx].set(true)
	if !currentConfig.ShowOnline {
		t.Fatal("toggle did not turn ShowOnline on")
	}
}

func TestSubscribePresenceSkipsGroupsAndNoChat(t *testing.T) {
	if (m{}).subscribePresenceCmd() != nil {
		t.Fatal("no active chat: nothing to subscribe")
	}
	if (m{active: "1@g.us"}).subscribePresenceCmd() != nil {
		t.Fatal("groups have no presence")
	}
	if (m{active: presenceChat}).subscribePresenceCmd() == nil {
		t.Fatal("1-to-1 chat should subscribe")
	}
}

func TestShowOnlineDefaultsOn(t *testing.T) {
	old := currentConfig
	t.Cleanup(func() { currentConfig = old })
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	loadConfig()
	if !currentConfig.ShowOnline {
		t.Fatal("ShowOnline should default to true")
	}
}
