package tui

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
)

// --- Window title (bug: only ESC/BEL were stripped) ---

// Negative: every control character from a chat name is removed, not just
// ESC and BEL, and newlines collapse to spaces.
func TestCleanWindowTitleStripsAllControlChars(t *testing.T) {
	in := "Ali\x1b]0;evil\x07ce\b\v\x9b\x85 (2)\nBob\r\nCarl"
	got := cleanWindowTitle(in)
	if strings.ContainsFunc(got, unicode.IsControl) {
		t.Fatalf("title still has control chars: %q", got)
	}
	for _, want := range []string{"Ali", "ce", "(2)", "Bob", "Carl"} {
		if !strings.Contains(got, want) {
			t.Fatalf("title %q lost visible text %q", got, want)
		}
	}
}

// Positive: a normal title passes through unchanged.
func TestCleanWindowTitleKeepsPlainTitle(t *testing.T) {
	if got := cleanWindowTitle("WhatZap (3) Alice, Bob"); got != "WhatZap (3) Alice, Bob" {
		t.Fatalf("plain title changed to %q", got)
	}
}

// Edge: a title that is only control characters or spaces falls back.
func TestCleanWindowTitleFallsBack(t *testing.T) {
	for _, in := range []string{"", "   ", "\x1b\x07\b", "\n\n"} {
		if got := cleanWindowTitle(in); got != "WhatZap" {
			t.Errorf("cleanWindowTitle(%q) = %q, want WhatZap", in, got)
		}
	}
}

// --- Request building (bug: `req, _ :=` then crash on a nil request) ---

// Negative: a request that can't be built returns an error instead of
// panicking on a nil request.
func TestDoAPIRequestBadURLReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("doAPIRequest panicked: %v", r)
		}
	}()
	for _, u := range []string{"http://[::1", "://nohost", "http://bad host/x"} {
		res, err := doAPIRequest(context.Background(), http.DefaultClient, http.MethodGet, u, nil, "tok")
		if err == nil {
			res.Body.Close()
			t.Errorf("url %q: expected an error", u)
		}
	}
	if _, err := doAPIRequest(context.Background(), http.DefaultClient, "BAD METHOD", "http://127.0.0.1/", nil, ""); err == nil {
		t.Error("invalid method: expected an error")
	}
}

// Negative: the same failure surfaces through a real command as a normal
// error message rather than a crash.
func TestGetChatsBadURLReportsError(t *testing.T) {
	msg := getChats(context.Background(), http.DefaultClient, "http://[::1")()
	cm, ok := msg.(chatsMsg)
	if !ok || cm.err == nil {
		t.Fatalf("getChats with bad URL = %#v, want chatsMsg with an error", msg)
	}
}

// Positive: auth is attached to every request; JSON content-type only when
// there is a body.
func TestDoAPIRequestSetsHeaders(t *testing.T) {
	type seen struct{ auth, ctype, method, body string }
	got := make(chan seen, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		got <- seen{r.Header.Get(authHeaderName), r.Header.Get("content-type"), r.Method, b.String()}
	}))
	defer srv.Close()

	res, err := doAPIRequest(context.Background(), srv.Client(), http.MethodGet, srv.URL+"/chats", nil, "tok")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if s := <-got; s.auth != "Bearer tok" || s.ctype != "" || s.method != http.MethodGet {
		t.Fatalf("GET saw %+v, want auth set and no content-type", s)
	}

	res, err = doAPIRequest(context.Background(), srv.Client(), http.MethodPost, srv.URL+"/x", strings.NewReader(`{"a":1}`), "tok")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if s := <-got; s.auth != "Bearer tok" || s.ctype != "application/json" || s.body != `{"a":1}` {
		t.Fatalf("POST saw %+v, want auth, JSON content-type and the body", s)
	}
}

// Edge: an empty token sends no auth header at all.
func TestDoAPIRequestNoTokenNoAuthHeader(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get(authHeaderName)
	}))
	defer srv.Close()
	res, err := doAPIRequest(context.Background(), srv.Client(), http.MethodGet, srv.URL, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if auth != "" {
		t.Fatalf("auth header = %q, want none", auth)
	}
}

