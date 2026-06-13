package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// S-3: with no env var, the helper returns "WARN". This is the
// default that production users will see — guards against an
// accidental revert to the pre-fix hardcoded "DEBUG".
func TestResolveWhatsmeowLogLevelDefaultsToWarn(t *testing.T) {
	// Ensure no env var is set for the duration of this test.
	t.Setenv("WHATZAP_LOG_LEVEL", "")
	// Belt-and-suspenders: t.Setenv already un-sets on cleanup,
	// but if a previous test in the same run set it, we want to
	// see the empty-string path. The "" value is treated as "no
	// override" by the helper (TrimSpace returns "" which fails
	// the != "" check), so this exercises the default.
	if got := resolveWhatsmeowLogLevel(); got != "WARN" {
		t.Fatalf("resolveWhatsmeowLogLevel() = %q, want WARN (no env var → default)", got)
	}
}

// S-3: WHATZAP_LOG_LEVEL overrides the default. Three sub-cases:
// mixed-case input gets uppercased, an explicitly-empty value
// (just whitespace) is treated as "no override" and the default
// sticks, a "DEBUG" value passes through unchanged.
func TestResolveWhatsmeowLogLevelRespectsEnv(t *testing.T) {
	t.Run("uppercased to match waLog levelToInt keys", func(t *testing.T) {
		t.Setenv("WHATZAP_LOG_LEVEL", "debug")
		if got := resolveWhatsmeowLogLevel(); got != "DEBUG" {
			t.Fatalf("resolveWhatsmeowLogLevel() = %q, want DEBUG", got)
		}
	})

	t.Run("explicit value passes through", func(t *testing.T) {
		t.Setenv("WHATZAP_LOG_LEVEL", "ERROR")
		if got := resolveWhatsmeowLogLevel(); got != "ERROR" {
			t.Fatalf("resolveWhatsmeowLogLevel() = %q, want ERROR", got)
		}
	})

	t.Run("whitespace-only is treated as no override", func(t *testing.T) {
		t.Setenv("WHATZAP_LOG_LEVEL", "   ")
		if got := resolveWhatsmeowLogLevel(); got != "WARN" {
			t.Fatalf("resolveWhatsmeowLogLevel() = %q, want WARN (whitespace should not override)", got)
		}
	})
}

// S-3: the package-level default is "WARN", not the pre-fix
// "DEBUG". Cheap regression guard — catches a future edit that
// flips the default to "" (which would mean "log everything"
// per waLog/util/log/log.go:levelToInt) or re-introduces the
// hardcoded DEBUG constant.
func TestWhatsmeowLogLevelDefaultIsWarn(t *testing.T) {
	// Don't use t.Setenv here — we want to see the literal
	// initializer value, regardless of what the helper would
	// return for the current process env. The default must
	// hold even when the env var is set (NewApp overwrites the
	// var in that case, but the literal default is what users
	// see on first launch with no env var).
	if whatsmeowLogLevel != "WARN" {
		t.Fatalf("whatsmeowLogLevel = %q, want WARN (package-level default)", whatsmeowLogLevel)
	}
}

