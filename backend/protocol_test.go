package main

import (
	"strconv"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func protoMsgWithType(t *testing.T, typ waE2E.ProtocolMessage_Type) *waE2E.Message {
	t.Helper()
	return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: typ.Enum()}}
}

func TestIsInvisibleProtocolMessage(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want bool
	}{
		{"nil", nil, false},
		{"text", &waE2E.Message{Conversation: proto.String("hi")}, false},
		{"history sync notification", protoMsgWithType(t, waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION), true},
		{"key share", protoMsgWithType(t, waE2E.ProtocolMessage_APP_STATE_SYNC_KEY_SHARE), true},
		{"fanout request", protoMsgWithType(t, waE2E.ProtocolMessage_MSG_FANOUT_BACKFILL_REQUEST), true},
		{"revoke stays", protoMsgWithType(t, waE2E.ProtocolMessage_REVOKE), false},
		{"edit stays", protoMsgWithType(t, waE2E.ProtocolMessage_MESSAGE_EDIT), false},
		{"ephemeral setting stays", protoMsgWithType(t, waE2E.ProtocolMessage_EPHEMERAL_SETTING), false},
	}
	for _, c := range cases {
		if got := isInvisibleProtocolMessage(c.msg); got != c.want {
			t.Errorf("%s: isInvisibleProtocolMessage = %v, want %v", c.name, got, c.want)
		}
	}
}

func insertProtocolRow(t *testing.T, app *App, chatID, msgID, typ string) {
	t.Helper()
	msgJSON := `{"protocolMessage":{"type":` + strconv.Quote(typ) + `}}`
	if _, err := app.db.Exec(`INSERT OR IGNORE INTO messages (id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto)
		VALUES (?, ?, 0, '', 1, '', '', ?, '')`, msgID, chatID, msgJSON); err != nil {
		t.Fatalf("insert protocol row: %v", err)
	}
}

func TestPurgeInvisibleProtocolMessages(t *testing.T) {
	app := newTestApp(t)
	insertProtocolRow(t, app, "c1", "sys1", "HISTORY_SYNC_NOTIFICATION")
	insertProtocolRow(t, app, "c1", "sys2", "APP_STATE_SYNC_KEY_SHARE")
	insertProtocolRow(t, app, "c1", "del1", "REVOKE")
	insertFTSFixture(t, app, "c1", "txt1", 1, "real message")

	app.purgeInvisibleProtocolMessages()

	mustCount := func(msgID string) int {
		var c int
		if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = ?`, msgID).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", msgID, err)
		}
		return c
	}
	if c := mustCount("sys1"); c != 0 {
		t.Errorf("history sync notification kept (%d rows), want purged", c)
	}
	if c := mustCount("sys2"); c != 0 {
		t.Errorf("key share kept (%d rows), want purged", c)
	}
	if c := mustCount("del1"); c != 1 {
		t.Errorf("revoke kept = %d rows, want 1", c)
	}
	if c := mustCount("txt1"); c != 1 {
		t.Errorf("normal message kept = %d rows, want 1", c)
	}

	// Second run is a no-op (flag set).
	app.purgeInvisibleProtocolMessages()
	if c := mustCount("del1"); c != 1 {
		t.Errorf("revoke after rerun = %d rows, want 1", c)
	}
}
