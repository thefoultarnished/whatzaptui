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

func altKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}
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

func TestAltDTogglesArchivedViewFromAnyMode(t *testing.T) {
	for _, mode := range []string{"nav", "chat", "search"} {
		x := m{sidebarTab: "contacts", mode: mode, status: "ready", chats: archiveTestChats(), sel: 3, sideScroll: 2, search: "abc"}
		next, _ := x.key(altKey('d'))
		got := next.(m)
		if !got.archivedView || got.sidebarTab != "chats" || got.mode == "chat" || got.mode == "search" ||
			got.sel != 0 || got.sideScroll != 0 || got.search != "" || !got.sidebarFocused {
			t.Fatalf("%s: view not switched/reset: archived=%v tab=%s mode=%s sel=%d scroll=%d search=%q",
				mode, got.archivedView, got.sidebarTab, got.mode, got.sel, got.sideScroll, got.search)
		}
		next, _ = got.key(altKey('d'))
		if next.(m).archivedView {
			t.Fatalf("%s: second alt+d should go back to normal chats", mode)
		}
	}
}

func TestAltDIgnoredUntilReady(t *testing.T) {
	x := m{sidebarTab: "chats", status: "Connecting...", chats: archiveTestChats()}
	next, _ := x.key(altKey('d'))
	if next.(m).archivedView {
		t.Fatal("alt+d must do nothing before the session is ready")
	}
}

func TestAltCLeavesArchivedView(t *testing.T) {
	x := m{sidebarTab: "chats", archivedView: true, status: "ready", chats: archiveTestChats()}
	next, _ := x.key(altKey('c'))
	if next.(m).archivedView {
		t.Fatal("alt+c should return to normal chats")
	}
}

func TestArchiveFooterVisibility(t *testing.T) {
	none := []chat{{ID: "1@s.whatsapp.net", Name: "A", ConversationTimestamp: 1}}
	cases := []struct {
		name string
		x    m
		want bool
	}{
		{"normal, none archived", m{sidebarTab: "chats", chats: none}, false},
		{"normal, some archived", m{sidebarTab: "chats", chats: archiveTestChats()}, true},
		{"archived view, none left", m{sidebarTab: "chats", archivedView: true, chats: none}, true},
		{"archived view, some", m{sidebarTab: "chats", archivedView: true, chats: archiveTestChats()}, true},
		{"people tab", m{sidebarTab: "contacts", chats: archiveTestChats()}, false},
		{"people tab in archived view", m{sidebarTab: "contacts", archivedView: true, chats: archiveTestChats()}, false},
	}
	for _, c := range cases {
		if got := c.x.showArchiveFooter(); got != c.want {
			t.Errorf("%s: showArchiveFooter = %v, want %v", c.name, got, c.want)
		}
		wantH := 0
		if c.want {
			wantH = 1
		}
		if got := c.x.archiveFooterHeight(); got != wantH {
			t.Errorf("%s: archiveFooterHeight = %d, want %d", c.name, got, wantH)
		}
	}
}

func TestArchiveFooterText(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{sidebarTab: "chats", chats: archiveTestChats()}
	x.chats[1].UnreadCount = 2
	x.chats[3].UnreadCount = 3

	normal := ansiStripRe.ReplaceAllString(x.renderArchiveFooter(28), "")
	if !strings.Contains(normal, "Archived") || !strings.Contains(normal, "5") || !strings.HasSuffix(strings.TrimRight(normal, " "), "alt+d") {
		t.Fatalf("normal footer should show label, unread total and shortcut: %q", normal)
	}
	if len([]rune(normal)) != 28 {
		t.Fatalf("footer width = %d, want 28: %q", len([]rune(normal)), normal)
	}

	x.chats[1].UnreadCount, x.chats[3].UnreadCount = 0, 0
	if quiet := ansiStripRe.ReplaceAllString(x.renderArchiveFooter(28), ""); strings.ContainsAny(quiet, "0123456789") {
		t.Fatalf("no unread: footer must not show a count: %q", quiet)
	}

	x.archivedView = true
	back := ansiStripRe.ReplaceAllString(x.renderArchiveFooter(28), "")
	if !strings.Contains(back, "‹ Chats") || !strings.Contains(back, "alt+d") || strings.Contains(back, "Archived") {
		t.Fatalf("archived footer should be the way back: %q", back)
	}

	narrow := ansiStripRe.ReplaceAllString(x.renderArchiveFooter(6), "")
	if !strings.Contains(narrow, "Chats") {
		t.Fatalf("too-narrow footer should keep the label: %q", narrow)
	}
}

