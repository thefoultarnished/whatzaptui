package tui

import (
	"strings"
	"testing"
)

func TestPriorChoice(t *testing.T) {
	x := pollModel()
	votes := []wireMsg{
		voteMsg("v1", "P", false, "", []string{"Pizza"}),          // Alice picks Pizza
		voteMsg("v2", "P", true, "", []string{"Sushi"}),           // I pick Sushi
		voteMsg("v3", "P", false, "", nil),                        // Alice, unreadable
		voteMsg("v4", "P", false, "", []string{}),                 // Alice withdraws
		voteMsg("v5", "P", false, "", []string{"Ramen", "Pizza"}), // Alice picks two
		voteMsg("v6", "P", false, "", []string{}),                 // Alice withdraws again
		voteMsg("v7", "P", true, "", []string{}),                  // I withdraw
	}
	cases := []struct {
		at   int
		want string
	}{
		{0, ""},            // nothing before the first vote
		{1, ""},            // my first vote, Alice's does not count
		{3, "Pizza"},       // the unreadable vote in between is skipped
		{4, ""},            // right after a withdrawal there is nothing to withdraw
		{5, "Ramen|Pizza"}, // the latest earlier choice, not the first
		{6, "Sushi"},       // my own history, not Alice's
	}
	for _, c := range cases {
		if got := strings.Join(x.priorChoice(votes, c.at), "|"); got != c.want {
			t.Errorf("priorChoice(%d) = %q, want %q", c.at, got, c.want)
		}
	}
}

