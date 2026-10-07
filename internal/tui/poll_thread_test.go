package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func lipglossWidth(s string) int { return lipgloss.Width(s) }

func pollWithID(id string) wireMsg {
	msg := wireMsg{Message: map[string]any{"pollCreationMessage": map[string]any{"name": "Lunch on Friday?", "options": []any{"Pizza", "Sushi"}}}, MessageTimestamp: 300}
	msg.Key.ID = id
	msg.Key.RemoteJID = fwdHere
	msg.Key.FromMe = true
	return msg
}

func TestGroupPollVotesGathersVotesUnderTheirPoll(t *testing.T) {
	items := []wireMsg{
		pollWithID("P1"),
		pollWithID("P2"),
		voteMsg("v1", "P1", false, "", []string{"Pizza"}),
		voteMsg("v2", "P2", false, "", []string{"Sushi"}),
		voteMsg("v3", "P1", true, "", []string{"Sushi"}),
	}
	under, hidden := groupPollVotes(items)
	if len(under["P1"]) != 2 || under["P1"][0].Key.ID != "v1" || under["P1"][1].Key.ID != "v3" {
		t.Fatalf("P1 votes should be oldest first: %+v", under["P1"])
	}
	if len(under["P2"]) != 1 || under["P2"][0].Key.ID != "v2" {
		t.Fatalf("P2 votes: %+v", under["P2"])
	}
	for _, id := range []string{"v1", "v2", "v3"} {
		if !hidden[id] {
			t.Errorf("vote %s belongs under a poll and should not be drawn on its own", id)
		}
	}
	if hidden["P1"] || hidden["P2"] {
		t.Error("the polls themselves must stay visible")
	}
}

func TestGroupPollVotesKeepsVotesForUnknownPollsVisible(t *testing.T) {
	items := []wireMsg{
		voteMsg("v1", "GONE", false, "", []string{"Pizza"}),
		voteMsg("v2", "", false, "", []string{"Pizza"}),
		voteMsg("v3", "P1", false, "", nil), // poll P1 is not in the chat either
	}
	under, hidden := groupPollVotes(items)
	if len(under) != 0 || len(hidden) != 0 {
		t.Fatalf("with no poll in the chat nothing may be hidden: under=%v hidden=%v", under, hidden)
	}
	if u, h := groupPollVotes(nil); len(u) != 0 || len(h) != 0 {
		t.Fatal("an empty chat has nothing to group")
	}
}

func TestGroupPollVotesIgnoresOtherMessages(t *testing.T) {
	poll := pollWithID("P1")
	text := wireMsg{Message: map[string]any{"conversation": "hi"}}
	text.Key.ID = "t1"
	noID := pollWithID("")
	under, hidden := groupPollVotes([]wireMsg{poll, text, noID, voteMsg("v1", "", false, "", []string{"x"})})
	if len(under) != 0 || len(hidden) != 0 {
		t.Fatalf("plain messages and votes with no target are not grouped: %v %v", under, hidden)
	}
}

func TestPollVoteNotes(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	if got := x.pollVoteNotes(nil); got != nil {
		t.Fatalf("no votes, no lines, got %v", got)
	}
	vote := func(i int) wireMsg {
		return voteMsg(fmt.Sprintf("v%d", i), "P1", false, "", []string{fmt.Sprintf("Option %d", i)})
	}
	for _, n := range []int{1, 2, 3} {
		votes := make([]wireMsg, n)
		for i := range votes {
			votes[i] = vote(i + 1)
		}
		lines := x.pollVoteNotes(votes)
		if len(lines) != n {
			t.Fatalf("%d votes should give %d lines, got %d", n, n, len(lines))
		}
		if !strings.Contains(lines[0].plain(), `Alice voted "Option 1"`) {
			t.Errorf("first line = %q", lines[0].plain())
		}
	}
	five := []wireMsg{vote(1), vote(2), vote(3), vote(4), vote(5)}
	lines := x.pollVoteNotes(five)
	if len(lines) != 4 || !strings.Contains(lines[0].plain(), "2 earlier") {
		t.Fatalf("five votes should fold two away: %v", lines)
	}
	for i, want := range []string{"Option 3", "Option 4", "Option 5"} {
		if !strings.Contains(lines[i+1].plain(), want) {
			t.Errorf("line %d = %q, want the latest votes in order (%s)", i+1, lines[i+1].plain(), want)
		}
	}
}

