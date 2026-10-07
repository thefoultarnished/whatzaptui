package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func trueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestVoterNameHasTheColourItHasInTheChat(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()

	mine := voteMsg("v1", "P", true, "", []string{"Pizza"})
	if got := x.pollVoteNote(mine, nil)[0]; got.color != sentName || got.text != "You" || !got.bold {
		t.Errorf("my name should use the sent colour, got %+v", got)
	}
	theirs := voteMsg("v2", "P", false, "", []string{"Pizza"})
	if got := x.pollVoteNote(theirs, nil)[0]; got.color != receivedName || got.text != "Alice" {
		t.Errorf("their name in a one to one chat should use the received colour, got %+v", got)
	}

	x.active = "120363000000000001@g.us"
	member := voteMsg("v3", "P", false, voterCat, []string{"Pizza"})
	if got := x.pollVoteNote(member, nil)[0]; got.color != senderColor(voterCat) {
		t.Errorf("a group member's name should use their own sender colour, got %+v", got)
	}
	if got := x.pollVoteNote(mine, nil)[0]; got.color != sentName {
		t.Errorf("my name in a group is still the sent colour, got %+v", got)
	}
}

func TestPickedOptionsAreColouredAndTheRestIsMuted(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	note := x.pollVoteNote(voteMsg("v1", "P", false, "", []string{"Pizza", "Sushi"}), nil)
	if note.plain() != `Alice voted "Pizza, Sushi"` {
		t.Fatalf("text = %q", note.plain())
	}
	var options, muted []string
	for _, s := range note[1:] {
		if s.color == accent && s.bold {
			options = append(options, s.text)
		} else if s.color == "" {
			muted = append(muted, s.text)
		} else {
			t.Errorf("unexpected colour on %q: %v", s.text, s.color)
		}
	}
	if strings.Join(options, "|") != "Pizza|Sushi" {
		t.Errorf("both picked options should be in the accent colour, got %q", options)
	}
	if strings.Join(muted, "") != ` voted ", "` {
		t.Errorf("the words around them stay muted, got %q", muted)
	}
}

func TestVoteLinesWithoutAnOptionHaveNoAccentPart(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	for name, msg := range map[string]wireMsg{
		"removed":    voteMsg("v1", "P", false, "", []string{}),
		"unreadable": voteMsg("v2", "P", false, "", nil),
	} {
		for _, s := range x.pollVoteNote(msg, nil)[1:] {
			if s.color != "" {
				t.Errorf("%s: only the name is coloured, got %+v", name, s)
			}
		}
	}
}

func TestColoursReachTheScreen(t *testing.T) {
	useTokyoNight(t)
	trueColor(t)
	x := pollModel()
	note := x.pollVoteNote(voteMsg("v1", "P", true, "", []string{"Pizza"}), nil)
	styled := note.styled()
	name := lipgloss.NewStyle().Foreground(sentName).Bold(true).Render("You")
	option := lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Pizza")
	if !strings.Contains(styled, name) || !strings.Contains(styled, option) {
		t.Fatalf("the name and option should be drawn in their colours:\n%q", styled)
	}
	if name == option {
		t.Fatal("the name and the option must not share a style")
	}
	card := renderPollCard(pollCard("Pizza", "Sushi"), pollVotes{"me": {"Pizza"}}, []pollNote{note}, 0)
	if !strings.Contains(card, name) || !strings.Contains(card, option) {
		t.Fatalf("the colours must survive inside the poll card:\n%q", card)
	}
	if !strings.Contains(x.pollVoteLine(voteMsg("v1", "P", true, "", []string{"Pizza"})), option) {
		t.Fatal("a vote line drawn on its own has the same colours")
	}
}

func TestNoteRenderFitsExactly(t *testing.T) {
	useTokyoNight(t)
	note := pollNote{{text: "Alice", color: receivedName, bold: true}, {text: ` voted "`}, {text: "Pizza", color: accent}, {text: `"`}}
	plain := note.plain()
	for _, w := range []int{0, 1, 2, 3, 4, 10, 20, len([]rune(plain)) - 1, len([]rune(plain)), 60} {
		got := lipglossWidth(note.render(w))
		if got != w {
			t.Errorf("render(%d) is %d cells wide", w, got)
		}
	}
	if got := plainLine(note.render(60)); got != plain+strings.Repeat(" ", 60-len([]rune(plain))) {
		t.Errorf("a short note is padded, got %q", got)
	}
	if got := plainLine(note.render(12)); got != "Alice vote..." && got != "Alice vot..." {
		if !strings.HasSuffix(got, "...") || len([]rune(got)) != 12 {
			t.Errorf("a long note is cut with an ellipsis, got %q", got)
		}
	}
	if got := plainLine(note.render(len([]rune(plain)))); got != plain {
		t.Errorf("a note that fits exactly is not cut, got %q", got)
	}
}

func TestNoteRenderCutsInsideAColouredPart(t *testing.T) {
	useTokyoNight(t)
	trueColor(t)
	note := pollNote{{text: "Alice"}, {text: " picked "}, {text: "Extraordinarily long option", color: accent, bold: true}}
	got := note.render(20)
	if lipglossWidth(got) != 20 || !strings.HasSuffix(plainLine(got), "...") {
		t.Fatalf("got %q", plainLine(got))
	}
	if !strings.Contains(got, lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Extr")) {
		t.Fatalf("the cut part keeps its colour: %q", got)
	}
}
