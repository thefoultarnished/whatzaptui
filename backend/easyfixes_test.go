package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithTxCommits(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	if _, err := app.db.Exec(`CREATE TABLE IF NOT EXISTS contacts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create contacts: %v", err)
	}
	if err := app.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO contacts (id) VALUES ('a'), ('b')`)
		return err
	}); err != nil {
		t.Fatalf("withTx: %v", err)
	}
	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("count = %d, err = %v, want 2", n, err)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	if _, err := app.db.Exec(`CREATE TABLE IF NOT EXISTS contacts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create contacts: %v", err)
	}
	_ = app.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO contacts (id) VALUES ('x')`); err != nil {
			return err
		}
		return sql.ErrTxDone
	})
	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM contacts`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("count = %d, err = %v, want 0 (rolled back)", n, err)
	}
}

func TestWithRecoveryCatchesPanic(t *testing.T) {
	panicer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	withRecovery(panicer).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("body = %q, want internal server error", rec.Body.String())
	}
}

func TestWithRecoveryPassesThrough(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := httptest.NewRecorder()
	withRecovery(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("code = %d, want 418", rec.Code)
	}
}

func TestMessageListQueriesBeforeAndAll(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	chatID := "15551230001@s.whatsapp.net"
	for _, tc := range []struct{ id string; ts int64 }{{"m1", 10}, {"m2", 20}, {"m3", 30}} {
		if err := app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: tc.id}, MessageTimestamp: tc.ts}); err != nil {
			t.Fatalf("insert %s: %v", tc.id, err)
		}
	}
	// qAll path (no before)
	req := httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=10", nil)
	rec := httptest.NewRecorder()
	app.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("qAll code = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "m3") {
		t.Fatalf("qAll body missing m3: %s", rec.Body.String())
	}
	// qBefore path
	req = httptest.NewRequest(http.MethodGet, "/messages?chatId="+chatID+"&limit=10&before=25", nil)
	rec = httptest.NewRecorder()
	app.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("qBefore code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "m2") || strings.Contains(body, "m3") {
		t.Fatalf("qBefore body wrong: %s", body)
	}
}

func TestSearchQueriesAllAndChatScoped(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	chatID := "15551230001@s.whatsapp.net"
	if err := app.insertMessageToDB(chatID, WireMessage{Key: WireKey{ID: "s1"}, Message: map[string]any{"conversation": "hello world"}}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for _, q := range []string{"/search?q=hello&limit=10", "/search?q=hello&chatId=" + chatID + "&limit=10"} {
		req := httptest.NewRequest(http.MethodGet, q, nil)
		rec := httptest.NewRecorder()
		app.handleSearch(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d, body = %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestBackendListenPort(t *testing.T) {
	t.Setenv("WHATZAP_PORT", "")
	if backendListenPort() != backendPort {
		t.Fatalf("default = %q, want %q", backendListenPort(), backendPort)
	}
	t.Setenv("WHATZAP_PORT", "18989")
	if backendListenPort() != "18989" {
		t.Fatalf("override = %q, want 18989", backendListenPort())
	}
}

func TestInitPersistentResourcesSingleWriter(t *testing.T) {
	app := &App{cacheDir: t.TempDir()}
	if err := app.initPersistentResources(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer app.db.Close()
	defer app.storeContainer.Close()
	if got := app.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestPersistWorkerStops(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	app.startPersistWorker()
	app.stopPersistWorker()
	app.stopPersistWorker() // idempotent, must not panic
}