func TestPollCardHoldsTheVoteLinesBelowARule(t *testing.T) {
	useTokyoNight(t)
	votes := pollVotes{"me": {"Pizza"}, "15550000002": {"Sushi"}}
	notes := []pollNote{{{text: `Bob voted "Sushi"`}}, {{text: `You voted "Pizza"`}}}
	lines := cardLines(t, renderPollCard(pollCard("Pizza", "Sushi"), votes, notes, 0))
	joined := strings.Join(lines, "\n")

	if n := strings.Count(joined, "├"); n != 2 {
		t.Fatalf("a rule under the question and one above the vote lines, got %d:\n%s", n, joined)
	}
	rule := -1
	for i, l := range lines {
		if strings.Contains(l, "├") {
			rule = i
		}
	}
	if rule < 0 || !strings.Contains(lines[rule+1], `Bob voted "Sushi"`) || !strings.Contains(lines[rule+2], `You voted "Pizza"`) {
		t.Fatalf("the vote lines belong right under the second rule:\n%s", joined)
	}
	if !strings.Contains(lines[rule-1], "2 votes") {
		t.Fatalf("the rule comes after the vote count:\n%s", joined)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "╰") {
		t.Fatalf("the card closes under the vote lines:\n%s", joined)
	}
	for i, l := range lines[rule+1 : len(lines)-1] {
		if trim := strings.TrimSpace(l); !strings.HasPrefix(trim, "│") || !strings.HasSuffix(trim, "│") {
			t.Errorf("vote line %d must sit inside the card walls: %q", i, l)
		}
	}
	want := lipglossWidth(lines[0])
	for i, l := range lines {
		if lipglossWidth(l) != want {
			t.Errorf("line %d is %d cells wide, the top is %d:\n%s", i, lipglossWidth(l), want, joined)
		}
	}
}

func TestPollCardWithoutNotesHasOneRule(t *testing.T) {
	useTokyoNight(t)
	plain := strings.Join(cardLines(t, renderPollCard(pollCard("Pizza", "Sushi"), pollVotes{"me": {"Pizza"}}, nil, 0)), "\n")
	if strings.Count(plain, "├") != 1 {
		t.Fatalf("no vote lines, no second rule:\n%s", plain)
	}
}

func TestPollCardIsWiderThanBefore(t *testing.T) {
	useTokyoNight(t)
	lines := cardLines(t, renderPollCard(pollCard("A", "B"), nil, nil, 0))
	if w := lipglossWidth(lines[0]); w < 38 {
		t.Fatalf("even a small poll card should be at least 38 cells wide, got %d", w)
	}
}

func TestPollCardWidthIsCappedByTheLimit(t *testing.T) {
	useTokyoNight(t)
	long := strings.Repeat("a very long option name ", 5)
	notes := []pollNote{{{text: strings.Repeat("Alice voted a long way ", 6)}}}
	votes := pollVotes{"x": {long}}
	for _, limit := range []int{0, 24, 30, 44, 80} {
		lines := cardLines(t, renderPollCard(pollCard(long, "B"), votes, notes, limit))
		max := 50
		if limit > 0 && limit < max {
			max = limit
		}
		for i, l := range lines {
			if w := lipglossWidth(l); w > max {
				t.Errorf("limit %d: line %d is %d cells wide, cap is %d:\n%s", limit, i, w, max, strings.Join(lines, "\n"))
			}
		}
	}
	lines := cardLines(t, renderPollCard(pollCard(long, "B"), votes, notes, 30))
	if joined := strings.Join(lines, "\n"); strings.Contains(joined, "...") {
		t.Fatalf("text that does not fit wraps onto more rows, it is not cut:\n%s", joined)
	}
}

