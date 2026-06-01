package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
	"go.mau.fi/whatsmeow/types"
	waProto "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types/events"
)

func TestCanonicalChatIDDeviceID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"123456789:1@s.whatsapp.net", "123456789@s.whatsapp.net"},
		{"123456789.0:2@s.whatsapp.net", "123456789@s.whatsapp.net"},
		{"987654321@s.whatsapp.net", "987654321@s.whatsapp.net"},
		{"120363040000001234@g.us", "120363040000001234@g.us"},
		{"status@broadcast", "status@broadcast"},
	}

	for _, tc := range tests {
		got := canonicalChatID(tc.input)
		if got != tc.expected {
			t.Errorf("canonicalChatID(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestMarkChatAsReadEventHandler(t *testing.T) {
	app := newTestApp(t)
	chatID := "123456789@s.whatsapp.net"
	app.state.Chats[chatID] = Chat{
		ID:          chatID,
		UnreadCount: 5,
	}
	app.connected = true

	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{}
	header.Set(authHeaderName, "Bearer "+app.apiToken)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}

	jid, _ := types.ParseJID(chatID)
	evt := &events.MarkChatAsRead{
		JID:       jid,
		Timestamp: time.Now(),
		Action: &waProto.MarkChatAsReadAction{
			Read: proto.Bool(true),
		},
	}

	handler := func(evt interface{}) {
		switch v := evt.(type) {
		case *events.MarkChatAsRead:
			cID := app.canonicalizeChatID(v.JID.String())
			if cID != "" && v.Action != nil && v.Action.GetRead() {
				app.mu.Lock()
				chat := app.state.Chats[cID]
				chat.ID = cID
				chat.UnreadCount = 0
				app.state.Chats[cID] = chat
				app.mu.Unlock()
				_ = app.persistStateWithErr()
				app.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		}
	}

	handler(evt)

	app.mu.RLock()
	unread := app.state.Chats[chatID].UnreadCount
	app.mu.RUnlock()

	if unread != 0 {
		t.Errorf("expected UnreadCount = 0, got %d", unread)
	}

	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read expected broadcast: %v", err)
	}
	if !strings.Contains(string(msg), "chats:loaded") {
		t.Errorf("expected broadcast to contain 'chats:loaded', got %q", string(msg))
	}
}

func TestReceiptTypeReadSelfEventHandler(t *testing.T) {
	app := newTestApp(t)
	chatID := "123456789@s.whatsapp.net"
	app.state.Chats[chatID] = Chat{
		ID:          chatID,
		UnreadCount: 3,
	}
	app.connected = true

	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := http.Header{}
	header.Set(authHeaderName, "Bearer "+app.apiToken)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}

	jid, _ := types.ParseJID(chatID)
	evt := &events.Receipt{
		MessageSource: types.MessageSource{
			Chat: jid,
		},
		Type:       types.ReceiptTypeReadSelf,
		MessageIDs: []types.MessageID{"msg1"},
	}

	handler := func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Receipt:
			cID := app.canonicalizeChatID(v.Chat.String())
			if v.Type == types.ReceiptTypeReadSelf {
				app.mu.Lock()
				chat := app.state.Chats[cID]
				chat.ID = cID
				chat.UnreadCount = 0
				app.state.Chats[cID] = chat
				app.mu.Unlock()
				_ = app.persistStateWithErr()
				app.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		}
	}

	handler(evt)

	app.mu.RLock()
	unread := app.state.Chats[chatID].UnreadCount
	app.mu.RUnlock()

	if unread != 0 {
		t.Errorf("expected UnreadCount = 0, got %d", unread)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read expected broadcast: %v", err)
	}
	if !strings.Contains(string(msg), "chats:loaded") {
		t.Errorf("expected broadcast to contain 'chats:loaded', got %q", string(msg))
	}
}
