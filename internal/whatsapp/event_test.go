package whatsapp

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

func TestReceiptStatusFromType(t *testing.T) {
	cases := []struct {
		in   types.ReceiptType
		want string
	}{
		{types.ReceiptTypeDelivered, "delivered"},
		{types.ReceiptTypeSender, "delivered"},
		{types.ReceiptTypeRead, "read"},
		{types.ReceiptTypeReadSelf, "read"},
		{types.ReceiptTypePlayed, "played"},
		{types.ReceiptTypePlayedSelf, "played"},
		{types.ReceiptTypeServerError, ""},
	}

	for _, c := range cases {
		if got := ReceiptStatusFromType(c.in); got != c.want {
			t.Errorf("ReceiptStatusFromType(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildCallMeta(t *testing.T) {
	creator, _ := types.ParseJID("15551234567@s.whatsapp.net")
	group, _ := types.ParseJID("120363000000000000@g.us")
	meta := types.BasicCallMeta{
		CallCreator: creator,
		GroupJID:    group,
		CallID:      "call_xyz_123",
	}

	call := BuildCallMeta("incoming", meta, "audio", "", func(s string) string {
		return strings.ToUpper(s)
	})

	if call.Status != "incoming" {
		t.Errorf("call.Status = %q, want 'incoming'", call.Status)
	}
	if call.CallerID != strings.ToUpper(creator.String()) {
		t.Errorf("call.CallerID = %q, want %q", call.CallerID, strings.ToUpper(creator.String()))
	}
	if call.GroupID != strings.ToUpper(group.String()) {
		t.Errorf("call.GroupID = %q, want %q", call.GroupID, strings.ToUpper(group.String()))
	}
	if call.CallID != "call_xyz_123" {
		t.Errorf("call.CallID = %q, want 'call_xyz_123'", call.CallID)
	}
	if call.Media != "audio" {
		t.Errorf("call.Media = %q, want 'audio'", call.Media)
	}
}

func TestIsMessageEditAndRevoke(t *testing.T) {
	editPM := &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		EditedMessage: &waE2E.Message{Conversation: proto.String("new")},
	}
	if !IsMessageEdit(editPM) {
		t.Errorf("IsMessageEdit should report true for MESSAGE_EDIT with EditedMessage")
	}

	revokePM := &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_REVOKE.Enum(),
	}
	if !IsMessageRevoke(revokePM) {
		t.Errorf("IsMessageRevoke should report true for REVOKE")
	}
	if IsMessageEdit(revokePM) {
		t.Errorf("IsMessageEdit should report false for REVOKE")
	}
}