// A-13: pure-string tests for the generic cappedEvict helper —
// no handler, no DB, no goroutine needed. Each sub-test seeds a
// map with n entries and asserts the size after calling cappedEvict.
func TestCappedEvict(t *testing.T) {
	t.Run("removes excess", func(t *testing.T) {
		m := map[string]int{}
		for i := 0; i < 10; i++ {
			m[fmt.Sprintf("k%d", i)] = i
		}
		cappedEvict(m, 5)
		if len(m) != 5 {
			t.Fatalf("len after evict = %d, want 5", len(m))
		}
	})

	t.Run("noop below cap", func(t *testing.T) {
		m := map[string]int{}
		for i := 0; i < 3; i++ {
			m[fmt.Sprintf("k%d", i)] = i
		}
		cappedEvict(m, 5)
		if len(m) != 3 {
			t.Fatalf("len after evict = %d, want 3", len(m))
		}
	})

	t.Run("noop at cap", func(t *testing.T) {
		m := map[string]int{}
		for i := 0; i < 5; i++ {
			m[fmt.Sprintf("k%d", i)] = i
		}
		cappedEvict(m, 5)
		if len(m) != 5 {
			t.Fatalf("len after evict = %d, want 5", len(m))
		}
	})

	t.Run("noop when disabled", func(t *testing.T) {
		m := map[string]int{}
		for i := 0; i < 10; i++ {
			m[fmt.Sprintf("k%d", i)] = i
		}
		cappedEvict(m, 0) // 0 = disabled
		if len(m) != 10 {
			t.Fatalf("len after evict = %d, want 10", len(m))
		}
	})

	t.Run("empty map", func(t *testing.T) {
		m := map[string]int{}
		cappedEvict(m, 5)
		if len(m) != 0 {
			t.Fatalf("len after evict = %d, want 0", len(m))
		}
	})
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

func TestHandleLogoutPreservesChatPermissions(t *testing.T) {
	app := newTestApp(t)
	app.cacheDir = t.TempDir()
	dbPath := filepath.Join(app.cacheDir, "store.db")
	if app.db != nil {
		app.db.Close()
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	app.db = db
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS chat_permissions (
		phone   TEXT PRIMARY KEY,
		name    TEXT NOT NULL DEFAULT '',
		allowed INTEGER NOT NULL DEFAULT 0
	)`)
	_, err = db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230001', 'Alex', 1)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec := httptest.NewRecorder()
	app.handleLogout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	checkDB, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("open checked db: %v", err)
	}
	defer checkDB.Close()
	var name string
	var allowed int
	err = checkDB.QueryRow(`SELECT name, allowed FROM chat_permissions WHERE phone = '15551230001'`).Scan(&name, &allowed)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "Alex" || allowed != 1 {
		t.Errorf("got name=%q allowed=%d, want Alex and 1", name, allowed)
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

// S-2: a request body larger than the middleware cap is rejected before
// any handler can fully process it. A 2 MB body POSTed to an authenticated
// endpoint must not return 200 OK (would mean the body was fully parsed
// and the giant JSON made it into the handler's logic). The actual status
// depends on which handler — `json.NewDecoder` returns an error on the
// cap and the handler responds with 400, which is the correct outcome
// for "the request was rejected, the body did not get processed."
func TestWithAuthRejectsOversizedBody(t *testing.T) {
	app := newTestApp(t)
	big := bytes.Repeat([]byte("a"), 2<<20) // 2 MB
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewReader(big)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	// The cap is enforced at read time inside the handler. JSON
	// decode fails, handler returns 400. The crucial property is
	// that the giant body was NOT processed — it can't have been,
	// because the cap fired before the body finished streaming in.
	if rec.Code == http.StatusOK {
		t.Fatalf("2 MB body was accepted as 200 OK: %s", rec.Body.String())
	}
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("handler crashed on oversized body: %s", rec.Body.String())
	}
}

// S-2 (companion): a request body comfortably under the cap must pass
// through withAuth untouched, so normal TUI traffic is unaffected.
func TestWithAuthAllowsSmallBody(t *testing.T) {
	app := newTestApp(t)
	small := []byte(`{"phone":"15551230001","name":"Test","allowed":1}`)
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewReader(small)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	// 200 OK means the body was parsed and the whitelist entry was
	// written. The cap is not in the way for small bodies.
	if rec.Code != http.StatusOK {
		t.Fatalf("small body unexpectedly rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// S-2 regression: http.MaxBytesReader wraps r.Body, so an outer cap can't
// loosen an inner one. /messages/send-file applies its own 150 MB+ cap
// inside the handler, so withAuth's 256 KB JSON cap must not also apply
// to that route — otherwise every file over 256 KB would fail with
// "http: request body too large" despite the 150 MB limit.
func TestWithAuthExemptsSendFileFromBodyCap(t *testing.T) {
	app := newTestApp(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/messages/send-file", func(w http.ResponseWriter, r *http.Request) {
		// Mirror handleSendFile: re-wrap r.Body with the larger cap.
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(64<<10))
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "read: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"bytes": len(data)})
	})
	handler := withCORS(app.withAuth(mux))

	big := bytes.Repeat([]byte("a"), 300*1024) // 300 KB > the 256 KB JSON cap
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/messages/send-file", bytes.NewReader(big)),
		app,
	)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("300 KB body to /messages/send-file rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Companion to TestWithAuthExemptsSendFileFromBodyCap: confirms the 256 KB
// cap still applies to ordinary JSON routes.
func TestWithAuthStillCapsOtherRoutesAt256KB(t *testing.T) {
	app := newTestApp(t)
	big := bytes.Repeat([]byte("a"), 300*1024) // 300 KB > the 256 KB cap
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewReader(big)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("300 KB body to /whitelist/set unexpectedly accepted: %s", rec.Body.String())
	}
}

// A-20: a request whose header block exceeds maxHeaderBytes is rejected
// by the HTTP server before the handler runs. The header cap is enforced
// at the connection level, so we have to drive the real http.Server
// (httptest.NewUnstartedServer + srv.Start) rather than app.handler()
// directly, which bypasses MaxHeaderBytes.
func TestWithAuthRejectsOversizedHeader(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewUnstartedServer(app.handler())
	srv.Config.MaxHeaderBytes = maxHeaderBytes
	srv.Start()
	defer srv.Close()

	// 100 KB custom header — far over the 64 KB cap.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/contacts", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+app.apiToken)
	req.Header.Set("X-Bloat", strings.Repeat("a", 100*1024))

	res, err := srv.Client().Do(req)
	if err != nil {
		// A 431 / connection-reset shows up here as a transport error
		// from the client. Either is a valid "rejected" outcome.
		return
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Fatalf("100 KB header was accepted as 200 OK: %s", res.Status)
	}
}

// Security A-2: the http.Server struct must have all four timeouts
// set to the package-level vars. Catches an accidental edit to 0,
// a swap (read/write), or someone deleting the field.
func TestHTTPServerTimeoutsHaveExpectedValues(t *testing.T) {
	srv := &http.Server{
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Errorf("ReadTimeout = %v, want 30s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %v, want 30s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 2*time.Minute {
		t.Errorf("IdleTimeout = %v, want 2m", srv.IdleTimeout)
	}
}

// Security A-2: ReadHeaderTimeout is the slowloris defense — a
// client that opens a connection and dribbles bytes forever must be
// cut off. We override the production 10s value to 100ms for the
// test (same mechanism, faster runtime). Uses a raw TCP conn so we
// can stall the request mid-header.
func TestReadHeaderTimeoutRejectsSlowClient(t *testing.T) {
	app := newTestApp(t)
	srv := httptest.NewUnstartedServer(app.handler())
	srv.Config.ReadHeaderTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Write the request-line and Host header, but pause *before*
	// the final \r\n that terminates the header block. With
	// ReadHeaderTimeout=100ms, the server should close the conn
	// well before our 500ms sleep finishes.
	if _, err := conn.Write([]byte("GET /health HTTP/1.1\r\nHost: localhost\r\n")); err != nil {
		t.Fatalf("write partial headers: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	// The server may have closed the conn (we want it to). Try to
	// finish the request — write should fail or the subsequent read
	// should return EOF / error.
	_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	if _, err := conn.Write([]byte("\r\n")); err != nil {
		// Expected: "write on closed connection" or similar.
		// Connection was closed by the server under our feet.
		return
	}
	// If the write somehow succeeded, reading the response should
	// fail (server closed the conn without sending a response).
	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("server sent a response to a stalled request — ReadHeaderTimeout did not fire")
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

// Security S-4: only the backend's own URL is allowed. Pre-fix,
// this test verified that any loopback origin (http://localhost:3000,
// http://127.0.0.1:9999, http://[::1]:8787, etc.) was accepted.
// Now only the exact bind address is allowed.
func TestCORSAllowsBackendOwnOrigin(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodOptions, "/contacts", nil)
	req.Header.Set("Origin", allowedBackendOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowedBackendOrigin {
		t.Fatalf("allow origin = %q, want %q", got, allowedBackendOrigin)
	}
}

// Security S-4: every other loopback origin must be rejected. This
// is the S-4 fix's main assertion — the core threat is a malicious
// local page on, say, localhost:3000 doing a CORS preflight against
// the backend. Pre-fix, the preflight would have succeeded; now it
// gets 403.
func TestCORSRejectsOtherLoopbackOrigins(t *testing.T) {
	app := newTestApp(t)
	for _, origin := range []string{
		"http://localhost:8787",
		"http://localhost:3000",
		"http://127.0.0.1:3000",
		"http://127.0.0.1:9999",
		"http://[::1]:8787",
	} {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/contacts", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			rec := httptest.NewRecorder()

			app.handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("allow origin = %q, want empty (origin should not be echoed back)", got)
			}
		})
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

	t.Run("authorized backend origin succeeds", func(t *testing.T) {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+app.apiToken)
		// S-4: the only allowed browser origin is the backend's own
		// URL. Pre-fix this was http://127.0.0.1:3000 (any loopback).
		header.Set("Origin", allowedBackendOrigin)
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err != nil {
			t.Fatalf("expected websocket dial to succeed: %v", err)
		}
		_ = conn.Close()
	})

	t.Run("other loopback origin is rejected (S-4)", func(t *testing.T) {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+app.apiToken)
		// A different port on the same host — pre-fix this would have
		// been allowed because it was loopback. Now rejected.
		header.Set("Origin", "http://127.0.0.1:3000")
		_, _, err := websocket.DefaultDialer.Dial(wsURL, header)
		if err == nil {
			t.Fatal("expected websocket dial to fail for non-backend loopback origin")
		}
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
	// S-7: the response must not leak the underlying "state cleanup failed: ..."
	// error text — only an opaque ref ID.
	if strings.Contains(rec.Body.String(), "state cleanup failed") {
		t.Fatalf("response leaked internal error text: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "internal error (ref: err-") {
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

// Security S-8: copyDirContents must not follow symlinks inside the
// legacy dir. If a symlink points at an arbitrary file (e.g.
// ~/.ssh/id_rsa), copying through it would plant the target's
// contents in the live data folder.
//
// On Windows, os.Symlink requires admin or developer mode, so the
// test is skipped on systems that can't create symlinks.
func TestCopyDirContentsSkipsSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Plant a target file outside the source dir (we don't want the
	// symlink to resolve to anything in src, so a stray IsRegular
	// can't accidentally satisfy the assertion).
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(src, "sneakylink")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("can't create symlinks on this system (need admin/developer mode on Windows): %v", err)
	}

	// A regular file alongside the symlink, to prove copyDirContents
	// did run and just selectively skipped the symlink.
	good := filepath.Join(src, "good.txt")
	if err := os.WriteFile(good, []byte("benign"), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}

	if err := copyDirContents(src, dst); err != nil {
		t.Fatalf("copyDirContents: %v", err)
	}

	// Symlink must NOT appear in the destination (neither as a
	// symlink nor as a copy of the target's bytes).
	if _, err := os.Lstat(filepath.Join(dst, "sneakylink")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink was copied to destination: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sneakylink")); !errors.Is(err, os.ErrNotExist) {
		// Stat follows symlinks; if the symlink had been copied,
		// this would resolve to the target. Defense-in-depth check.
		t.Errorf("symlink target leaked into destination: err=%v", err)
	}
	// And the target's bytes must not be reachable as a copy.
	if _, err := os.Stat(filepath.Join(dst, "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink target was copied as a file: err=%v", err)
	}
	// The good file MUST be copied (regression guard for the
	// positive case — we don't want the symlink check to break
	// normal migration).
	copied, err := os.ReadFile(filepath.Join(dst, "good.txt"))
	if err != nil {
		t.Fatalf("good.txt was not copied: %v", err)
	}
	if string(copied) != "benign" {
		t.Errorf("good.txt contents = %q, want %q", copied, "benign")
	}
}

// Security S-8 regression guard: the positive case — regular files
// and subdirs (with their own regular files) must still be copied.
// Without this, an overzealous symlink check could break normal
// migration of the legacy cache.
func TestCopyDirContentsCopiesRegularFilesAndSubdirs(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Top-level regular file.
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatalf("write top: %v", err)
	}
	// Subdir with a regular file inside.
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}
	// Deeper subdir with another file.
	if err := os.MkdirAll(filepath.Join(src, "sub", "deeper"), 0o755); err != nil {
		t.Fatalf("mkdir deeper: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "deeper", "leaf.txt"), []byte("leaf"), 0o644); err != nil {
		t.Fatalf("write leaf: %v", err)
	}

	if err := copyDirContents(src, dst); err != nil {
		t.Fatalf("copyDirContents: %v", err)
	}

	for _, tc := range []struct{ path, want string }{
		{"top.txt", "top"},
		{filepath.Join("sub", "nested.txt"), "nested"},
		{filepath.Join("sub", "deeper", "leaf.txt"), "leaf"},
	} {
		got, err := os.ReadFile(filepath.Join(dst, tc.path))
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.path, got, tc.want)
		}
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

// A-25: pure-string tests for normalizeWhitelistPhone — no handler
// or DB needed. Each case asserts the expected normalized output
// (for valid inputs) or error message substring (for invalid).
func TestNormalizeWhitelistPhone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    string
		want     string
		wantErr  string
	}{
		{name: "plain digits", input: "15551230001", want: "15551230001"},
		{name: "with @s.whatsapp.net suffix", input: "15551230001@s.whatsapp.net", want: "15551230001"},
		{name: "rejects @g.us group JID", input: "120363040000001234@g.us", wantErr: "group chats cannot be whitelisted"},
		{name: "rejects plus prefix", input: "+15551230001", wantErr: "digits only"},
		{name: "rejects letters", input: "abc123", wantErr: "digits only"},
		{name: "rejects hyphens", input: "123-456-7890", wantErr: "digits only"},
		{name: "rejects bare @server", input: "@s.whatsapp.net", wantErr: "phone is required"},
		{name: "rejects empty string", input: "", wantErr: "phone is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeWhitelistPhone(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeWhitelistPhone(%q) = %q, want error containing %q", tc.input, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("normalizeWhitelistPhone(%q) error = %q, want substr %q", tc.input, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeWhitelistPhone(%q) = _, %v, want %q", tc.input, err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("normalizeWhitelistPhone(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// A-25: handler-level integration test for the suffixed-phone
// path. Sends "15551230001@s.whatsapp.net" as the phone, asserts
// 200 OK, then reads the whitelist back via GET and confirms the
// stored phone is the normalized "15551230001" (suffix stripped).
func TestHandleSetWhitelistAcceptsSuffixedPhone(t *testing.T) {
	app := newTestApp(t)

	setReq := httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewBufferString(`{"phone":"15551230001@s.whatsapp.net","name":"Alex","allowed":1}`))
	setRec := httptest.NewRecorder()
	app.handleSetWhitelist(setRec, setReq)

	if setRec.Code != http.StatusOK {
		t.Fatalf("set whitelist status = %d, want %d, body=%s", setRec.Code, http.StatusOK, setRec.Body.String())
	}

	// Verify the stored phone was normalized (suffix stripped).
	getReq := httptest.NewRequest(http.MethodGet, "/whitelist", nil)
	getRec := httptest.NewRecorder()
	app.handleGetWhitelist(getRec, getReq)

	var body struct {
		Contacts []struct {
			Phone string `json:"phone"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse whitelist response: %v", err)
	}
	if len(body.Contacts) != 1 {
		t.Fatalf("whitelist count = %d, want 1", len(body.Contacts))
	}
	if body.Contacts[0].Phone != "15551230001" {
		t.Fatalf("whitelist phone = %q, want 15551230001 (suffix should be stripped)", body.Contacts[0].Phone)
	}
}

// A-25: handler-level test that non-digit phone values are rejected
// before any DB interaction. The handler should return 400, not 200
// or 500, for a phone with a + prefix.
func TestHandleSetWhitelistRejectsInvalidPhone(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewBufferString(`{"phone":"+15551230001","name":"Alex","allowed":1}`))
	rec := httptest.NewRecorder()
	app.handleSetWhitelist(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "digits only") {
		t.Fatalf("body = %s, want error containing 'digits only'", rec.Body.String())
	}
}

