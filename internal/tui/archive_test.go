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

func archiveTestChats() []chat {
	return []chat{
		{ID: "1@s.whatsapp.net", Name: "Newest", ConversationTimestamp: 300},
		{ID: "2@s.whatsapp.net", Name: "Middle", ConversationTimestamp: 200, Archived: true},
		{ID: "3@s.whatsapp.net", Name: "Oldest", ConversationTimestamp: 100},
		{ID: "4@s.whatsapp.net", Name: "Away", ConversationTimestamp: 50, Archived: true},
	}
}

func visibleIDs(x m) string {
	items := x.filtered()
	ids := make([]string, len(items))
	for i, c := range items {
		ids[i] = c.ID[:1]
	}
	return strings.Join(ids, "")
}

func TestChatJSONReadsArchived(t *testing.T) {
	var cs []chat
	if err := json.Unmarshal([]byte(`[{"id":"1@s.whatsapp.net","archived":true},{"id":"2@s.whatsapp.net"}]`), &cs); err != nil {
		t.Fatal(err)
	}
	if !cs[0].Archived || cs[1].Archived {
		t.Fatalf("archived flags = %v,%v want true,false", cs[0].Archived, cs[1].Archived)
	}
}

func TestChatsTabHidesArchivedUntilArchivedView(t *testing.T) {
	x := m{sidebarTab: "chats", chats: archiveTestChats()}
	if got := visibleIDs(x); got != "13" {
		t.Fatalf("normal view shows %q, want 13", got)
	}
	x.archivedView = true
	if got := visibleIDs(x); got != "24" {
		t.Fatalf("archived view shows %q, want 24", got)
	}
}

func TestArchivedCommandTogglesViewAndResetsSelection(t *testing.T) {
	x := m{sidebarTab: "contacts", mode: "chat", chats: archiveTestChats(), sel: 3, sideScroll: 2, search: "abc"}
	cmd, handled := x.runCommand("/archived", true)
	if !handled || cmd == nil {
		t.Fatalf("/archived not handled: handled=%v cmd=%v", handled, cmd != nil)
	}
	if !x.archivedView || x.sidebarTab != "chats" || x.mode != "nav" || x.sel != 0 || x.sideScroll != 0 || x.search != "" {
		t.Fatalf("view not reset: archived=%v tab=%s mode=%s sel=%d scroll=%d search=%q",
			x.archivedView, x.sidebarTab, x.mode, x.sel, x.sideScroll, x.search)
	}
	x.runCommand("/archived", true)
	if x.archivedView {
		t.Fatal("second /archived should go back to normal chats")
	}
}

func TestArchivedTabLabel(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{sidebarTab: "chats", chats: archiveTestChats()}
	normal := ansiStripRe.ReplaceAllString(x.renderSide(28, 10), "")
	if strings.Contains(normal, "Archived") {
		t.Fatalf("normal view must not say Archived: %q", normal)
	}
	x.archivedView = true
	archived := ansiStripRe.ReplaceAllString(x.renderSide(28, 10), "")
	if !strings.Contains(archived, "Archived") || !strings.Contains(archived, "People") {
		t.Fatalf("archived view should label the tab and keep People: %q", archived)
	}
	normalRow := strings.Split(normal, "\n")[0]
	archivedRow := strings.Split(archived, "\n")[0]
	if len([]rune(archivedRow)) != len([]rune(normalRow)) {
		t.Fatalf("archived tab row width %d differs from normal %d: %q vs %q",
			len([]rune(archivedRow)), len([]rune(normalRow)), archivedRow, normalRow)
	}
}

