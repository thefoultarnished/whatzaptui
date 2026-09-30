package backend

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolvePollBuildsTheMessage(t *testing.T) {
	app := forwardTestApp(t)
	plan, status, msg := app.resolvePoll(fwdBob, "  Lunch on Friday?  ", []string{"Pizza", " ", "Sushi"}, true)
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want success", status, msg)
	}
	poll := plan.msg.GetPollCreationMessage()
	if poll == nil || poll.GetName() != "Lunch on Friday?" || len(poll.GetOptions()) != 2 {
		t.Fatalf("unexpected poll: %+v", plan.msg)
	}
	if plan.chatID != fwdBob || plan.jid.User != "15550000002" || plan.question != "Lunch on Friday?" || len(plan.options) != 2 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if poll.GetSelectableOptionsCount() != 0 {
		t.Errorf("multiple answers should be 0 (any number), got %d", poll.GetSelectableOptionsCount())
	}
}

func TestResolvePollStripsControlCharacters(t *testing.T) {
	app := forwardTestApp(t)
	plan, status, msg := app.resolvePoll(fwdBob, "Lu\x00nch\x07?", []string{"Piz\x1bza", "Sushi"}, false)
	if status != 0 {
		t.Fatalf("status=%d msg=%q", status, msg)
	}
	if plan.question != "Lunch?" || plan.options[0] != "Pizza" {
		t.Fatalf("control characters must be removed: %q %q", plan.question, plan.options)
	}
	if plan.msg.GetPollCreationMessage().GetSelectableOptionsCount() != 1 {
		t.Error("single choice should be 1")
	}
}

func TestResolvePollRefusals(t *testing.T) {
	app := forwardTestApp(t)
	twelve := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = strings.Repeat("x", i+1)
		}
		return out
	}
	cases := []struct {
		name       string
		chat, q    string
		options    []string
		wantStatus int
	}{
		{"chat not whitelisted", fwdCat, "q", twelve(2), http.StatusForbidden},
		{"no chat", "", "q", twelve(2), http.StatusBadRequest},
		{"status broadcast", "status@broadcast", "q", twelve(2), http.StatusBadRequest},
		{"no question", fwdBob, "", twelve(2), http.StatusBadRequest},
		{"question is only control characters", fwdBob, "\x00\x01", twelve(2), http.StatusBadRequest},
		{"one option", fwdBob, "q", twelve(1), http.StatusBadRequest},
		{"no options", fwdBob, "q", nil, http.StatusBadRequest},
		{"thirteen options", fwdBob, "q", twelve(13), http.StatusBadRequest},
		{"duplicate options", fwdBob, "q", []string{"same", "SAME"}, http.StatusBadRequest},
		{"question too long", fwdBob, strings.Repeat("q", 256), twelve(2), http.StatusBadRequest},
		{"option too long", fwdBob, "q", []string{strings.Repeat("o", 101), "b"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		plan, status, msg := app.resolvePoll(c.chat, c.q, c.options, false)
		if status != c.wantStatus {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.wantStatus)
		}
		if msg == "" {
			t.Errorf("%s: a refusal must say why", c.name)
		}
		if plan.msg != nil {
			t.Errorf("%s: nothing must be built on failure", c.name)
		}
	}
	if _, _, msg := app.resolvePoll(fwdBob, "q", twelve(1), false); !strings.Contains(msg, "at least 2") {
		t.Errorf("the option-count refusal should say the minimum, got %q", msg)
	}
}

func TestResolvePollAcceptsTheLimits(t *testing.T) {
	app := forwardTestApp(t)
	options := make([]string, 12)
	for i := range options {
		options[i] = strings.Repeat("o", i+1)
	}
	if _, status, msg := app.resolvePoll(fwdBob, strings.Repeat("q", 255), options, false); status != 0 {
		t.Fatalf("255-character question and 12 options must pass: %d %q", status, msg)
	}
	if _, status, msg := app.resolvePoll(fwdBob, "q", []string{"a", "b"}, false); status != 0 {
		t.Fatalf("2 options must pass: %d %q", status, msg)
	}
}

func TestHandleSendPollNeedsConnectionAndPost(t *testing.T) {
	app := forwardTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/poll", bytes.NewBufferString(`{"chatId":"a","question":"q","options":["x","y"]}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST without a connected client = %d, want 409", rec.Code)
	}
	req = authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages/poll", nil), app)
	rec = httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}
}

func TestSentPollIsStoredLikeAReceivedOne(t *testing.T) {
	app := forwardTestApp(t)
	plan, status, msg := app.resolvePoll(fwdBob, "Lunch?", []string{"Pizza", "Sushi"}, false)
	if status != 0 {
		t.Fatalf("status=%d msg=%q", status, msg)
	}
	wire, _ := app.wireMessagePayload(plan.msg, plan.msg, plan.chatID, false)
	poll, _ := wire["pollCreationMessage"].(map[string]any)
	options, _ := poll["options"].([]string)
	if poll == nil || poll["name"] != "Lunch?" || len(options) != 2 || options[0] != "Pizza" {
		t.Fatalf("wire payload = %v, want the name and options the chat shows", wire)
	}
}
