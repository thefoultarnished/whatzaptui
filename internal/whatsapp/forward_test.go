package whatsapp

import "testing"

func TestBuildForwardedTextMessageIsMarkedForwarded(t *testing.T) {
	msg := BuildForwardedTextMessage("hello there")
	ext := msg.GetExtendedTextMessage()
	if ext == nil || ext.GetText() != "hello there" {
		t.Fatalf("expected an extended text message with the text, got %+v", msg)
	}
	ctx := ext.GetContextInfo()
	if ctx == nil || !ctx.GetIsForwarded() {
		t.Fatalf("message must be marked forwarded, context = %+v", ctx)
	}
	if ctx.GetForwardingScore() != 1 {
		t.Fatalf("forwarding score = %d, want 1", ctx.GetForwardingScore())
	}
	if ctx.GetQuotedMessage() != nil || ctx.GetStanzaID() != "" {
		t.Fatalf("a forward must not quote anything: %+v", ctx)
	}
}
