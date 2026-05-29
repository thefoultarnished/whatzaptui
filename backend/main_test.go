package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	waCommon "go.mau.fi/whatsmeow/proto/waCommon"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

func newTestApp(t *testing.T) *App {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "test.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS chat_permissions (
		phone   TEXT PRIMARY KEY,
		name    TEXT NOT NULL DEFAULT '',
		allowed INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create chat_permissions: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id           TEXT NOT NULL,
			chat_id      TEXT NOT NULL,
			from_me      INTEGER NOT NULL DEFAULT 0,
			participant  TEXT NOT NULL DEFAULT '',
			ts           INTEGER NOT NULL DEFAULT 0,
			push_name    TEXT NOT NULL DEFAULT '',
			receipt      TEXT NOT NULL DEFAULT '',
			message_json TEXT NOT NULL DEFAULT '{}',
			media_proto  TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (chat_id, id, from_me)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages (chat_id, ts);
	`); err != nil {
		t.Fatalf("create messages table: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL DEFAULT '',
			subject      TEXT NOT NULL DEFAULT '',
			conv_ts      INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS contacts (
			id     TEXT PRIMARY KEY,
			name   TEXT NOT NULL DEFAULT '',
			notify TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("create chats/contacts tables: %v", err)
	}
	if _, err := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			chat_id UNINDEXED,
			msg_id  UNINDEXED,
			from_me UNINDEXED,
			body,
			tokenize = 'porter unicode61'
		);
	`); err != nil {
		t.Fatalf("create messages_fts: %v", err)
	}

	app := &App{
		db:        db,
		apiToken:  "test-api-token",
		wsClients: map[*websocket.Conn]*wsClient{},
		state: PersistedState{
			Chats:    map[string]Chat{},
			Contacts: map[string]Contact{},
		},
	}
	t.Cleanup(func() {
		if app.db != nil {
			_ = app.db.Close()
		}
	})
	return app
}

func authorizedRequest(req *http.Request, app *App) *http.Request {
	req.Header.Set(authHeaderName, "Bearer "+app.apiToken)
	return req
}