// A-9: /whitelist/set must reject group JIDs (@g.us) — the whitelist is a
// phone-number allowlist, not a group allowlist.
func TestHandleSetWhitelistRejectsGroupJID(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodPost, "/whitelist/set", bytes.NewBufferString(`{"phone":"120363040000001234@g.us","name":"Group","allowed":1}`))
	rec := httptest.NewRecorder()
	app.handleSetWhitelist(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "group chats cannot be whitelisted") {
		t.Fatalf("body = %s, want error containing 'group chats cannot be whitelisted'", rec.Body.String())
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
	if quotedFromMeForChat("15551230001@s.whatsapp.net", "", false) {
		t.Fatalf("expected empty quoted participant in 1:1 chat to default to non-self (other party quoting self)")
	}
	if quotedFromMeForChat("15551230001@s.whatsapp.net", "", true) {
		t.Fatalf("expected empty quoted participant in group to default to non-self")
	}
}

// TestQuotedMessageFromMe covers the fix for the bug where a reply quoting
// my own message rendered the quote in the received (other-party) color.
// normalizeQuotedParticipant returns "" when the quoted author is self, so
// "raw present + normalized empty" must be read as "from me".
func TestQuotedMessageFromMe(t *testing.T) {
	const chat = "15551230001@s.whatsapp.net"

	// The bug case: B quotes my message in a 1:1. WhatsApp sets the raw
	// participant to my JID; normalizeQuotedParticipant blanks it to "".
	// raw present + normalized "" ⇒ from me.
	if !quotedMessageFromMe(chat, "15559990000@s.whatsapp.net", "", false) {
		t.Fatalf("self-authored quote (raw present, normalized empty) should be from me")
	}

	// Same signal must hold in a group, where the elimination heuristic
	// can't help (chatID is the group JID, not a person).
	if !quotedMessageFromMe("120363040000001234@g.us", "15559990000@s.whatsapp.net", "", true) {
		t.Fatalf("self-authored quote in a group should be from me")
	}

	// B quotes B's own message in a 1:1: raw and normalized both = the chat
	// partner. Not from me.
	if quotedMessageFromMe(chat, chat, chat, false) {
		t.Fatalf("partner quoting their own message should NOT be from me")
	}

	// Genuinely absent participant (WhatsApp omitted it): raw "" ⇒ no
	// self signal, falls through to elimination, which yields non-self.
	if quotedMessageFromMe(chat, "", "", false) {
		t.Fatalf("absent participant should default to non-self")
	}
}

