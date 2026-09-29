package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// runCmdSync runs a command and, for a batch, every sub-command in order.
func runCmdSync(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub != nil {
				_ = sub()
			}
		}
	}
}

// plainIcons makes a test independent of the Nerd Font setting in the user's config.
func plainIcons(t *testing.T) {
	t.Helper()
	old := currentConfig
	t.Cleanup(func() { currentConfig = old })
	currentConfig.MediaIconStyle, currentConfig.MediaViewStyle = "", ""
}

func pinTestChats() []chat {
	return []chat{
		{ID: "1@s.whatsapp.net", Name: "Newest", ConversationTimestamp: 300},
		{ID: "2@s.whatsapp.net", Name: "Middle", ConversationTimestamp: 200},
		{ID: "3@s.whatsapp.net", Name: "Oldest", ConversationTimestamp: 100},
	}
}

func chatIDs(cs []chat) string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID[:1]
	}
	return strings.Join(ids, "")
}

func TestResortChatsPinnedFirstThenNewest(t *testing.T) {
	x := m{chats: pinTestChats()}
	x.chats[2].Pinned = true
	x.chats[1].Pinned = true
	x.sel = 0
	x.resortChats("1@s.whatsapp.net")
	if got := chatIDs(x.chats); got != "231" {
		t.Fatalf("order = %s, want 231 (pinned by time, then the rest)", got)
	}
	if x.chats[x.sel].ID != "1@s.whatsapp.net" {
		t.Fatalf("selection moved off chat 1: sel=%d", x.sel)
	}
}

func TestChatJSONReadsPinned(t *testing.T) {
	var cs []chat
	if err := json.Unmarshal([]byte(`[{"id":"1@s.whatsapp.net","pinned":true},{"id":"2@s.whatsapp.net"}]`), &cs); err != nil {
		t.Fatal(err)
	}
	if !cs[0].Pinned || cs[1].Pinned {
		t.Fatalf("pinned flags = %v,%v want true,false", cs[0].Pinned, cs[1].Pinned)
	}
}

func TestPinCommandTargetsHighlightedChatFromCommandBar(t *testing.T) {
	x := m{chats: pinTestChats(), demoMode: true, sel: 1}
	cmd, handled := x.runCommand("/pin", true)
	if !handled || cmd == nil {
		t.Fatalf("/pin not handled: handled=%v cmd=%v", handled, cmd != nil)
	}
	byID := map[string]bool{}
	for _, c := range x.chats {
		byID[c.ID] = c.Pinned
	}
	if !byID["2@s.whatsapp.net"] || byID["1@s.whatsapp.net"] || byID["3@s.whatsapp.net"] {
		t.Fatalf("pinned = %v, want only chat 2", byID)
	}
	if x.chats[0].ID != "2@s.whatsapp.net" {
		t.Fatalf("pinned chat should move to the top, order = %s", chatIDs(x.chats))
	}
}

func TestPinCommandTogglesOpenChatOffAgain(t *testing.T) {
	x := m{chats: pinTestChats(), demoMode: true, active: "3@s.whatsapp.net"}
	x.runCommand("/pin", false)
	if !x.chats[0].Pinned || x.chats[0].ID != "3@s.whatsapp.net" {
		t.Fatalf("open chat not pinned to top: %+v", x.chats)
	}
	x.runCommand("/pin", false)
	if x.chats[0].Pinned || chatIDs(x.chats) != "123" {
		t.Fatalf("second /pin should unpin and restore time order: %s %+v", chatIDs(x.chats), x.chats)
	}
}

func TestPinCommandWithNoChatShowsMessage(t *testing.T) {
	x := m{chats: pinTestChats(), demoMode: true, sel: -1}
	if cmd, handled := x.runCommand("/pin", true); !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd != nil)
	}
	for _, c := range x.chats {
		if c.Pinned {
			t.Fatalf("nothing should be pinned: %+v", x.chats)
		}
	}
	// A chat that is not in the chat list (for example a bare contact) cannot be pinned.
	y := m{chats: pinTestChats(), demoMode: true, active: "99@s.whatsapp.net"}
	y.runCommand("/pin", false)
	for _, c := range y.chats {
		if c.Pinned {
			t.Fatalf("unknown chat must not pin anything: %+v", y.chats)
		}
	}
}

