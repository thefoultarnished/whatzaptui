package backend

import "testing"

// Positive: empty means "let WhatsApp pick"; WhatsApp-shaped IDs pass
// through (surrounding spaces trimmed).
func TestParseClientMessageIDAccepts(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "",
		"   ":                      "",
		"3EB0592AC926367B65A1F2":   "3EB0592AC926367B65A1F2",
		" 3EB0592AC926367B65A1F2 ": "3EB0592AC926367B65A1F2",
		"0123456789ABCDEF":         "0123456789ABCDEF",
		"3EB0" + repeatHex(60):     "3EB0" + repeatHex(60),
	} {
		got, err := parseClientMessageID(in)
		if err != nil || string(got) != want {
			t.Errorf("parseClientMessageID(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
}

// Negative: anything that isn't uppercase hex of a sane length is refused,
// so a caller can't smuggle arbitrary strings into a WhatsApp message ID.
func TestParseClientMessageIDRejects(t *testing.T) {
	for _, in := range []string{
		"local-123",
		"3eb0592ac926367b65a1f2",
		"3EB0592AC9",
		"3EB0592AC926367B65A1G2",
		"3EB0 592AC926367B65A1F2",
		"3EB0592AC926367B65A1F2\x00",
		"3EB0" + repeatHex(61),
	} {
		if _, err := parseClientMessageID(in); err == nil {
			t.Errorf("parseClientMessageID(%q) accepted, want error", in)
		}
	}
}

func TestSendExtra(t *testing.T) {
	if got := sendExtra(""); got != nil {
		t.Fatalf("sendExtra(\"\") = %v, want nil (WhatsApp picks the ID)", got)
	}
	got := sendExtra("3EB0592AC926367B65A1F2")
	if len(got) != 1 || got[0].ID != "3EB0592AC926367B65A1F2" {
		t.Fatalf("sendExtra(id) = %+v, want one extra carrying the ID", got)
	}
}

func repeatHex(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = "0123456789ABCDEF"[i%16]
	}
	return string(b)
}
