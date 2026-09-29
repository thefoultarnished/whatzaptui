package backend

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestOwnPresenceHiddenByDefault(t *testing.T) {
	app := newTestApp(t)
	if got := app.ownPresenceState(); got != types.PresenceUnavailable {
		t.Fatalf("default presence = %q, want unavailable", got)
	}
	app.setShowOnline(true)
	if got := app.ownPresenceState(); got != types.PresenceAvailable {
		t.Fatalf("presence after ON = %q, want available", got)
	}
	app.setShowOnline(false)
	if got := app.ownPresenceState(); got != types.PresenceUnavailable {
		t.Fatalf("presence after OFF = %q, want unavailable", got)
	}
}

func TestHandlePresenceStoresSettingWhenNotConnected(t *testing.T) {
	app := newTestApp(t)
	post := func(body string) *httptest.ResponseRecorder {
		req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/presence", bytes.NewBufferString(body)), app)
		rec := httptest.NewRecorder()
		app.handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"online":true}`); rec.Code != http.StatusOK || !app.wantsOnline() {
		t.Fatalf("ON: status=%d wantsOnline=%v", rec.Code, app.wantsOnline())
	}
	if rec := post(`{"online":false}`); rec.Code != http.StatusOK || app.wantsOnline() {
		t.Fatalf("OFF: status=%d wantsOnline=%v", rec.Code, app.wantsOnline())
	}
}

func TestHandlePresenceValidation(t *testing.T) {
	app := newTestApp(t)
	for name, tc := range map[string]struct {
		method, body string
		want         int
	}{
		"missing online": {http.MethodPost, `{}`, http.StatusBadRequest},
		"bad json":       {http.MethodPost, `nope`, http.StatusBadRequest},
		"wrong method":   {http.MethodGet, ``, http.StatusMethodNotAllowed},
	} {
		req := authorizedRequest(httptest.NewRequest(tc.method, "/presence", bytes.NewBufferString(tc.body)), app)
		rec := httptest.NewRecorder()
		app.handler().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestHandlePresenceSubscribeNeedsConnection(t *testing.T) {
	app := newTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/presence/subscribe", bytes.NewBufferString(`{"chatId":"15551230001@s.whatsapp.net"}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestWirePresence(t *testing.T) {
	app := newTestApp(t)
	jid := types.NewJID("15551230001", types.DefaultUserServer)

	online := app.wirePresence(&events.Presence{From: jid})
	if online["online"] != true || online["lastSeen"] != int64(0) || online["chatId"] != jid.String() {
		t.Fatalf("online payload = %v", online)
	}

	seen := time.Unix(1710000000, 0)
	off := app.wirePresence(&events.Presence{From: jid, Unavailable: true, LastSeen: seen})
	if off["online"] != false || off["lastSeen"] != int64(1710000000) {
		t.Fatalf("offline payload = %v", off)
	}

	hidden := app.wirePresence(&events.Presence{From: jid, Unavailable: true})
	if hidden["online"] != false || hidden["lastSeen"] != int64(0) {
		t.Fatalf("hidden last seen payload = %v", hidden)
	}
}
