package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

const (
	voterBob  = "15550000002@s.whatsapp.net"
	voterCat  = "15550000003@s.whatsapp.net"
	pollMsgID = "POLL1"
)

// voteMsg is a poll vote as the chat stores it. names nil means the vote was not
// decrypted, and an empty non-nil slice is a removed vote.
func voteMsg(id, poll string, fromMe bool, participant string, names []string) wireMsg {
	update := map[string]any{"pollChatID": fwdHere, "pollMsgID": poll}
	if names != nil {
		update["selectedOptionNames"] = names
	}
	msg := wireMsg{Message: map[string]any{"pollUpdateMessage": update}}
	msg.Key.ID = id
	msg.Key.RemoteJID = fwdHere
	msg.Key.FromMe = fromMe
	msg.Key.Participant = participant
	return msg
}

func pollCard(options ...string) map[string]any {
	return map[string]any{"name": "Lunch?", "options": options}
}

func TestVoteNames(t *testing.T) {
	cases := []struct {
		name  string
		in    map[string]any
		want  string
		known bool
	}{
		{"string slice", map[string]any{"selectedOptionNames": []string{"a", "b"}}, "a|b", true},
		{"json slice", map[string]any{"selectedOptionNames": []any{"a", "b"}}, "a|b", true},
		{"json slice with junk", map[string]any{"selectedOptionNames": []any{"a", 7, nil}}, "a", true},
		{"removed vote", map[string]any{"selectedOptionNames": []string{}}, "", true},
		{"removed vote from json", map[string]any{"selectedOptionNames": []any{}}, "", true},
		{"not decrypted", map[string]any{"pollMsgID": "x"}, "", false},
		{"wrong type", map[string]any{"selectedOptionNames": "a"}, "", false},
		{"nil map", nil, "", false},
	}
	for _, c := range cases {
		names, known := voteNames(c.in)
		if known != c.known || strings.Join(names, "|") != c.want {
			t.Errorf("%s: got %q known=%v, want %q known=%v", c.name, names, known, c.want, c.known)
		}
	}
}

func TestCollectPollVotesKeepsTheLatestVotePerPerson(t *testing.T) {
	x := pollModel()
	items := []wireMsg{
		voteMsg("v1", pollMsgID, false, "", []string{"Pizza"}),
		voteMsg("v2", pollMsgID, true, "", []string{"Sushi"}),
		voteMsg("v3", pollMsgID, false, "", []string{"Sushi", "Ramen"}),
	}
	got := x.collectPollVotes(items)[pollMsgID]
	if len(got) != 2 {
		t.Fatalf("two people voted, got %v", got)
	}
	if strings.Join(got["me"], "|") != "Sushi" {
		t.Errorf("my vote = %q, want Sushi", got["me"])
	}
	if strings.Join(got["15550000001"], "|") != "Sushi|Ramen" {
		t.Errorf("their re-vote should replace the first one, got %v", got)
	}
}

func TestCollectPollVotesRemovedVoteTakesThePersonOut(t *testing.T) {
	x := pollModel()
	items := []wireMsg{
		voteMsg("v1", pollMsgID, false, "", []string{"Pizza"}),
		voteMsg("v2", pollMsgID, false, "", []string{}),
	}
	if got := x.collectPollVotes(items)[pollMsgID]; len(got) != 0 {
		t.Fatalf("a removed vote must leave no vote behind, got %v", got)
	}
}

func TestCollectPollVotesIgnoresVotesThatCouldNotBeRead(t *testing.T) {
	x := pollModel()
	items := []wireMsg{
		voteMsg("v1", pollMsgID, false, "", []string{"Pizza"}),
		voteMsg("v2", pollMsgID, false, "", nil), // not decrypted: says nothing
	}
	got := x.collectPollVotes(items)[pollMsgID]
	if strings.Join(got["15550000001"], "|") != "Pizza" {
		t.Fatalf("an unreadable vote must not erase the earlier one, got %v", got)
	}
	if only := x.collectPollVotes(items[1:]); len(only[pollMsgID]) != 0 {
		t.Fatalf("an unreadable vote alone counts for nothing, got %v", only)
	}
}