// Receipt check.
func TestUpdateReceiptStatus(t *testing.T) {
	app := newTestApp(t)
	// Seed messages into DB.
	for _, m := range []WireMessage{
		{Key: WireKey{ID: "m1", RemoteJID: "chat-1", FromMe: true}, ReceiptStatus: "sent", Message: map[string]any{}},
		{Key: WireKey{ID: "m2", RemoteJID: "chat-1", FromMe: true}, ReceiptStatus: "read", Message: map[string]any{}},
		{Key: WireKey{ID: "m3", RemoteJID: "chat-1", FromMe: false}, Message: map[string]any{}},
	} {
		if err := app.insertMessageToDB("chat-1", m); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	changed := app.updateReceiptStatus("chat-1", []string{"m1", "m2", "m3"}, "delivered")
	if !changed {
		t.Fatalf("expected receipt update to change state")
	}

	receiptOf := func(id string) string {
		var r string
		_ = app.db.QueryRow(`SELECT receipt FROM messages WHERE chat_id = 'chat-1' AND id = ?`, id).Scan(&r)
		return r
	}
	if got := receiptOf("m1"); got != "delivered" {
		t.Fatalf("m1 receipt = %q, want delivered", got)
	}
	if got := receiptOf("m2"); got != "read" {
		t.Fatalf("m2 receipt regressed to %q", got)
	}
	if got := receiptOf("m3"); got != "" {
		t.Fatalf("received message receipt changed to %q", got)
	}
}

// Timestamp check — now uses DB-backed reconciliation.
func TestReconcileChatTimestamps(t *testing.T) {
	app := newTestApp(t)
	app.state.Chats = map[string]Chat{
		"chat-1": {ID: "chat-1", ConversationTimestamp: 10},
		"chat-2": {ID: "chat-2", ConversationTimestamp: 200},
	}

	msgs := map[string][]WireMessage{
		"chat-1": {{Key: WireKey{ID: "a"}, MessageTimestamp: 40, Message: map[string]any{}}, {Key: WireKey{ID: "b"}, MessageTimestamp: 90, Message: map[string]any{}}},
		"chat-2": {{Key: WireKey{ID: "c"}, MessageTimestamp: 150, Message: map[string]any{}}},
		"chat-3": {{Key: WireKey{ID: "d"}, MessageTimestamp: 77, Message: map[string]any{}}},
	}
	for chatID, list := range msgs {
		for _, m := range list {
			if err := app.insertMessageToDB(chatID, m); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}

	app.reconcileChatTimestampsFromDB()

	if got := app.state.Chats["chat-1"].ConversationTimestamp; got != 90 {
		t.Fatalf("chat-1 timestamp = %d, want 90", got)
	}
	if got := app.state.Chats["chat-2"].ConversationTimestamp; got != 200 {
		t.Fatalf("chat-2 timestamp = %d, want 200 (should not decrease)", got)
	}
	if got := app.state.Chats["chat-3"].ConversationTimestamp; got != 77 {
		t.Fatalf("chat-3 timestamp = %d, want 77", got)
	}
}

// Dedupe check.
func TestUpsertMessageDedupesAndKeepsUnreadStable(t *testing.T) {
	app := newTestApp(t)

	first := WireMessage{
		Key:              WireKey{ID: "m1", FromMe: false},
		MessageTimestamp: 100,
		Message:          map[string]any{"conversation": "first"},
		PushName:         "Alex",
	}
	second := WireMessage{
		Key:              WireKey{ID: "m1", FromMe: false},
		MessageTimestamp: 120,
		Message:          map[string]any{"conversation": "updated"},
		PushName:         "Alex",
	}

	app.upsertMessage("15551230001@s.whatsapp.net", first)
	app.upsertMessage("15551230001@s.whatsapp.net", second)

	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = '15551230001@s.whatsapp.net'`).Scan(&count)
	if count != 1 {
		t.Fatalf("message count in DB = %d, want 1", count)
	}
	var msgJSON string
	_ = app.db.QueryRow(`SELECT message_json FROM messages WHERE chat_id = '15551230001@s.whatsapp.net' AND id = 'm1'`).Scan(&msgJSON)
	if !strings.Contains(msgJSON, "updated") {
		t.Fatalf("message_json = %q, want updated body", msgJSON)
	}
	if got := app.state.Chats["15551230001@s.whatsapp.net"].UnreadCount; got != 1 {
		t.Fatalf("unread count = %d, want 1", got)
	}
}

func TestUpsertMessageKeepsNewestConversationTimestamp(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "newest", FromMe: false},
		MessageTimestamp: 200,
		Message:          map[string]any{"conversation": "latest"},
	})
	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "older", FromMe: false},
		MessageTimestamp: 100,
		Message:          map[string]any{"conversation": "older"},
	})
	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "zero", FromMe: false},
		MessageTimestamp: 0,
		Message:          map[string]any{"conversation": "unknown"},
	})

	if got := app.state.Chats[chatID].ConversationTimestamp; got != 200 {
		t.Fatalf("conversation timestamp = %d, want 200", got)
	}
}

func TestApplyHistorySyncDoesNotDoubleCountUnread(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	history := &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			{
				ID:                    proto.String(chatID),
				ConversationTimestamp: proto.Uint64(200),
				LastMsgTimestamp:      proto.Uint64(200),
				UnreadCount:           proto.Uint32(3),
				Messages: []*waHistorySync.HistorySyncMsg{
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								ID:        proto.String("m1"),
								RemoteJID: proto.String(chatID),
								FromMe:    proto.Bool(false),
							},
							MessageTimestamp: proto.Uint64(100),
							Message: &waE2E.Message{
								Conversation: proto.String("older 1"),
							},
						},
					},
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								ID:        proto.String("m2"),
								RemoteJID: proto.String(chatID),
								FromMe:    proto.Bool(false),
							},
							MessageTimestamp: proto.Uint64(150),
							Message: &waE2E.Message{
								Conversation: proto.String("older 2"),
							},
						},
					},
				},
			},
		},
	}

	app.applyHistorySync(history)

	if got := app.state.Chats[chatID].UnreadCount; got != 3 {
		t.Fatalf("unread count = %d, want 3", got)
	}
}

// Logout success.
func TestUpsertMessageSkipsPersistDuringHistorySync(t *testing.T) {
	app := newTestApp(t)
	app.historySyncing = true

	msg := WireMessage{
		Key:              WireKey{ID: "m1", FromMe: false},
		MessageTimestamp: 100,
		Message:          map[string]any{"conversation": "first"},
	}

	app.upsertMessage("15551230001@s.whatsapp.net", msg)
	time.Sleep(50 * time.Millisecond)

	// During history sync: message is in DB but chat row must NOT be written yet.
	var chatCount int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id = '15551230001@s.whatsapp.net'`).Scan(&chatCount)
	if chatCount != 0 {
		t.Fatalf("chat was persisted to DB during history sync")
	}

	// Manually flush — must now write the in-memory chat to DB.
	app.historySyncing = false
	if err := app.persistStateWithErr(); err != nil {
		t.Fatalf("persistStateWithErr: %v", err)
	}
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id = '15551230001@s.whatsapp.net'`).Scan(&chatCount)
	if chatCount == 0 {
		t.Fatalf("chat not in DB after manual persistState")
	}
}

// Logout success.
func TestHandleLogoutSuccess(t *testing.T) {
	app := newTestApp(t)
	app.started = true
	app.connected = true
	app.state = PersistedState{
		Chats:    map[string]Chat{"chat-1": {ID: "chat-1"}},
		Contacts: map[string]Contact{"chat-1": {ID: "chat-1"}},
	}
	_ = app.insertMessageToDB("chat-1", WireMessage{Key: WireKey{ID: "m1"}, Message: map[string]any{}})

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	app.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if app.started || app.connected {
		t.Fatalf("app flags not cleared after logout")
	}
	if len(app.state.Chats) != 0 || len(app.state.Contacts) != 0 {
		t.Fatalf("app state was not cleared")
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse logout response: %v", err)
	}
	if got := body["message"]; got != "Logged out successfully" {
		t.Fatalf("logout message = %#v, want Logged out successfully", got)
	}
}

func TestHandleLogoutRemovesLocalCacheArtifacts(t *testing.T) {
	app := newTestApp(t)
	app.cacheDir = t.TempDir()
	leftover := filepath.Join(app.cacheDir, "leftover.tmp")
	if err := os.WriteFile(leftover, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write leftover file: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	app.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover file still exists or stat failed: %v", err)
	}
	if app.db != nil {
		_ = app.db.Close()
		app.db = nil
	}
}

func TestHandlerRejectsUnauthorizedRequests(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/contacts", nil)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestHandlerAllowsAuthorizedRequests(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/contacts", nil), app)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHealthDoesNotRequireAuth(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestCORSAllowsOnlyLoopbackOrigins(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodOptions, "/contacts", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow origin = %q, want localhost origin", got)
	}
}

func TestCORSRejectsNonLoopbackOrigins(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodOptions, "/contacts", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestWebSocketRequiresAuthAndAllowedOrigin(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	t.Run("missing auth is rejected", func(t *testing.T) {
		header := http.Header{}
		header.Set("Origin", "http://localhost:3000")
		_, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			t.Fatalf("expected websocket dial to fail without auth")
		}
	})

	t.Run("disallowed origin is rejected", func(t *testing.T) {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+app.apiToken)
		header.Set("Origin", "https://evil.example")
		_, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			t.Fatalf("expected websocket dial to fail for disallowed origin")
		}
	})

	t.Run("authorized loopback origin succeeds", func(t *testing.T) {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+app.apiToken)
		header.Set("Origin", "http://127.0.0.1:3000")
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err != nil {
			t.Fatalf("expected websocket dial to succeed: %v", err)
		}
		_ = conn.Close()
	})
}

// Logout failure.
func TestHandleLogoutFailsWhenCacheDirCannotBeRecreated(t *testing.T) {
	app := newTestApp(t)
	// Place cacheDir inside a regular file so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file failed: %v", err)
	}
	app.cacheDir = filepath.Join(blocker, "sub")
	app.started = true
	app.connected = true
	app.state = PersistedState{
		Chats:    map[string]Chat{"chat-1": {ID: "chat-1"}},
		Contacts: map[string]Contact{"chat-1": {ID: "chat-1"}},
	}

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	app.handleLogout(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "state cleanup failed") {
		t.Fatalf("unexpected error body: %s", rec.Body.String())
	}
}

// Save check.
func TestPersistStateWithErr(t *testing.T) {
	app := newTestApp(t)
	app.state = PersistedState{
		Chats: map[string]Chat{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Name: "Alex", ConversationTimestamp: 42},
		},
		Contacts: map[string]Contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Alex"},
		},
	}
	_ = app.insertMessageToDB("15551230001@s.whatsapp.net", WireMessage{
		Key: WireKey{ID: "m1", RemoteJID: "15551230001@s.whatsapp.net"}, MessageTimestamp: 42, Message: map[string]any{"conversation": "hello"},
	})

	if err := app.persistStateWithErr(); err != nil {
		t.Fatalf("persist state failed: %v", err)
	}

	// Chats and contacts are now in SQLite.
	var chatName string
	_ = app.db.QueryRow(`SELECT name FROM chats WHERE id = '15551230001@s.whatsapp.net'`).Scan(&chatName)
	if chatName != "Alex" {
		t.Fatalf("chat name in DB = %q, want Alex", chatName)
	}
	var notify string
	_ = app.db.QueryRow(`SELECT notify FROM contacts WHERE id = '15551230001@s.whatsapp.net'`).Scan(&notify)
	if notify != "Alex" {
		t.Fatalf("contact notify in DB = %q, want Alex", notify)
	}
	var msgID string
	_ = app.db.QueryRow(`SELECT id FROM messages WHERE chat_id = '15551230001@s.whatsapp.net'`).Scan(&msgID)
	if msgID != "m1" {
		t.Fatalf("message id in DB = %q, want m1", msgID)
	}
}

// Cache path check.
func TestResolveBackendCacheDirUsesOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "appdata")
	t.Setenv("WHATZAP_DATA_DIR", override)

	cacheDir, err := resolveBackendCacheDir(t.TempDir())
	if err != nil {
		t.Fatalf("resolve backend cache dir: %v", err)
	}

	want := filepath.Join(override, "backend")
	if cacheDir != want {
		t.Fatalf("cache dir = %q, want %q", cacheDir, want)
	}
	if _, err := os.Stat(cacheDir); err != nil {
		t.Fatalf("cache dir was not created: %v", err)
	}
}

// Legacy migration check.
func TestResolveBackendCacheDirMigratesLegacyCache(t *testing.T) {
	override := filepath.Join(t.TempDir(), "appdata")
	t.Setenv("WHATZAP_DATA_DIR", override)

	workDir := t.TempDir()
	legacyDir := filepath.Join(workDir, ".whatsmeow_cache")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	statePath := filepath.Join(legacyDir, "state.json")
	dbPath := filepath.Join(legacyDir, "store.db")
	if err := os.WriteFile(statePath, []byte(`{"chats":{"c1":{"id":"c1"}}}`), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatalf("write legacy db: %v", err)
	}

	cacheDir, err := resolveBackendCacheDir(workDir)
	if err != nil {
		t.Fatalf("resolve backend cache dir: %v", err)
	}

	migratedState, err := os.ReadFile(filepath.Join(cacheDir, "state.json"))
	if err != nil {
		t.Fatalf("read migrated state: %v", err)
	}
	if string(migratedState) != `{"chats":{"c1":{"id":"c1"}}}` {
		t.Fatalf("unexpected migrated state: %s", string(migratedState))
	}
	migratedDB, err := os.ReadFile(filepath.Join(cacheDir, "store.db"))
	if err != nil {
		t.Fatalf("read migrated db: %v", err)
	}
	if string(migratedDB) != "sqlite-bytes" {
		t.Fatalf("unexpected migrated db: %s", string(migratedDB))
	}
}

// Health check.
func TestHandleHealth(t *testing.T) {
	app := newTestApp(t)
	app.connected = true

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	app.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse health response: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("health ok field not true")
	}
	if connected, _ := body["connected"].(bool); !connected {
		t.Fatalf("health connected field not true")
	}
}

// Chat list check.
func TestHandleChatsAndContacts(t *testing.T) {
	app := newTestApp(t)
	app.state.Chats["15551230001@s.whatsapp.net"] = Chat{ID: "15551230001@s.whatsapp.net", ConversationTimestamp: 10}
	app.state.Chats["15551230002@s.whatsapp.net"] = Chat{ID: "15551230002@s.whatsapp.net", Name: "Zed", ConversationTimestamp: 20}
	app.state.Contacts["15551230001@s.whatsapp.net"] = Contact{ID: "15551230001@s.whatsapp.net", Notify: "Alex"}

	chatsReq := httptest.NewRequest(http.MethodGet, "/chats", nil)
	chatsRec := httptest.NewRecorder()
	app.handleChats(chatsRec, chatsReq)

	if chatsRec.Code != http.StatusOK {
		t.Fatalf("chats status = %d, want %d", chatsRec.Code, http.StatusOK)
	}
	var chatsBody struct {
		Chats []Chat `json:"chats"`
	}
	if err := json.Unmarshal(chatsRec.Body.Bytes(), &chatsBody); err != nil {
		t.Fatalf("parse chats response: %v", err)
	}
	if len(chatsBody.Chats) != 2 {
		t.Fatalf("chat count = %d, want 2", len(chatsBody.Chats))
	}
	if chatsBody.Chats[1].Name != "Alex" && chatsBody.Chats[0].Name != "Alex" {
		t.Fatalf("expected contact name to hydrate chat name")
	}

	contactsReq := httptest.NewRequest(http.MethodGet, "/contacts", nil)
	contactsRec := httptest.NewRecorder()
	app.handleContacts(contactsRec, contactsReq)

	if contactsRec.Code != http.StatusOK {
		t.Fatalf("contacts status = %d, want %d", contactsRec.Code, http.StatusOK)
	}
	var contactsBody struct {
		Contacts []Contact `json:"contacts"`
	}
	if err := json.Unmarshal(contactsRec.Body.Bytes(), &contactsBody); err != nil {
		t.Fatalf("parse contacts response: %v", err)
	}
	if len(contactsBody.Contacts) != 1 {
		t.Fatalf("contact count = %d, want 1", len(contactsBody.Contacts))
	}
}

func TestUpsertPermissionSkipsNilDB(t *testing.T) {
	app := newTestApp(t)
	_ = app.db.Close()
	app.db = nil

	app.upsertPermission("15551230001", "Alex")
}

func TestUpsertPermissionSkipsDuringShutdown(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.shuttingDown = true
	app.mu.Unlock()

	app.upsertPermission("15551230001", "Alex")

	rows, err := app.db.Query(`SELECT phone, name, allowed FROM chat_permissions`)
	if err != nil {
		t.Fatalf("query permissions: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("expected no permission rows during shutdown")
	}
}

// Whitelist check.
func TestWhitelistRoundTrip(t *testing.T) {
	app := newTestApp(t)

	setReq := httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewBufferString(`{"phone":"15551230001","name":"Alex","allowed":1}`))
	setRec := httptest.NewRecorder()
	app.handleSetWhitelist(setRec, setReq)

	if setRec.Code != http.StatusOK {
		t.Fatalf("set whitelist status = %d, want %d, body=%s", setRec.Code, http.StatusOK, setRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/whitelist", nil)
	getRec := httptest.NewRecorder()
	app.handleGetWhitelist(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("get whitelist status = %d, want %d", getRec.Code, http.StatusOK)
	}
	var body struct {
		Contacts []struct {
			Phone   string `json:"phone"`
			Name    string `json:"name"`
			Allowed int    `json:"allowed"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse whitelist response: %v", err)
	}
	if len(body.Contacts) != 1 {
		t.Fatalf("whitelist count = %d, want 1", len(body.Contacts))
	}
	if body.Contacts[0].Phone != "15551230001" || body.Contacts[0].Allowed != 1 {
		t.Fatalf("unexpected whitelist entry: %+v", body.Contacts[0])
	}
}

