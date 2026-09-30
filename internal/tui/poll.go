package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Poll limits. They repeat what the backend enforces (internal/whatsapp/poll.go),
// so a poll is checked before it is sent. Change both together.
const (
	minPollOptions       = 2
	maxPollOptions       = 12
	maxPollQuestionRunes = 255
	maxPollOptionRunes   = 100
)

// pollSentMsg is the answer to a poll send.
type pollSentMsg struct {
	chatID string
	msg    wireMsg
	err    error
}

// pollAction tells the caller what a key did to the poll form.
type pollAction int

const (
	pollNone  pollAction = iota
	pollSend             // the poll is valid and should be sent
	pollClose            // the form should close
)

// pollForm is the "New poll" panel: a question, 2 to 12 options and a switch
// for allowing several answers. field is the focused row: 0 is the question,
// 1..len(options) are the options, then the switch, then the send button.
type pollForm struct {
	open         bool
	question     string
	options      []string
	field        int
	multiple     bool
	discardArmed bool
	msg          string
}

// openForm shows an empty form.
func (f *pollForm) openForm() {
	*f = pollForm{open: true, options: make([]string, minPollOptions)}
}

func (f *pollForm) multipleField() int { return len(f.options) + 1 }
func (f *pollForm) sendField() int     { return len(f.options) + 2 }
func (f *pollForm) fieldCount() int    { return len(f.options) + 3 }

// isBlank reports whether nothing has been typed yet.
func (f *pollForm) isBlank() bool {
	if strings.TrimSpace(f.question) != "" {
		return false
	}
	for _, o := range f.options {
		if strings.TrimSpace(o) != "" {
			return false
		}
	}
	return true
}

// cleanPollDraft tidies a poll and returns what is wrong with it, or "" when it
// can be sent. It repeats the backend's rules, with the same wording.
func cleanPollDraft(question string, options []string) (string, []string, string) {
	tidy := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	question = tidy(question)
	if question == "" {
		return "", nil, "a poll needs a question"
	}
	if utf8.RuneCountInString(question) > maxPollQuestionRunes {
		return "", nil, fmt.Sprintf("the question is too long (max %d characters)", maxPollQuestionRunes)
	}
	var cleaned []string
	for _, o := range options {
		if o = tidy(o); o != "" {
			cleaned = append(cleaned, o)
		}
	}
	if len(cleaned) < minPollOptions {
		return "", nil, fmt.Sprintf("a poll needs at least %d options", minPollOptions)
	}
	if len(cleaned) > maxPollOptions {
		return "", nil, fmt.Sprintf("a poll can have at most %d options", maxPollOptions)
	}
	seen := map[string]bool{}
	for _, o := range cleaned {
		if utf8.RuneCountInString(o) > maxPollOptionRunes {
			return "", nil, fmt.Sprintf("an option is too long (max %d characters)", maxPollOptionRunes)
		}
		key := strings.ToLower(o)
		if seen[key] {
			return "", nil, fmt.Sprintf("options must all be different: %q appears twice", o)
		}
		seen[key] = true
	}
	return question, cleaned, ""
}

// parseInlinePoll reads "/poll [-m] Question | option | option ...". The -m
// flag allows several answers. ok is false when txt is not a /poll command with
// arguments.
func parseInlinePoll(txt string) (question string, options []string, multiple, ok bool) {
	rest, found := strings.CutPrefix(txt, "/poll ")
	if !found {
		return "", nil, false, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "-m" {
		multiple, rest = true, ""
	} else if r, cut := strings.CutPrefix(rest, "-m "); cut {
		multiple, rest = true, strings.TrimSpace(r)
	}
	if rest == "" {
		return "", nil, false, false
	}
	parts := strings.Split(rest, "|")
	return parts[0], parts[1:], multiple, true
}

// insertText adds typed or pasted text to the focused row, capped to the row's
// limit. Line breaks and other control characters become spaces or are dropped.
func (f *pollForm) insertText(s string) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case unicode.IsPrint(r):
			b.WriteRune(r)
		}
	}
	add := b.String()
	if add == "" {
		return
	}
	switch {
	case f.field == 0:
		f.question = capRunes(f.question+add, maxPollQuestionRunes)
	case f.field >= 1 && f.field <= len(f.options):
		i := f.field - 1
		f.options[i] = capRunes(f.options[i]+add, maxPollOptionRunes)
	}
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// backspace deletes the last character of the focused row. On an empty option
// row it removes the row instead, as long as two options remain.
func (f *pollForm) backspace() {
	switch {
	case f.field == 0:
		f.question = dropLastRune(f.question)
	case f.field >= 1 && f.field <= len(f.options):
		i := f.field - 1
		if f.options[i] != "" {
			f.options[i] = dropLastRune(f.options[i])
			return
		}
		if len(f.options) > minPollOptions {
			f.options = append(f.options[:i], f.options[i+1:]...)
			f.field--
		}
	}
}

func dropLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// step moves the focus by delta rows and wraps around at the ends.
func (f *pollForm) step(delta int) {
	f.field = wrappedIndex(f.field, f.fieldCount(), delta)
}

// tryToSend reports pollSend when the poll is valid, otherwise it keeps the form
// open and says what is wrong.
func (f *pollForm) tryToSend() pollAction {
	if _, _, problem := cleanPollDraft(f.question, f.options); problem != "" {
		f.msg = problem
		return pollNone
	}
	return pollSend
}

// handleKey applies a key to the form.
func (f *pollForm) handleKey(k tea.KeyMsg) pollAction {
	if k.Type == tea.KeyEsc {
		if f.isBlank() || f.discardArmed {
			return pollClose
		}
		f.discardArmed = true
		f.msg = "Press Esc again to discard this poll"
		return pollNone
	}
	f.discardArmed = false
	f.msg = ""

	if k.String() == "alt+s" {
		return f.tryToSend()
	}
	switch k.Type {
	case tea.KeyTab, tea.KeyDown:
		f.step(1)
	case tea.KeyShiftTab, tea.KeyUp:
		f.step(-1)
	case tea.KeyBackspace:
		f.backspace()
	case tea.KeyEnter:
		return f.enter()
	case tea.KeySpace:
		if f.field == f.multipleField() {
			f.multiple = !f.multiple
		} else {
			f.insertText(" ")
		}
	case tea.KeyRunes:
		if !k.Alt {
			f.insertText(string(k.Runes))
		}
	}
	return pollNone
}

// enter moves to the next row, adds an option row after the last filled one, or
// acts on the switch and the send button.
func (f *pollForm) enter() pollAction {
	switch {
	case f.field == 0:
		f.field = 1
	case f.field <= len(f.options):
		i := f.field - 1
		last := i == len(f.options)-1
		if last && f.options[i] != "" && len(f.options) < maxPollOptions {
			f.options = append(f.options, "")
		}
		f.field++
	case f.field == f.multipleField():
		f.multiple = !f.multiple
	default:
		return f.tryToSend()
	}
	return pollNone
}

// tailFit keeps the end of s, with a leading ellipsis, so it fits in w cells.
func tailFit(s string, w int) string {
	if w <= 0 || lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// renderPollForm draws the "New poll" panel.
func (x m) renderPollForm(w, h int) string {
	f := x.poll
	width := min(max(48, x.w-20), 64)
	inner := width - 6

	titleStyle := lipgloss.NewStyle().Foreground(v2Color(text, accent)).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(v2Color(purple, muted)).Bold(themeV2)
	textStyle := lipgloss.NewStyle().Foreground(text)
	cursor := lipgloss.NewStyle().Foreground(accent).Render("|")
	if !x.cursorOn {
		cursor = " "
	}
	row := func(focused bool, prefix, value, ghost string) string {
		mark := "  "
		if focused {
			mark = accentStyle.Render("> ")
		}
		val := textStyle.Render(tailFit(value, inner-lipgloss.Width(prefix)-3))
		if value == "" && !focused {
			val = ghostStyle.Render(ghost)
		}
		if focused {
			val += cursor
		}
		return mark + prefix + val
	}

	body := []string{
		titleStyle.Render("New poll") + "  " + mutedStyle.Render("Esc cancel  Tab next  Alt+S send"),
		"",
		labelStyle.Render("Question"),
		row(f.field == 0, "", f.question, "what do you want to ask?"),
		"",
		labelStyle.Render(fmt.Sprintf("Options (%d/%d)", len(f.options), maxPollOptions)),
	}

	// Show a window of the option rows when the terminal is short.
	visible := max(3, min(len(f.options), h-18))
	start := 0
	if f.field >= 1 && f.field <= len(f.options) {
		start = max(0, min(f.field-1-visible/2, len(f.options)-visible))
	}
	if start > 0 {
		body = append(body, mutedStyle.Render("  ..."))
	}
	for i := start; i < min(len(f.options), start+visible); i++ {
		body = append(body, row(f.field == i+1, fmt.Sprintf("%2d  ", i+1), f.options[i], "option"))
	}
	if start+visible < len(f.options) {
		body = append(body, mutedStyle.Render("  ..."))
	}

	box := "[ ]"
	if f.multiple {
		box = "[x]"
	}
	multiMark := "  "
	if f.field == f.multipleField() {
		multiMark = accentStyle.Render("> ")
	}
	sendMark := "  "
	sendLabel := mutedStyle.Render("[ Send poll ]")
	if f.field == f.sendField() {
		sendMark = accentStyle.Render("> ")
		sendLabel = lipgloss.NewStyle().Foreground(buttonInk).Background(accent).Bold(true).Render(" Send poll ")
	}
	body = append(body, "", multiMark+textStyle.Render(box+" Allow several answers"), sendMark+sendLabel)
	if f.msg != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(v2Color(amber, red)).Render(tailFit(f.msg, inner)))
	}
	return baseBoxStyle.Copy().
		Align(lipgloss.Left, lipgloss.Top).
		Width(width).
		Render(strings.Join(body, "\n"))
}