func TestCollectPollVotesSeparatesPollsAndGroupMembers(t *testing.T) {
	x := pollModel()
	items := []wireMsg{
		voteMsg("v1", "POLL1", false, voterBob, []string{"Pizza"}),
		voteMsg("v2", "POLL1", false, voterCat, []string{"Pizza"}),
		voteMsg("v3", "POLL2", false, voterBob, []string{"Yes"}),
		voteMsg("v4", "", false, voterBob, []string{"Pizza"}), // points at no poll
	}
	got := x.collectPollVotes(items)
	if len(got["POLL1"]) != 2 || len(got["POLL2"]) != 1 || len(got) != 2 {
		t.Fatalf("votes should be grouped per poll and per member: %v", got)
	}
	if strings.Join(got["POLL2"]["15550000002"], "|") != "Yes" {
		t.Errorf("POLL2 = %v", got["POLL2"])
	}
}

func TestCollectPollVotesIgnoresEverythingElse(t *testing.T) {
	x := pollModel()
	got := x.collectPollVotes(x.msgs[fwdHere])
	if len(got) != 0 {
		t.Fatalf("plain messages hold no votes, got %v", got)
	}
	if got := x.collectPollVotes(nil); len(got) != 0 {
		t.Fatalf("no messages, no votes, got %v", got)
	}
}

func TestPollTally(t *testing.T) {
	options := []string{"Pizza", "Sushi", "Ramen"}
	votes := pollVotes{
		"me":          {"Pizza"},
		"15550000002": {"Pizza", "Sushi"},
		"15550000003": {"Sushi"},
	}
	counts, voters, mine := pollTally(options, votes)
	if fmt.Sprint(counts) != "[2 2 0]" || voters != 3 {
		t.Fatalf("counts=%v voters=%d, want [2 2 0] and 3 voters", counts, voters)
	}
	if fmt.Sprint(mine) != "[true false false]" {
		t.Fatalf("mine = %v, want only Pizza", mine)
	}
}

func TestPollTallyEdgeCases(t *testing.T) {
	options := []string{"A", "B"}
	if counts, voters, _ := pollTally(options, nil); voters != 0 || fmt.Sprint(counts) != "[0 0]" {
		t.Errorf("no votes: counts=%v voters=%d", counts, voters)
	}
	if counts, voters, _ := pollTally(options, pollVotes{"x": {"nope"}}); voters != 0 || fmt.Sprint(counts) != "[0 0]" {
		t.Errorf("a vote for an option the poll does not have counts for nothing: counts=%v voters=%d", counts, voters)
	}
	if counts, voters, _ := pollTally(options, pollVotes{"x": {"A", "A"}}); voters != 1 || fmt.Sprint(counts) != "[1 0]" {
		t.Errorf("a repeated name in one vote counts once: counts=%v voters=%d", counts, voters)
	}
	if counts, voters, _ := pollTally(options, pollVotes{"x": {"nope", "B"}}); voters != 1 || fmt.Sprint(counts) != "[0 1]" {
		t.Errorf("only matching names count: counts=%v voters=%d", counts, voters)
	}
	if counts, voters, mine := pollTally(nil, pollVotes{"me": {"A"}}); len(counts) != 0 || voters != 0 || len(mine) != 0 {
		t.Errorf("a poll with no options tallies nothing")
	}
}