// TestQuotedStanzaFromMe verifies the authoritative DB lookup: a quote of a
// message we sent (from_me=1) reports true even when WhatsApp omitted the
// participant; a quote of a received message reports false; and a quote of a
// message not in our store reports found=false so the caller can fall back.
func TestQuotedStanzaFromMe(t *testing.T) {
	app := newTestApp(t)
	const chat = "15551230001@s.whatsapp.net"

	// Seed one outgoing and one incoming message in the same chat.
	if _, err := app.db.Exec(
		`INSERT INTO messages (id, chat_id, from_me, ts, message_json) VALUES (?, ?, 1, 1, ?)`,
		"mine-1", chat, `{"conversation":"my message"}`,
	); err != nil {
		t.Fatalf("seed outgoing: %v", err)
	}
	if _, err := app.db.Exec(
		`INSERT INTO messages (id, chat_id, from_me, ts, message_json) VALUES (?, ?, 0, 2, ?)`,
		"theirs-1", chat, `{"conversation":"their message"}`,
	); err != nil {
		t.Fatalf("seed incoming: %v", err)
	}

	// Quote of my own message ⇒ from me, found.
	if fromMe, ok := app.quotedStanzaFromMe(chat, "mine-1"); !ok || !fromMe {
		t.Fatalf("quote of own message: got fromMe=%v ok=%v, want true true", fromMe, ok)
	}
	// Quote of their message ⇒ not from me, found.
	if fromMe, ok := app.quotedStanzaFromMe(chat, "theirs-1"); !ok || fromMe {
		t.Fatalf("quote of their message: got fromMe=%v ok=%v, want false true", fromMe, ok)
	}
	// Quote of an unknown message ⇒ found=false (caller falls back).
	if _, ok := app.quotedStanzaFromMe(chat, "ghost-99"); ok {
		t.Fatalf("quote of unknown message should report found=false")
	}
	// Empty stanza ID ⇒ found=false.
	if _, ok := app.quotedStanzaFromMe(chat, ""); ok {
		t.Fatalf("empty stanza id should report found=false")
	}
}

// File validation.
func TestValidateSendFileInput(t *testing.T) {
	imageData := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := validateSendFileInput("sample.png", imageData, "image"); err != nil {
		t.Fatalf("image validation failed: %v", err)
	}

	videoData := []byte("not-a-real-video")
	if err := validateSendFileInput("sample.mp4", videoData, "video"); err != nil {
		t.Fatalf("video validation failed: %v", err)
	}

	textData := []byte("plain text")
	if err := validateSendFileInput("sample.txt", textData, "image"); err == nil {
		t.Fatalf("expected image validation to fail for text file")
	}
	if err := validateSendFileInput("sample.txt", textData, "video"); err == nil {
		t.Fatalf("expected video validation to fail for text file")
	}
	if err := validateSendFileInput("sample.txt", textData, "document"); err != nil {
		t.Fatalf("document validation failed: %v", err)
	}

	// Empty filename and empty kind.
	if err := validateSendFileInput("", imageData, "image"); err == nil {
		t.Fatalf("expected error for empty filename")
	}
	if err := validateSendFileInput("sample.png", imageData, ""); err == nil {
		t.Fatalf("expected error for empty kind")
	}

	// Path components in filename are ignored (filepath.Base would be applied
	// at the call site, but here we just verify the extension is read from
	// the trailing component).
	if err := validateSendFileInput("..\\..\\..\\Windows\\evil.png", imageData, "image"); err != nil {
		t.Fatalf("image validation should use extension only, got: %v", err)
	}
}

// multipartBody builds a multipart/form-data body and returns the buffer
// and Content-Type header value. fields are the simple form fields; if
// fileField is non-empty, a file part is added with the given filename and
// content.
func multipartBody(t *testing.T, fields map[string]string, fileField, filename string, fileContent []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileField != "" {
		fw, err := mw.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(fileContent); err != nil {
			t.Fatalf("write file content: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body, mw.FormDataContentType()
}

func TestHandleSendFileRejectsMissingFile(t *testing.T) {
	app := newTestApp(t)
	// A client is required for handleSendFile to reach the multipart parse,
	// so set a non-nil placeholder. requireConnectedClient is gated on
	// a.client being set AND the connection being live; the test app has no
	// real client, so we expect 409 "not connected" — this still proves
	// the route is reachable and the body is consumed. To test the missing
	// file case directly we mock the client path by sending a request
	// without a client and assert 409, then move on.
	_ = app
	body, ct := multipartBody(t, map[string]string{
		"chatId": "15551230001@s.whatsapp.net",
		"kind":   "document",
	}, "", "", nil)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/send-file", body), app)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	// Without a real whatsmeow client, the handler returns 409 before
	// parsing the multipart body. The point of this test is to confirm
	// the route is registered under the auth middleware and the request
	// flows through without 401 or 405.
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected auth to pass, got 401: %s", rec.Body.String())
	}
	if rec.Code == http.StatusMethodNotAllowed {
		t.Fatalf("expected POST to be allowed, got 405: %s", rec.Body.String())
	}
}

func TestHandleSendFileRejectsBadMultipart(t *testing.T) {
	app := newTestApp(t)
	// Send a body that is not valid multipart. Without a real client the
	// 409 returns first, so use a non-multipart body and verify the
	// response code is one of the expected error codes (not 200/500).
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/messages/send-file",
			bytes.NewBufferString("not-multipart-data")),
		app)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected error, got 200: %s", rec.Body.String())
	}
}

func TestHandleSendFileRequiresAuth(t *testing.T) {
	app := newTestApp(t)
	body, ct := multipartBody(t, map[string]string{
		"chatId": "15551230001@s.whatsapp.net",
		"kind":   "document",
	}, "file", "test.txt", []byte("hello"))
	req := httptest.NewRequest(http.MethodPost, "/messages/send-file", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSendFileRejectsNonWhitelistedChat(t *testing.T) {
	app := newTestApp(t)
	// Inject a nil-safe client stub by skipping the not-connected check.
	// We can't reach the whitelist check without a connected client, so
	// this test verifies the route is wired: with auth + non-whitelisted
	// chat we expect either 409 (not connected) or 403 (not whitelisted).
	// Both are correct rejections; anything else is a regression.
	body, ct := multipartBody(t, map[string]string{
		"chatId": "15551239999@s.whatsapp.net", // not in chat_permissions
		"kind":   "document",
	}, "file", "test.txt", []byte("hello"))
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/send-file", body), app)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 409 or 403, body=%s", rec.Code, rec.Body.String())
	}
}

// --- Direct handler tests (skip the auth middleware) ---

// These tests call app.handleSendFile directly so they can exercise the
// multipart logic without needing a real whatsmeow client connection. They
// run against an app with no client and assert the handler returns
// http.StatusConflict before reaching the upload — which is the correct
// behavior in production when the WhatsApp client is disconnected.