func TestArchiveCommandMovesChatBetweenLists(t *testing.T) {
	x := m{sidebarTab: "chats", chats: archiveTestChats(), demoMode: true, sel: 0} // sel 0 = chat 1
	x.chats[0].Pinned = true
	x.resortChats("")

	x.runCommand("/archive", true)
	if got := visibleIDs(x); got != "3" {
		t.Fatalf("after archiving chat 1 the normal list is %q, want 3", got)
	}
	for _, c := range x.chats {
		if c.ID == "1@s.whatsapp.net" && (!c.Archived || c.Pinned) {
			t.Fatalf("archiving must set Archived and clear Pinned: %+v", c)
		}
	}

	// In the archived view /archive on the highlighted chat brings it back.
	x.runCommand("/archived", true)
	if got := visibleIDs(x); got != "124" {
		t.Fatalf("archived list is %q, want 124", got)
	}
	x.sel = 0
	x.runCommand("/archive", true)
	x.runCommand("/archived", true)
	if got := visibleIDs(x); got != "13" && got != "31" {
		t.Fatalf("after unarchiving the normal list is %q, want chats 1 and 3", got)
	}
}

func TestArchiveSendsStateToBackend(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chats/archive" || r.Method != http.MethodPost {
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

	x := m{client: srv.Client(), baseURL: srv.URL, chats: archiveTestChats(), active: "1@s.whatsapp.net"}
	runCmdSync(x.toggleArchive(false))
	if len(bodies) != 1 || bodies[0]["chatId"] != "1@s.whatsapp.net" || bodies[0]["archived"] != true {
		t.Fatalf("bodies = %v, want one archive for chat 1", bodies)
	}
	if x.chats[0].Archived {
		t.Fatal("local state must only change once the backend reports it")
	}

	bodies = nil
	x.active = "2@s.whatsapp.net" // already archived: must ask for unarchive
	runCmdSync(x.toggleArchive(false))
	if len(bodies) != 1 || bodies[0]["archived"] != false {
		t.Fatalf("bodies = %v, want one unarchive", bodies)
	}
}

func TestArchiveWithNoChatShowsMessage(t *testing.T) {
	x := m{chats: archiveTestChats(), demoMode: true, sel: -1}
	if cmd := x.toggleArchive(true); cmd == nil {
		t.Fatal("expected a top bar message")
	}
	for _, c := range x.chats {
		if c.ID == "1@s.whatsapp.net" && c.Archived {
			t.Fatal("nothing should change without a target")
		}
	}
}

func TestPinRefusesArchivedChat(t *testing.T) {
	x := m{chats: archiveTestChats(), demoMode: true, active: "2@s.whatsapp.net"}
	x.runCommand("/pin", false)
	for _, c := range x.chats {
		if c.Pinned {
			t.Fatalf("an archived chat must not be pinned: %+v", x.chats)
		}
	}
}

func TestSelectionStaysOnSameChatWhenArchivedChatsAreHidden(t *testing.T) {
	// Visible list is chats 1 and 3; the highlighted row (index 1) is chat 3.
	// A re-sort must keep it there even though x.chats also holds archived ones.
	x := m{sidebarTab: "chats", chats: archiveTestChats(), sel: 1}
	selected := x.selectedChatID()
	if selected != "3@s.whatsapp.net" {
		t.Fatalf("selectedChatID = %q, want chat 3", selected)
	}
	x.chats[2].ConversationTimestamp = 500 // chat 3 becomes newest
	x.resortChats(selected)
	if got := x.filtered()[x.sel].ID; got != "3@s.whatsapp.net" {
		t.Fatalf("selection moved to %q, want chat 3", got)
	}
}

func TestHeaderUnreadIgnoresArchivedChats(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{chats: []chat{
		{ID: "1@s.whatsapp.net", UnreadCount: 2},
		{ID: "2@s.whatsapp.net", UnreadCount: 5, Archived: true},
	}}
	visible := ansiStripRe.ReplaceAllString(x.renderHeaderContainer(120, 30), "")
	if !strings.Contains(visible, "2 unread") || strings.Contains(visible, "7 unread") {
		t.Fatalf("header should count only non-archived unread, got %q", visible)
	}
}

func TestAltCLeavesArchivedView(t *testing.T) {
	x := m{sidebarTab: "chats", archivedView: true, status: "ready", chats: archiveTestChats()}
	next, _ := x.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true})
	if next.(m).archivedView {
		t.Fatal("alt+c should return to normal chats")
	}
}