func TestPollBar(t *testing.T) {
	cases := []struct {
		count, total int
		want         string
	}{
		{0, 4, "░░░░░"},
		{4, 4, "█████"},
		{2, 4, "███░░"}, // 2.5 rounds up
		{1, 4, "█░░░░"},
		{1, 3, "██░░░"},
		{0, 0, "░░░░░"},
		{9, 3, "█████"}, // never draws past the end
		{-1, 3, "░░░░░"},
	}
	for _, c := range cases {
		if got := pollBar(c.count, c.total); got != c.want {
			t.Errorf("pollBar(%d,%d) = %q, want %q", c.count, c.total, got, c.want)
		}
		if lipgloss.Width(pollBar(c.count, c.total)) != pollBarCells {
			t.Errorf("pollBar(%d,%d) is not %d cells wide", c.count, c.total, pollBarCells)
		}
	}
}

func TestFitCell(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 6, "abc   "},
		{"abcdef", 6, "abcdef"},
		{"abcdefgh", 6, "abc..."},
		{"abcdefgh", 3, "abc"},
		{"abcdefgh", 0, ""},
		{"", 3, "   "},
		{"héllo wörld", 8, "héllo..."},
	}
	for _, c := range cases {
		if got := fitCell(c.in, c.w); got != c.want {
			t.Errorf("fitCell(%q,%d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
}

func cardLines(t *testing.T, card string) []string {
	t.Helper()
	return strings.Split(ansiStripRe.ReplaceAllString(card, ""), "\n")
}

func TestPlainPollCardHasNoCounts(t *testing.T) {
	useTokyoNight(t)
	plain := strings.Join(cardLines(t, renderPollCard(pollCard("Pizza", "Sushi"), nil)), "\n")
	if strings.Contains(plain, "vote") || strings.Contains(plain, "█") || strings.Contains(plain, "●") || strings.Count(plain, "◯") != 2 {
		t.Fatalf("a poll nobody voted on stays plain:\n%s", plain)
	}
	if got := renderPollCard(pollCard("Pizza", "Sushi"), pollVotes{"x": {"other"}}); got != renderPollCard(pollCard("Pizza", "Sushi"), nil) {
		t.Fatal("votes that match no option must not change the card")
	}
	if renderMessageBody(map[string]any{"pollCreationMessage": pollCard("Pizza", "Sushi")}) != renderPollCard(pollCard("Pizza", "Sushi"), nil) {
		t.Fatal("renderMessageBody should still draw the plain card")
	}
}

func TestPollCardShowsCountsBarsAndMyVote(t *testing.T) {
	useTokyoNight(t)
	votes := pollVotes{"me": {"Pizza"}, "15550000002": {"Pizza"}, "15550000003": {"Sushi"}}
	lines := cardLines(t, renderPollCard(pollCard("Pizza", "Sushi", "Ramen"), votes))
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Lunch?", "3 votes", "● Pizza", "◯ Sushi", "◯ Ramen"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in:\n%s", want, joined)
		}
	}
	find := func(opt string) string {
		for _, l := range lines {
			if strings.Contains(l, opt) {
				return l
			}
		}
		t.Fatalf("no row for %s in:\n%s", opt, joined)
		return ""
	}
	if !strings.Contains(find("Pizza"), " 2 ") || !strings.Contains(find("Pizza"), "███") {
		t.Errorf("Pizza has 2 of 3 votes: %q", find("Pizza"))
	}
	if !strings.Contains(find("Sushi"), " 1 ") {
		t.Errorf("Sushi has 1 vote: %q", find("Sushi"))
	}
	if r := find("Ramen"); !strings.Contains(r, " 0 ") || !strings.Contains(r, "░░░░░") || strings.Contains(r, "█") {
		t.Errorf("Ramen has no votes and an empty bar: %q", r)
	}
}

func TestPollCardSingularVoteAndNoMineMark(t *testing.T) {
	useTokyoNight(t)
	joined := strings.Join(cardLines(t, renderPollCard(pollCard("Pizza", "Sushi"), pollVotes{"15550000002": {"Pizza"}})), "\n")
	if !strings.Contains(joined, "1 vote") || strings.Contains(joined, "1 votes") {
		t.Errorf("one voter should read 1 vote:\n%s", joined)
	}
	if strings.Contains(joined, "●") {
		t.Errorf("only my own picks get the filled bullet:\n%s", joined)
	}
}