func TestWithdrawnVoteNamesTheOption(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	cases := []struct {
		name string
		msg  wireMsg
		prev []string
		want string
	}{
		{"one option", voteMsg("v1", "P", false, "", []string{}), []string{"Pizza"}, `Alice withdrew "Pizza"`},
		{"several options", voteMsg("v2", "P", false, "", []string{}), []string{"Pizza", "Sushi"}, `Alice withdrew "Pizza, Sushi"`},
		{"my own", voteMsg("v3", "P", true, "", []string{}), []string{"Pizza"}, `You withdrew "Pizza"`},
		{"earlier vote not known", voteMsg("v4", "P", false, "", []string{}), nil, `Alice withdrew their vote`},
		{"my earlier vote not known", voteMsg("v5", "P", true, "", []string{}), nil, `You withdrew your vote`},
		{"a first vote", voteMsg("v6", "P", false, "", []string{"Sushi"}), nil, `Alice voted "Sushi"`},
		{"switching to another option", voteMsg("v7", "P", false, "", []string{"Sushi"}), []string{"Pizza"}, `Alice voted "Sushi", withdrew "Pizza"`},
	}
	for _, c := range cases {
		if got := x.pollVoteNote(c.msg, c.prev).plain(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWithdrawnOptionsAreInTheAccentColour(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	note := x.pollVoteNote(voteMsg("v1", "P", false, "", []string{}), []string{"Pizza", "Sushi"})
	var coloured []string
	for _, s := range note[1:] {
		if s.color == accent && s.bold {
			coloured = append(coloured, s.text)
		}
	}
	if strings.Join(coloured, "|") != "Pizza|Sushi" {
		t.Fatalf("the withdrawn options should be coloured like picked ones, got %q", coloured)
	}
	if note[0].color != receivedName {
		t.Fatalf("the name keeps its chat colour, got %v", note[0].color)
	}
}

func TestWithdrawnVoteInsideThePollNamesTheOptionEvenWhenItsVoteIsFolded(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	votes := []wireMsg{
		voteMsg("v1", "P", false, "", []string{"Pizza"}), // folded away below
		voteMsg("v2", "P", true, "", []string{"Sushi"}),
		voteMsg("v3", "P", true, "", []string{"Ramen"}),
		voteMsg("v4", "P", true, "", []string{}),
		voteMsg("v5", "P", false, "", []string{}), // withdraws v1, which is folded
	}
	notes := x.pollVoteNotes(votes)
	if len(notes) != 4 || !strings.Contains(notes[0].plain(), "2 earlier") {
		t.Fatalf("two votes fold away: %v", notes)
	}
	if got := notes[2].plain(); got != `You withdrew "Ramen"` {
		t.Errorf("my withdrawal = %q", got)
	}
	if got := notes[3].plain(); got != `Alice withdrew "Pizza"` {
		t.Errorf("a withdrawal of a folded vote still names it, got %q", got)
	}
}

func TestWithdrawAfterAChangedVoteNamesTheLatestChoice(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	votes := []wireMsg{
		voteMsg("v1", "P", false, "", []string{"Pizza"}),
		voteMsg("v2", "P", false, "", []string{"Sushi"}),
		voteMsg("v3", "P", false, "", []string{}),
	}
	notes := x.pollVoteNotes(votes)
	if got := notes[2].plain(); got != `Alice withdrew "Sushi"` {
		t.Fatalf("got %q, want the option they had at that moment", got)
	}
}

func TestStandaloneWithdrawnVoteUsesTheChatHistory(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v1 := voteMsg("v1", "GONE", false, "", []string{"Pizza"})
	v2 := voteMsg("v2", "GONE", false, "", []string{})
	x.msgs[fwdHere] = append(x.msgs[fwdHere], v1, v2)
	if got := plainLine(x.pollVoteLine(v2)); !strings.HasSuffix(got, `Alice withdrew "Pizza"`) {
		t.Fatalf("line = %q", got)
	}
	lone := voteMsg("v3", "GONE2", false, "", []string{})
	x.msgs[fwdHere] = append(x.msgs[fwdHere], lone)
	if got := plainLine(x.pollVoteLine(lone)); !strings.HasSuffix(got, "Alice withdrew their vote") {
		t.Fatalf("with no earlier vote the line = %q", got)
	}
	notInChat := voteMsg("v4", "GONE3", false, "", []string{})
	if got := plainLine(x.pollVoteLine(notInChat)); !strings.HasSuffix(got, "Alice withdrew their vote") {
		t.Fatalf("a vote that is not in the chat list = %q", got)
	}
}

func TestChatShowsWithdrawnOptionInsideThePoll(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v1 := voteMsg("v1", "P1", false, "", []string{"Pizza"})
	v1.MessageTimestamp = 310
	v2 := voteMsg("v2", "P1", false, "", []string{})
	v2.MessageTimestamp = 320
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"), v1, v2)
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, `Alice voted "Pizza"`) || !strings.Contains(rendered, `Alice withdrew "Pizza"`) {
		t.Fatalf("both the vote and its withdrawal should show in the card:\n%s", rendered)
	}
	if strings.Contains(rendered, "removed") {
		t.Fatalf("the old wording is gone:\n%s", rendered)
	}
}

func TestChangedVotesShowOnlyWhatChanged(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	cases := []struct {
		name string
		prev []string
		now  []string
		want string
	}{
		{"took one of two back", []string{"Pizza", "Sushi"}, []string{"Pizza"}, `Alice withdrew "Sushi"`},
		{"took the other one back", []string{"Pizza", "Sushi"}, []string{"Sushi"}, `Alice withdrew "Pizza"`},
		{"added a second option", []string{"Pizza"}, []string{"Pizza", "Sushi"}, `Alice voted "Sushi"`},
		{"swapped one for another", []string{"Pizza", "Sushi"}, []string{"Pizza", "Ramen"}, `Alice voted "Ramen", withdrew "Sushi"`},
		{"swapped everything", []string{"Pizza"}, []string{"Ramen", "Sushi"}, `Alice voted "Ramen, Sushi", withdrew "Pizza"`},
		{"took two back", []string{"A", "B", "C"}, []string{"A"}, `Alice withdrew "B, C"`},
		{"same choice again", []string{"Pizza", "Sushi"}, []string{"Sushi", "Pizza"}, `Alice voted "Sushi, Pizza"`},
		{"added two", []string{"A"}, []string{"A", "B", "C"}, `Alice voted "B, C"`},
	}
	for _, c := range cases {
		msg := voteMsg("v", "P", false, "", c.now)
		if got := x.pollVoteNote(msg, c.prev).plain(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestChangedVoteColoursBothPartsOfTheLine(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	note := x.pollVoteNote(voteMsg("v", "P", false, "", []string{"Pizza", "Ramen"}), []string{"Pizza", "Sushi"})
	var coloured []string
	for _, s := range note[1:] {
		if s.color == accent && s.bold {
			coloured = append(coloured, s.text)
		}
	}
	if strings.Join(coloured, "|") != "Ramen|Sushi" {
		t.Fatalf("the added and the withdrawn option are both in the accent colour, got %q", coloured)
	}
}

func TestUntickingOneOptionInTheChatSaysWhichWasWithdrawn(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v1 := voteMsg("v1", "P1", false, "", []string{"Pizza", "Sushi"})
	v1.MessageTimestamp = 310
	v2 := voteMsg("v2", "P1", false, "", []string{"Pizza"})
	v2.MessageTimestamp = 320
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"), v1, v2)
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, `Alice voted "Pizza, Sushi"`) || !strings.Contains(rendered, `Alice withdrew "Sushi"`) {
		t.Fatalf("expected the vote and then the withdrawn option: %s", rendered)
	}
	if strings.Count(rendered, `voted "Pizza"`) != 0 {
		t.Fatalf("the second line must not read like a fresh vote for Pizza: %s", rendered)
	}
	if !strings.Contains(rendered, "1 vote") {
		t.Fatalf("the card counts the person once: %s", rendered)
	}
}

func TestStandaloneChangedVoteUsesTheChatHistory(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	v1 := voteMsg("v1", "GONE", false, "", []string{"Pizza", "Sushi"})
	v2 := voteMsg("v2", "GONE", false, "", []string{"Pizza"})
	x.msgs[fwdHere] = append(x.msgs[fwdHere], v1, v2)
	if got := plainLine(x.pollVoteLine(v2)); !strings.HasSuffix(got, `Alice withdrew "Sushi"`) {
		t.Fatalf("line = %q", got)
	}
}
