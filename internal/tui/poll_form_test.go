package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func pollModel() m {
	x := fwdModel()
	x.whitelist["15550000001"] = "Alice"
	return x
}

func typed(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func press(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func altS() tea.KeyMsg               { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s"), Alt: true} }

// fill types text into the focused row, one key at a time.
func fill(f *pollForm, s string) {
	for _, r := range s {
		if r == ' ' {
			f.handleKey(press(tea.KeySpace))
		} else {
			f.handleKey(typed(string(r)))
		}
	}
}

func openedForm() pollForm {
	var f pollForm
	f.openForm()
	return f
}

func TestNewFormStartsEmptyWithTwoOptions(t *testing.T) {
	f := openedForm()
	if !f.open || f.field != 0 || len(f.options) != minPollOptions || !f.isBlank() || f.multiple {
		t.Fatalf("unexpected new form: %+v", f)
	}
}

func TestTypingFillsTheFocusedRow(t *testing.T) {
	f := openedForm()
	fill(&f, "Lunch?")
	f.handleKey(press(tea.KeyEnter))
	fill(&f, "Pizza pie")
	f.handleKey(press(tea.KeyEnter))
	fill(&f, "Sushi")
	if f.question != "Lunch?" || f.options[0] != "Pizza pie" || f.options[1] != "Sushi" {
		t.Fatalf("question=%q options=%q", f.question, f.options)
	}
}

func TestTabAndArrowsMoveAndWrap(t *testing.T) {
	f := openedForm()
	rows := f.fieldCount() // question, 2 options, multiple, send
	if rows != 5 {
		t.Fatalf("fieldCount = %d, want 5", rows)
	}
	f.handleKey(press(tea.KeyShiftTab))
	if f.field != rows-1 {
		t.Fatalf("Shift+Tab from the top should wrap to the send button, got %d", f.field)
	}
	f.handleKey(press(tea.KeyTab))
	if f.field != 0 {
		t.Fatalf("Tab from the send button should wrap to the question, got %d", f.field)
	}
	f.handleKey(press(tea.KeyDown))
	f.handleKey(press(tea.KeyDown))
	f.handleKey(press(tea.KeyUp))
	if f.field != 1 {
		t.Fatalf("Down, Down, Up = row %d, want 1", f.field)
	}
}

func TestEnterAddsAnOptionRowAfterTheLastFilledOne(t *testing.T) {
	f := openedForm()
	f.field = 2 // last option
	fill(&f, "Sushi")
	f.handleKey(press(tea.KeyEnter))
	if len(f.options) != 3 || f.field != 3 {
		t.Fatalf("Enter on a filled last option should add a row and move to it: options=%d field=%d", len(f.options), f.field)
	}
}

func TestEnterOnAnEmptyLastOptionDoesNotAddAnotherRow(t *testing.T) {
	f := openedForm()
	f.field = 2
	f.handleKey(press(tea.KeyEnter))
	if len(f.options) != 2 || f.field != f.multipleField() {
		t.Fatalf("Enter on an empty last option should go to the switch: options=%d field=%d", len(f.options), f.field)
	}
}

func TestEnterInTheMiddleOnlyMovesDown(t *testing.T) {
	f := openedForm()
	f.field = 1
	fill(&f, "Pizza")
	f.handleKey(press(tea.KeyEnter))
	if len(f.options) != 2 || f.field != 2 {
		t.Fatalf("options=%d field=%d, want 2 options and focus on row 2", len(f.options), f.field)
	}
}

func TestOptionsStopAtTwelve(t *testing.T) {
	f := openedForm()
	f.field = 1
	for i := 0; i < 30; i++ {
		fill(&f, "opt")
		f.handleKey(press(tea.KeyEnter))
	}
	if len(f.options) != maxPollOptions {
		t.Fatalf("options = %d, want the cap of %d", len(f.options), maxPollOptions)
	}
	if f.field > f.fieldCount()-1 {
		t.Fatalf("focus %d ran past the last row", f.field)
	}
}

func TestBackspaceEditsThenRemovesAnEmptyOption(t *testing.T) {
	f := openedForm()
	f.options = []string{"a", "b", "c"}
	f.field = 3
	f.handleKey(press(tea.KeyBackspace))
	if f.options[2] != "" || len(f.options) != 3 {
		t.Fatalf("first Backspace should delete the character: %q", f.options)
	}
	f.handleKey(press(tea.KeyBackspace))
	if len(f.options) != 2 || f.field != 2 {
		t.Fatalf("Backspace on an empty option should remove it and go up: options=%d field=%d", len(f.options), f.field)
	}
}

func TestBackspaceNeverRemovesBelowTwoOptions(t *testing.T) {
	f := openedForm()
	f.field = 2
	f.handleKey(press(tea.KeyBackspace))
	f.handleKey(press(tea.KeyBackspace))
	if len(f.options) != minPollOptions {
		t.Fatalf("options = %d, must stay at %d", len(f.options), minPollOptions)
	}
}

func TestBackspaceInTheMiddleKeepsTheOtherOptions(t *testing.T) {
	f := openedForm()
	f.options = []string{"a", "", "c"}
	f.field = 2
	f.handleKey(press(tea.KeyBackspace))
	if strings.Join(f.options, "|") != "a|c" || f.field != 1 {
		t.Fatalf("options=%q field=%d, want a|c with focus on the first option", f.options, f.field)
	}
}

func TestBackspaceOnTheQuestionAndOnNonTextRowsIsSafe(t *testing.T) {
	f := openedForm()
	f.handleKey(press(tea.KeyBackspace)) // empty question
	fill(&f, "héy")
	f.handleKey(press(tea.KeyBackspace))
	if f.question != "hé" {
		t.Fatalf("question = %q, want one character removed", f.question)
	}
	f.field = f.multipleField()
	f.handleKey(press(tea.KeyBackspace))
	f.field = f.sendField()
	f.handleKey(press(tea.KeyBackspace))
	if len(f.options) != 2 || f.question != "hé" {
		t.Fatalf("Backspace on the switch or button must do nothing: %+v", f)
	}
}

func TestSpaceTogglesTheSwitchAndTypesElsewhere(t *testing.T) {
	f := openedForm()
	f.field = f.multipleField()
	f.handleKey(press(tea.KeySpace))
	if !f.multiple {
		t.Fatal("Space on the switch should turn it on")
	}
	f.handleKey(press(tea.KeyEnter))
	if f.multiple {
		t.Fatal("Enter on the switch should turn it off again")
	}
	f.field = 0
	fill(&f, "a b")
	if f.question != "a b" {
		t.Fatalf("question = %q, Space should type a space", f.question)
	}
	f.field = f.multipleField()
	f.handleKey(typed("z"))
	if f.multiple || f.question != "a b" {
		t.Fatal("letters on the switch must do nothing")
	}
}

func TestTextIsCappedAtTheLimits(t *testing.T) {
	f := openedForm()
	f.handleKey(typed(strings.Repeat("q", maxPollQuestionRunes+50)))
	if utf8.RuneCountInString(f.question) != maxPollQuestionRunes {
		t.Fatalf("question is %d characters, want the cap of %d", utf8.RuneCountInString(f.question), maxPollQuestionRunes)
	}
	f.field = 1
	f.handleKey(typed(strings.Repeat("😀", maxPollOptionRunes+10)))
	if utf8.RuneCountInString(f.options[0]) != maxPollOptionRunes {
		t.Fatalf("option is %d characters, want the cap of %d", utf8.RuneCountInString(f.options[0]), maxPollOptionRunes)
	}
	if !utf8.ValidString(f.options[0]) {
		t.Fatal("the cap must not cut an emoji in half")
	}
}

func TestPastedLineBreaksBecomeSpacesAndControlsAreDropped(t *testing.T) {
	f := openedForm()
	f.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo\r\nthree\tfour\x00\x1b"), Paste: true})
	if f.question != "one two  three four" {
		t.Fatalf("question = %q", f.question)
	}
	f.field = f.sendField()
	f.handleKey(typed("ignored"))
	if f.question != "one two  three four" {
		t.Fatal("typing on the send button must do nothing")
	}
}

func TestAltKeysAreSwallowedNotTyped(t *testing.T) {
	f := openedForm()
	f.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t"), Alt: true})
	if f.question != "" {
		t.Fatalf("Alt+T must not type into the form, question = %q", f.question)
	}
}

func TestEscClosesABlankFormAtOnce(t *testing.T) {
	f := openedForm()
	if got := f.handleKey(press(tea.KeyEsc)); got != pollClose {
		t.Fatalf("Esc on a blank form = %v, want close", got)
	}
}

func TestEscOnAFilledFormAsksThenCloses(t *testing.T) {
	f := openedForm()
	fill(&f, "Lunch?")
	if got := f.handleKey(press(tea.KeyEsc)); got != pollNone || !f.discardArmed || !strings.Contains(f.msg, "Esc again") {
		t.Fatalf("first Esc should ask: action=%v armed=%v msg=%q", got, f.discardArmed, f.msg)
	}
	if got := f.handleKey(press(tea.KeyEsc)); got != pollClose {
		t.Fatalf("second Esc = %v, want close", got)
	}
}

func TestAnyKeyDisarmsTheDiscardQuestion(t *testing.T) {
	f := openedForm()
	fill(&f, "Lunch?")
	f.handleKey(press(tea.KeyEsc))
	f.handleKey(typed("x"))
	if f.discardArmed || f.msg != "" {
		t.Fatalf("typing should clear the question: armed=%v msg=%q", f.discardArmed, f.msg)
	}
	if got := f.handleKey(press(tea.KeyEsc)); got != pollNone {
		t.Fatalf("Esc after typing should ask again, got %v", got)
	}
}

func TestSendIsRefusedWithAReasonUntilThePollIsValid(t *testing.T) {
	f := openedForm()
	if got := f.handleKey(altS()); got != pollNone || !strings.Contains(f.msg, "needs a question") {
		t.Fatalf("empty form: action=%v msg=%q", got, f.msg)
	}
	fill(&f, "Lunch?")
	if got := f.handleKey(altS()); got != pollNone || !strings.Contains(f.msg, "at least 2") {
		t.Fatalf("no options: action=%v msg=%q", got, f.msg)
	}
	f.field = 1
	fill(&f, "Pizza")
	if got := f.handleKey(altS()); got != pollNone || !strings.Contains(f.msg, "at least 2") {
		t.Fatalf("one option: action=%v msg=%q", got, f.msg)
	}
	f.field = 2
	fill(&f, "pizza")
	if got := f.handleKey(altS()); got != pollNone || !strings.Contains(f.msg, "different") {
		t.Fatalf("duplicate option: action=%v msg=%q", got, f.msg)
	}
	f.handleKey(press(tea.KeyBackspace))
	fill(&f, "z")
	if got := f.handleKey(altS()); got != pollSend {
		t.Fatalf("valid poll: action=%v msg=%q, want send", got, f.msg)
	}
}

func TestEnterOnTheSendButtonSendsAValidPollOnly(t *testing.T) {
	f := openedForm()
	f.field = f.sendField()
	if got := f.handleKey(press(tea.KeyEnter)); got != pollNone || f.msg == "" {
		t.Fatalf("an empty poll must not send: action=%v msg=%q", got, f.msg)
	}
	f.question = "q"
	f.options = []string{"a", "b"}
	if got := f.handleKey(press(tea.KeyEnter)); got != pollSend {
		t.Fatalf("a valid poll should send, got %v", got)
	}
}

func TestCleanPollDraft(t *testing.T) {
	q, o, problem := cleanPollDraft("  Hi \n there ", []string{" a ", "", "b  c"})
	if problem != "" || q != "Hi there" || strings.Join(o, "|") != "a|b c" {
		t.Fatalf("q=%q o=%q problem=%q", q, o, problem)
	}
	cases := map[string]struct {
		q       string
		options []string
		want    string
	}{
		"no question":      {"", []string{"a", "b"}, "question"},
		"long question":    {strings.Repeat("q", maxPollQuestionRunes+1), []string{"a", "b"}, "too long"},
		"one option":       {"q", []string{"a"}, "at least 2"},
		"blank options":    {"q", []string{" ", "  ", "a"}, "at least 2"},
		"too many":         {"q", strings.Split(strings.Repeat("x,", 13)+"y", ","), "at most 12"},
		"duplicate":        {"q", []string{"a", "A"}, "different"},
		"very long option": {"q", []string{strings.Repeat("o", maxPollOptionRunes+1), "b"}, "too long"},
	}
	for name, c := range cases {
		if _, _, problem := cleanPollDraft(c.q, c.options); !strings.Contains(problem, c.want) {
			t.Errorf("%s: problem = %q, want it to mention %q", name, problem, c.want)
		}
	}
}

func TestParseInlinePoll(t *testing.T) {
	cases := []struct {
		in       string
		q        string
		opts     []string
		multiple bool
		ok       bool
	}{
		{"/poll Lunch? | Pizza | Sushi", "Lunch? ", []string{" Pizza ", " Sushi"}, false, true},
		{"/poll -m Lunch? | Pizza | Sushi", "Lunch? ", []string{" Pizza ", " Sushi"}, true, true},
		{"/poll   -m   Q|a|b", "Q", []string{"a", "b"}, true, true},
		{"/poll only a question", "only a question", []string{}, false, true},
		{"/poll", "", nil, false, false},
		{"/poll ", "", nil, false, false},
		{"/poll -m ", "", nil, false, false},
		{"/poll -m", "", nil, false, false},
		{"/pollx a | b", "", nil, false, false},
		{"hello | there", "", nil, false, false},
	}
	for _, c := range cases {
		q, opts, multiple, ok := parseInlinePoll(c.in)
		if ok != c.ok || (ok && (q != c.q || strings.Join(opts, "\x00") != strings.Join(c.opts, "\x00") || multiple != c.multiple)) {
			t.Errorf("%q: got q=%q opts=%q multiple=%v ok=%v, want q=%q opts=%q multiple=%v ok=%v", c.in, q, opts, multiple, ok, c.q, c.opts, c.multiple, c.ok)
		}
	}
}
