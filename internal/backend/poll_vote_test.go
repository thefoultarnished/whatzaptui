package backend

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestSelectedPollOptions(t *testing.T) {
	options := []string{"Pizza", "Sushi", "Ramen"}
	hash := func(names ...string) [][]byte {
		out := make([][]byte, len(names))
		for i, n := range names {
			out[i] = sha256OfString(n)
		}
		return out
	}
	cases := []struct {
		name    string
		options []string
		hashes  [][]byte
		want    string
	}{
		{"one option", options, hash("Sushi"), "Sushi"},
		{"several options keep the vote's order", options, hash("Ramen", "Pizza"), "Ramen|Pizza"},
		{"a hash that matches nothing", options, hash("Burger"), ""},
		{"a mix of known and unknown", options, hash("Burger", "Pizza"), "Pizza"},
		{"no hashes", options, nil, ""},
		{"no options", nil, hash("Pizza"), ""},
		{"a truncated hash matches nothing", options, [][]byte{sha256OfString("Pizza")[:8]}, ""},
		{"an empty hash matches nothing", options, [][]byte{{}}, ""},
	}
	for _, c := range cases {
		got := selectedPollOptions(c.options, c.hashes)
		if strings.Join(got, "|") != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func storePoll(t *testing.T, app *App, chat, id string, options []string) {
	t.Helper()
	app.upsertMessage(chat, WireMessage{
		Key:              WireKey{ID: id, RemoteJID: chat, FromMe: true},
		Message:          map[string]any{"pollCreationMessage": map[string]any{"name": "Lunch?", "options": options}},
		MessageTimestamp: 100,
	})
}

func TestPollOptionsForVoteFindsThePollInTheChatTheVoteArrivedIn(t *testing.T) {
	app := forwardTestApp(t)
	storePoll(t, app, fwdFrom, "P1", []string{"Pizza", "Sushi"})
	voteChat, _ := types.ParseJID(fwdFrom)
	// In a one to one chat the key inside the vote names the chat as the voter
	// sees it, which is our own number, not the chat the poll is stored under.
	key := &waCommon.MessageKey{ID: proto.String("P1"), RemoteJID: proto.String("15550000009@s.whatsapp.net")}
	got := app.pollOptionsForVote(voteChat, key)
	if strings.Join(got, "|") != "Pizza|Sushi" {
		t.Fatalf("options = %q, want the poll stored under the vote's chat", got)
	}
}

func TestPollOptionsForVoteFallsBackToTheKeysChat(t *testing.T) {
	app := forwardTestApp(t)
	storePoll(t, app, fwdFrom, "P1", []string{"Pizza", "Sushi"})
	elsewhere, _ := types.ParseJID("15550000008@s.whatsapp.net")
	key := &waCommon.MessageKey{ID: proto.String("P1"), RemoteJID: proto.String(fwdFrom)}
	if got := app.pollOptionsForVote(elsewhere, key); strings.Join(got, "|") != "Pizza|Sushi" {
		t.Fatalf("options = %q, want the fallback to the key's chat", got)
	}
}

func TestPollOptionsForVoteInAGroup(t *testing.T) {
	app := forwardTestApp(t)
	const group = "120363000000000001@g.us"
	storePoll(t, app, group, "G1", []string{"Yes", "No"})
	groupJID, _ := types.ParseJID(group)
	key := &waCommon.MessageKey{ID: proto.String("G1"), RemoteJID: proto.String(group)}
	if got := app.pollOptionsForVote(groupJID, key); strings.Join(got, "|") != "Yes|No" {
		t.Fatalf("options = %q, want the group's poll", got)
	}
}

func TestPollOptionsForVoteMissesCleanly(t *testing.T) {
	app := forwardTestApp(t)
	storePoll(t, app, fwdFrom, "P1", []string{"Pizza", "Sushi"})
	elsewhere := types.JID{User: "15550000007", Server: types.DefaultUserServer}
	cases := map[string]*waCommon.MessageKey{
		"unknown poll id":       {ID: proto.String("nope"), RemoteJID: proto.String(fwdFrom)},
		"no poll id":            {RemoteJID: proto.String(fwdFrom)},
		"nil key":               nil,
		"poll in another chat":  {ID: proto.String("P1"), RemoteJID: proto.String("15550000009@s.whatsapp.net")},
		"malformed remote chat": {ID: proto.String("P1"), RemoteJID: proto.String("???")},
	}
	for name, key := range cases {
		if got := app.pollOptionsForVote(elsewhere, key); got != nil {
			t.Errorf("%s: options = %q, want nil", name, got)
		}
	}
	// A message that is not a poll is not mistaken for one.
	voteChat, _ := types.ParseJID(fwdFrom)
	app.upsertMessage(fwdFrom, WireMessage{Key: WireKey{ID: "T1", RemoteJID: fwdFrom}, Message: map[string]any{"conversation": "hi"}, MessageTimestamp: 1})
	if got := app.pollOptionsForVote(voteChat, &waCommon.MessageKey{ID: proto.String("T1")}); got != nil {
		t.Errorf("a text message has no options, got %q", got)
	}
}

func TestPollVoteErrorNamesTheCauseWithoutDetails(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{whatsmeow.ErrOriginalMessageSecretNotFound, "secret-not-found"},
		{fmt.Errorf("failed to decrypt poll vote: %w", whatsmeow.ErrOriginalMessageSecretNotFound), "secret-not-found"},
		{errors.New("failed to decrypt secret message: message authentication failed (sender: 1555@lid)"), "auth-failed"},
		{errors.New("failed to get original message secret key: database is locked"), "secret-lookup"},
		{errors.New("something else"), "other"},
	}
	for _, c := range cases {
		got := pollVoteError(c.err)
		if got != c.want {
			t.Errorf("%v: got %q, want %q", c.err, got, c.want)
		}
		if strings.ContainsAny(got, "@0123456789") {
			t.Errorf("the log label %q must not carry chat or phone details", got)
		}
	}
}

func TestDecryptPollVoteOptionsWithoutAClientReturnsNil(t *testing.T) {
	app := newTestApp(t)
	if got := app.decryptPollVoteOptions(&events.Message{}); got != nil {
		t.Fatalf("no client, no names: got %q", got)
	}
}