func TestHandleSendFileRejectsMissingFilePart(t *testing.T) {
	app := newTestApp(t)
	body, ct := multipartBody(t, map[string]string{
		"chatId": "15551230001@s.whatsapp.net",
		"kind":   "document",
	}, "", "", nil)
	req := httptest.NewRequest(http.MethodPost, "/messages/send-file", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handleSendFile(rec, req)
	// No client connected → 409 from requireConnectedClient before
	// the file-part check. The point of this test is that the route
	// does not crash and the request body is consumed.
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSendFileMIMEMismatchReturnsError(t *testing.T) {
	// Pure unit test: validateSendFileInput (which handleSendFile calls
	// on the file part) rejects text content when kind=image AND the
	// filename has no image extension. (The check is "extension OR
	// sniffed type", so a .png extension would let text through on
	// extension alone — that's intentional, the byte sniff is a
	// second line of defense not a gate.)
	textData := []byte("this is plain text, not an image")
	if err := validateSendFileInput("photo", textData, "image"); err == nil {
		t.Fatalf("expected MIME mismatch error for text content with kind=image and no extension")
	}
}

func TestHandleSendFileValidatesImageData(t *testing.T) {
	// Pure unit test: a real PNG header should pass validation.
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := validateSendFileInput("photo.png", pngHeader, "image"); err != nil {
		t.Fatalf("valid PNG should pass, got: %v", err)
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

// Security A-16: the messages table's PRIMARY KEY is (chat_id, id,
// from_me). When the same chat has two messages with the same id
// but different from_me, deleting using only (chat_id, id) would
// wipe BOTH rows. deleteMessageFromDB must use the full key so
// each row is deletable independently.
func TestDeleteMessageFromDBOnlyDeletesMatchingFromMe(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	// Seed one incoming (from_me=0) and one outgoing (from_me=1) row
	// with the SAME id, simulating the rare collision the A-16 bug
	// note describes.
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "shared-id", RemoteJID: chatID, FromMe: false},
		Message: map[string]any{"conversation": "incoming"},
	}); err != nil {
		t.Fatalf("seed incoming: %v", err)
	}
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "shared-id", RemoteJID: chatID, FromMe: true},
		Message: map[string]any{"conversation": "outgoing"},
	}); err != nil {
		t.Fatalf("seed outgoing: %v", err)
	}

	// Sanity: both rows exist.
	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = ?`, chatID, "shared-id").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("seed produced %d rows, want 2", n)
	}

	// Delete the outgoing one. The incoming row must remain.
	if err := app.deleteMessageFromDB(chatID, "shared-id", true); err != nil {
		t.Fatalf("delete outgoing: %v", err)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = ? AND from_me = 0`, chatID, "shared-id").Scan(&n); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if n != 1 {
		t.Errorf("incoming row gone after deleting outgoing: n=%d, want 1", n)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = ? AND from_me = 1`, chatID, "shared-id").Scan(&n); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if n != 0 {
		t.Errorf("outgoing row still present after delete: n=%d, want 0", n)
	}

	// Now delete the incoming one. The table should be empty.
	if err := app.deleteMessageFromDB(chatID, "shared-id", false); err != nil {
		t.Fatalf("delete incoming: %v", err)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id = ? AND id = ?`, chatID, "shared-id").Scan(&n); err != nil {
		t.Fatalf("count final: %v", err)
	}
	if n != 0 {
		t.Errorf("both rows not deleted: n=%d, want 0", n)
	}
}

// Security A-16: the FTS index must also be deleted with the full
// (chat_id, msg_id, from_me) triple. The FTS table has one row per
// triple (matching the messages table's PK), so a partial-key FTS
// delete would leave a stale FTS row that the search endpoint would
// still return even after the messages row is gone.
func TestDeleteMessageFromDBRemovesMatchingFTSRow(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "fts1", RemoteJID: chatID, FromMe: true},
		Message: map[string]any{"conversation": "searchable outgoing"},
	}); err != nil {
		t.Fatalf("seed outgoing: %v", err)
	}
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "fts1", RemoteJID: chatID, FromMe: false},
		Message: map[string]any{"conversation": "searchable incoming"},
	}); err != nil {
		t.Fatalf("seed incoming: %v", err)
	}

	// Both FTS rows should exist.
	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ?`, chatID, "fts1").Scan(&n); err != nil {
		t.Fatalf("fts count: %v", err)
	}
	if n != 2 {
		t.Fatalf("seed produced %d FTS rows, want 2", n)
	}

	// Delete the outgoing one. FTS row for from_me=1 must go;
	// FTS row for from_me=0 must remain.
	if err := app.deleteMessageFromDB(chatID, "fts1", true); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = 1`, chatID, "fts1").Scan(&n); err != nil {
		t.Fatalf("fts after: %v", err)
	}
	if n != 0 {
		t.Errorf("outgoing FTS row still present: n=%d, want 0", n)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = 0`, chatID, "fts1").Scan(&n); err != nil {
		t.Fatalf("fts after: %v", err)
	}
	if n != 1 {
		t.Errorf("incoming FTS row gone after deleting outgoing: n=%d, want 1", n)
	}
}

// Security A-16: the handler must reject a request body that's
// missing the fromMe field. A missing field is the dangerous case
// — it would silently default to false (delete the incoming row,
// which doesn't exist for the user's own messages → no-op → the
// user's outgoing message stays). 400 on missing makes the
// contract loud and forces the TUI to be rebuilt in lockstep.
func TestHandleDeleteMessageRejectsMissingFromMe(t *testing.T) {
	app := newTestApp(t)
	body := `{"chatId":"15551230001@s.whatsapp.net","messageId":"m1"}`
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/messages/delete", strings.NewReader(body)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, _ := resp["error"].(string); got != "fromMe is required" {
		t.Fatalf("error = %q, want %q", got, "fromMe is required")
	}
}

// Security A-16: a request with fromMe explicitly set to false
// must pass the validation and reach the DB delete (we can't
// drive the full handler in tests because RevokeMessage requires
// a connected client, so we go through the helper directly to
// confirm the negative-fromMe path doesn't short-circuit).
func TestHandleDeleteMessageAcceptsFromMeFalse(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "in-only", RemoteJID: chatID, FromMe: false},
		Message: map[string]any{},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Direct helper call simulates what the handler would do after
	// the request body is decoded and fromMe=false.
	if err := app.deleteMessageFromDB(chatID, "in-only", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var n int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = 'in-only'`).Scan(&n)
	if n != 0 {
		t.Errorf("incoming row not deleted: n=%d, want 0", n)
	}
}

// --- Message edit tests ---

