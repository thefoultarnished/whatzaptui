package backend

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// storeVotablePoll saves a poll the way the backend does, in a whitelisted chat.
func storeVotablePoll(t *testing.T, app *App, chat, id string, fromMe bool, selectable any) {
	t.Helper()
	poll := map[string]any{"name": "Lunch?", "options": []string{"Pizza", "Sushi", "Ramen"}}
	if selectable != nil {
		poll["selectableCount"] = selectable
	}
	app.upsertMessage(chat, WireMessage{
		Key:              WireKey{ID: id, RemoteJID: chat, FromMe: fromMe},
		Message:          map[string]any{"pollCreationMessage": poll},
		MessageTimestamp: 100,
	})
}

func TestResolvePollVoteAcceptsAValidChoice(t *testing.T) {
	app := forwardTestApp(t)
	storeVotablePoll(t, app, fwdBob, "P1", true, uint32(1))
	plan, status, msg := app.resolvePollVote(fwdBob, "P1", []string{"Sushi"})
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want success", status, msg)
	}
	if plan.pollID != "P1" || plan.chatID != fwdBob || plan.chat.User != "15550000002" || strings.Join(plan.options, "|") != "Sushi" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if !plan.fromMe || plan.isGroup {
		t.Fatalf("my own poll in a one to one chat: %+v", plan)
	}
}

func TestResolvePollVoteWithdraw(t *testing.T) {
	app := forwardTestApp(t)
	storeVotablePoll(t, app, fwdBob, "P1", false, uint32(1))
	for name, chosen := range map[string][]string{"nil": nil, "empty": {}} {
		plan, status, msg := app.resolvePollVote(fwdBob, "P1", chosen)
		if status != 0 {
			t.Fatalf("%s: status=%d msg=%q, withdrawing is allowed", name, status, msg)
		}
		if plan.options == nil || len(plan.options) != 0 {
			t.Errorf("%s: withdraw is an empty, non-nil list, got %#v", name, plan.options)
		}
		if plan.fromMe {
			t.Errorf("%s: this poll was sent by the other person", name)
		}
	}
}

func TestResolvePollVoteLimits(t *testing.T) {
	app := forwardTestApp(t)
	storeVotablePoll(t, app, fwdBob, "single", false, float64(1)) // as read back from JSON
	storeVotablePoll(t, app, fwdBob, "any", false, uint32(0))
	storeVotablePoll(t, app, fwdBob, "two", false, uint32(2))
	storeVotablePoll(t, app, fwdBob, "unknown", false, nil)
	three := []string{"Pizza", "Sushi", "Ramen"}
	cases := []struct {
		name, poll string
		chosen     []string
		wantStatus int
	}{
		{"two on a single choice poll", "single", three[:2], http.StatusBadRequest},
		{"one on a single choice poll", "single", three[:1], 0},
		{"three on an any-number poll", "any", three, 0},
		{"two on a cap of two", "two", three[:2], 0},
		{"three on a cap of two", "two", three, http.StatusBadRequest},
		{"three on a poll of unknown limit", "unknown", three, 0},
	}
	for _, c := range cases {
		_, status, msg := app.resolvePollVote(fwdBob, c.poll, c.chosen)
		if status != c.wantStatus {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.wantStatus)
		}
	}
}

func TestResolvePollVoteRefusals(t *testing.T) {
	app := forwardTestApp(t)
	storeVotablePoll(t, app, fwdBob, "P1", false, uint32(0))
	storeVotablePoll(t, app, fwdCat, "P2", false, uint32(0)) // Cat is not whitelisted
	app.upsertMessage(fwdBob, WireMessage{Key: WireKey{ID: "T1", RemoteJID: fwdBob}, Message: map[string]any{"conversation": "hi"}, MessageTimestamp: 1})
	cases := []struct {
		name, chat, poll string
		chosen           []string
		wantStatus       int
	}{
		{"chat not whitelisted", fwdCat, "P2", []string{"Pizza"}, http.StatusForbidden},
		{"no chat", "", "P1", []string{"Pizza"}, http.StatusBadRequest},
		{"no poll id", fwdBob, "", []string{"Pizza"}, http.StatusBadRequest},
		{"status broadcast", "status@broadcast", "P1", []string{"Pizza"}, http.StatusBadRequest},
		{"unknown poll", fwdBob, "nope", []string{"Pizza"}, http.StatusNotFound},
		{"poll in another chat", fwdBob, "P2", []string{"Pizza"}, http.StatusNotFound},
		{"not a poll", fwdBob, "T1", []string{"Pizza"}, http.StatusBadRequest},
		{"an option the poll does not have", fwdBob, "P1", []string{"Burger"}, http.StatusBadRequest},
		{"the same option twice", fwdBob, "P1", []string{"Pizza", "Pizza"}, http.StatusBadRequest},
		{"wrong letter case", fwdBob, "P1", []string{"pizza"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		plan, status, msg := app.resolvePollVote(c.chat, c.poll, c.chosen)
		if status != c.wantStatus {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.wantStatus)
		}
		if msg == "" || plan.pollID != "" {
			t.Errorf("%s: a refusal says why and builds nothing: %q %+v", c.name, msg, plan)
		}
	}
}

func TestJIDFormsWithoutAClientIsJustTheAddress(t *testing.T) {
	app := newTestApp(t)
	jid := types.JID{User: "15550000002", Server: types.DefaultUserServer}
	if forms := app.jidForms(nil, jid); len(forms) != 1 || forms[0] != jid {
		t.Fatalf("forms = %v, want just %v", forms, jid)
	}
}

func TestHandleVotePollNeedsConnectionAndPost(t *testing.T) {
	app := forwardTestApp(t)
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/messages/poll/vote", bytes.NewBufferString(`{"chatId":"a","pollMessageId":"b","options":["x"]}`)), app)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST without a connected client = %d, want 409", rec.Code)
	}
	req = authorizedRequest(httptest.NewRequest(http.MethodGet, "/messages/poll/vote", nil), app)
	rec = httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}
}

func TestPollCreationRecordsHowManyAnswersAreAllowed(t *testing.T) {
	app := forwardTestApp(t)
	for name, multiple := range map[string]bool{"single": false, "multiple": true} {
		plan, status, msg := app.resolvePoll(fwdBob, "Lunch?", []string{"Pizza", "Sushi"}, multiple)
		if status != 0 {
			t.Fatalf("%s: status=%d msg=%q", name, status, msg)
		}
		wire, _ := app.wireMessagePayload(plan.msg, plan.msg, plan.chatID, false)
		poll, _ := wire["pollCreationMessage"].(map[string]any)
		want := uint32(1)
		if multiple {
			want = 0
		}
		if got, ok := poll["selectableCount"].(uint32); !ok || got != want {
			t.Errorf("%s: selectableCount = %#v, want %d", name, poll["selectableCount"], want)
		}
	}
}
