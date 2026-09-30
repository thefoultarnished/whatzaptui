package backend

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestForwardableText(t *testing.T) {
	cases := []struct {
		name string
		msg  map[string]any
		want string
		ok   bool
	}{
		{"plain text", map[string]any{"conversation": "hi"}, "hi", true},
		{"extended text", map[string]any{"extendedTextMessage": map[string]any{"text": "quoted reply"}}, "quoted reply", true},
		{"already forwarded", map[string]any{"extendedTextMessage": map[string]any{"text": "again", "forwarded": true}}, "again", true},
		{"blank text", map[string]any{"conversation": "   "}, "", false},
		{"image", map[string]any{"imageMessage": map[string]any{"caption": "pic"}}, "", false},
		{"reaction", map[string]any{"reactionMessage": map[string]any{"emoji": "fire"}}, "", false},
		{"empty", map[string]any{}, "", false},
	}
	for _, c := range cases {
		got, ok := forwardableText(c.msg)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: forwardableText = %q,%v want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func forwardTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	// Bob (15550000002) is whitelisted, Cat (15550000003) is not.
	if _, err := app.db.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('15550000002', 'Bob', 1)`); err != nil {
		t.Fatal(err)
	}
	app.upsertMessage("15550000001@s.whatsapp.net", WireMessage{
		Key:              WireKey{ID: "text1", RemoteJID: "15550000001@s.whatsapp.net"},
		Message:          map[string]any{"conversation": "lunch at noon?"},
		MessageTimestamp: 100,
	})
	app.upsertMessage("15550000001@s.whatsapp.net", WireMessage{
		Key:              WireKey{ID: "img1", RemoteJID: "15550000001@s.whatsapp.net"},
		Message:          map[string]any{"imageMessage": map[string]any{"caption": "a photo"}},
		MessageTimestamp: 101,
	})
	return app
}

func TestResolveForwardReturnsTheStoredText(t *testing.T) {
	app := forwardTestApp(t)
	text, to, jid, status, msg := app.resolveForward("15550000001@s.whatsapp.net", "text1", "15550000002@s.whatsapp.net")
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want success", status, msg)
	}
	if text != "lunch at noon?" || to != "15550000002@s.whatsapp.net" || jid.User != "15550000002" {
		t.Fatalf("got text=%q to=%q jid=%v", text, to, jid)
	}
}

func TestResolveForwardRefusals(t *testing.T) {
	app := forwardTestApp(t)
	cases := []struct {
		name         string
		from, id, to string
		wantStatus   int
	}{
		{"target not whitelisted", "15550000001@s.whatsapp.net", "text1", "15550000003@s.whatsapp.net", http.StatusForbidden},
		{"message not found", "15550000001@s.whatsapp.net", "nope", "15550000002@s.whatsapp.net", http.StatusNotFound},
		{"wrong source chat", "15550000009@s.whatsapp.net", "text1", "15550000002@s.whatsapp.net", http.StatusNotFound},
		{"not a text message", "15550000001@s.whatsapp.net", "img1", "15550000002@s.whatsapp.net", http.StatusBadRequest},
		{"missing target", "15550000001@s.whatsapp.net", "text1", "", http.StatusBadRequest},
		{"missing message id", "15550000001@s.whatsapp.net", "", "15550000002@s.whatsapp.net", http.StatusBadRequest},
		{"missing source", "", "text1", "15550000002@s.whatsapp.net", http.StatusBadRequest},
		{"status broadcast", "15550000001@s.whatsapp.net", "text1", "status@broadcast", http.StatusBadRequest},
	}
	for _, c := range cases {
		text, _, _, status, msg := app.resolveForward(c.from, c.id, c.to)
		if status != c.wantStatus {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.wantStatus)
		}
		if text != "" {
			t.Errorf("%s: nothing must be forwarded on failure, got %q", c.name, text)
		}
	}
}

func TestHandleForwardMessageNeedsConnectionAndPost(t *testing.T) {
	app := forwardTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/forward", bytes.NewBufferString(`{"fromChatId":"a","messageId":"b","toChatId":"c"}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST without a connected client = %d, want 409", rec.Code)
	}
	req = authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages/forward", nil), app)
	rec = httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}
}

func TestReceivedForwardedMessageIsFlaggedOnTheWire(t *testing.T) {
	app := newTestApp(t)
	forwarded := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String("passed along"),
		ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true)},
	}}
	wire, _ := app.wireMessagePayload(forwarded, forwarded, "15550000001@s.whatsapp.net", false)
	ext, _ := wire["extendedTextMessage"].(map[string]any)
	if ext == nil || ext["text"] != "passed along" || ext["forwarded"] != true {
		t.Fatalf("wire payload = %v, want text and forwarded=true", wire)
	}

	plain := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String("just a link"),
		ContextInfo: &waE2E.ContextInfo{},
	}}
	wire, _ = app.wireMessagePayload(plain, plain, "15550000001@s.whatsapp.net", false)
	if ext, _ := wire["extendedTextMessage"].(map[string]any); ext == nil || ext["forwarded"] != nil {
		t.Fatalf("a normal message must not be flagged forwarded: %v", wire)
	}
}