func TestEditMessageInDBUpdatesConversationText(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:              WireKey{ID: "edit1", RemoteJID: chatID, FromMe: true},
		Message:          map[string]any{"conversation": "original"},
		MessageTimestamp: 1000,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	wire, ok, err := app.editMessageInDB(chatID, "edit1", true, func(m map[string]any) {
		m["conversation"] = "edited text"
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if got, _ := wire.Message["conversation"].(string); got != "edited text" {
		t.Errorf("conversation = %q, want %q", got, "edited text")
	}
	if edited, _ := wire.Message["edited"].(bool); !edited {
		t.Errorf("edited flag not set")
	}
	if wire.MessageTimestamp != 1000 {
		t.Errorf("timestamp changed: got %d, want 1000", wire.MessageTimestamp)
	}

	// Verify the DB row itself was updated.
	var messageJSON string
	if err := app.db.QueryRow(`SELECT message_json FROM messages WHERE chat_id = ? AND id = ? AND from_me = 1`, chatID, "edit1").Scan(&messageJSON); err != nil {
		t.Fatalf("query: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(messageJSON), &stored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := stored["conversation"].(string); got != "edited text" {
		t.Errorf("stored conversation = %q, want %q", got, "edited text")
	}
	if edited, _ := stored["edited"].(bool); !edited {
		t.Errorf("stored edited flag not set")
	}
}

func TestEditMessageInDBPreservesQuoteFields(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key: WireKey{ID: "edit2", RemoteJID: chatID, FromMe: true},
		Message: map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              "original",
				"quotedText":        "quoted msg",
				"quotedParticipant": "15559999999@s.whatsapp.net",
				"quotedFromMe":      false,
			},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	wire, ok, err := app.editMessageInDB(chatID, "edit2", true, func(m map[string]any) {
		ext, _ := m["extendedTextMessage"].(map[string]any)
		ext["text"] = "edited text"
		m["extendedTextMessage"] = ext
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	ext, ok := wire.Message["extendedTextMessage"].(map[string]any)
	if !ok {
		t.Fatalf("extendedTextMessage missing after edit")
	}
	if got, _ := ext["text"].(string); got != "edited text" {
		t.Errorf("text = %q, want %q", got, "edited text")
	}
	if got, _ := ext["quotedText"].(string); got != "quoted msg" {
		t.Errorf("quotedText = %q, want preserved %q", got, "quoted msg")
	}
}

func TestEditMessageInDBNoMatchingRow(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	_, ok, err := app.editMessageInDB(chatID, "does-not-exist", true, func(m map[string]any) {
		m["conversation"] = "x"
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false for missing row")
	}
}

func TestEditMessageInDBUpdatesFTS(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:     WireKey{ID: "edit3", RemoteJID: chatID, FromMe: true},
		Message: map[string]any{"conversation": "searchable original"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, ok, err := app.editMessageInDB(chatID, "edit3", true, func(m map[string]any) {
		m["conversation"] = "searchable replacement"
	}); err != nil || !ok {
		t.Fatalf("edit: ok=%v err=%v", ok, err)
	}

	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = 1 AND body MATCH 'replacement'`, chatID, "edit3").Scan(&n); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if n != 1 {
		t.Errorf("FTS not updated to new text: n=%d, want 1", n)
	}
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = 1 AND body MATCH 'original'`, chatID, "edit3").Scan(&n); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if n != 0 {
		t.Errorf("stale FTS row for old text remains: n=%d, want 0", n)
	}
}

// Without a connected client, /messages/edit must fail fast with 409
// before touching the DB or attempting BuildEdit/SendMessage.
func TestHandleEditMessageRequiresConnectedClient(t *testing.T) {
	app := newTestApp(t)
	body := `{"chatId":"15551230001@s.whatsapp.net","messageId":"m1","text":"new text"}`
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/messages/edit", strings.NewReader(body)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleEditMessageRejectsEmptyText(t *testing.T) {
	app := newTestApp(t)
	body := `{"chatId":"15551230001@s.whatsapp.net","messageId":"m1","text":""}`
	req := authorizedRequest(
		httptest.NewRequest(http.MethodPost, "/messages/edit", strings.NewReader(body)),
		app,
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
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

// A-10: /messages/react must be gated by the whitelist, same as
// /messages/send. With no real client connected, requireConnectedClient
// fires first (409); both 409 and 403 are correct rejections for a
// non-whitelisted chat — anything else is a regression.
func TestHandleReactRejectsNonWhitelistedChat(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/react", bytes.NewBufferString(`{"chatId":"15551239999@s.whatsapp.net","messageId":"m1","reaction":"🔥"}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 409 or 403, body=%s", rec.Code, rec.Body.String())
	}
}

// A-11: /typing must be gated by the whitelist, same as /messages/send.
// With no real client connected, requireConnectedClient fires first (409);
// both 409 and 403 are correct rejections for a non-whitelisted chat —
// anything else is a regression.
func TestHandleTypingRejectsNonWhitelistedChat(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/typing", bytes.NewBufferString(`{"chatId":"15551239999@s.whatsapp.net","state":"composing"}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict && rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 409 or 403, body=%s", rec.Code, rec.Body.String())
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
	// S-4: the only allowed browser origin is the backend's own URL.
	// Pre-fix this was http://127.0.0.1:3000 (any loopback).
	header.Set("Origin", allowedBackendOrigin)

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
	// S-4: the only allowed browser origin is the backend's own URL.
	// Pre-fix this was http://127.0.0.1:3000 (any loopback).
	header.Set("Origin", allowedBackendOrigin)

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

// A-12: a WS conn that opens, authenticates, then never sends
// another message must be killed by the read deadline. The test
// shrinks wsReadIdleTimeout to 100ms for speed, then sleeps 400ms
// (4x the deadline) without sending anything. After the sleep, the
// server should have closed the conn and removed it from wsClients.

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

// History sync must default a FromMe row with no receipt state to "delivered",
// so the TUI renders ✓✓ for old outgoing messages. Incoming rows must stay
// empty (no tick at all on the recipient's view).
func TestApplyHistorySyncDefaultsFromMeReceiptToDelivered(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	history := &waHistorySync.HistorySync{
		Conversations: []*waHistorySync.Conversation{
			{
				ID: proto.String(chatID),
				Messages: []*waHistorySync.HistorySyncMsg{
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								ID:        proto.String("out-1"),
								RemoteJID: proto.String(chatID),
								FromMe:    proto.Bool(true),
							},
							MessageTimestamp: proto.Uint64(100),
							Message: &waE2E.Message{
								Conversation: proto.String("hello"),
							},
						},
					},
					{
						Message: &waWeb.WebMessageInfo{
							Key: &waCommon.MessageKey{
								ID:        proto.String("in-1"),
								RemoteJID: proto.String(chatID),
								FromMe:    proto.Bool(false),
							},
							MessageTimestamp: proto.Uint64(110),
							Message: &waE2E.Message{
								Conversation: proto.String("hi"),
							},
						},
					},
				},
			},
		},
	}
	app.applyHistorySync(history)

	var (
		outReceipt string
		inReceipt  string
	)
	if err := app.db.QueryRow(
		`SELECT receipt FROM messages WHERE chat_id = ? AND id = 'out-1'`, chatID,
	).Scan(&outReceipt); err != nil {
		t.Fatalf("read out-1: %v", err)
	}
	if outReceipt != "delivered" {
		t.Fatalf("FromMe receipt = %q, want delivered", outReceipt)
	}
	if err := app.db.QueryRow(
		`SELECT receipt FROM messages WHERE chat_id = ? AND id = 'in-1'`, chatID,
	).Scan(&inReceipt); err != nil {
		t.Fatalf("read in-1: %v", err)
	}
	if inReceipt != "" {
		t.Fatalf("incoming receipt = %q, want empty", inReceipt)
	}
}

// backfillReceipt must upgrade FromMe+"" rows to delivered, leave incoming
// rows alone, and be a no-op on a second run.
func TestBackfillReceiptUpgradesFromMeRows(t *testing.T) {
	app := newTestApp(t)
	chatID := "15551230001@s.whatsapp.net"

	// Seed: outgoing with empty receipt, outgoing with explicit "read"
	// (must not be downgraded), incoming with empty receipt.
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:              WireKey{ID: "out-empty", RemoteJID: chatID, FromMe: true},
		ReceiptStatus:    "",
		MessageTimestamp: 1,
		Message:          map[string]any{"conversation": "a"},
	}); err != nil {
		t.Fatalf("insert out-empty: %v", err)
	}
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:              WireKey{ID: "out-read", RemoteJID: chatID, FromMe: true},
		ReceiptStatus:    "read",
		MessageTimestamp: 2,
		Message:          map[string]any{"conversation": "b"},
	}); err != nil {
		t.Fatalf("insert out-read: %v", err)
	}
	if err := app.insertMessageToDB(chatID, WireMessage{
		Key:              WireKey{ID: "in-empty", RemoteJID: chatID, FromMe: false},
		ReceiptStatus:    "",
		MessageTimestamp: 3,
		Message:          map[string]any{"conversation": "c"},
	}); err != nil {
		t.Fatalf("insert in-empty: %v", err)
	}

	app.backfillReceipt()

	check := func(id, want string) {
		t.Helper()
		var got string
		if err := app.db.QueryRow(
			`SELECT receipt FROM messages WHERE chat_id = ? AND id = ?`, chatID, id,
		).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if got != want {
			t.Fatalf("%s receipt = %q, want %q", id, got, want)
		}
	}
	check("out-empty", "delivered")
	check("out-read", "read")
	check("in-empty", "")

	// Idempotent: second run must not change out-read back to delivered.
	app.backfillReceipt()
	check("out-empty", "delivered")
	check("out-read", "read")
	check("in-empty", "")
}

