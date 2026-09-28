package backend

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postHandler(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func permissionName(t *testing.T, app *App, phone string) (string, bool) {
	t.Helper()
	var name string
	err := app.db.QueryRow(`SELECT name FROM chat_permissions WHERE phone = ?`, phone).Scan(&name)
	if err != nil {
		return "", false
	}
	return name, true
}

// Positive: a rename sent as a full JID is stored under the bare number, so
// it lands on the same row as the whitelist entry instead of a duplicate.
func TestSetNameNormalizesPhone(t *testing.T) {
	app := newTestApp(t)
	rec := postHandler(app.handleSetName, `{"phone":"15551230001@s.whatsapp.net","name":"Buddy"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if name, ok := permissionName(t, app, "15551230001"); !ok || name != "Buddy" {
		t.Fatalf("row for bare number = (%q,%v), want (Buddy,true)", name, ok)
	}
	if _, ok := permissionName(t, app, "15551230001@s.whatsapp.net"); ok {
		t.Fatal("rename created a duplicate row keyed by the full JID")
	}
}

// Negative: formatted numbers, empty phones and group IDs are rejected and
// nothing is written.
func TestSetNameRejectsBadPhone(t *testing.T) {
	app := newTestApp(t)
	for _, phone := range []string{"+1 555-1234", "", "abc", "12036300@g.us"} {
		rec := postHandler(app.handleSetName, `{"phone":"`+phone+`","name":"X"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("phone %q: status = %d, want 400", phone, rec.Code)
		}
	}
	var n int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chat_permissions`).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected renames wrote %d rows", n)
	}
}

// Negative: a per-contact whitelist flag other than 0/1 is rejected instead
// of being stored and silently treated as "blocked".
func TestSetWhitelistRejectsInvalidAllowed(t *testing.T) {
	app := newTestApp(t)
	for _, allowed := range []string{"2", "-1", "7"} {
		rec := postHandler(app.handleSetWhitelist, `{"phone":"15551230001","name":"X","allowed":`+allowed+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("allowed=%s: status = %d, want 400", allowed, rec.Code)
		}
	}
	if _, ok := permissionName(t, app, "15551230001"); ok {
		t.Fatal("invalid allowed value was written")
	}
}

// Positive: 0 and 1 are still accepted.
func TestSetWhitelistAcceptsZeroAndOne(t *testing.T) {
	app := newTestApp(t)
	for _, allowed := range []string{"1", "0"} {
		rec := postHandler(app.handleSetWhitelist, `{"phone":"15551230001","name":"X","allowed":`+allowed+`}`)
		if rec.Code != http.StatusOK {
			t.Errorf("allowed=%s: status = %d, want 200 (body=%s)", allowed, rec.Code, rec.Body.String())
		}
	}
}

// Negative/edge: a malformed or non-positive "before" is a 400, not a
// silent empty page.
func TestMessagesRejectsBadBefore(t *testing.T) {
	app := newTestApp(t)
	for _, before := range []string{"abc", "0", "-5", "12.5"} {
		req := httptest.NewRequest(http.MethodGet, "/messages?chatId=15551230001@s.whatsapp.net&before="+before, nil)
		rec := httptest.NewRecorder()
		app.handleMessages(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("before=%q: status = %d, want 400", before, rec.Code)
		}
	}
}

// Positive: a valid "before" and no "before" both still work.
func TestMessagesAcceptsValidBefore(t *testing.T) {
	app := newTestApp(t)
	for _, q := range []string{"&before=1700000000", ""} {
		req := httptest.NewRequest(http.MethodGet, "/messages?chatId=15551230001@s.whatsapp.net"+q, nil)
		rec := httptest.NewRecorder()
		app.handleMessages(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("query %q: status = %d, want 200 (body=%s)", q, rec.Code, rec.Body.String())
		}
	}
}