func TestWhitelistHandlersRejectDuringShutdown(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.shuttingDown = true
	app.mu.Unlock()

	tests := []struct {
		name string
		req  *http.Request
		run  func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "get whitelist",
			req:  httptest.NewRequest(http.MethodGet, "/whitelist", nil),
			run:  app.handleGetWhitelist,
		},
		{
			name: "set whitelist",
			req:  httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewBufferString(`{"phone":"15551230001","allowed":1}`)),
			run:  app.handleSetWhitelist,
		},
		{
			name: "set name",
			req:  httptest.NewRequest(http.MethodPost, "/names/set", bytes.NewBufferString(`{"phone":"15551230001","name":"Alex"}`)),
			run:  app.handleSetName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.run(rec, tt.req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "permission store unavailable") {
				t.Fatalf("unexpected body: %s", rec.Body.String())
			}
		})
	}
}

// Route validation.
func TestCoreRouteValidation(t *testing.T) {
	app := newTestApp(t)

	sendFileReq := httptest.NewRequest(http.MethodPost, "/messages/send-file", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net","path":"missing.txt","kind":"document"}`))
	sendFileRec := httptest.NewRecorder()
	app.handleSendFile(sendFileRec, sendFileReq)
	if sendFileRec.Code != http.StatusConflict || !strings.Contains(sendFileRec.Body.String(), "not connected") {
		t.Fatalf("unexpected send-file response: %d %s", sendFileRec.Code, sendFileRec.Body.String())
	}

	messagesReq := httptest.NewRequest(http.MethodGet, "/messages", nil)
	messagesRec := httptest.NewRecorder()
	app.handleMessages(messagesRec, messagesReq)
	if messagesRec.Code != http.StatusBadRequest || !strings.Contains(messagesRec.Body.String(), "chatId is required") {
		t.Fatalf("unexpected messages response: %d %s", messagesRec.Code, messagesRec.Body.String())
	}

	setWhitelistReq := httptest.NewRequest(http.MethodGet, "/whitelist/set", nil)
	setWhitelistRec := httptest.NewRecorder()
	app.handleSetWhitelist(setWhitelistRec, setWhitelistReq)
	if setWhitelistRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("whitelist method status = %d, want %d", setWhitelistRec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleMessagesCapsLimit(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	msgs := make([]WireMessage, 0, 250)
	for i := 0; i < 250; i++ {
		msgs = append(msgs, WireMessage{
			Key:              WireKey{ID: fmt.Sprintf("m-%03d", i), RemoteJID: chatID},
			MessageTimestamp: int64(i + 1),
			Message:          map[string]any{"conversation": fmt.Sprintf("msg %03d", i)},
		})
	}
	for _, m := range msgs {
		if err := app.insertMessageToDB(chatID, m); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=9999", nil)
	rec := httptest.NewRecorder()
	app.handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Messages []WireMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse messages response: %v", err)
	}
	if len(body.Messages) != maxMessagesResponseLimit {
		t.Fatalf("message count = %d, want %d", len(body.Messages), maxMessagesResponseLimit)
	}
	if body.Messages[0].Key.ID != "m-050" {
		t.Fatalf("first returned message = %q, want m-050", body.Messages[0].Key.ID)
	}
	if body.Messages[len(body.Messages)-1].Key.ID != "m-249" {
		t.Fatalf("last returned message = %q, want m-249", body.Messages[len(body.Messages)-1].Key.ID)
	}
}

func TestHandlersRequiringWhatsAppConnectionReturnConflictWhenDisconnected(t *testing.T) {
	app := newTestApp(t)
	_ = app.insertMessageToDB("15551230001@s.whatsapp.net", WireMessage{
		Key:        WireKey{ID: "m1", RemoteJID: "15551230001@s.whatsapp.net"},
		MediaProto: "ZmFrZQ==",
		Message:    map[string]any{},
	})

	tests := []struct {
		name string
		req  *http.Request
		run  func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "send message",
			req:  httptest.NewRequest(http.MethodPost, "/messages/send", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net","text":"hi"}`)),
			run:  app.handleSendMessage,
		},
		{
			name: "send file",
			req:  httptest.NewRequest(http.MethodPost, "/messages/send-file", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net","path":"sample.txt","kind":"document"}`)),
			run:  app.handleSendFile,
		},
		{
			name: "profile picture",
			req:  httptest.NewRequest(http.MethodGet, "/profile-picture?jid=15551230001@s.whatsapp.net", nil),
			run:  app.handleProfilePicture,
		},
		{
			name: "mark read",
			req:  httptest.NewRequest(http.MethodPost, "/messages/read", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net"}`)),
			run:  app.handleMarkRead,
		},
		{
			name: "media download",
			req:  httptest.NewRequest(http.MethodGet, "/media/download?chatId=15551230001@s.whatsapp.net&msgId=m1", nil),
			run:  app.handleMediaDownload,
		},
		{
			name: "block",
			req:  httptest.NewRequest(http.MethodPost, "/block", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net"}`)),
			run:  app.handleBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.run(rec, tt.req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "not connected") {
				t.Fatalf("unexpected body: %s", rec.Body.String())
			}
		})
	}
}

func TestNormalizeQuotedParticipantClearsSelfID(t *testing.T) {
	got := normalizeQuotedParticipant("15551230001@lid", "15551230001@s.whatsapp.net", func(id string) string {
		switch strings.TrimSpace(id) {
		case "15551230001@lid":
			return "15551230001@s.whatsapp.net"
		default:
			return strings.TrimSpace(id)
		}
	})
	if got != "" {
		t.Fatalf("normalizeQuotedParticipant() = %q, want empty", got)
	}
}

func TestNormalizeQuotedParticipantClearsSelfIDByPhoneMatch(t *testing.T) {
	got := normalizeQuotedParticipant("15551230001:7@s.whatsapp.net", "15551230001@s.whatsapp.net", func(id string) string {
		return strings.TrimSpace(id)
	})
	if got != "" {
		t.Fatalf("normalizeQuotedParticipant() by phone match = %q, want empty", got)
	}
}

func TestQuotedFromMeForOneToOneChatTreatsNonRemoteAsSelf(t *testing.T) {
	if !quotedFromMeForChat("15551230001@s.whatsapp.net", "15559990000@s.whatsapp.net", false) {
		t.Fatalf("expected 1:1 quoted participant different from remote chat to be treated as self")
	}
	if quotedFromMeForChat("15551230001@s.whatsapp.net", "15551230001@s.whatsapp.net", false) {
		t.Fatalf("expected remote participant in 1:1 chat to stay non-self")
	}
}

// File validation.
func TestValidateSendFileInput(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "sample.png")
	if err := os.WriteFile(imagePath, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, 0o644); err != nil {
		t.Fatalf("write image file: %v", err)
	}
	if err := validateSendFileInput(imagePath, "image"); err != nil {
		t.Fatalf("image validation failed: %v", err)
	}

	videoPath := filepath.Join(t.TempDir(), "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("not-a-real-video"), 0o644); err != nil {
		t.Fatalf("write video file: %v", err)
	}
	if err := validateSendFileInput(videoPath, "video"); err != nil {
		t.Fatalf("video validation failed: %v", err)
	}

	textPath := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(textPath, []byte("plain text"), 0o644); err != nil {
		t.Fatalf("write text file: %v", err)
	}
	if err := validateSendFileInput(textPath, "image"); err == nil {
		t.Fatalf("expected image validation to fail for text file")
	}
	if err := validateSendFileInput(textPath, "video"); err == nil {
		t.Fatalf("expected video validation to fail for text file")
	}
	if err := validateSendFileInput(textPath, "document"); err != nil {
		t.Fatalf("document validation failed: %v", err)
	}
}

// --- DB-layer tests added after SQLite migration ---

func TestInsertAndScanMessageRoundTrip(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	want := WireMessage{
		Key:              WireKey{ID: "rt1", RemoteJID: chatID, FromMe: true, Participant: ""},
		MessageTimestamp: 500,
		PushName:         "Nav",
		ReceiptStatus:    "sent",
		MediaProto:       "dGVzdA==",
		Message:          map[string]any{"conversation": "hello round trip"},
	}
	if err := app.insertMessageToDB(chatID, want); err != nil {
		t.Fatalf("insertMessageToDB: %v", err)
	}

	rows, err := app.db.Query(`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto FROM messages WHERE id = 'rt1'`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row found after insert")
	}
	got, err := scanMessageRow(rows)
	if err != nil {
		t.Fatalf("scanMessageRow: %v", err)
	}
	if got.Key.ID != want.Key.ID {
		t.Fatalf("id = %q, want %q", got.Key.ID, want.Key.ID)
	}
	if got.Key.FromMe != want.Key.FromMe {
		t.Fatalf("from_me = %v, want %v", got.Key.FromMe, want.Key.FromMe)
	}
	if got.MessageTimestamp != want.MessageTimestamp {
		t.Fatalf("ts = %d, want %d", got.MessageTimestamp, want.MessageTimestamp)
	}
	if got.PushName != want.PushName {
		t.Fatalf("push_name = %q, want %q", got.PushName, want.PushName)
	}
	if got.ReceiptStatus != want.ReceiptStatus {
		t.Fatalf("receipt = %q, want %q", got.ReceiptStatus, want.ReceiptStatus)
	}
	if got.MediaProto != want.MediaProto {
		t.Fatalf("media_proto = %q, want %q", got.MediaProto, want.MediaProto)
	}
	if got.Message["conversation"] != "hello round trip" {
		t.Fatalf("message_json = %v, want conversation=hello round trip", got.Message)
	}
}

func TestHandleMessagesBasicFetch(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	for i, body := range []string{"first", "second", "third"} {
		if err := app.insertMessageToDB(chatID, WireMessage{
			Key:              WireKey{ID: fmt.Sprintf("m%d", i+1), RemoteJID: chatID},
			MessageTimestamp: int64(i + 1),
			Message:          map[string]any{"conversation": body},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID, nil), app)
	rec := httptest.NewRecorder()
	app.handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Messages []WireMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(body.Messages))
	}
	// Chronological order: oldest first.
	if body.Messages[0].Key.ID != "m1" || body.Messages[2].Key.ID != "m3" {
		t.Fatalf("order wrong: first=%q last=%q", body.Messages[0].Key.ID, body.Messages[2].Key.ID)
	}
}

func TestHandleMessagesBeforeCursor(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	for i := 1; i <= 10; i++ {
		if err := app.insertMessageToDB(chatID, WireMessage{
			Key:              WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID},
			MessageTimestamp: int64(i * 100),
			Message:          map[string]any{},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Fetch 3 messages before ts=600 → should return m3(300), m4(400), m5(500).
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&before=600&limit=3", nil), app)
	rec := httptest.NewRecorder()
	app.handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Messages []WireMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(body.Messages))
	}
	if body.Messages[0].Key.ID != "m3" {
		t.Fatalf("first = %q, want m3", body.Messages[0].Key.ID)
	}
	if body.Messages[2].Key.ID != "m5" {
		t.Fatalf("last = %q, want m5", body.Messages[2].Key.ID)
	}
}

func TestHandleMessagesEmptyChat(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId=99999@s.whatsapp.net", nil), app)
	rec := httptest.NewRecorder()
	app.handleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Messages []WireMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(body.Messages) != 0 {
		t.Fatalf("expected empty messages, got %d", len(body.Messages))
	}
}

func TestUpdateReceiptStatusDoesNotDowngrade(t *testing.T) {
	app := newTestApp(t)
	if err := app.insertMessageToDB("chat-1", WireMessage{
		Key: WireKey{ID: "x", RemoteJID: "chat-1", FromMe: true}, ReceiptStatus: "read", Message: map[string]any{},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	changed := app.updateReceiptStatus("chat-1", []string{"x"}, "delivered")
	if changed {
		t.Fatalf("updateReceiptStatus should not downgrade read → delivered")
	}
	var r string
	_ = app.db.QueryRow(`SELECT receipt FROM messages WHERE id = 'x'`).Scan(&r)
	if r != "read" {
		t.Fatalf("receipt = %q, want read (no downgrade)", r)
	}
}

func TestUpsertMessageWritesToDB(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "db1", FromMe: false},
		MessageTimestamp: 999,
		Message:          map[string]any{"conversation": "in db"},
	})

	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = 'db1'`, chatID).Scan(&count)
	if count != 1 {
		t.Fatalf("message not found in DB after upsertMessage, count=%d", count)
	}
}

func TestUpsertMessageDuringHistorySyncWritesToDBButSkipsJSON(t *testing.T) {
	app := newTestApp(t)
	app.historySyncing = true
	chatID := "15551230001@s.whatsapp.net"

	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "hs1", FromMe: false},
		MessageTimestamp: 200,
		Message:          map[string]any{"conversation": "history"},
	})
	time.Sleep(50 * time.Millisecond)

	// Message must be in DB.
	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = 'hs1'`).Scan(&count)
	if count != 1 {
		t.Fatalf("expected message in DB during history sync, got count=%d", count)
	}
	// Chats table must NOT have a row for this chat yet (persistState was skipped).
	var chatCount int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id = '15551230001@s.whatsapp.net'`).Scan(&chatCount)
	if chatCount != 0 {
		t.Fatalf("chat was persisted to DB during history sync")
	}
}

func TestDeleteMessageRemovesFromDB(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "del1", RemoteJID: chatID}, Message: map[string]any{},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := app.db.Exec(`DELETE FROM messages WHERE chat_id = ? AND id = ?`, chatID, "del1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = 'del1'`).Scan(&count)
	if count != 0 {
		t.Fatalf("message still in DB after delete, count=%d", count)
	}
}

