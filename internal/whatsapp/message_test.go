package whatsapp

import (
	"strings"
	"testing"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestBuildTextMessage(t *testing.T) {
	// Plain text
	m1 := BuildTextMessage("hello world", "", "", "")
	if m1.GetConversation() != "hello world" {
		t.Fatalf("expected conversation 'hello world', got %q", m1.GetConversation())
	}
	if m1.GetExtendedTextMessage() != nil {
		t.Fatalf("expected no extendedTextMessage for plain text")
	}

	// Quoted text
	m2 := BuildTextMessage("my reply", "stanza123", "sender456", "quoted original")
	ext := m2.GetExtendedTextMessage()
	if ext == nil {
		t.Fatalf("expected extendedTextMessage for reply")
	}
	if ext.GetText() != "my reply" {
		t.Fatalf("expected reply text 'my reply', got %q", ext.GetText())
	}
	ctx := ext.GetContextInfo()
	if ctx == nil {
		t.Fatalf("expected contextInfo")
	}
	if ctx.GetStanzaID() != "stanza123" {
		t.Fatalf("expected stanza ID 'stanza123', got %q", ctx.GetStanzaID())
	}
	if ctx.GetParticipant() != "sender456" {
		t.Fatalf("expected participant 'sender456', got %q", ctx.GetParticipant())
	}
	if ctx.GetQuotedMessage().GetConversation() != "quoted original" {
		t.Fatalf("expected quoted conversation 'quoted original', got %q", ctx.GetQuotedMessage().GetConversation())
	}
}

func TestEffectiveMessage(t *testing.T) {
	inner := &waE2E.Message{Conversation: proto.String("deep inside")}
	wrapped := &waE2E.Message{
		EphemeralMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				DeviceSentMessage: &waE2E.DeviceSentMessage{
					Message: inner,
				},
			},
		},
	}

	res := EffectiveMessage(wrapped)
	if res == nil || res.GetConversation() != "deep inside" {
		t.Fatalf("failed to unwrap effective message: got %+v", res)
	}
}

func TestQuotedText(t *testing.T) {
	tests := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"text", &waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("long")}}, "long"},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}, "[image]"},
		{"video", &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}, "[video]"},
		{"document with name", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("test.pdf")}}, "[file: test.pdf]"},
		{"document without name", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{}}, "[document]"},
		{"ptt audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true)}}, "[voice]"},
		{"normal audio", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(false)}}, "[audio]"},
		{"sticker", &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, "[sticker]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := QuotedText(tc.msg); got != tc.want {
				t.Errorf("QuotedText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractSearchableText(t *testing.T) {
	payload := map[string]any{
		"conversation": "base text",
		"extendedTextMessage": map[string]any{
			"text":       "more text",
			"quotedText": "quoted context",
		},
		"documentMessage": map[string]any{
			"caption":  "read this",
			"fileName": "report.pdf",
		},
	}

	searchable := ExtractSearchableText(payload)
	for _, term := range []string{"base text", "more text", "quoted context", "read this", "report.pdf"} {
		if !strings.Contains(searchable, term) {
			t.Errorf("ExtractSearchableText missing expected term %q in %q", term, searchable)
		}
	}
}

func TestVisibleProtocolType(t *testing.T) {
	if !VisibleProtocolType(waE2E.ProtocolMessage_REVOKE) {
		t.Errorf("REVOKE should be visible")
	}
	if !VisibleProtocolType(waE2E.ProtocolMessage_MESSAGE_EDIT) {
		t.Errorf("MESSAGE_EDIT should be visible")
	}
	if !VisibleProtocolType(waE2E.ProtocolMessage_EPHEMERAL_SETTING) {
		t.Errorf("EPHEMERAL_SETTING should be visible")
	}
	if VisibleProtocolType(waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION) {
		t.Errorf("HISTORY_SYNC_NOTIFICATION should not be visible")
	}
}

func TestIsInvisibleProtocolMessage(t *testing.T) {
	revokeMsg := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		},
	}
	if IsInvisibleProtocolMessage(revokeMsg) {
		t.Errorf("revoke should not be considered invisible")
	}

	syncMsg := &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type: waE2E.ProtocolMessage_HISTORY_SYNC_NOTIFICATION.Enum(),
		},
	}
	if !IsInvisibleProtocolMessage(syncMsg) {
		t.Errorf("history sync notification should be considered invisible")
	}
}