func TestPollCardLinesAllHaveTheSameWidth(t *testing.T) {
	useTokyoNight(t)
	long := strings.Repeat("a very long option name ", 5)
	for name, tc := range map[string]struct {
		card  map[string]any
		votes pollVotes
	}{
		"plain":            {pollCard("Pizza", "Sushi"), nil},
		"short with votes": {pollCard("A", "B"), pollVotes{"me": {"A"}}},
		"long options":     {pollCard(long, "B"), pollVotes{"x": {long}, "y": {"B"}}},
		"long question":    {map[string]any{"name": long, "options": []string{"A", "B"}}, pollVotes{"x": {"A"}}},
		"emoji options":    {pollCard("🍕 Pizza", "🍣 Sushi"), pollVotes{"x": {"🍕 Pizza"}}},
	} {
		lines := cardLines(t, renderPollCard(tc.card, tc.votes))
		want := lipgloss.Width(lines[0])
		for i, l := range lines {
			// Wide characters can differ by a cell between width systems, so allow
			// one cell of slack for the emoji case only.
			slack := 0
			if name == "emoji options" {
				slack = 1
			}
			if d := lipgloss.Width(l) - want; d > slack || d < -slack {
				t.Errorf("%s: line %d is %d cells wide, the top is %d:\n%s", name, i, lipgloss.Width(l), want, strings.Join(lines, "\n"))
			}
		}
		if want > 46 {
			t.Errorf("%s: card is %d cells wide, too wide for the chat", name, want)
		}
	}
}

func TestPollCardWithTwelveOptionsAndManyVoters(t *testing.T) {
	useTokyoNight(t)
	options := make([]string, 12)
	for i := range options {
		options[i] = fmt.Sprintf("choice %d", i+1)
	}
	votes := pollVotes{}
	for i := 0; i < 25; i++ {
		votes[fmt.Sprintf("voter%d", i)] = []string{options[i%12]}
	}
	lines := cardLines(t, renderPollCard(map[string]any{"name": "Big poll", "options": options}, votes))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "25 votes") || len(lines) != 3+12+1+1 {
		t.Fatalf("expected 25 votes over %d lines:\n%s", 3+12+1+1, joined)
	}
}

func TestChatShowsLiveVoteCountsOnThePoll(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	poll := wireMsg{Message: map[string]any{"pollCreationMessage": map[string]any{"name": "Lunch on Friday?", "options": []any{"Pizza", "Sushi"}}}, MessageTimestamp: 300}
	poll.Key.ID = pollMsgID
	poll.Key.RemoteJID = fwdHere
	poll.Key.FromMe = true
	x.msgs[fwdHere] = append(x.msgs[fwdHere], poll)

	before := ansiStripRe.ReplaceAllString(x.renderMain(100, 30), "")
	if strings.Contains(before, "vote") && !strings.Contains(before, "voted") {
		t.Fatalf("no votes yet, the card must be plain:\n%s", before)
	}

	v1 := voteMsg("v1", pollMsgID, false, "", []string{"Pizza"})
	v1.MessageTimestamp = 310
	x.msgs[fwdHere] = append(x.msgs[fwdHere], v1)
	after := ansiStripRe.ReplaceAllString(x.renderMain(100, 30), "")
	if !strings.Contains(after, "1 vote") || !strings.Contains(after, "Lunch on Friday?") {
		t.Fatalf("the card should show the new vote:\n%s", after)
	}

	v2 := voteMsg("v2", pollMsgID, false, "", []string{"Sushi"})
	v2.MessageTimestamp = 320
	x.msgs[fwdHere] = append(x.msgs[fwdHere], v2)
	changed := ansiStripRe.ReplaceAllString(x.renderMain(100, 30), "")
	if !strings.Contains(changed, "1 vote") || strings.Contains(changed, "2 votes") {
		t.Fatalf("the same person changing their vote is still one vote:\n%s", changed)
	}
}
