package tui

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"testing"
)

// Regression: after a fresh login the backend returns chats with
// ConversationTimestamp=0 (WhatsApp only stamps chats that have a last-message
// time). The previous sidebar filter dropped every such chat whose messages
// were not yet pre-loaded, so the loading screen reported "26 chats loaded"
// while the main view showed zero rows. Chats are now passed through; the
// zero-timestamp case simply means "no preview yet", which the right pane and
// header handle when the user opens the chat.
func TestSidebarItemsShowsZeroTimestampChats(t *testing.T) {
	model := m{
		sidebarTab: "chats",
		chats: []chat{
			{ID: "15550000001@s.whatsapp.net", Name: "Recent", ConversationTimestamp: 1710000000},
			{ID: "15550000002@s.whatsapp.net", Name: "Fresh", ConversationTimestamp: 0},
			{ID: "status@broadcast", Name: "Status"},
		},
		msgs: map[string][]wireMsg{},
	}

	items := model.sidebarItems()
	if len(items) != 2 {
		t.Fatalf("sidebarItems() returned %d chats, want 2 (status@broadcast must be filtered, fresh chat must be visible): %+v", len(items), items)
	}
	for _, c := range items {
		if c.ID == "status@broadcast" {
			t.Fatalf("status@broadcast leaked into sidebar: %+v", items)
		}
	}
}

func TestRegisterSessionReportsRequestErrors(t *testing.T) {
	msg, ok := registerSession(context.Background(), http.DefaultClient, "://invalid", "")().(sessionRegisterMsg)
	if !ok {
		t.Fatalf("registerSession returned %T, want sessionRegisterMsg", msg)
	}
	if msg.err == nil {
		t.Fatal("registerSession returned nil error for invalid URL")
	}
}

func TestConfigFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file mode does not provide ACL protection")
	}
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	saveConfig()

	info, err := os.Stat(resolveConfigPath())
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config mode = %o, want owner-only permissions", info.Mode().Perm())
	}
}