func TestVotesAppearUnderThePollAndNotOnTheirOwn(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	after := wireMsg{Message: map[string]any{"conversation": "see you there"}, MessageTimestamp: 400}
	after.Key.ID = "after1"
	after.Key.RemoteJID = fwdHere
	v1 := voteMsg("v1", "P1", false, "", []string{"Sushi"})
	v1.MessageTimestamp = 310
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"), v1, after)

	rendered := plainLine(x.renderMain(100, 40))
	if n := strings.Count(rendered, `voted "Sushi"`); n != 1 {
		t.Fatalf("the vote should be shown once, under the poll, got %d:\n%s", n, rendered)
	}
	card := strings.Index(rendered, "Lunch on Friday?")
	vote := strings.Index(rendered, `Alice voted "Sushi"`)
	later := strings.Index(rendered, "see you there")
	if card < 0 || vote < card || later < vote {
		t.Fatalf("expected the poll, then its vote, then the next message (card=%d vote=%d later=%d):\n%s", card, vote, later, rendered)
	}
	if !strings.Contains(rendered, "1 vote") {
		t.Fatalf("the card still counts the vote:\n%s", rendered)
	}
}

func TestVoteForAPollNotInTheChatStaysVisible(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v := voteMsg("v1", "GONE", false, "", []string{"Pizza"})
	v.MessageTimestamp = 310
	x.msgs[fwdHere] = append(x.msgs[fwdHere], v)
	rendered := plainLine(x.renderMain(100, 30))
	if !strings.Contains(rendered, `Alice voted "Pizza"`) {
		t.Fatalf("a vote whose poll is not loaded must still show:\n%s", rendered)
	}
}

func TestLongVoteListsFoldOlderVotes(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	msgs := append([]wireMsg{}, x.msgs[fwdHere]...)
	msgs = append(msgs, pollWithID("P1"))
	for i := 1; i <= 5; i++ {
		v := voteMsg(fmt.Sprintf("v%d", i), "P1", i%2 == 0, "", []string{fmt.Sprintf("Option %d", i)})
		v.MessageTimestamp = int64(310 + i)
		msgs = append(msgs, v)
	}
	x.msgs[fwdHere] = msgs
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, "2 earlier") || strings.Contains(rendered, `voted "Option 1"`) || !strings.Contains(rendered, `"Option 5"`) {
		t.Fatalf("only the latest three votes show, older ones fold:\n%s", rendered)
	}
}

func TestRemovedVoteShowsUnderThePoll(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v1 := voteMsg("v1", "P1", false, "", []string{"Pizza"})
	v1.MessageTimestamp = 310
	v2 := voteMsg("v2", "P1", false, "", []string{})
	v2.MessageTimestamp = 320
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"), v1, v2)
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, `Alice withdrew "Pizza"`) {
		t.Fatalf("a removed vote should show under the poll:\n%s", rendered)
	}
	if strings.Contains(rendered, "1 vote") {
		t.Fatalf("a removed vote leaves no count:\n%s", rendered)
	}
}

func TestHiddenVotesCannotBePickedForReply(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere],
		pollWithID("P1"),
		voteMsg("v1", "P1", false, "", []string{"Pizza"}),
		voteMsg("orphan", "GONE", false, "", []string{"Pizza"}),
	)
	seen := map[string]bool{}
	for _, msg := range x.replyPickCandidates() {
		seen[msg.Key.ID] = true
	}
	if seen["v1"] {
		t.Error("a vote drawn under its poll must not be selectable, it is not on screen")
	}
	if !seen["P1"] || !seen["orphan"] || !seen["t1"] {
		t.Errorf("the poll, a vote that is still drawn and normal messages stay selectable: %v", seen)
	}
}
