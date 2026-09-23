package backend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Non-whitelisted chats must be rejected with 403 before any connection
// or revoke work happens (mirrors handleEditMessage).
func TestHandleDeleteMessageWhitelist(t *testing.T) {
	app := newTestApp(t)
	post := func(body string) *httptest.ResponseRecorder {
		req := authorizedRequest(
			httptest.NewRequest(http.MethodPost, "/messages/delete", strings.NewReader(body)),
			app,
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		app.handler().ServeHTTP(rec, req)
		return rec
	}

	if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230001', 'Alex', 0)`); err != nil {
		t.Fatalf("seed deny row: %v", err)
	}
	rec := post(`{"chatId":"15551230001@s.whatsapp.net","messageId":"MSG1","fromMe":true}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("denied chat status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}

	// Allowed chat passes the whitelist gate and is only stopped by the
	// missing live connection (409) — proving the 403 gate was passed.
	if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15551230002', 'Sam', 1)`); err != nil {
		t.Fatalf("seed allow row: %v", err)
	}
	rec = post(`{"chatId":"15551230002@s.whatsapp.net","messageId":"MSG2","fromMe":false}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("allowed chat status = %d, want 409, body=%s", rec.Code, rec.Body.String())
	}
}
