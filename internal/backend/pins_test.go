package backend

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func newPinTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	if _, err := app.db.Exec(`CREATE TABLE IF NOT EXISTS chat_pins (chat_id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS chat_archives (chat_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create flag tables: %v", err)
	}
	return app
}

func servedChats(t *testing.T, app *App) []Chat {
	t.Helper()
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/chats", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/chats status = %d", rec.Code)
	}
	var out struct {
		Chats []Chat `json:"chats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Chats
}

func TestChatsServePinnedFirstThenNewest(t *testing.T) {
	app := newPinTestApp(t)
	app.state.Chats = map[string]Chat{
		"1@s.whatsapp.net": {ID: "1@s.whatsapp.net", Name: "Newest", ConversationTimestamp: 300},
		"2@s.whatsapp.net": {ID: "2@s.whatsapp.net", Name: "Middle", ConversationTimestamp: 200},
		"3@s.whatsapp.net": {ID: "3@s.whatsapp.net", Name: "Oldest", ConversationTimestamp: 100},
		"4@g.us":           {ID: "4@g.us", Subject: "Group", ConversationTimestamp: 150},
	}
	app.applyPin(types.NewJID("3", types.DefaultUserServer), true, false)
	app.applyPin(types.NewJID("4", types.GroupServer), true, false)

	got := servedChats(t, app)
	wantOrder := []string{"4@g.us", "3@s.whatsapp.net", "1@s.whatsapp.net", "2@s.whatsapp.net"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d chats, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Fatalf("order[%d] = %s, want %s (all: %+v)", i, got[i].ID, id, got)
		}
	}
	if !got[0].Pinned || !got[1].Pinned || got[2].Pinned || got[3].Pinned {
		t.Fatalf("pinned flags wrong: %+v", got)
	}
}

func TestUnpinPutsChatBackInTimeOrder(t *testing.T) {
	app := newPinTestApp(t)
	app.state.Chats = map[string]Chat{
		"1@s.whatsapp.net": {ID: "1@s.whatsapp.net", Name: "New", ConversationTimestamp: 300},
		"2@s.whatsapp.net": {ID: "2@s.whatsapp.net", Name: "Old", ConversationTimestamp: 100},
	}
	jid := types.NewJID("2", types.DefaultUserServer)
	app.applyPin(jid, true, false)
	if got := servedChats(t, app); got[0].ID != "2@s.whatsapp.net" {
		t.Fatalf("pinned chat should lead, got %+v", got)
	}
	app.applyPin(jid, false, false)
	got := servedChats(t, app)
	if got[0].ID != "1@s.whatsapp.net" || got[1].Pinned {
		t.Fatalf("unpinned chat should return to time order, got %+v", got)
	}
}

func TestApplyPinCollectsFullSyncPinsOnlyWhileResyncing(t *testing.T) {
	app := newPinTestApp(t)
	jid := types.NewJID("5", types.DefaultUserServer)

	app.applyPin(jid, true, true) // no resync running: nothing is collected
	if app.pinSeen != nil {
		t.Fatalf("pinSeen = %v, want nil outside a resync", app.pinSeen)
	}

	app.pinSeen = map[string]bool{}
	app.applyPin(types.NewJID("6", types.DefaultUserServer), true, true)
	app.applyPin(types.NewJID("7", types.DefaultUserServer), true, false) // live change: not a snapshot entry
	app.applyPin(types.NewJID("8", types.DefaultUserServer), false, true) // unpin is not a snapshot pin
	if len(app.pinSeen) != 1 || !app.pinSeen["6@s.whatsapp.net"] {
		t.Fatalf("pinSeen = %v, want only 6@s.whatsapp.net", app.pinSeen)
	}
}

func TestHandlePinChatNeedsConnection(t *testing.T) {
	app := newPinTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/chats/pin", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 without a connected client", rec.Code)
	}
}

func TestChatsServeArchivedFlagAndDropPin(t *testing.T) {
	app := newPinTestApp(t)
	app.state.Chats = map[string]Chat{
		"1@s.whatsapp.net": {ID: "1@s.whatsapp.net", Name: "Keep", ConversationTimestamp: 300},
		"2@s.whatsapp.net": {ID: "2@s.whatsapp.net", Name: "Away", ConversationTimestamp: 200},
	}
	jid := types.NewJID("2", types.DefaultUserServer)
	app.applyPin(jid, true, false)
	app.applyArchive(jid, true, false) // archiving also unpins

	got := servedChats(t, app)
	byID := map[string]Chat{}
	for _, c := range got {
		byID[c.ID] = c
	}
	if len(got) != 2 {
		t.Fatalf("archived chats must still be served (flagged), got %d", len(got))
	}
	if !byID["2@s.whatsapp.net"].Archived || byID["1@s.whatsapp.net"].Archived {
		t.Fatalf("archived flags wrong: %+v", got)
	}
	if byID["2@s.whatsapp.net"].Pinned {
		t.Fatalf("archived chat must not be pinned: %+v", byID["2@s.whatsapp.net"])
	}
	if pins := app.pinnedChats(); pins["2@s.whatsapp.net"] {
		t.Fatalf("archiving should have removed the stored pin: %v", pins)
	}

	app.applyArchive(jid, false, false)
	for _, c := range servedChats(t, app) {
		if c.Archived {
			t.Fatalf("chat still archived after unarchive: %+v", c)
		}
	}
}

func TestApplyArchiveCollectsFullSyncArchivesOnlyWhileResyncing(t *testing.T) {
	app := newPinTestApp(t)
	app.applyArchive(types.NewJID("5", types.DefaultUserServer), true, true)
	if app.archiveSeen != nil {
		t.Fatalf("archiveSeen = %v, want nil outside a resync", app.archiveSeen)
	}
	app.archiveSeen = map[string]bool{}
	app.applyArchive(types.NewJID("6", types.DefaultUserServer), true, true)
	app.applyArchive(types.NewJID("7", types.DefaultUserServer), true, false)
	app.applyArchive(types.NewJID("8", types.DefaultUserServer), false, true)
	if len(app.archiveSeen) != 1 || !app.archiveSeen["6@s.whatsapp.net"] {
		t.Fatalf("archiveSeen = %v, want only 6@s.whatsapp.net", app.archiveSeen)
	}
}

func TestFullSyncArchiveKeepsExistingPinOfSameChat(t *testing.T) {
	// A full sync replays old actions; an old archive must not wipe a pin the
	// phone still has (the phone sends the unpin itself when it archives).
	app := newPinTestApp(t)
	jid := types.NewJID("9", types.DefaultUserServer)
	app.applyPin(jid, true, false)
	app.applyArchive(jid, true, true)
	if !app.pinnedChats()["9@s.whatsapp.net"] {
		t.Fatal("full-sync archive replay must not unpin")
	}
}

func TestHandleArchiveChatNeedsConnection(t *testing.T) {
	app := newPinTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/chats/archive", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 without a connected client", rec.Code)
	}
}