func TestPinSendsStateToBackend(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chats/pin" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		bodies = append(bodies, b)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	x := m{client: srv.Client(), baseURL: srv.URL, chats: pinTestChats(), active: "2@s.whatsapp.net"}
	x.chats[1].Pinned = true // already pinned: /pin must ask for unpin
	cmd := x.togglePin(false)
	if cmd == nil {
		t.Fatal("togglePin returned nil")
	}
	if x.chats[1].Pinned != true {
		t.Fatal("local state must only change once the backend reports it")
	}
	runCmdSync(cmd)
	if len(bodies) != 1 || bodies[0]["chatId"] != "2@s.whatsapp.net" || bodies[0]["pinned"] != false {
		t.Fatalf("request bodies = %v, want one unpin for chat 2", bodies)
	}

	bodies = nil
	x.chats[1].Pinned = false
	runCmdSync(x.togglePin(false))
	if len(bodies) != 1 || bodies[0]["pinned"] != true {
		t.Fatalf("request bodies = %v, want one pin", bodies)
	}
}

func TestSidebarRowShowsPinMarkOnlyWhenPinned(t *testing.T) {
	setTestTheme(t, TokyoNight)
	plainIcons(t)
	x := m{whitelist: map[string]string{}, chats: pinTestChats()}
	x.chats[0].Pinned = true
	rows := x.renderUserList(x.chats, 0, 2, 30)
	if !strings.Contains(ansiStripRe.ReplaceAllString(rows[0], ""), pinMarkFallback) {
		t.Fatalf("pinned row missing pin mark: %q", rows[0])
	}
	if !strings.HasSuffix(ansiStripRe.ReplaceAllString(rows[0], ""), pinMarkFallback+" ") {
		t.Fatalf("pin mark should be followed by one space at the row end: %q", rows[0])
	}
	if strings.Contains(ansiStripRe.ReplaceAllString(rows[1], ""), pinMarkFallback) {
		t.Fatalf("unpinned row shows pin mark: %q", rows[1])
	}
}

func TestSidebarPinMarkKeepsRowWidth(t *testing.T) {
	setTestTheme(t, TokyoNight)
	plainIcons(t)
	x := m{whitelist: map[string]string{}, chats: pinTestChats()}
	x.chats[0].UnreadCount = 3
	plain := ansiStripRe.ReplaceAllString(x.renderUserList(x.chats, 0, 1, 30)[0], "")
	x.chats[0].Pinned = true
	pinned := ansiStripRe.ReplaceAllString(x.renderUserList(x.chats, 0, 1, 30)[0], "")
	if len([]rune(plain)) != len([]rune(pinned)) {
		t.Fatalf("pin mark changed row width: %q vs %q", plain, pinned)
	}
	if !strings.Contains(pinned, "3"+pinMarkFallback+" ") {
		t.Fatalf("unread badge and pin mark should both show: %q", pinned)
	}
}

func TestPinMarkFollowsNerdFontSetting(t *testing.T) {
	plainIcons(t)
	if got := pinMark(); got != pinMarkFallback {
		t.Fatalf("no Nerd Font: pinMark = %q, want fallback %q", got, pinMarkFallback)
	}
	currentConfig.MediaIconStyle = "nerd"
	if got := pinMark(); got != pinMarkNerd {
		t.Fatalf("nerd icons: pinMark = %q, want %q", got, pinMarkNerd)
	}
	currentConfig.MediaIconStyle, currentConfig.MediaViewStyle = "", "glyph"
	if got := pinMark(); got != pinMarkNerd {
		t.Fatalf("glyph media view: pinMark = %q, want %q", got, pinMarkNerd)
	}
}

func TestSidebarRowUsesNerdPinAndKeepsWidth(t *testing.T) {
	setTestTheme(t, TokyoNight)
	plainIcons(t)

	x := m{whitelist: map[string]string{}, chats: pinTestChats()}
	x.chats[0].Pinned = true
	fallback := ansiStripRe.ReplaceAllString(x.renderUserList(x.chats, 0, 1, 30)[0], "")
	currentConfig.MediaIconStyle = "nerd"
	nerd := ansiStripRe.ReplaceAllString(x.renderUserList(x.chats, 0, 1, 30)[0], "")

	if !strings.Contains(nerd, pinMarkNerd) || strings.Contains(nerd, pinMarkFallback) {
		t.Fatalf("nerd row should use the Nerd pin only: %q", nerd)
	}
	if len([]rune(nerd)) != len([]rune(fallback)) {
		t.Fatalf("nerd pin changed row width: %q vs %q", nerd, fallback)
	}
}
