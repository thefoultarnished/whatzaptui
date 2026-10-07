package tui

import (
	"strings"
	"testing"
)

func words(s string) []string { return strings.Fields(s) }

func TestWrapLines(t *testing.T) {
	if got := wrapLines("short", 20); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text stays on one line: %q", got)
	}
	if got := wrapLines("", 20); len(got) != 1 || got[0] != "" {
		t.Fatalf("empty text is one empty line so the row is still drawn: %q", got)
	}
	if got := wrapLines("   ", 20); len(got) != 1 || got[0] != "" {
		t.Fatalf("blank text is one empty line: %q", got)
	}
	got := wrapLines("the quick brown fox jumps over the lazy dog", 12)
	if len(got) < 4 {
		t.Fatalf("expected the text to wrap onto several lines: %q", got)
	}
	for _, l := range got {
		if runeDisplayWidth(l) > 12 {
			t.Errorf("line %q is wider than 12", l)
		}
	}
	if strings.Join(words(strings.Join(got, " ")), " ") != "the quick brown fox jumps over the lazy dog" {
		t.Errorf("no word may be lost or reordered: %q", got)
	}
	long := wrapLines(strings.Repeat("x", 30), 10)
	if len(long) != 3 {
		t.Fatalf("a word longer than a line is split: %q", long)
	}
	if got := wrapLines("tiny width", 2); len(got) != 1 {
		t.Fatalf("a width too small to wrap leaves the text alone: %q", got)
	}
}

func TestNoteWrapKeepsEveryWordAndColour(t *testing.T) {
	note := pollNote{
		{text: "Alice", color: receivedName, bold: true},
		{text: ` voted "`},
		{text: "A rather long option name", color: accent, bold: true},
		{text: `" today`},
	}
	lines := note.wrap(16)
	if len(lines) < 3 {
		t.Fatalf("expected several lines, got %d", len(lines))
	}
	var plain []string
	for _, l := range lines {
		if n := len([]rune(l.plain())); n > 16 {
			t.Errorf("line %q is %d characters, max is 16", l.plain(), n)
		}
		plain = append(plain, l.plain())
	}
	if strings.Join(words(strings.Join(plain, " ")), " ") != strings.Join(words(note.plain()), " ") {
		t.Fatalf("no word may be lost: %q", plain)
	}
	if lines[0][0].color != receivedName || !lines[0][0].bold || lines[0][0].text != "Alice" {
		t.Errorf("the name keeps its colour on the first line: %+v", lines[0])
	}
	var coloured []string
	for _, l := range lines {
		for _, s := range l {
			if s.color == accent && s.bold && s.text != "Alice" {
				coloured = append(coloured, s.text)
			}
		}
	}
	if strings.Join(words(strings.Join(coloured, " ")), " ") != "A rather long option name" {
		t.Errorf("the option keeps its colour across lines, got %q", coloured)
	}
}

func TestNoteWrapEdgeCases(t *testing.T) {
	short := pollNote{{text: "Alice voted"}}
	if got := short.wrap(40); len(got) != 1 || got[0].plain() != "Alice voted" {
		t.Fatalf("a short note is one line: %v", got)
	}
	if got := (pollNote{}).wrap(10); len(got) != 1 || got[0].plain() != "" {
		t.Fatalf("an empty note is one empty line: %v", got)
	}
	word := pollNote{{text: strings.Repeat("y", 25)}}
	lines := word.wrap(10)
	if len(lines) != 3 || lines[0].plain() != strings.Repeat("y", 10) {
		t.Fatalf("a long word with no space is cut at the width: %v", lines)
	}
	if got := short.wrap(2); len(got) != 1 {
		t.Fatalf("a width too small to wrap leaves the note alone: %v", got)
	}
	exact := pollNote{{text: "abcde fghij"}}
	if got := exact.wrap(11); len(got) != 1 {
		t.Fatalf("a note that exactly fits is not wrapped: %v", got)
	}
}

func TestCardWrapsALongQuestion(t *testing.T) {
	useTokyoNight(t)
	question := "Where should the whole team go for lunch on Friday if the weather turns bad again?"
	lines := cardLines(t, renderPollCard(map[string]any{"name": question, "options": []string{"Pizza", "Sushi"}}, nil, nil, 0))
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "...") {
		t.Fatalf("a long question wraps, it is not cut:\n%s", joined)
	}
	rule := -1
	for i, l := range lines {
		if strings.Contains(l, "├") {
			rule = i
			break
		}
	}
	if rule < 3 {
		t.Fatalf("the question should take several rows before the rule:\n%s", joined)
	}
	var got []string
	for _, l := range lines[1:rule] {
		got = append(got, strings.Trim(strings.TrimSpace(l), "│ "))
	}
	if strings.Join(words(strings.Join(got, " ")), " ") != question {
		t.Fatalf("the whole question must be there: %q", got)
	}
	want := lipglossWidth(lines[0])
	for i, l := range lines {
		if lipglossWidth(l) != want {
			t.Errorf("line %d is %d cells wide, the top is %d:\n%s", i, lipglossWidth(l), want, joined)
		}
	}
}

