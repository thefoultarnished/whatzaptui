package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGroupMembersRequiresConnectedClient(t *testing.T) {
	app := newTestApp(t)
	// No client connected; must return 409 Conflict without panicking
	req := authorizedRequest(httptest.NewRequest(http.MethodGet, "/group/members?jid=120363040000001234@g.us", nil), app)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 Conflict (not connected), body=%s", rec.Code, rec.Body.String())
	}
}