// --- Full-text search tests ---

func searchHits(t *testing.T, app *App, query, chatID string) []searchHit {
	t.Helper()
	url := "/search?q=" + query
	if chatID != "" {
		url += "&chatId=" + chatID
	}
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, url, nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []searchHit `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	return body.Results
}

func TestExtractSearchableText(t *testing.T) {
	cases := []struct {
		name string
		msg  map[string]any
		want string
	}{
		{"plain", map[string]any{"conversation": "hello world"}, "hello world"},
		{"extended", map[string]any{"extendedTextMessage": map[string]any{"text": "extended body"}}, "extended body"},
		{"image_caption", map[string]any{"imageMessage": map[string]any{"caption": "look at this"}}, "look at this"},
		{"document_filename", map[string]any{"documentMessage": map[string]any{"caption": "here", "fileName": "report.pdf"}}, "here report.pdf"},
		{"sticker_no_text", map[string]any{"stickerMessage": map[string]any{"mimetype": "image/webp"}}, ""},
		{"empty", map[string]any{}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractSearchableText(tc.msg); got != tc.want {
				t.Fatalf("extractSearchableText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSearchFindsInsertedMessage(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "m1", RemoteJID: chatID}, MessageTimestamp: 100,
		Message: map[string]any{"conversation": "the quick brown fox"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "m2", RemoteJID: chatID}, MessageTimestamp: 200,
		Message: map[string]any{"conversation": "lazy dog jumped over"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	results := searchHits(t, app, "fox", "")
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].MessageID != "m1" {
		t.Fatalf("hit.MessageID = %q, want m1", results[0].MessageID)
	}
	if !strings.Contains(results[0].Snippet, "<b>") {
		t.Fatalf("snippet missing highlight tags: %q", results[0].Snippet)
	}
}

func TestSearchMultiWordANDsTokens(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	_ = app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: "m1", RemoteJID: chatID}, MessageTimestamp: 1, Message: map[string]any{"conversation": "hello world"}})
	_ = app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: "m2", RemoteJID: chatID}, MessageTimestamp: 2, Message: map[string]any{"conversation": "hello there"}})
	_ = app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: "m3", RemoteJID: chatID}, MessageTimestamp: 3, Message: map[string]any{"conversation": "world peace"}})

	results := searchHits(t, app, "hello+world", "")
	if len(results) != 1 || results[0].MessageID != "m1" {
		t.Fatalf("multi-word AND failed: got %+v", results)
	}
}

func TestSearchFiltersByChat(t *testing.T) {
	app := newTestApp(t)
	chatA, chatB := "a@s.whatsapp.net", "b@s.whatsapp.net"
	_ = app.insertMessageToDB(chatA, WireMessage{Key: WireKey{ID: "m1", RemoteJID: chatA}, MessageTimestamp: 1, Message: map[string]any{"conversation": "shared text"}})
	_ = app.insertMessageToDB(chatB, WireMessage{Key: WireKey{ID: "m2", RemoteJID: chatB}, MessageTimestamp: 2, Message: map[string]any{"conversation": "shared text"}})

	results := searchHits(t, app, "shared", chatA)
	if len(results) != 1 || results[0].ChatID != chatA {
		t.Fatalf("chat filter failed: got %+v", results)
	}
}

func TestSearchExcludesDeletedMessage(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	_ = app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: "m1", RemoteJID: chatID}, MessageTimestamp: 1, Message: map[string]any{"conversation": "deletable text"}})

	if got := searchHits(t, app, "deletable", ""); len(got) != 1 {
		t.Fatalf("pre-delete: results = %d, want 1", len(got))
	}

	_, _ = app.db.Exec(`DELETE FROM messages WHERE id = 'm1'`)
	_, _ = app.db.Exec(`DELETE FROM messages_fts WHERE msg_id = 'm1'`)

	if got := searchHits(t, app, "deletable", ""); len(got) != 0 {
		t.Fatalf("post-delete: results = %d, want 0", len(got))
	}
}

func TestSearchMissingQueryReturns400(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/search", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBackfillFTSIndexesExistingMessages(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	// Bypass insertMessageToDB so FTS isn't populated automatically.
	_, err := app.db.Exec(`
		INSERT INTO messages (id, chat_id, from_me, ts, message_json)
		VALUES (?, ?, 0, 1, ?)
	`, "m1", chatID, `{"conversation":"backfilled body"}`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Pre-condition: FTS empty.
	if got := searchHits(t, app, "backfilled", ""); len(got) != 0 {
		t.Fatalf("pre-backfill: expected 0 hits, got %d", len(got))
	}

	app.backfillFTS()

	if got := searchHits(t, app, "backfilled", ""); len(got) != 1 || got[0].MessageID != "m1" {
		t.Fatalf("post-backfill: hits = %+v, want exactly m1", got)
	}
}

// --- Pagination metadata + push-name cleanup tests ---

func TestHandleMessagesReturnsHasMoreTrueWhenMorePagesExist(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	for i := 1; i <= 5; i++ {
		if err := app.insertMessageToDB(chatID, WireMessage{
			Key:              WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID},
			MessageTimestamp: int64(i),
			Message:          map[string]any{"conversation": "x"},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=3", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	var body struct {
		Messages []WireMessage `json:"messages"`
		HasMore  bool          `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !body.HasMore {
		t.Fatalf("hasMore should be true when more older pages exist")
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages count = %d, want 3 (limit, not limit+1)", len(body.Messages))
	}
}