// handlePollKey routes a key to the open poll form and acts on the result.
func (x m) handlePollKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		x.cancelRequests()
		return x, tea.Quit
	}
	switch x.poll.handleKey(k) {
	case pollClose:
		x.poll.open = false
		x.invalidate()
		return x, x.setTopBar("Poll cancelled")
	case pollSend:
		question, options, _ := cleanPollDraft(x.poll.question, x.poll.options)
		multiple := x.poll.multiple
		x.poll.open = false
		x.invalidate()
		return x, x.sendPollCmd(question, options, multiple)
	}
	x.invalidate()
	return x, nil
}

// sendPollCmd sends a checked poll to the open chat.
func (x *m) sendPollCmd(question string, options []string, multiple bool) tea.Cmd {
	chatID := x.active
	if x.demoMode {
		return x.setTopBar("Demo mode: poll send disabled")
	}
	return tea.Batch(
		x.setTopBar("Sending poll..."),
		sendPoll(x.reqCtx(), x.client, x.baseURL, chatID, question, options, multiple),
	)
}

// sendPoll posts a poll to the backend and reports the stored message back.
func sendPoll(ctx context.Context, c *http.Client, base, chatID, question string, options []string, multiple bool) tea.Cmd {
	return func() tea.Msg {
		payload, _ := json.Marshal(map[string]any{"chatId": chatID, "question": question, "options": options, "multiple": multiple})
		res, err := doAPIRequest(ctx, c, http.MethodPost, base+"/messages/poll", bytes.NewReader(payload), apiTokenFromURL(base))
		if err != nil {
			return pollSentMsg{chatID: chatID, err: err}
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode/100 != 2 {
			return pollSentMsg{chatID: chatID, err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		var out struct {
			Message wireMsg `json:"message"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return pollSentMsg{chatID: chatID, err: err}
		}
		return pollSentMsg{chatID: chatID, msg: out.Message}
	}
}

// runPollCommand handles /poll. Alone it opens the form. With arguments
// ("/poll Lunch? | Pizza | Sushi", or "-m" first to allow several answers) it
// sends straight away.
func (x *m) runPollCommand(txt string) (tea.Cmd, bool) {
	if txt != "/poll" && !strings.HasPrefix(txt, "/poll ") {
		return nil, false
	}
	if x.active == "" {
		return x.setTopBar("Open a chat first, then /poll"), true
	}
	if !x.isAllowed(num(x.active)) {
		return x.setTopBar("Not whitelisted - use /whitelist to enable"), true
	}
	if x.demoMode {
		return x.setTopBar("Demo mode: poll send disabled"), true
	}
	question, options, multiple, inline := parseInlinePoll(txt)
	if !inline {
		x.poll.openForm()
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	}
	question, options, problem := cleanPollDraft(question, options)
	if problem != "" {
		return x.setTopBar("Poll: " + problem + ". Usage: /poll [-m] question | option | option"), true
	}
	return x.sendPollCmd(question, options, multiple), true
}

// applyPollSent shows a sent poll in its chat, or reports why it failed.
func (x *m) applyPollSent(v pollSentMsg) tea.Cmd {
	if v.err != nil {
		return x.setTopBar("Poll failed: " + v.err.Error())
	}
	if x.messageIndex(v.chatID, v.msg.Key.ID) < 0 {
		x.msgs[v.chatID] = append(x.msgs[v.chatID], v.msg)
	}
	x.invalidate()
	x.scroll = 0
	return x.setTopBar("Poll sent")
}