func TestCardWrapsALongOptionAndKeepsTheBarOnTheFirstRow(t *testing.T) {
	useTokyoNight(t)
	option := "The new Italian place next to the station with the garden and the long menu"
	votes := pollVotes{"me": {option}, "x": {"Sushi"}}
	lines := cardLines(t, renderPollCard(pollCard(option, "Sushi"), votes, nil, 0))
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "...") {
		t.Fatalf("a long option wraps:\n%s", joined)
	}
	first := -1
	for i, l := range lines {
		if strings.Contains(l, "● The") {
			first = i
		}
	}
	if first < 0 {
		t.Fatalf("the option's first row carries the bullet:\n%s", joined)
	}
	if !strings.Contains(lines[first], "█") && !strings.Contains(lines[first], "░") {
		t.Errorf("the bar belongs on the first row: %q", lines[first])
	}
	cont := lines[first+1]
	if strings.ContainsAny(cont, "●◯█░") {
		t.Errorf("a continuation row has no bullet, bar or count: %q", cont)
	}
	bulletAt := strings.Index(lines[first], "●")
	textAt := strings.Index(lines[first], "The")
	contAt := strings.IndexFunc(cont[strings.Index(cont, "│")+len("│"):], func(r rune) bool { return r != ' ' })
	if bulletAt < 0 || textAt <= bulletAt || contAt < 0 {
		t.Fatalf("could not find the columns:\n%s", joined)
	}
	want := lipglossWidth(lines[0])
	for i, l := range lines {
		if lipglossWidth(l) != want {
			t.Errorf("line %d is %d cells wide, the top is %d:\n%s", i, lipglossWidth(l), want, joined)
		}
	}
	pos := 0
	for _, word := range words(option) {
		at := strings.Index(joined[pos:], word)
		if at < 0 {
			t.Fatalf("the word %q of the option is missing or out of order: %s", word, joined)
		}
		pos += at + len(word)
	}
}

func TestCardWrapsLongVoteLinesWithAnIndent(t *testing.T) {
	useTokyoNight(t)
	note := pollNote{
		{text: "Alexandria-Catherine", color: receivedName, bold: true},
		{text: ` voted "`},
		{text: "Pizza, Sushi, Ramen, Burgers and Tacos", color: accent, bold: true},
		{text: `"`},
	}
	lines := cardLines(t, renderPollCard(pollCard("Pizza", "Sushi"), pollVotes{"x": {"Pizza"}}, []pollNote{note}, 0))
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "...") {
		t.Fatalf("a long vote line wraps:\n%s", joined)
	}
	rule := -1
	for i, l := range lines {
		if strings.Contains(l, "├") {
			rule = i
		}
	}
	body := lines[rule+1 : len(lines)-1]
	if len(body) < 2 {
		t.Fatalf("the vote line should take several rows:\n%s", joined)
	}
	if strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(body[0]), "│ "), " ") {
		t.Errorf("the first row is not indented: %q", body[0])
	}
	for _, l := range body[1:] {
		inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(l), "│"), "│")
		if !strings.HasPrefix(inner, "   ") {
			t.Errorf("continuation rows are indented: %q", l)
		}
	}
	for _, word := range []string{"Alexandria-Catherine", "Pizza,", "Sushi,", "Ramen,", "Burgers", "Tacos"} {
		if !strings.Contains(joined, word) {
			t.Errorf("%q is missing:\n%s", word, joined)
		}
	}
	want := lipglossWidth(lines[0])
	for i, l := range lines {
		if lipglossWidth(l) != want {
			t.Errorf("line %d is %d cells wide, the top is %d", i, lipglossWidth(l), want)
		}
	}
}

func TestCardStaysInsideATightLimitWhileWrapping(t *testing.T) {
	useTokyoNight(t)
	long := strings.Repeat("a very long option name ", 4)
	notes := []pollNote{{{text: strings.Repeat("Alice voted a long way ", 4)}}}
	for _, limit := range []int{24, 26, 30, 38, 50} {
		lines := cardLines(t, renderPollCard(pollCard(long, "B"), pollVotes{"x": {long}}, notes, limit))
		want := lipglossWidth(lines[0])
		if want > limit {
			t.Errorf("limit %d: the card is %d wide", limit, want)
		}
		for i, l := range lines {
			if lipglossWidth(l) != want {
				t.Errorf("limit %d: line %d is %d wide, the top is %d:\n%s", limit, i, lipglossWidth(l), want, strings.Join(lines, "\n"))
			}
		}
		if strings.Contains(strings.Join(lines, "\n"), "...") {
			t.Errorf("limit %d: nothing is cut", limit)
		}
	}
}