func TestArchiveFooterKeepsSidebarHeightAndShrinksList(t *testing.T) {
	setTestTheme(t, TokyoNight)
	many := make([]chat, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, chat{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@s.whatsapp.net", Name: "Chat", ConversationTimestamp: int64(1000 - i)})
	}
	plain := m{sidebarTab: "chats", chats: many, whitelist: map[string]string{}}
	withArchive := plain
	withArchive.chats = append(append([]chat{}, many...), chat{ID: "z@s.whatsapp.net", Name: "Old", ConversationTimestamp: 1, Archived: true})

	const h = 20
	a := plain.renderSide(28, h)
	b := withArchive.renderSide(28, h)
	if strings.Count(a, "\n") != strings.Count(b, "\n") {
		t.Fatalf("sidebar height changed: %d vs %d lines", strings.Count(a, "\n")+1, strings.Count(b, "\n")+1)
	}
	rowsA := len(strings.Split(ansiStripRe.ReplaceAllString(a, ""), "\n"))
	if rowsA != h {
		t.Fatalf("sidebar renders %d lines, want %d", rowsA, h)
	}
	last := strings.Split(strings.TrimRight(ansiStripRe.ReplaceAllString(b, ""), "\n"), "\n")
	if !strings.Contains(last[len(last)-1], "Archived") || !strings.Contains(last[len(last)-1], "alt+d") {
		t.Fatalf("footer should be the last sidebar line: %q", last[len(last)-1])
	}
	if strings.Contains(ansiStripRe.ReplaceAllString(a, ""), "alt+d") {
		t.Fatal("no footer expected when nothing is archived")
	}
}

func TestSideViewRowsLosesOneRowForFooter(t *testing.T) {
	plain := m{h: 40, sidebarTab: "chats", chats: []chat{{ID: "1@s.whatsapp.net", ConversationTimestamp: 1}}}
	withArchive := plain
	withArchive.chats = append(append([]chat{}, plain.chats...), chat{ID: "2@s.whatsapp.net", ConversationTimestamp: 2, Archived: true})
	if got, want := withArchive.sideViewRows(), plain.sideViewRows()-1; got != want {
		t.Fatalf("sideViewRows with footer = %d, want %d", got, want)
	}
	people := withArchive
	people.sidebarTab = "contacts"
	if people.sideViewRows() != plain.sideViewRows() {
		t.Fatalf("People tab must not reserve footer space: %d vs %d", people.sideViewRows(), plain.sideViewRows())
	}
}

func TestLastChatStaysReachableWithFooter(t *testing.T) {
	// 30 chats plus one archived: scrolling to the last chat must keep it inside the visible window.
	chats := make([]chat, 0, 31)
	for i := 0; i < 30; i++ {
		chats = append(chats, chat{ID: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@s.whatsapp.net", Name: "Chat", ConversationTimestamp: int64(1000 - i)})
	}
	chats = append(chats, chat{ID: "z@s.whatsapp.net", ConversationTimestamp: 1, Archived: true})
	x := m{h: 30, sidebarTab: "chats", chats: chats, sel: 29}
	x.ensureSideVisible(x.sideViewRows())
	if x.sel < x.sideScroll || x.sel >= x.sideScroll+x.sideViewRows() {
		t.Fatalf("last chat not visible: sel=%d scroll=%d rows=%d", x.sel, x.sideScroll, x.sideViewRows())
	}
}

func TestArchivedTabLabel(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := m{sidebarTab: "chats", chats: archiveTestChats()}
	normal := ansiStripRe.ReplaceAllString(x.renderSide(28, 12), "")
	if strings.Split(normal, "\n")[0] == "" || strings.Contains(strings.Split(normal, "\n")[0], "Archived") {
		t.Fatalf("normal tab row must not say Archived: %q", normal)
	}
	x.archivedView = true
	archived := ansiStripRe.ReplaceAllString(x.renderSide(28, 12), "")
	first := strings.Split(archived, "\n")[0]
	if !strings.Contains(first, "Archived") || !strings.Contains(first, "People") {
		t.Fatalf("archived view should label the tab and keep People: %q", first)
	}
	if len([]rune(first)) != len([]rune(strings.Split(normal, "\n")[0])) {
		t.Fatalf("archived tab row width differs from normal: %q", first)
	}
}

func TestArchiveAndUnarchiveCommandsMoveChatBetweenLists(t *testing.T) {
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

	x.toggleArchivedView()
	if got := visibleIDs(x); got != "124" {
		t.Fatalf("archived list is %q, want 124", got)
	}
	x.sel = 0
	x.runCommand("/unarchive", true)
	x.toggleArchivedView()
	if got := visibleIDs(x); got != "13" && got != "31" {
		t.Fatalf("after unarchiving the normal list is %q, want chats 1 and 3", got)
	}
}

func TestArchiveAndUnarchiveRefuseWrongState(t *testing.T) {
	// Chat 1 is not archived, chat 2 is.
	x := m{sidebarTab: "chats", chats: archiveTestChats(), demoMode: true, active: "1@s.whatsapp.net"}
	if cmd := x.archiveChat(false, false); cmd == nil {
		t.Fatal("expected a top bar message")
	}
	if x.chats[0].Archived {
		t.Fatal("/unarchive on a normal chat must change nothing")
	}
	x.active = "2@s.whatsapp.net"
	if cmd := x.archiveChat(false, true); cmd == nil {
		t.Fatal("expected a top bar message")
	}
	if !x.chats[1].Archived {
		t.Fatal("/archive on an archived chat must change nothing")
	}
}

func TestArchiveCommandsSendStateToBackend(t *testing.T) {
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
	runCmdSync(x.archiveChat(false, true))
	if len(bodies) != 1 || bodies[0]["chatId"] != "1@s.whatsapp.net" || bodies[0]["archived"] != true {
		t.Fatalf("bodies = %v, want one archive for chat 1", bodies)
	}
	if x.chats[0].Archived {
		t.Fatal("local state must only change once the backend reports it")
	}

	bodies = nil
	x.active = "2@s.whatsapp.net"
	runCmdSync(x.archiveChat(false, false))
	if len(bodies) != 1 || bodies[0]["chatId"] != "2@s.whatsapp.net" || bodies[0]["archived"] != false {
		t.Fatalf("bodies = %v, want one unarchive for chat 2", bodies)
	}
}

func TestArchiveWithNoChatShowsMessage(t *testing.T) {
	x := m{chats: archiveTestChats(), demoMode: true, sel: -1}
	if cmd := x.archiveChat(true, true); cmd == nil {
		t.Fatal("expected a top bar message")
	}
	if cmd := x.archiveChat(true, false); cmd == nil {
		t.Fatal("expected a top bar message")
	}
	for _, c := range x.chats {
		if c.ID == "1@s.whatsapp.net" && c.Archived {
			t.Fatal("nothing should change without a target")
		}
	}
}

func TestArchivedViewCommandIsGone(t *testing.T) {
	x := m{sidebarTab: "chats", chats: archiveTestChats()}
	x.runCommand("/archived", true)
	if x.archivedView {
		t.Fatal("/archived was replaced by alt+d")
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