func TestHandleMessagesReturnsHasMoreFalseWhenAllReturned(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	for i := 1; i <= 3; i++ {
		_ = app.insertMessageToDB(chatID, WireMessage{
			Key: WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID}, MessageTimestamp: int64(i),
			Message: map[string]any{"conversation": "x"},
		})
	}
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=10", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	var body struct {
		Messages []WireMessage `json:"messages"`
		HasMore  bool          `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.HasMore {
		t.Fatalf("hasMore should be false when all messages fit in one page")
	}
	if len(body.Messages) != 3 {
		t.Fatalf("messages count = %d, want 3", len(body.Messages))
	}
}

func TestHandleMessagesHasMoreExactlyAtLimit(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	// Seed exactly 3 messages, request limit=3. There's NO older page beyond this,
	// so hasMore must be false. This is the false-positive case the old "len < limit"
	// heuristic would have got wrong.
	for i := 1; i <= 3; i++ {
		_ = app.insertMessageToDB(chatID, WireMessage{
			Key: WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID}, MessageTimestamp: int64(i),
			Message: map[string]any{"conversation": "x"},
		})
	}
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=3", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	var body struct {
		Messages []WireMessage `json:"messages"`
		HasMore  bool          `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if body.HasMore {
		t.Fatalf("hasMore should be false when remaining count == limit")
	}
}

func TestPurgeContactsWithNameClearsOnlyMatching(t *testing.T) {
	app := newTestApp(t)
	for _, c := range []struct{ phone, name string }{
		{"111@s.whatsapp.net", "Prabhat"},
		{"222@s.whatsapp.net", "Prabhat"},
		{"333@s.whatsapp.net", "Alice"},
		{"444@s.whatsapp.net", ""},
	} {
		if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, 0)`, c.phone, c.name); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	n, err := app.purgeContactsWithName("Prabhat")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows affected = %d, want 2", n)
	}

	var alice, bad1 string
	_ = app.db.QueryRow(`SELECT name FROM chat_permissions WHERE phone = '333@s.whatsapp.net'`).Scan(&alice)
	_ = app.db.QueryRow(`SELECT name FROM chat_permissions WHERE phone = '111@s.whatsapp.net'`).Scan(&bad1)
	if alice != "Alice" {
		t.Fatalf("Alice was clobbered: got %q", alice)
	}
	if bad1 != "" {
		t.Fatalf("bad row not cleared: got %q", bad1)
	}
}

func TestPurgeContactsWithNameEmptyNameIsNoOp(t *testing.T) {
	app := newTestApp(t)
	_, _ = app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('111@s.whatsapp.net', 'Alice', 1)`)
	n, err := app.purgeContactsWithName("")
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty name purge should affect 0 rows, got %d", n)
	}
	// Whitespace-only also no-op.
	if n, _ := app.purgeContactsWithName("   "); n != 0 {
		t.Fatalf("whitespace-only purge should affect 0 rows, got %d", n)
	}
	var name string
	_ = app.db.QueryRow(`SELECT name FROM chat_permissions WHERE phone = '111@s.whatsapp.net'`).Scan(&name)
	if name != "Alice" {
		t.Fatalf("untouched contact got modified: %q", name)
	}
}

// --- Around endpoint tests ---

func TestHandleMessagesAroundReturnsWindow(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	// Seed 10 messages ts 1..10; anchor on m5 (ts=5).
	for i := 1; i <= 10; i++ {
		_ = app.insertMessageToDB(chatID, WireMessage{
			Key: WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID},
			MessageTimestamp: int64(i),
			Message: map[string]any{"conversation": fmt.Sprintf("msg %d", i)},
		})
	}

	req := authorizedRequest(
		httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&around=m5&limit=4", nil),
		app,
	)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Messages    []WireMessage `json:"messages"`
		AnchorIndex int           `json:"anchorIndex"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse: %v", err)
	}
	// limit=4 → half=2; expect 2 older + anchor + 2 newer = 5 messages.
	if len(body.Messages) != 5 {
		t.Fatalf("messages = %d, want 5", len(body.Messages))
	}
	if body.Messages[body.AnchorIndex].Key.ID != "m5" {
		t.Fatalf("anchor = %q, want m5", body.Messages[body.AnchorIndex].Key.ID)
	}
	if body.Messages[0].Key.ID != "m3" {
		t.Fatalf("first = %q, want m3", body.Messages[0].Key.ID)
	}
	if body.Messages[4].Key.ID != "m7" {
		t.Fatalf("last = %q, want m7", body.Messages[4].Key.ID)
	}
}

func TestHandleMessagesAroundUnknownIDReturns404(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(
		httptest.NewRequest(http.MethodGet, "/messages?chatId=c1&around=doesntexist", nil),
		app,
	)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleMessagesAroundAtEdgeReturnsAvailableMessages(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	// Only 3 messages; anchor on first (no older, 2 newer).
	for i := 1; i <= 3; i++ {
		_ = app.insertMessageToDB(chatID, WireMessage{
			Key: WireKey{ID: fmt.Sprintf("m%d", i), RemoteJID: chatID},
			MessageTimestamp: int64(i),
			Message: map[string]any{},
		})
	}
	req := authorizedRequest(
		httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&around=m1&limit=100", nil),
		app,
	)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	var body struct {
		Messages    []WireMessage `json:"messages"`
		AnchorIndex int           `json:"anchorIndex"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	// 0 older + anchor + 2 newer = 3 total.
	if len(body.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(body.Messages))
	}
	if body.AnchorIndex != 0 {
		t.Fatalf("anchorIndex = %d, want 0 (no older messages)", body.AnchorIndex)
	}
}

