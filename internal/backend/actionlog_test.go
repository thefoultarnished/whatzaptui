package backend

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Tests for the action log file writer and its redactors. These tests
// are about the log structure and redaction guarantees, not about the
// wider app — they never touch the DB or the whatsmeow client.

func TestActionLogRedactsBearerToken(t *testing.T) {
	dir := t.TempDir()
	tok := "supersecrettoken-do-not-keep"
	a, err := openActionLog(dir, time.Now(), func() string { return tok })
	if err != nil {
		t.Fatalf("openActionLog: %v", err)
	}
	defer a.Close()
	a.Event("test.event", map[string]string{
		"line": "Authorization: Bearer " + tok,
	})
	body, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(body), tok) {
		t.Fatalf("token leaked into log: %q", string(body))
	}
	if !strings.Contains(string(body), redactPlaceholder) {
		t.Fatalf("redaction marker missing from log: %q", string(body))
	}
}

func TestActionLogOwnerOnly(t *testing.T) {
	if os.Getenv("GOOS_"+os.Getenv("USER")) == "_windows" || os.PathSeparator == '\\' {
		// Mode bits on Windows are advisory for ACLs; the test asserts
		// only the Unix case to avoid platform-flake.
		t.Skip("file mode is enforced via ACL on Windows")
	}
	dir := t.TempDir()
	a, err := openActionLog(dir, time.Now(), nil)
	if err != nil {
		t.Fatalf("openActionLog: %v", err)
	}
	defer a.Close()
	info, err := os.Stat(a.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("log mode = %o, want owner-only", info.Mode().Perm())
	}
}

func TestActionLogRedactPhoneIsStable(t *testing.T) {
	// Same phone number should always produce the same prefix so an
	// investigator can grep the same contact across multiple events
	// without ever recovering the digits.
	a := intPtr("15551230001")
	b := intPtr("15551230001")
	c := intPtr("15551230002")
	if redactPhone(*a) != redactPhone(*b) {
		t.Fatalf("stable prefix for same phone violated: %q vs %q",
			redactPhone(*a), redactPhone(*b))
	}
	if redactPhone(*a) == redactPhone(*c) {
		t.Fatalf("distinct phones collided: %q vs %q",
			redactPhone(*a), redactPhone(*c))
	}
	if !strings.HasPrefix(redactPhone(*a), "phone:") {
		t.Fatalf("prefix missing: %q", redactPhone(*a))
	}
}

func TestActionLogRedactChatIDClass(t *testing.T) {
	cases := map[string]string{
		"":                            "chat:empty",
		"status@broadcast":            "chat:status",
		"120363419339396825@g.us":     "chat:group",
		"15551230001@s.whatsapp.net":  "", // compare separately
	}
	for in, want := range cases {
		if want == "" {
			continue
		}
		got := redactChatID(in)
		if got != want {
			t.Errorf("redactChatID(%q) = %q, want %q", in, got, want)
		}
	}
	ind := redactChatID("15551230001@s.whatsapp.net")
	if !strings.HasPrefix(ind, "chat:phone:") {
		t.Errorf("individual chat prefix wrong: %q", ind)
	}
	if strings.Contains(ind, "15551230001") {
		t.Errorf("phone digits leaked: %q", ind)
	}
}

func TestActionLogRedactPushName(t *testing.T) {
	if redactPushName("") != "" {
		t.Errorf("empty pushname should be empty, got %q", redactPushName(""))
	}
	a := redactPushName("Alice")
	b := redactPushName("Alice")
	c := redactPushName("Bob")
	if a != b {
		t.Errorf("stable prefix for same pushname violated: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("distinct pushnames collided: %q vs %q", a, c)
	}
	if !strings.HasPrefix(a, "name:") {
		t.Errorf("prefix wrong: %q", a)
	}
}

func TestActionLogMessageKind(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want string
	}{
		{nil, "unknown"},
		{map[string]any{"conversation": "hi"}, "text"},
		{map[string]any{"imageMessage": map[string]any{}}, "image"},
		{map[string]any{"videoMessage": map[string]any{}}, "video"},
		{map[string]any{"audioMessage": map[string]any{}}, "audio"},
		{map[string]any{"documentMessage": map[string]any{}}, "document"},
		{map[string]any{"documentWithCaptionMessage": map[string]any{}}, "document"},
		{map[string]any{"stickerMessage": map[string]any{}}, "sticker"},
		{map[string]any{"contactMessage": map[string]any{}}, "contact"},
		{map[string]any{"locationMessage": map[string]any{}}, "location"},
		{map[string]any{"pollCreationMessage": map[string]any{}}, "poll"},
		{map[string]any{"reactionMessage": map[string]any{}}, "reaction"},
		{map[string]any{"protocolMessage": map[string]any{}}, "protocol"},
	}
	for _, c := range cases {
		if got := redactMessageKind(c.in); got != c.want {
			t.Errorf("redactMessageKind(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestActionLogTimedEmitsStartAndEnd(t *testing.T) {
	dir := t.TempDir()
	a, err := openActionLog(dir, time.Now(), nil)
	if err != nil {
		t.Fatalf("openActionLog: %v", err)
	}
	defer a.Close()
	finish := a.Timed("test.timed", map[string]string{"k": "v"})
	finish("ok", map[string]string{"extra": "yes"})
	body, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(body), "test.timed.start") {
		t.Errorf("missing start: %q", string(body))
	}
	if !strings.Contains(string(body), "test.timed.end") {
		t.Errorf("missing end: %q", string(body))
	}
	if !strings.Contains(string(body), "elapsedMs=") {
		t.Errorf("missing elapsedMs: %q", string(body))
	}
	if !strings.Contains(string(body), "status=ok") {
		t.Errorf("missing status: %q", string(body))
	}
}
// intPtr returns a pointer to its argument; the callers above want a
// string value they can pass to redactPhone via a dereference so the
// test reads naturally without an extra variable inside the table.
func intPtr(s string) *string { return &s }

// TestScrubTokenKeepsLength semantically the same when no token matches.
// Spot-checks the line is untouched so a non-token substring in the same
// payload (e.g. a hex-encoded hash that happens to overlap) cannot be
// silently mutated.
func TestScrubTokenKeepsNonTokenBytes(t *testing.T) {
	want := "Authorization header summary: rejected (len=42)"
	got := string(scrubToken([]byte(want), "no-such-token-anywhere"))
	if got != want {
		t.Errorf("scrubToken modified unrelated bytes: got %q want %q", got, want)
	}
}
