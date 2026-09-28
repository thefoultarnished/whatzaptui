package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const matchChat = "15551230001@s.whatsapp.net"

// echo builds WhatsApp's own copy of a message we sent (or one sent from
// another of our devices), as delivered over the websocket.
func echo(id, text string, ts int64) wsEvtMsg {
	wm := wireMsg{MessageTimestamp: ts, ReceiptStatus: "sent"}
	wm.Key.ID = id
	wm.Key.RemoteJID = matchChat
	wm.Key.FromMe = true
	wm.Message = map[string]any{"conversation": text}
	b, _ := json.Marshal(wm)
	return wsEvtMsg{ok: true, evt: env{Type: "message", Payload: b}}
}

func receipt(status string, ids ...string) wsEvtMsg {
	b, _ := json.Marshal(receiptMsg{ChatID: matchChat, MessageIDs: ids, ReceiptStatus: status})
	return wsEvtMsg{ok: true, evt: env{Type: "receipt", Payload: b}}
}

// sendResponse is the backend's reply to a successful send.
func sendResponse(pendingID, realID, text string, ts int64) sentMsg {
	wm := wireMsg{MessageTimestamp: ts, ReceiptStatus: "sent"}
	wm.Key.ID = realID
	wm.Key.RemoteJID = matchChat
	wm.Key.FromMe = true
	wm.Message = map[string]any{"conversation": text}
	return sentMsg{chatID: matchChat, pendingID: pendingID, msg: wm}
}

func update(t *testing.T, x m, msg any) m {
	t.Helper()
	next, _ := x.Update(msg)
	return next.(m)
}

// withPlaceholders seeds one pending placeholder per text, as a real send
// would, and returns their IDs in send order.
func withPlaceholders(texts ...string) (m, []string) {
	x := baseModel(matchChat)
	x.pendingSendText = map[string]string{}
	now := time.Now().Unix()
	ids := make([]string, len(texts))
	for i, txt := range texts {
		ids[i] = newOutgoingMessageID()
		x.msgs[matchChat] = append(x.msgs[matchChat], optimisticOutgoingMessage(matchChat, txt, ids[i], nil))
		x.msgs[matchChat][i].MessageTimestamp = now
		x.pendingSendText[ids[i]] = txt
	}
	return x, ids
}

func bodies(x m) []string {
	var out []string
	for _, msg := range x.msgs[matchChat] {
		out = append(out, renderMessageBody(msg.Message))
	}
	return out
}