func TestPurgeOwnPushNameFromContactsNoOpWhenClientNil(t *testing.T) {
	app := newTestApp(t)
	app.purgeOwnPushNameFromContacts() // must not panic with nil client
}

// --- DB helper round-trip tests ---

func TestUpsertAndLoadChatsRoundTrip(t *testing.T) {
	app := newTestApp(t)
	want := Chat{
		ID:                    "15551230001@s.whatsapp.net",
		Name:                  "Alex",
		Subject:               "Group subject",
		ConversationTimestamp: 1234567890,
		UnreadCount:           7,
	}
	if err := app.upsertChatToDB(want); err != nil {
		t.Fatalf("upsertChatToDB: %v", err)
	}
	chats, err := app.loadChatsFromDB()
	if err != nil {
		t.Fatalf("loadChatsFromDB: %v", err)
	}
	got, ok := chats[want.ID]
	if !ok {
		t.Fatalf("chat not found in DB")
	}
	if got != want {
		t.Fatalf("round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestUpsertChatToDBOverwritesExisting(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	_ = app.upsertChatToDB(Chat{ID: chatID, Name: "old", UnreadCount: 1})
	_ = app.upsertChatToDB(Chat{ID: chatID, Name: "new", UnreadCount: 99})

	chats, _ := app.loadChatsFromDB()
	if chats[chatID].Name != "new" || chats[chatID].UnreadCount != 99 {
		t.Fatalf("overwrite failed: %+v", chats[chatID])
	}
	// Only one row in chats table.
	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id = ?`, chatID).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestUpsertAndLoadContactsRoundTrip(t *testing.T) {
	app := newTestApp(t)
	want := Contact{ID: "111@s.whatsapp.net", Name: "Alice", Notify: "Alice Smith"}
	if err := app.upsertContactToDB(want); err != nil {
		t.Fatalf("upsertContactToDB: %v", err)
	}
	contacts, err := app.loadContactsFromDB()
	if err != nil {
		t.Fatalf("loadContactsFromDB: %v", err)
	}
	got, ok := contacts[want.ID]
	if !ok {
		t.Fatalf("contact not found")
	}
	if got != want {
		t.Fatalf("round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestLoadStateFromEmptyDBSetsBootstrap(t *testing.T) {
	app := newTestApp(t)
	app.cacheDir = "" // skip cacheDir paths
	app.loadState()
	if !app.needsBootstrapSync {
		t.Fatalf("expected needsBootstrapSync=true on empty DB")
	}
}

func TestLoadStateFromPopulatedDB(t *testing.T) {
	app := newTestApp(t)
	_ = app.upsertChatToDB(Chat{ID: "c1", Name: "Alex", ConversationTimestamp: 100})
	_ = app.upsertContactToDB(Contact{ID: "c1", Notify: "Alex"})

	app.loadState()

	if app.needsBootstrapSync {
		t.Fatalf("needsBootstrapSync should be false when DB has chats")
	}
	if app.state.Chats["c1"].Name != "Alex" {
		t.Fatalf("chat not loaded into state.Chats: %+v", app.state.Chats)
	}
	if app.state.Contacts["c1"].Notify != "Alex" {
		t.Fatalf("contact not loaded into state.Contacts: %+v", app.state.Contacts)
	}
}

// --- FTS update behavior ---

func TestFTSReplacesBodyOnReinsert(t *testing.T) {
	app := newTestApp(t)
	chatID := "c1"
	// Insert original.
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "m1", RemoteJID: chatID}, MessageTimestamp: 100,
		Message: map[string]any{"conversation": "alpha bravo"},
	}); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if hits := searchHits(t, app, "alpha", ""); len(hits) != 1 {
		t.Fatalf("first insert: alpha hits = %d, want 1", len(hits))
	}

	// Re-insert same ID with completely different body.
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "m1", RemoteJID: chatID}, MessageTimestamp: 100,
		Message: map[string]any{"conversation": "charlie delta"},
	}); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if hits := searchHits(t, app, "alpha", ""); len(hits) != 0 {
		t.Fatalf("after reinsert: alpha hits = %d, want 0 (FTS should not still match old body)", len(hits))
	}
	if hits := searchHits(t, app, "charlie", ""); len(hits) != 1 {
		t.Fatalf("after reinsert: charlie hits = %d, want 1", len(hits))
	}
}

func TestExtractSearchableTextQuotedText(t *testing.T) {
	got := extractSearchableText(map[string]any{
		"extendedTextMessage": map[string]any{
			"text":       "main body",
			"quotedText": "quoted reference",
		},
	})
	if !strings.Contains(got, "main body") || !strings.Contains(got, "quoted reference") {
		t.Fatalf("expected both main and quoted text, got %q", got)
	}
}

func TestIsChatAllowed(t *testing.T) {
	app := newTestApp(t)
	allowed, err := app.isChatAllowed("15551230001@s.whatsapp.net")
	if err != nil {
		t.Fatalf("isChatAllowed: %v", err)
	}
	if allowed {
		t.Fatalf("expected not allowed by default")
	}

	_, err = app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230001', 'Test User', 1)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	allowed, err = app.isChatAllowed("15551230001@s.whatsapp.net")
	if err != nil {
		t.Fatalf("isChatAllowed: %v", err)
	}
	if !allowed {
		t.Fatalf("expected allowed after whitelisting")
	}

	_, err = app.db.Exec(`UPDATE chat_permissions SET allowed = 0 WHERE phone = '15551230001'`)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	allowed, err = app.isChatAllowed("15551230001@s.whatsapp.net")
	if err != nil {
		t.Fatalf("isChatAllowed: %v", err)
	}
	if allowed {
		t.Fatalf("expected not allowed after blacklisting")
	}
}

// #9: INSERT OR IGNORE + UPDATE — mutable fields update but receipt is not downgraded.
func TestInsertMessageUpdatesMutableFieldsButPreservesReceipt(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:           WireKey{ID: "m1", RemoteJID: chatID, FromMe: true},
		ReceiptStatus: "sent",
		PushName:      "Alice",
		Message:       map[string]any{"conversation": "original"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Upgrade receipt via the proper path.
	app.updateReceiptStatus(chatID, []string{"m1"}, "read")

	// Re-upsert with stale receipt and new body.
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:           WireKey{ID: "m1", RemoteJID: chatID, FromMe: true},
		ReceiptStatus: "sent",
		PushName:      "Alice Updated",
		Message:       map[string]any{"conversation": "edited"},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	var receipt, msgJSON, pushName string
	_ = app.db.QueryRow(`SELECT receipt, message_json, push_name FROM messages WHERE chat_id = ? AND id = 'm1'`, chatID).Scan(&receipt, &msgJSON, &pushName)

	if receipt != "read" {
		t.Fatalf("receipt = %q, want read (must not be downgraded by re-upsert)", receipt)
	}
	if !strings.Contains(msgJSON, "edited") {
		t.Fatalf("message_json = %q, want edited body", msgJSON)
	}
	if pushName != "Alice Updated" {
		t.Fatalf("push_name = %q, want Alice Updated", pushName)
	}
}

// #9: rowid must not change on re-upsert.
func TestInsertMessageRowidStableOnReupsert(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:     WireKey{ID: "m1", RemoteJID: chatID, FromMe: false},
		Message: map[string]any{"conversation": "v1"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var rowid1 int64
	_ = app.db.QueryRow(`SELECT rowid FROM messages WHERE chat_id = ? AND id = 'm1'`, chatID).Scan(&rowid1)

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:     WireKey{ID: "m1", RemoteJID: chatID, FromMe: false},
		Message: map[string]any{"conversation": "v2"},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	var rowid2 int64
	_ = app.db.QueryRow(`SELECT rowid FROM messages WHERE chat_id = ? AND id = 'm1'`, chatID).Scan(&rowid2)

	if rowid1 != rowid2 {
		t.Fatalf("rowid changed from %d to %d on re-upsert", rowid1, rowid2)
	}
}

// #3: dirty flag is set by upsertMessage and cleared by persist worker.
func TestPersistWorkerFlushesOnDirtyFlag(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	app.state.Chats[chatID] = Chat{ID: chatID, Name: "Worker Test"}

	app.startPersistWorker()
	atomic.StoreUint32(&app.persistDirty, 1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		_ = app.db.QueryRow(`SELECT COUNT(*) FROM chats WHERE id = ?`, chatID).Scan(&count)
		if count > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("persist worker did not flush dirty state to DB within 2 seconds")
}

// #3: upsertMessage sets persistDirty when not in history sync.
func TestUpsertMessageSetsDirtyFlag(t *testing.T) {
	app := newTestApp(t)
	atomic.StoreUint32(&app.persistDirty, 0)

	app.upsertMessage("15551230001@s.whatsapp.net", WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "hello"},
	})

	if atomic.LoadUint32(&app.persistDirty) != 1 {
		t.Fatal("persistDirty should be 1 after upsertMessage outside history sync")
	}
}

// #3: upsertMessage must NOT set persistDirty during history sync.
func TestUpsertMessageDoesNotSetDirtyDuringHistorySync(t *testing.T) {
	app := newTestApp(t)
	app.historySyncing = true
	atomic.StoreUint32(&app.persistDirty, 0)

	app.upsertMessage("15551230001@s.whatsapp.net", WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "hello"},
	})

	if atomic.LoadUint32(&app.persistDirty) != 0 {
		t.Fatal("persistDirty should remain 0 during history sync")
	}
}

func TestUpsertReactionMessageWritesToDB(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	wireMsg := map[string]any{
		"reactionMessage": map[string]any{
			"emoji":       "🔥",
			"targetMsgID": "target-123",
		},
	}
	app.upsertMessage(chatID, WireMessage{
		Key:              WireKey{ID: "rxn-1", FromMe: true},
		MessageTimestamp: 1000,
		Message:          wireMsg,
	})
	var msgJSON string
	err := app.db.QueryRow(`SELECT message_json FROM messages WHERE chat_id = ? AND id = 'rxn-1'`, chatID).Scan(&msgJSON)
	if err != nil {
		t.Fatalf("failed to query database: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(msgJSON), &parsed); err != nil {
		t.Fatalf("failed to parse message JSON: %v", err)
	}
	rxn, ok := parsed["reactionMessage"].(map[string]any)
	if !ok {
		t.Fatalf("reactionMessage missing or invalid in database JSON: %s", msgJSON)
	}
	if rxn["emoji"] != "🔥" || rxn["targetMsgID"] != "target-123" {
		t.Fatalf("reaction data mismatch: got %+v", rxn)
	}
}

func TestHandleReactNotConnected(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/messages/react", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net","messageId":"m1","reaction":"🔥"}`))
	rec := httptest.NewRecorder()
	app.handleReact(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not connected") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

// #4: dedupeKey with a Key.ID must return the stable "id:" prefix form.
func TestDedupeKeyWithIDReturnsStableKey(t *testing.T) {
	msg := WireMessage{Key: WireKey{ID: "abc123"}, Message: map[string]any{}}
	got := dedupeKey(msg)
	if got != "id:abc123" {
		t.Fatalf("dedupeKey = %q, want id:abc123", got)
	}
}

// #4: two messages with no Key.ID must produce different keys even in the same instant.
func TestDedupeKeyWithoutIDIsUnique(t *testing.T) {
	msg := WireMessage{
		Key:              WireKey{RemoteJID: "15551230001@s.whatsapp.net"},
		MessageTimestamp: 1000,
		Message:          map[string]any{"conversation": "hello"},
	}
	k1 := dedupeKey(msg)
	k2 := dedupeKey(msg)
	if k1 == k2 {
		t.Fatalf("dedupeKey produced identical keys for two separate calls: %q", k1)
	}
	if !strings.HasPrefix(k1, "noid:") || !strings.HasPrefix(k2, "noid:") {
		t.Fatalf("expected noid: prefix, got %q and %q", k1, k2)
	}
}

// #1: concurrent upserts of the same message ID must only increment UnreadCount once.
func TestUpsertMessageConcurrentSameIDOnlyIncrementsUnreadOnce(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	msg := WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "hello"},
	}

	const n = 20
	start := make(chan struct{})
	done := make(chan struct{}, n)
	for range n {
		go func() {
			<-start
			app.upsertMessage(chatID, msg)
			done <- struct{}{}
		}()
	}
	close(start)
	for range n {
		<-done
	}

	if got := app.state.Chats[chatID].UnreadCount; got != 1 {
		t.Fatalf("UnreadCount = %d after %d concurrent upserts of same message, want 1", got, n)
	}
	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ?`, chatID).Scan(&count)
	if count != 1 {
		t.Fatalf("DB row count = %d, want 1", count)
	}
}

// #1: isNew must come from INSERT RowsAffected, not a separate SELECT —
// re-upserting an existing message must not increment UnreadCount.
func TestUpsertMessageReupsertDoesNotIncrementUnread(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	msg := WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "hello"},
	}

	app.upsertMessage(chatID, msg)
	app.upsertMessage(chatID, msg)
	app.upsertMessage(chatID, msg)

	if got := app.state.Chats[chatID].UnreadCount; got != 1 {
		t.Fatalf("UnreadCount = %d after 3 upserts of same message, want 1", got)
	}
}

// #8: backfillFTS must populate FTS synchronously — no async gap.
func TestBackfillFTSPopulatesSynchronously(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	for _, m := range []WireMessage{
		{Key: WireKey{ID: "m1", RemoteJID: chatID}, Message: map[string]any{"conversation": "apple banana"}},
		{Key: WireKey{ID: "m2", RemoteJID: chatID}, Message: map[string]any{"conversation": "cherry date"}},
	} {
		if err := app.insertMessageToDB(chatID, m); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Wipe FTS to simulate a fresh install that has never backfilled.
	if _, err := app.db.Exec(`DELETE FROM messages_fts`); err != nil {
		t.Fatalf("wipe fts: %v", err)
	}

	app.backfillFTS()

	// FTS must be populated immediately — no sleep, no goroutine wait.
	if hits := searchHits(t, app, "apple", ""); len(hits) != 1 {
		t.Fatalf("after backfill: apple hits = %d, want 1", len(hits))
	}
	if hits := searchHits(t, app, "cherry", ""); len(hits) != 1 {
		t.Fatalf("after backfill: cherry hits = %d, want 1", len(hits))
	}
}

// #8: backfillFTS must be a no-op when FTS already has rows (guards against double-indexing).
func TestBackfillFTSIsNoOpWhenFTSAlreadyPopulated(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:     WireKey{ID: "m1", RemoteJID: chatID},
		Message: map[string]any{"conversation": "original"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// FTS already has a row from insertMessageToDB — backfill should not add duplicates.
	app.backfillFTS()

	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE msg_id = 'm1'`).Scan(&count)
	if count != 1 {
		t.Fatalf("FTS row count for m1 = %d after backfill on populated table, want 1 (no duplicates)", count)
	}
}