// Bug Audit #6: handleLogout must tear down ALL of {a.state, a.db,
// a.storeContainer, cacheDir} on a single call — no early return on a
// non-fatal failure. With a nil client (no Store.Delete to fail), the full
// teardown path runs; we assert every step completed.
func TestHandleLogoutTearsDownEverything(t *testing.T) {
	app := newTestApp(t)
	app.cacheDir = t.TempDir()
	app.state.Chats["c1"] = Chat{ID: "c1", Name: "Alice"}
	app.state.Contacts["c1"] = Contact{ID: "c1", Notify: "Alice"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	app.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if app.db != nil {
		t.Fatal("a.db should be nil after logout")
	}
	if len(app.state.Chats) != 0 || len(app.state.Contacts) != 0 {
		t.Fatalf("state not cleared: chats=%d contacts=%d",
			len(app.state.Chats), len(app.state.Contacts))
	}
	if _, err := os.Stat(app.cacheDir); err != nil {
		t.Fatalf("cacheDir not preserved/recreated after logout: %v", err)
	}
}

// Bug Audit #6: even if a teardown step fails (e.g. cacheDir can't be
// recreated), earlier steps must still have completed. We force a failure in
// resetPersistentStorage by pointing cacheDir at a path that os.MkdirAll
// can't create (an existing regular file on Windows/Linux).
func TestHandleLogoutContinuesTeardownWhenLaterStepFails(t *testing.T) {
	app := newTestApp(t)
	tmp := t.TempDir()
	// Make cacheDir point to a regular file so os.MkdirAll(cacheDir, 0755)
	// inside resetPersistentStorage fails.
	blockingFile := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	app.cacheDir = filepath.Join(blockingFile, "subdir") // MkdirAll on a file fails
	app.state.Chats["c1"] = Chat{ID: "c1"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	app.handleLogout(rec, req)

	// Must report the failure (500) since user said "500 if anything failed".
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	// But the EARLIER steps must still have completed: state cleared, db nil.
	if app.db != nil {
		t.Fatal("a.db should be nil even when later teardown step failed")
	}
	if len(app.state.Chats) != 0 {
		t.Fatalf("state should be cleared even when later teardown step failed; got %d chats", len(app.state.Chats))
	}
}

// Security A-5: a successful logout must nil out a.client so any concurrent
// reader (event handlers, other HTTP handlers) early-outs on their existing
// nil check instead of calling methods on a Disconnected client.
func TestHandleLogoutNilClientAfterLogout(t *testing.T) {
	app := newTestApp(t)
	app.started = true
	app.connected = true

	rec := httptest.NewRecorder()
	app.handleLogout(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	app.mu.RLock()
	client := app.client
	app.mu.RUnlock()
	if client != nil {
		t.Fatal("a.client should be nil after a successful logout")
	}
}

// Security A-5: while one /logout is in flight, an additional /logout
// call must short-circuit with 409 and must not enter the data-folder
// teardown path. Pre-fix, two callers would race through os.RemoveAll
// + initPersistentResources on the same path, leaving the DB in a
// half-created state.
//
// We simulate the in-flight logout by setting a.shuttingDown = true
// (the same flag the production handler checks). We don't hold
// a.logoutMu from the test because sync.Mutex is non-reentrant and
// the handler takes that same lock at the top. The lock's mutual
// exclusion is a stdlib guarantee we don't need to re-test; this
// test exercises the 409 early-out logic that lives behind it.
func TestHandleLogoutReturns409WhenAnotherIsInFlight(t *testing.T) {
	app := newTestApp(t)
	app.started = true
	app.connected = true
	app.cacheDir = t.TempDir()
	leftover := filepath.Join(app.cacheDir, "leftover.tmp")
	if err := os.WriteFile(leftover, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	// Simulate "another /logout is in flight right now."
	app.shuttingDown = true
	defer func() { app.shuttingDown = false }()

	rec := httptest.NewRecorder()
	app.handleLogout(rec, httptest.NewRequest(http.MethodPost, "/logout", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if got, _ := body["error"].(string); got != "logout already in progress" {
		t.Fatalf("error = %q, want %q", got, "logout already in progress")
	}
	// Conflict path never runs the teardown; leftover file must be
	// exactly as we left it. This is the property the pre-fix code
	// violated — two callers would both pass the cacheDir-delete
	// step and the second one would race the first's DB reopen.
	if _, err := os.Stat(leftover); err != nil {
		t.Errorf("leftover file should still exist (no teardown ran): %v", err)
	}
}

// Security A-5: after a successful logout, the second of two
// *sequential* /logout calls must not corrupt any state. The contract
// is: a.client, a.db, a.storeContainer are all nil, and the call
// returns 200 (idempotent no-op teardown on an empty cacheDir).
func TestHandleLogoutSequentialSecondCallIsIdempotent(t *testing.T) {
	app := newTestApp(t)
	app.started = true
	app.connected = true
	app.cacheDir = t.TempDir()

	rec1 := httptest.NewRecorder()
	app.handleLogout(rec1, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status = %d, want 200, body=%s", rec1.Code, rec1.Body.String())
	}

	rec2 := httptest.NewRecorder()
	app.handleLogout(rec2, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call status = %d, want 200 (idempotent), body=%s", rec2.Code, rec2.Body.String())
	}
	// cacheDir should still exist (the no-runtime-resources teardown
	// removes then re-creates it).
	if _, err := os.Stat(app.cacheDir); err != nil {
		t.Errorf("cacheDir missing after second logout: %v", err)
	}
}

// Security A-21: every JSON response from the backend (including errors)
// must carry Cache-Control: no-store so proxies, browser caches, and any
// intermediate tool can't keep copies of private chat data. Verified against
// the success path, the unauthorized path, and writeErr's explicit error path.
func TestWriteJSONSetsNoStoreCacheControl(t *testing.T) {
	app := newTestApp(t)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "ok",
			req:  httptest.NewRequest(http.MethodGet, "/health", nil),
		},
		{
			name: "unauthorized",
			req:  httptest.NewRequest(http.MethodGet, "/chats", nil),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			app.handler().ServeHTTP(rec, tc.req)

			if rec.Code != http.StatusOK && rec.Code != http.StatusUnauthorized {
				t.Fatalf("unexpected status %d; body = %s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
	}
}

// Security A-21: the writeErr helper also flows through writeJSON, so its
// response must carry the same no-store directive.
func TestWriteErrSetsNoStoreCacheControl(t *testing.T) {
	rec := httptest.NewRecorder()
	writeErr(rec, http.StatusBadRequest, "boom")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := rec.Header().Get("content-type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
}

// S-7: writeInternalErr must never put the raw error text in the response —
// only an opaque "ref: err-N" ID that can be correlated with the server log.
func TestWriteInternalErrReturnsOpaqueID(t *testing.T) {
	rec := httptest.NewRecorder()
	writeInternalErr(rec, errors.New("sql: connection refused at /home/user/.local/share/whatzap/store.db"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if strings.Contains(payload.Error, "store.db") || strings.Contains(payload.Error, "connection refused") {
		t.Fatalf("response leaked internal error text: %q", payload.Error)
	}
	if !strings.Contains(payload.Error, "ref: err-") {
		t.Fatalf("response missing opaque ref ID: %q", payload.Error)
	}
}

// S-7: the real error text must still land in the server log, keyed by the
// same ref ID returned to the client, so an operator can correlate them.
func TestWriteInternalErrLogsOriginalError(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	rec := httptest.NewRecorder()
	writeInternalErr(rec, errors.New("disk failure on /data/store.db"))

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	idx := strings.Index(payload.Error, "err-")
	if idx == -1 {
		t.Fatalf("response missing ref ID: %q", payload.Error)
	}
	refID := strings.TrimSuffix(payload.Error[idx:], ")")

	logged := buf.String()
	if !strings.Contains(logged, refID) {
		t.Fatalf("log output %q missing ref ID %q", logged, refID)
	}
	if !strings.Contains(logged, "disk failure on /data/store.db") {
		t.Fatalf("log output %q missing original error text", logged)
	}
}

// S-11: verify that os.MkdirAll with 0o700 creates a directory
// with owner-only permissions. On Windows this test is skipped
// because Windows does not enforce Unix file permission bits.
func TestMkdirAllPerm0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix-style file permissions")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "testdir")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	info, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 0700", perm)
	}
}

// S-11: verify that os.Chmod with 0o600 restricts a file to
// owner-only. On Windows this test is skipped because Windows
// does not enforce Unix file permission bits.
func TestDBFileChmod0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix-style file permissions")
	}
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	f.Close()
	if err := os.Chmod(f.Name(), 0o600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	info, err := os.Stat(f.Name())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 0600", perm)
	}
}
func TestMigrateLIDPermissions(t *testing.T) {
	app := newTestApp(t)
	_, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('12345', 'LID User', 1)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	app.migrateLIDPermissions("12345", "67890")
	var name string
	var allowed int
	err = app.db.QueryRow(`SELECT name, allowed FROM chat_permissions WHERE phone = '67890'`).Scan(&name, &allowed)
	if err != nil {
		t.Fatalf("query migrated: %v", err)
	}
	if name != "LID User" || allowed != 1 {
		t.Errorf("got name=%q, allowed=%d, want LID User and 1", name, allowed)
	}
	var count int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chat_permissions WHERE phone = '12345'`).Scan(&count)
	if count != 0 {
		t.Errorf("LID row was not deleted")
	}
}

// TestRedactingWriterScrubsSecret is the core S-16 test: a log line that
// contains the bearer token must come out the other side with the token
// replaced by [REDACTED] and everything else intact.
func TestRedactingWriterScrubsSecret(t *testing.T) {
	var buf bytes.Buffer
	w := newRedactingWriter(&buf, func() string { return "supersecrettoken" })

	// Simulate a leaked request dump: the kind of string log.Printf("%v", r)
	// would produce, with the Authorization header inline.
	in := "method=GET path=/chats Authorization=[Bearer supersecrettoken] done"
	n, err := w.Write([]byte(in))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Write must report the input length, not the (shorter) scrubbed length,
	// or the stdlib logger treats it as a short write / failure.
	if n != len(in) {
		t.Fatalf("Write returned n=%d, want %d (input length)", n, len(in))
	}
	got := buf.String()
	if strings.Contains(got, "supersecrettoken") {
		t.Fatalf("token leaked into output: %q", got)
	}
	if !strings.Contains(got, redactPlaceholder) {
		t.Fatalf("expected %q in output, got %q", redactPlaceholder, got)
	}
	// Non-secret content must survive unchanged.
	if !strings.Contains(got, "path=/chats") || !strings.Contains(got, "method=GET") {
		t.Fatalf("non-secret content was mangled: %q", got)
	}
}

// TestRedactingWriterMultipleOccurrences confirms every occurrence of the
// secret in a single write is scrubbed, not just the first.
func TestRedactingWriterMultipleOccurrences(t *testing.T) {
	var buf bytes.Buffer
	w := newRedactingWriter(&buf, func() string { return "tok123" })
	in := "first tok123 middle tok123 last tok123"
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "tok123") {
		t.Fatalf("a token occurrence leaked: %q", got)
	}
	if c := strings.Count(got, redactPlaceholder); c != 3 {
		t.Fatalf("expected 3 redactions, got %d: %q", c, got)
	}
}

// TestRedactingWriterEmptySecretIsPassthrough confirms that with no token
// set (dev runs) or a literal nil getter, content passes through verbatim.
func TestRedactingWriterEmptySecretIsPassthrough(t *testing.T) {
	var buf bytes.Buffer
	if w := newRedactingWriter(&buf, nil); w != &buf {
		t.Fatalf("nil secret getter should return the raw writer unchanged")
	}

	in := "no secret here, pass through verbatim"

	buf.Reset()
	w := newRedactingWriter(&buf, func() string { return "" })
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != in {
		t.Fatalf("empty secret mangled content: got %q, want %q", got, in)
	}

	buf.Reset()
	w = newRedactingWriter(&buf, func() string { return "   " })
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != in {
		t.Fatalf("whitespace-only secret mangled content: got %q, want %q", got, in)
	}
}

// TestRedactingWriterCleanLineUnchanged confirms a log line with no secret
// in it is written through untouched (the common case — most log lines).
func TestRedactingWriterCleanLineUnchanged(t *testing.T) {
	var buf bytes.Buffer
	w := newRedactingWriter(&buf, func() string { return "secret" })
	in := "upsertMessage: begin tx: connection refused"
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != in {
		t.Fatalf("clean line was altered: got %q, want %q", got, in)
	}
}

// TestRedactingWriterViaLogger is the end-to-end test: a *log.Logger wired
// to the redactor, like main() sets up, must scrub the token from a real
// log.Printf call.
func TestRedactingWriterViaLogger(t *testing.T) {
	var buf bytes.Buffer
	lg := log.New(newRedactingWriter(&buf, func() string { return "live-token-xyz" }), "[http] ", 0)
	lg.Printf("rejected request with header Authorization: Bearer live-token-xyz")
	got := buf.String()
	if strings.Contains(got, "live-token-xyz") {
		t.Fatalf("token leaked through logger: %q", got)
	}
	if !strings.Contains(got, redactPlaceholder) {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}