// --- Typed text on send failure (bug: text was lost) ---

func failedSendModel(active string) m {
	return m{
		active:          active,
		msgs:            map[string][]wireMsg{},
		pendingSendText: map[string]string{"local-1": "hello there"},
	}
}

// Positive: a failed send in the open chat puts the text back in the box.
func TestFailedSendRestoresText(t *testing.T) {
	x := failedSendModel("a@s.whatsapp.net")
	res, _ := x.Update(sentMsg{chatID: "a@s.whatsapp.net", pendingID: "local-1", err: errors.New("boom")})
	got := res.(m)
	if got.input != "hello there" {
		t.Fatalf("input = %q, want the failed text restored", got.input)
	}
	if _, ok := got.pendingSendText["local-1"]; ok {
		t.Fatal("pending text not cleared after the failure")
	}
}

// Edge: if the user already typed something new, it is never overwritten.
func TestFailedSendKeepsNewTyping(t *testing.T) {
	for _, tc := range []struct{ input, buf string }{{"new msg", ""}, {"", "n"}} {
		x := failedSendModel("a@s.whatsapp.net")
		x.input, x.inputBuf = tc.input, tc.buf
		res, _ := x.Update(sentMsg{chatID: "a@s.whatsapp.net", pendingID: "local-1", err: errors.New("boom")})
		if got := res.(m); got.input != tc.input {
			t.Fatalf("input = %q, want untouched %q", got.input, tc.input)
		}
	}
}

// Edge: if the user switched chats, the text goes into that chat's draft
// (only when the draft is empty).
func TestFailedSendInOtherChatBecomesDraft(t *testing.T) {
	x := failedSendModel("b@s.whatsapp.net")
	res, _ := x.Update(sentMsg{chatID: "a@s.whatsapp.net", pendingID: "local-1", err: errors.New("boom")})
	got := res.(m)
	if got.drafts["a@s.whatsapp.net"] != "hello there" {
		t.Fatalf("draft = %q, want the failed text", got.drafts["a@s.whatsapp.net"])
	}
	if got.input != "" {
		t.Fatalf("open chat's input changed to %q", got.input)
	}

	x = failedSendModel("b@s.whatsapp.net")
	x.drafts = map[string]string{"a@s.whatsapp.net": "existing draft"}
	res, _ = x.Update(sentMsg{chatID: "a@s.whatsapp.net", pendingID: "local-1", err: errors.New("boom")})
	if d := res.(m).drafts["a@s.whatsapp.net"]; d != "existing draft" {
		t.Fatalf("existing draft overwritten with %q", d)
	}
}

// Negative: a successful send restores nothing and forgets the text.
func TestSuccessfulSendDoesNotRestore(t *testing.T) {
	x := failedSendModel("a@s.whatsapp.net")
	res, _ := x.Update(sentMsg{chatID: "a@s.whatsapp.net", pendingID: "local-1"})
	got := res.(m)
	if got.input != "" {
		t.Fatalf("input = %q after a successful send, want empty", got.input)
	}
	if _, ok := got.pendingSendText["local-1"]; ok {
		t.Fatal("pending text kept after a successful send")
	}
}

// Positive (end to end): sending records the text so a later failure can
// restore it.
func TestComposerSendRecordsPendingText(t *testing.T) {
	x := m{
		active:           "a@s.whatsapp.net",
		msgs:             map[string][]wireMsg{},
		whitelist:        map[string]string{"a": "A"},
		input:            "hi",
		pendingSendArmed: true,
		pendingSendSeq:   1,
	}
	x.whitelist[num(x.active)] = "A"
	res, _ := x.Update(composerSendMsg{seq: 1})
	got := res.(m)
	if len(got.pendingSendText) != 1 {
		t.Fatalf("pendingSendText = %v, want the sent text recorded", got.pendingSendText)
	}
	for _, v := range got.pendingSendText {
		if v != "hi" {
			t.Fatalf("recorded %q, want hi", v)
		}
	}
}