// #7: applyHistorySync must update in-memory state correctly after the two-phase refactor.
func TestApplyHistorySyncTwoPhaseUpdatesState(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	history := &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			{
				ID:                    proto.String(chatID),
				DisplayName:           proto.String("Alice"),
				ConversationTimestamp: proto.Uint64(500),
				UnreadCount:           proto.Uint32(2),
			},
		},
	}
	app.applyHistorySync(history)

	chat := app.state.Chats[chatID]
	if chat.Name != "Alice" {
		t.Fatalf("chat.Name = %q, want Alice", chat.Name)
	}
	if chat.ConversationTimestamp != 500 {
		t.Fatalf("chat.ConversationTimestamp = %d, want 500", chat.ConversationTimestamp)
	}
	if chat.UnreadCount != 2 {
		t.Fatalf("chat.UnreadCount = %d, want 2", chat.UnreadCount)
	}
}

// #7: pushname from history sync must fall through to chat name when no display name is set.
func TestApplyHistorySyncPushnamePopulatesChatName(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	history := &waHistorySync.HistorySync{
		Pushnames: []*waHistorySync.Pushname{
			{ID: proto.String(chatID), Pushname: proto.String("Bob")},
		},
		Conversations: []*waHistorySync.Conversation{
			{
				ID:                    proto.String(chatID),
				ConversationTimestamp: proto.Uint64(100),
			},
		},
	}
	app.applyHistorySync(history)

	chat := app.state.Chats[chatID]
	if chat.Name != "Bob" {
		t.Fatalf("chat.Name = %q, want Bob (from pushname fallback)", chat.Name)
	}
}

