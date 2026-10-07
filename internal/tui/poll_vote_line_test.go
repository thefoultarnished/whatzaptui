package tui

import (
	"strings"
	"testing"
)

func plainLine(s string) string { return ansiStripRe.ReplaceAllString(s, "") }

func TestPollVoteLineNamesTheVoterAndTheOption(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	cases := []struct {
		name string
		msg  wireMsg
		want string
	}{
		{"someone else", voteMsg("v1", "P", false, "", []string{"Pizza"}), `Alice voted "Pizza"`},
		{"me", voteMsg("v2", "P", true, "", []string{"Sushi"}), `You voted "Sushi"`},
		{"several options", voteMsg("v3", "P", false, "", []string{"Pizza", "Sushi"}), `Alice voted "Pizza, Sushi"`},
		{"removed vote", voteMsg("v4", "P", false, "", []string{}), `Alice withdrew their vote`},
		{"vote that could not be read", voteMsg("v5", "P", false, "", nil), `Alice voted`},
		{"my removed vote", voteMsg("v6", "P", true, "", []string{}), `You withdrew your vote`},
	}
	for _, c := range cases {
		if got := plainLine(x.pollVoteLine(c.msg)); !strings.HasSuffix(got, c.want) {
			t.Errorf("%s: line = %q, want it to end with %q", c.name, got, c.want)
		}
	}
}

func TestPollVoteLineReadsVotesStraightFromJSON(t *testing.T) {
	x := pollModel()
	msg := wireMsg{Message: map[string]any{"pollUpdateMessage": map[string]any{
		"pollMsgID": "P", "selectedOptionNames": []any{"Pizza", "Sushi"},
	}}}
	msg.Key.RemoteJID = fwdHere
	if got := plainLine(x.pollVoteLine(msg)); !strings.HasSuffix(got, `Alice voted "Pizza, Sushi"`) {
		t.Fatalf("line = %q", got)
	}
}

func TestPollVoteLineUsesTheGroupMembersName(t *testing.T) {
	x := pollModel()
	x.names["15550000003"] = "Cat"
	msg := voteMsg("v1", "P", false, voterCat, []string{"Yes"})
	if got := plainLine(x.pollVoteLine(msg)); !strings.HasSuffix(got, `Cat voted "Yes"`) {
		t.Fatalf("line = %q, want the member's name and not the group's", got)
	}
}

func TestPollVoteLineFallsBackToThePushNameAndThenSomeone(t *testing.T) {
	x := pollModel()
	stranger := voteMsg("v1", "P", false, "15550000077@s.whatsapp.net", []string{"Yes"})
	stranger.PushName = "Zed"
	if got := plainLine(x.pollVoteLine(stranger)); !strings.HasSuffix(got, `Zed voted "Yes"`) {
		t.Fatalf("line = %q, want the push name", got)
	}
	anonymous := voteMsg("v2", "P", false, "", []string{"Yes"})
	anonymous.Key.RemoteJID = ""
	if got := plainLine(x.pollVoteLine(anonymous)); !strings.Contains(got, "voted \"Yes\"") || strings.Contains(got, "  ") {
		t.Fatalf("line = %q, want a readable line even with no name at all", got)
	}
}

func TestChatShowsWhoVotedForWhat(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	poll := wireMsg{Message: map[string]any{"pollCreationMessage": map[string]any{"name": "Lunch on Friday?", "options": []any{"Pizza", "Sushi"}}}, MessageTimestamp: 300}
	poll.Key.ID = pollMsgID
	poll.Key.RemoteJID = fwdHere
	poll.Key.FromMe = true
	vote := voteMsg("v1", pollMsgID, false, "", []string{"Sushi"})
	vote.MessageTimestamp = 310
	x.msgs[fwdHere] = append(x.msgs[fwdHere], poll, vote)
	rendered := plainLine(x.renderMain(100, 30))
	if !strings.Contains(rendered, `Alice voted "Sushi"`) {
		t.Fatalf("the chat should say who voted for what:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1 vote") {
		t.Fatalf("the poll card should count the vote too:\n%s", rendered)
	}
}

func TestOtherPlacesStillUseThePlainVoteText(t *testing.T) {
	msg := voteMsg("v1", "P", false, "", []string{"Pizza"})
	got := plainLine(renderMessageBody(msg.Message))
	if !strings.Contains(got, `voted "Pizza" on poll`) {
		t.Fatalf("renderMessageBody (used for notifications and quotes) = %q", got)
	}
}
