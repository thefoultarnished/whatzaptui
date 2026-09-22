package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestEnqueueLIDMigrationProcessesInQueue(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()
	_, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('lid_queue_1', 'Queued LID', 1)`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	app.enqueueLIDMigration("lid_queue_1", "pn_queue_1")

	// Wait for the single-worker background queue to process.
	var name string
	var allowed int
	found := false
	for range 50 {
		err := app.db.QueryRow(`SELECT name, allowed FROM chat_permissions WHERE phone = 'pn_queue_1'`).Scan(&name, &allowed)
		if err == nil && name == "Queued LID" && allowed == 1 {
			found = true
			break
		}
		// Yield briefly
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatalf("queued LID migration did not complete as expected")
	}
}

func TestHandleStartMethodValidation(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()

	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/start", nil), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /start status = %d, want 405", rec.Code)
	}
}

func TestHandleResolveLIDPN(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()

	// 1. POST returns 405
	postReq := authorizedRequest(httptest.NewRequest(http.MethodPost, "/resolve/lidpn", nil), app)
	postRec := httptest.NewRecorder()
	app.handler().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /resolve/lidpn status = %d, want 405", postRec.Code)
	}

	// 2. GET without client/store returns 500 lid mapping store unavailable
	noClientReq := authorizedRequest(httptest.NewRequest(http.MethodGet, "/resolve/lidpn?id=15551230001@s.whatsapp.net", nil), app)
	noClientRec := httptest.NewRecorder()
	app.handler().ServeHTTP(noClientRec, noClientReq)
	if noClientRec.Code != http.StatusInternalServerError || !strings.Contains(noClientRec.Body.String(), "lid mapping store unavailable") {
		t.Fatalf("GET /resolve/lidpn status = %d, want 500 lid mapping store unavailable, body=%s", noClientRec.Code, noClientRec.Body.String())
	}
}

func TestHandleSyncContactsAndGroups(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()

	// 1. /sync/contacts GET is 405
	cGet := authorizedRequest(httptest.NewRequest(http.MethodGet, "/sync/contacts", nil), app)
	cGetRec := httptest.NewRecorder()
	app.handler().ServeHTTP(cGetRec, cGet)
	if cGetRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /sync/contacts status = %d, want 405", cGetRec.Code)
	}

	// 2. /sync/contacts POST when disconnected returns 409
	cPost := authorizedRequest(httptest.NewRequest(http.MethodPost, "/sync/contacts", nil), app)
	cPostRec := httptest.NewRecorder()
	app.handler().ServeHTTP(cPostRec, cPost)
	if cPostRec.Code != http.StatusConflict {
		t.Fatalf("POST /sync/contacts disconnected status = %d, want 409", cPostRec.Code)
	}

	// 3. /sync/groups GET is 405
	gGet := authorizedRequest(httptest.NewRequest(http.MethodGet, "/sync/groups", nil), app)
	gGetRec := httptest.NewRecorder()
	app.handler().ServeHTTP(gGetRec, gGet)
	if gGetRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /sync/groups status = %d, want 405", gGetRec.Code)
	}

	// 4. /sync/groups POST when disconnected returns 409
	gPost := authorizedRequest(httptest.NewRequest(http.MethodPost, "/sync/groups", nil), app)
	gPostRec := httptest.NewRecorder()
	app.handler().ServeHTTP(gPostRec, gPost)
	if gPostRec.Code != http.StatusConflict {
		t.Fatalf("POST /sync/groups disconnected status = %d, want 409", gPostRec.Code)
	}
}

func TestHandleProfilePictureAndMediaDownloadValidation(t *testing.T) {
	app := newTestApp(t)
	defer app.db.Close()

	// Profile picture when disconnected returns 409
	pReq := authorizedRequest(httptest.NewRequest(http.MethodGet, "/profile-picture?jid=15551230001@s.whatsapp.net", nil), app)
	pRec := httptest.NewRecorder()
	app.handler().ServeHTTP(pRec, pReq)
	if pRec.Code != http.StatusConflict {
		t.Fatalf("GET /profile-picture disconnected status = %d, want 409", pRec.Code)
	}

	// Media download when disconnected returns 409
	mReq := authorizedRequest(httptest.NewRequest(http.MethodGet, "/media/download?chatId=c1&msgId=m1", nil), app)
	mRec := httptest.NewRecorder()
	app.handler().ServeHTTP(mRec, mReq)
	if mRec.Code != http.StatusConflict {
		t.Fatalf("GET /media/download disconnected status = %d, want 409", mRec.Code)
	}
}