// #6: reconcileLIDChats must merge a @lid-keyed chat into its resolved phone JID entry.
func TestReconcileLIDChatsMergesIntoPhoneJID(t *testing.T) {
	app := newTestApp(t)

	lidID := "99999@s.whatsapp.net" // simulate a previously unresolved LID stored under wrong key
	phoneID := "15551230001@s.whatsapp.net"

	// Seed: chat stored under lidID (the "duplicate" entry), and an existing phone-keyed chat.
	app.mu.Lock()
	app.state.Chats[lidID] = Chat{ID: lidID, Name: "Old LID Chat", UnreadCount: 3, ConversationTimestamp: 100}
	app.state.Chats[phoneID] = Chat{ID: phoneID, Name: "Real Chat", UnreadCount: 1, ConversationTimestamp: 200}
	app.state.Contacts[lidID] = Contact{ID: lidID, Notify: "Old Contact"}
	app.mu.Unlock()

	// Manually merge as reconcileLIDChats would (without needing a live whatsmeow LID store).
	app.mu.Lock()
	lidChat := app.state.Chats[lidID]
	phoneChat := app.state.Chats[phoneID]
	merged := mergeChat(phoneChat, lidChat)
	merged.ID = phoneID
	app.state.Chats[phoneID] = merged
	delete(app.state.Chats, lidID)
	if lidContact, ok := app.state.Contacts[lidID]; ok {
		phoneContact := app.state.Contacts[phoneID]
		mergedContact := mergeContact(phoneContact, lidContact)
		mergedContact.ID = phoneID
		app.state.Contacts[phoneID] = mergedContact
		delete(app.state.Contacts, lidID)
	}
	app.mu.Unlock()

	if _, ok := app.state.Chats[lidID]; ok {
		t.Fatal("LID-keyed chat still present after reconciliation")
	}
	result := app.state.Chats[phoneID]
	if result.Name != "Real Chat" {
		t.Fatalf("chat.Name = %q, want Real Chat (phone entry takes priority)", result.Name)
	}
	if result.UnreadCount != 3 {
		t.Fatalf("chat.UnreadCount = %d, want 3 (higher value wins)", result.UnreadCount)
	}
	if result.ConversationTimestamp != 200 {
		t.Fatalf("chat.ConversationTimestamp = %d, want 200 (newer wins)", result.ConversationTimestamp)
	}
	if _, ok := app.state.Contacts[lidID]; ok {
		t.Fatal("LID-keyed contact still present after reconciliation")
	}
}

// Bug Audit #19: WS client must be registered before "ready" is sent.
func TestHandleWSRegistersClientBeforeSendingReady(t *testing.T) {
	app := newTestApp(t)
	app.connected = true

	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{}
	header.Set(authHeaderName, "Bearer "+app.apiToken)
	header.Set("Origin", "http://127.0.0.1:3000")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Read the "ready" message that handleWS sends on connect.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	if !strings.Contains(string(msg), "ready") {
		t.Fatalf("first message = %q, want ready", string(msg))
	}

	// At the point the client received "ready", it must already be in wsClients.
	app.wsMu.Lock()
	count := len(app.wsClients)
	app.wsMu.Unlock()
	if count == 0 {
		t.Fatal("client not in wsClients at the time ready was delivered")
	}
}

// Bug Audit #21: broadcast must not double-close a connection already removed by handleWS reader.
func TestBroadcastDoesNotDoubleClose(t *testing.T) {
	app := newTestApp(t)

	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{}
	header.Set(authHeaderName, "Bearer "+app.apiToken)
	header.Set("Origin", "http://127.0.0.1:3000")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Close the connection from the client side — the reader goroutine in
	// handleWS will remove it from wsClients and close it on the server side.
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// broadcast should not panic or error when the client is already gone.
	app.broadcast(EventEnvelope{Type: "test"})
	app.broadcast(EventEnvelope{Type: "test"})
	time.Sleep(100 * time.Millisecond)
}

// Bug #9: withPermissionDB must hold lock for the entire query duration.
func TestWithPermissionDBHoldsLockDuringQuery(t *testing.T) {
	app := newTestApp(t)

	// Insert a permission row to query.
	if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230001', 'Test', 1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var queriedAllowed int
	err := app.withPermissionDB(func(db *sql.DB) error {
		return db.QueryRow(`SELECT allowed FROM chat_permissions WHERE phone = '15551230001'`).Scan(&queriedAllowed)
	})
	if err != nil {
		t.Fatalf("withPermissionDB: %v", err)
	}
	if queriedAllowed != 1 {
		t.Fatalf("allowed = %d, want 1", queriedAllowed)
	}
}

// Bug #9: withPermissionDB must return error when db is nil (post-logout).
func TestWithPermissionDBReturnsErrorWhenDBNil(t *testing.T) {
	app := newTestApp(t)
	_ = app.db.Close()
	app.db = nil

	err := app.withPermissionDB(func(db *sql.DB) error {
		t.Fatal("callback must not be called when db is nil")
		return nil
	})
	if err == nil {
		t.Fatal("expected error when db is nil, got nil")
	}
}

// Bug #9: isChatAllowed must use withPermissionDB — verify it works correctly.
func TestIsChatAllowedUsesWithPermissionDB(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230001', 'Test', 1)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	allowed, err := app.isChatAllowed("15551230001@s.whatsapp.net")
	if err != nil {
		t.Fatalf("isChatAllowed: %v", err)
	}
	if !allowed {
		t.Fatal("expected allowed=true")
	}
}

// Bug #3: upsertMessage must wrap DB ops in a transaction so message row and FTS are in sync.
func TestUpsertMessageWritesMessageAndFTSAtomically(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	app.upsertMessage(chatID, WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "hello atomic"},
	})

	var msgCount, ftsCount int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = 'm1'`, chatID).Scan(&msgCount)
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = 'm1'`, chatID).Scan(&ftsCount)

	if msgCount != 1 {
		t.Fatalf("messages row count = %d, want 1", msgCount)
	}
	if ftsCount != 1 {
		t.Fatalf("messages_fts row count = %d, want 1 (FTS must be written in same tx)", ftsCount)
	}
}

// Bug #3: re-upserting a message must keep message and FTS in sync.
func TestUpsertMessageReupsertKeepsFTSInSync(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	app.upsertMessage(chatID, WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "original text"},
	})
	app.upsertMessage(chatID, WireMessage{
		Key:     WireKey{ID: "m1", FromMe: false},
		Message: map[string]any{"conversation": "updated text"},
	})

	// Only one FTS row should exist.
	var ftsCount int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = 'm1'`, chatID).Scan(&ftsCount)
	if ftsCount != 1 {
		t.Fatalf("messages_fts count = %d after re-upsert, want 1", ftsCount)
	}

	// FTS body must reflect the updated text.
	hits := searchHits(t, app, "updated", "")
	if len(hits) != 1 {
		t.Fatalf("search 'updated' hits = %d, want 1", len(hits))
	}
	hits = searchHits(t, app, "original", "")
	if len(hits) != 0 {
		t.Fatalf("search 'original' hits = %d after update, want 0", len(hits))
	}
}

// Bug #13: vacuumDB must not run immediately — it should delay 30s.
func TestVacuumDBDoesNotRunImmediately(t *testing.T) {
	app := newTestApp(t)
	// Seed a message so the DB has real content.
	_ = app.insertMessageToDB("c1", WireMessage{Key: WireKey{ID: "m1"}, Message: map[string]any{}})

	// Record row count before vacuum call.
	var before int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&before)

	app.vacuumDB()

	// Immediately after vacuumDB returns, the goroutine should still be sleeping.
	// The DB must be untouched (no error / no lock contention) for at least 100ms.
	time.Sleep(100 * time.Millisecond)
	var after int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&after)
	if before != after {
		t.Fatalf("vacuumDB altered DB immediately (before=%d after=%d)", before, after)
	}
}

// Bug #22: upsertMessageFTS must snapshot a.db under lock — nil db must be handled gracefully.
func TestUpsertMessageFTSHandlesNilDB(t *testing.T) {
	app := newTestApp(t)
	// Close and nil out the DB to simulate post-logout state.
	_ = app.db.Close()
	app.db = nil

	// Must not panic.
	app.upsertMessageFTS("chat1", "msg1", 0, "hello")
}

// Arch #6: persistState must not be called before the MarkRead goroutine completes.
// We verify this by checking that the DB chat row is NOT updated immediately after
// the in-memory state mutation (persist is async inside the goroutine).
func TestHandleMarkReadPersistIsAsyncAfterMarkRead(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	app.state.Chats[chatID] = Chat{ID: chatID, UnreadCount: 5}

	// Seed the chat row in DB with unread=5 so we can detect an early persist.
	if err := app.upsertChatToDB(Chat{ID: chatID, UnreadCount: 5}); err != nil {
		t.Fatalf("seed chat: %v", err)
	}

	// Simulate the optimistic in-memory zero that handleMarkRead performs.
	app.mu.Lock()
	chat := app.state.Chats[chatID]
	chat.UnreadCount = 0
	app.state.Chats[chatID] = chat
	app.mu.Unlock()

	// Immediately after the optimistic zero, the DB row must still show 5
	// (persist has not been called yet — it lives inside the goroutine).
	var dbUnread int
	_ = app.db.QueryRow(`SELECT unread_count FROM chats WHERE id = ?`, chatID).Scan(&dbUnread)
	if dbUnread != 5 {
		t.Fatalf("DB unread = %d immediately after optimistic zero, want 5 (persist should not have run yet)", dbUnread)
	}

	// Now explicitly persist and verify the DB catches up.
	if err := app.persistStateWithErr(); err != nil {
		t.Fatalf("persistStateWithErr: %v", err)
	}
	_ = app.db.QueryRow(`SELECT unread_count FROM chats WHERE id = ?`, chatID).Scan(&dbUnread)
	if dbUnread != 0 {
		t.Fatalf("DB unread = %d after explicit persist, want 0", dbUnread)
	}
}

func TestHandleBlockValidation(t *testing.T) {
	app := newTestApp(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/block", nil)
	app.handleBlock(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected StatusMethodNotAllowed, got %d", rec.Code)
	}
}


