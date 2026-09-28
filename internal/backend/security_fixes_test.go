package backend

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"go.mau.fi/whatsmeow/types"
)

func TestBlockFailedErrRedactsPhoneNumbers(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	alt := types.NewJID("98765432109876", types.HiddenUserServer)
	cause := errors.New("server said no")

	err := blockFailedErr(jid, alt, cause)
	msg := err.Error()
	for _, raw := range []string{"15551234567", "98765432109876"} {
		if strings.Contains(msg, raw) {
			t.Fatalf("block error leaks %q: %s", raw, msg)
		}
	}
	if !errors.Is(err, cause) {
		t.Fatal("block error should wrap the cause")
	}

	noAlt := blockFailedErr(jid, types.JID{}, cause).Error()
	if !strings.Contains(noAlt, "alt: none") {
		t.Fatalf("missing alt JID should read as none: %s", noAlt)
	}
}

func TestCapCaption(t *testing.T) {
	short := "hello"
	if got := capCaption(short); got != short {
		t.Fatalf("short caption changed: %q", got)
	}
	exact := strings.Repeat("a", maxCaptionRunes)
	if got := capCaption(exact); got != exact {
		t.Fatal("caption at the limit should be kept whole")
	}
	// Multi-byte runes: the cap counts characters, not bytes, and never
	// splits a character.
	long := strings.Repeat("é", maxCaptionRunes+50)
	got := capCaption(long)
	if n := utf8.RuneCountInString(got); n != maxCaptionRunes {
		t.Fatalf("capped caption has %d runes, want %d", n, maxCaptionRunes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("capped caption is not valid UTF-8")
	}
}