func TestChatShowsAWrappedPollWithoutCuttingIt(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	poll := pollWithID("P1")
	poll.Message["pollCreationMessage"].(map[string]any)["name"] = "Where should the whole team go for lunch on Friday if the weather turns bad again?"
	x.msgs[fwdHere] = append(x.msgs[fwdHere], poll, voteMsg("v1", "P1", false, "", []string{"Sushi"}))
	rendered := plainLine(x.renderMain(100, 40))
	for _, word := range []string{"Where", "weather", "again?"} {
		if !strings.Contains(rendered, word) {
			t.Fatalf("%q is missing from the wrapped question:\n%s", word, rendered)
		}
	}
	if strings.Contains(rendered, "...") {
		t.Fatalf("the poll must not be cut:\n%s", rendered)
	}
}

func TestCapLines(t *testing.T) {
	three := []string{"one two", "three four", "five six"}
	if got := capLines(three, 3, 20); strings.Join(got, "|") != "one two|three four|five six" {
		t.Fatalf("lines within the cap are untouched: %q", got)
	}
	got := capLines(three, 2, 20)
	if len(got) != 2 || got[0] != "one two" || !strings.HasSuffix(got[1], "...") {
		t.Fatalf("lines over the cap are dropped and the last one ends in ...: %q", got)
	}
	if capLines(nil, 2, 20) != nil {
		t.Fatal("no lines stay no lines")
	}
	if got := capLines(three, 1, 6); runeDisplayWidth(got[0]) > 6 {
		t.Fatalf("the ... must not make the line too wide: %q", got)
	}
	if three[1] != "three four" {
		t.Fatal("the input must not be modified")
	}
}

func TestVotePanelWrapsALongQuestionAndOption(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 40
	poll := pollWithLimit("P1", uint32(0))
	inner := poll.Message["pollCreationMessage"].(map[string]any)
	inner["name"] = "Where should the whole team go for lunch on Friday if the weather turns bad again?"
	inner["options"] = []any{"The new Italian place next to the station with the garden", "Sushi"}
	x.msgs[fwdHere] = append(x.msgs[fwdHere], poll, voteMsg("v1", "P1", false, "", []string{"Sushi"}))
	x.openVoteForm("P1")
	lines := strings.Split(plainLine(x.renderVoteForm(100, 40)), "\n")
	joined := strings.Join(lines, "\n")
	for _, word := range []string{"Where", "weather", "again?", "Italian", "garden"} {
		if !strings.Contains(joined, word) {
			t.Errorf("%q should be on screen, the text wraps instead of being cut:\n%s", word, joined)
		}
	}
	if strings.Contains(joined, "...") {
		t.Errorf("nothing here is long enough to be cut:\n%s", joined)
	}
	first, second := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "[ ] The new Italian") {
			first, second = i, i+1
		}
	}
	if first < 0 {
		t.Fatalf("the option's first row:\n%s", joined)
	}
	if !strings.Contains(lines[second], "garden") || strings.Contains(lines[second], "[ ]") {
		t.Errorf("the second row continues the text with no checkbox: %q", lines[second])
	}
	want := lipglossWidth(lines[0])
	for i, l := range lines {
		if lipglossWidth(l) != want {
			t.Errorf("line %d is %d wide, the top is %d", i, lipglossWidth(l), want)
		}
	}
}

func TestVotePanelCutsWhatIsFarTooLong(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 40
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	x.openVoteForm("P1")
	x.voteForm.question = strings.Repeat("an endless question ", 30)
	x.voteForm.options[0] = strings.Repeat("an endless option ", 30)
	lines := strings.Split(plainLine(x.renderVoteForm(100, 40)), "\n")
	if len(lines) > 24 {
		t.Fatalf("a popup must not grow without limit, got %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "...") {
		t.Fatal("text beyond two rows ends in ...")
	}
}

func TestVoteCountStaysOnTheFirstRowOfAWrappedOption(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 40
	poll := pollWithLimit("P1", uint32(0))
	poll.Message["pollCreationMessage"].(map[string]any)["options"] = []any{"The new Italian place next to the station with the garden", "Sushi"}
	x.msgs[fwdHere] = append(x.msgs[fwdHere], poll, voteMsg("v1", "P1", false, "", []string{"The new Italian place next to the station with the garden"}))
	x.openVoteForm("P1")
	lines := strings.Split(plainLine(x.renderVoteForm(100, 40)), "\n")
	for i, l := range lines {
		if strings.Contains(l, "[ ] The new Italian") {
			if !strings.Contains(l, "  1") {
				t.Errorf("the count belongs on the first row: %q", l)
			}
			if strings.Contains(lines[i+1], "  1") {
				t.Errorf("the count is not repeated on the second row: %q", lines[i+1])
			}
		}
	}
}