func assertBodies(t *testing.T, x m, want ...string) {
	t.Helper()
	got := bodies(x)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

// --- ID generation ---

func TestNewOutgoingMessageIDShapeAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for range 2000 {
		id := newOutgoingMessageID()
		if !isClientMessageID(id) {
			t.Fatalf("generated ID %q is not a valid client message ID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = true
	}
}

func TestIsClientMessageID(t *testing.T) {
	for _, ok := range []string{"3EB0592AC926367B65A1F2", "3EB0000000000000000000"} {
		if !isClientMessageID(ok) {
			t.Errorf("isClientMessageID(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "local-123", "3eb0592ac926367b65a1f2", "3EB0592AC926367B65A1F", "3EB0592AC926367B65A1F2A", "ABCD592AC926367B65A1F2", "3EB0592AC926367B65A1G2", "3EB0 92AC926367B65A1F2"} {
		if isClientMessageID(bad) {
			t.Errorf("isClientMessageID(%q) = true, want false", bad)
		}
	}
}

// --- The original bug: quick successive sends ---

// Regression: WhatsApp's copies arrive in the opposite order to the sends.
// Each must land on its own placeholder — no swap, no duplicate.
func TestQuickSendsEchoesOutOfOrder(t *testing.T) {
	x, ids := withPlaceholders("first", "second")
	now := time.Now().Unix()
	x = update(t, x, echo(ids[1], "second", now))
	x = update(t, x, echo(ids[0], "first", now))
	assertBodies(t, x, "first", "second")
	for i, msg := range x.msgs[matchChat] {
		if msg.Key.ID != ids[i] || msg.pending {
			t.Fatalf("message %d = id %q pending %v, want id %q confirmed", i, msg.Key.ID, msg.pending, ids[i])
		}
	}

	// The send responses arriving afterwards change nothing.
	x = update(t, x, sendResponse(ids[0], ids[0], "first", now))
	x = update(t, x, sendResponse(ids[1], ids[1], "second", now))
	assertBodies(t, x, "first", "second")
}

// Edge: three sends with echoes and responses fully interleaved.
func TestQuickSendsInterleaved(t *testing.T) {
	x, ids := withPlaceholders("a", "b", "c")
	now := time.Now().Unix()
	x = update(t, x, sendResponse(ids[1], ids[1], "b", now))
	x = update(t, x, echo(ids[2], "c", now))
	x = update(t, x, echo(ids[0], "a", now))
	x = update(t, x, sendResponse(ids[2], ids[2], "c", now))
	x = update(t, x, echo(ids[1], "b", now))
	x = update(t, x, sendResponse(ids[0], ids[0], "a", now))
	assertBodies(t, x, "a", "b", "c")
}

// Edge: identical texts sent back to back stay as two messages.
func TestQuickSendsIdenticalText(t *testing.T) {
	x, ids := withPlaceholders("ok", "ok")
	now := time.Now().Unix()
	x = update(t, x, echo(ids[1], "ok", now))
	x = update(t, x, sendResponse(ids[0], ids[0], "ok", now))
	x = update(t, x, echo(ids[0], "ok", now))
	x = update(t, x, sendResponse(ids[1], ids[1], "ok", now))
	assertBodies(t, x, "ok", "ok")
}

// Negative: a message sent from your phone while a send is in flight must
// not hijack the placeholder (the old 10-second guess did exactly that).
func TestOtherDeviceMessageDoesNotStealPlaceholder(t *testing.T) {
	x, ids := withPlaceholders("from app")
	now := time.Now().Unix()
	x = update(t, x, echo("3EB0AAAAAAAAAAAAAAAAAA", "from phone", now))
	if !x.msgs[matchChat][0].pending || x.msgs[matchChat][0].Key.ID != ids[0] {
		t.Fatalf("placeholder was taken over: %+v", x.msgs[matchChat][0].Key)
	}
	x = update(t, x, sendResponse(ids[0], ids[0], "from app", now))
	assertBodies(t, x, "from app", "from phone")
}

// --- Echo vs response ordering for a single send ---

func TestEchoFirstThenResponse(t *testing.T) {
	x, ids := withPlaceholders("hi")
	now := time.Now().Unix()
	x = update(t, x, echo(ids[0], "hi", now))
	x = update(t, x, sendResponse(ids[0], ids[0], "hi", now))
	assertBodies(t, x, "hi")
}

func TestResponseFirstThenEcho(t *testing.T) {
	x, ids := withPlaceholders("hi")
	now := time.Now().Unix()
	x = update(t, x, sendResponse(ids[0], ids[0], "hi", now))
	if x.msgs[matchChat][0].pending {
		t.Fatal("response did not confirm the placeholder")
	}
	x = update(t, x, echo(ids[0], "hi", now))
	assertBodies(t, x, "hi")
}

// Edge: a "read" receipt that arrives before the echo/response is kept —
// neither may downgrade it back to "sent".
func TestReceiptBeforeEchoIsNotOverwritten(t *testing.T) {
	x, ids := withPlaceholders("hi")
	now := time.Now().Unix()
	x = update(t, x, receipt("read", ids[0]))
	x = update(t, x, echo(ids[0], "hi", now))
	x = update(t, x, sendResponse(ids[0], ids[0], "hi", now))
	assertBodies(t, x, "hi")
	if got := x.msgs[matchChat][0].ReceiptStatus; got != "read" {
		t.Fatalf("receipt = %q, want read (not downgraded)", got)
	}
}

// --- Failures ---

// Negative: send fails and WhatsApp never saw it — placeholder removed and
// the text handed back.
func TestSendFailureRemovesPlaceholderAndRestoresText(t *testing.T) {
	x, ids := withPlaceholders("lost")
	x = update(t, x, sentMsg{chatID: matchChat, pendingID: ids[0], err: errors.New("network down")})
	if len(x.msgs[matchChat]) != 0 {
		t.Fatalf("placeholder kept after failure: %q", bodies(x))
	}
	if x.input != "lost" {
		t.Fatalf("input = %q, want the text restored", x.input)
	}
}

// Edge: the response failed but WhatsApp's copy already arrived, so the
// message really went out — keep it and don't restore the text (which would
// invite a duplicate resend).
func TestSendFailureAfterEchoKeepsMessage(t *testing.T) {
	x, ids := withPlaceholders("made it")
	now := time.Now().Unix()
	x = update(t, x, echo(ids[0], "made it", now))
	x = update(t, x, sentMsg{chatID: matchChat, pendingID: ids[0], err: errors.New("response lost")})
	assertBodies(t, x, "made it")
	if x.input != "" {
		t.Fatalf("input = %q, want empty (message was delivered)", x.input)
	}
}

// Edge: same as above but confirmed by a receipt instead of an echo.
func TestSendFailureAfterReceiptKeepsMessage(t *testing.T) {
	x, ids := withPlaceholders("made it")
	x = update(t, x, receipt("delivered", ids[0]))
	x = update(t, x, sentMsg{chatID: matchChat, pendingID: ids[0], err: errors.New("response lost")})
	assertBodies(t, x, "made it")
}

// --- Fallback: backend answered with a different ID than the one we chose ---

func TestFallbackDifferentRealIDEchoFirst(t *testing.T) {
	x, ids := withPlaceholders("hi")
	now := time.Now().Unix()
	x = update(t, x, echo("3EB0BBBBBBBBBBBBBBBBBB", "hi", now))
	x = update(t, x, sendResponse(ids[0], "3EB0BBBBBBBBBBBBBBBBBB", "hi", now))
	assertBodies(t, x, "hi")
	if x.msgs[matchChat][0].Key.ID != "3EB0BBBBBBBBBBBBBBBBBB" {
		t.Fatalf("kept id %q, want the real one", x.msgs[matchChat][0].Key.ID)
	}
}

func TestFallbackDifferentRealIDResponseFirst(t *testing.T) {
	x, ids := withPlaceholders("hi")
	now := time.Now().Unix()
	x = update(t, x, sendResponse(ids[0], "3EB0BBBBBBBBBBBBBBBBBB", "hi", now))
	x = update(t, x, echo("3EB0BBBBBBBBBBBBBBBBBB", "hi", now))
	assertBodies(t, x, "hi")
}

// Edge: the response for a placeholder that is already gone (e.g. chat was
// reloaded) is added once, not twice.
func TestResponseWithoutPlaceholderAddsOnce(t *testing.T) {
	x := baseModel(matchChat)
	now := time.Now().Unix()
	id := newOutgoingMessageID()
	x = update(t, x, sendResponse(id, id, "hi", now))
	x = update(t, x, sendResponse(id, id, "hi", now))
	assertBodies(t, x, "hi")
}

// --- The chosen ID actually reaches the backend ---

func TestSendIncludesClientMessageID(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
	defer srv.Close()

	id := newOutgoingMessageID()
	send(context.Background(), srv.Client(), srv.URL, matchChat, "hi", nil, id, nil)()
	if body["messageId"] != id {
		t.Fatalf("messageId = %v, want %q", body["messageId"], id)
	}

	body = nil
	send(context.Background(), srv.Client(), srv.URL, matchChat, "hi", nil, "local-1", nil)()
	if body == nil {
		t.Fatal("second request never reached the server")
	}
	if _, ok := body["messageId"]; ok {
		t.Fatalf("a non-WhatsApp placeholder ID was sent to the backend: %v", body["messageId"])
	}
}

func TestSendFileIncludesClientMessageID(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		field := ""
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FormName() == "messageId" {
				b, _ := io.ReadAll(p)
				field = string(b)
			}
		}
		got <- field
		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := newOutgoingMessageID()
	sendFile(context.Background(), srv.Client(), srv.URL, matchChat, "document", path, "", id, make(chan fileProgressMsg, 16))()
	select {
	case field := <-got:
		if field != id {
			t.Fatalf("multipart messageId = %q, want %q", field, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the upload")
	}
}
