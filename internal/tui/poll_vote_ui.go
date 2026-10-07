package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"whatzap/internal/tui/picker"
)

// maxPollListRows is how many polls /polls lists, newest first.
const maxPollListRows = 8

// pollVotedMsg is the answer to a vote or a withdrawal.
type pollVotedMsg struct {
	chatID    string
	msg       wireMsg
	withdrawn bool
	err       error
}

// pollSelectable reads how many answers a poll allows from its stored payload:
// 1 for a single choice, a larger number for a cap, 0 for any number. known is
// false for polls saved before the limit was recorded.
func pollSelectable(poll map[string]any) (n int, known bool) {
	switch v := poll["selectableCount"].(type) {
	case float64:
		return int(v), true
	case uint32:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// pollVoteForm is the panel for choosing options on one poll. The rows are the
// options, then "Withdraw my vote".
type pollVoteForm struct {
	open     bool
	pollID   string
	question string
	options  []string
	counts   []int
	ticks    []bool
	single   bool // one answer at most
	maxPick  int  // 0 for no cap
	cursor   int
	hadVote  bool
	msg      string
}

func (f *pollVoteForm) withdrawRow() int { return len(f.options) }

func (f *pollVoteForm) ticked() []string {
	var out []string
	for i, on := range f.ticks {
		if on {
			out = append(out, f.options[i])
		}
	}
	return out
}

// toggle ticks or unticks the option under the cursor. On a single choice poll
// ticking one option clears the others, and on a capped poll the cap holds.
func (f *pollVoteForm) toggle() {
	i := f.cursor
	if i >= len(f.options) {
		return
	}
	if f.ticks[i] {
		f.ticks[i] = false
		return
	}
	if f.single {
		for j := range f.ticks {
			f.ticks[j] = false
		}
	} else if f.maxPick > 0 && len(f.ticked()) >= f.maxPick {
		f.msg = fmt.Sprintf("This poll allows at most %d answers", f.maxPick)
		return
	}
	f.ticks[i] = true
}

// voteAction tells the caller what a key did to the vote panel.
type voteAction int

const (
	voteNone  voteAction = iota
	voteSend             // send f.ticked() as the vote
	voteClear            // withdraw the vote
	voteClose            // close the panel
)

// handleKey applies a key to the panel.
func (f *pollVoteForm) handleKey(k tea.KeyMsg) voteAction {
	f.msg = ""
	rows := len(f.options) + 1
	// Some terminals send Space as a typed character instead of the Space key.
	if k.Type == tea.KeyRunes && !k.Alt && string(k.Runes) == " " {
		k = tea.KeyMsg{Type: tea.KeySpace, Runes: k.Runes}
	}
	switch k.Type {
	case tea.KeyEsc:
		return voteClose
	case tea.KeyUp:
		f.cursor = wrappedIndex(f.cursor, rows, -1)
	case tea.KeyDown, tea.KeyTab:
		f.cursor = wrappedIndex(f.cursor, rows, 1)
	case tea.KeySpace:
		f.toggle()
	case tea.KeyEnter:
		if f.cursor == f.withdrawRow() {
			if !f.hadVote {
				f.msg = "You have not voted on this poll"
				return voteNone
			}
			return voteClear
		}
		if f.single {
			// One answer: Enter on an option votes for it.
			for j := range f.ticks {
				f.ticks[j] = j == f.cursor
			}
			return voteSend
		}
		if len(f.ticked()) == 0 {
			f.msg = "Tick at least one option with Space"
			return voteNone
		}
		return voteSend
	}
	return voteNone
}

// pollListItems lists the polls of a chat as picker rows, newest first.
func (x m) pollListItems() []picker.Item {
	items := x.msgs[x.active]
	votesFor := x.collectPollVotes(items)
	var out []picker.Item
	for i := len(items) - 1; i >= 0 && len(out) < maxPollListRows; i-- {
		poll, ok := items[i].Message["pollCreationMessage"].(map[string]any)
		if !ok || items[i].Key.ID == "" {
			continue
		}
		name, _ := poll["name"].(string)
		_, voters, _ := pollTally(pollCardOptions(poll), votesFor[items[i].Key.ID])
		label := truncate(name, 34)
		switch voters {
		case 0:
		case 1:
			label += "  (1 vote)"
		default:
			label += fmt.Sprintf("  (%d votes)", voters)
		}
		out = append(out, picker.Item{Key: items[i].Key.ID, Label: label})
	}
	return out
}

// openPollList starts /polls: a list of the chat's polls to vote on.
func (x *m) openPollList() tea.Cmd {
	items := x.pollListItems()
	if len(items) == 0 {
		return x.setTopBar("No polls in this chat. Use /createpoll to make one")
	}
	x.pollListPicker = picker.New("Polls", items)
	x.pollListPicker.EmptyText = "no matching polls"
	x.pollListPicker.EnterLabel = "vote"
	x.pollListPicker.Open("")
	x.invalidate()
	return nil
}

// openVoteForm shows the options of one poll, with what this account has picked
// already ticked.
func (x *m) openVoteForm(pollID string) tea.Cmd {
	var poll map[string]any
	for _, msg := range x.msgs[x.active] {
		if msg.Key.ID == pollID {
			poll, _ = msg.Message["pollCreationMessage"].(map[string]any)
		}
	}
	options := pollCardOptions(poll)
	if poll == nil || len(options) == 0 {
		return x.setTopBar("That poll is not available")
	}
	name, _ := poll["name"].(string)
	mine := x.collectPollVotes(x.msgs[x.active])[pollID]["me"]
	counts, _, _ := pollTally(options, x.collectPollVotes(x.msgs[x.active])[pollID])
	f := pollVoteForm{
		open: true, pollID: pollID, question: name,
		options: options, counts: counts, ticks: make([]bool, len(options)),
		hadVote: len(mine) > 0,
	}
	if n, known := pollSelectable(poll); known {
		f.single = n == 1
		if n > 1 {
			f.maxPick = n
		}
	}
	for i, opt := range options {
		for _, m := range mine {
			if m == opt {
				f.ticks[i] = true
			}
		}
	}
	x.voteForm = f
	x.invalidate()
	return nil
}

// runPollsCommand handles /polls.
func (x *m) runPollsCommand(txt string) (tea.Cmd, bool) {
	if txt != "/polls" {
		return nil, false
	}
	if x.active == "" {
		return x.setTopBar("Open a chat first, then /polls"), true
	}
	if !x.isAllowed(num(x.active)) {
		return x.setTopBar("Not whitelisted - use /whitelist to enable"), true
	}
	if x.demoMode {
		return x.setTopBar("Demo mode: voting is disabled"), true
	}
	x.leftInput = ""
	x.leftInputFocused = false
	return x.openPollList(), true
}

// handlePollListKey moves through the poll list and opens the vote panel.
func (x m) handlePollListKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		x.cancelRequests()
		return x, tea.Quit
	}
	action, done := x.pollListPicker.HandleFilterList(k)
	if !done {
		x.invalidate()
		return x, nil
	}
	items := x.pollListPicker.VisibleItems()
	idx := x.pollListPicker.Idx
	x.pollListPicker.Close(false)
	x.invalidate()
	if action != "confirm" || idx < 0 || idx >= len(items) {
		return x, nil
	}
	cmd := x.openVoteForm(items[idx].Key)
	return x, cmd
}

// handleVoteFormKey routes a key to the open vote panel and acts on the result.
func (x m) handleVoteFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		x.cancelRequests()
		return x, tea.Quit
	}
	var cmd tea.Cmd
	switch x.voteForm.handleKey(k) {
	case voteClose:
		x.voteForm.open = false
	case voteSend:
		chosen := x.voteForm.ticked()
		pollID := x.voteForm.pollID
		x.voteForm.open = false
		cmd = x.sendVoteCmd(pollID, chosen)
	case voteClear:
		pollID := x.voteForm.pollID
		x.voteForm.open = false
		cmd = x.sendVoteCmd(pollID, []string{})
	}
	x.invalidate()
	return x, cmd
}

// sendVoteCmd votes on a poll in the open chat. An empty list withdraws.
func (x *m) sendVoteCmd(pollID string, options []string) tea.Cmd {
	if x.demoMode {
		return x.setTopBar("Demo mode: voting is disabled")
	}
	label := "Sending vote..."
	if len(options) == 0 {
		label = "Withdrawing vote..."
	}
	return tea.Batch(
		x.setTopBar(label),
		sendPollVote(x.reqCtx(), x.client, x.baseURL, x.active, pollID, options),
	)
}

// sendPollVote posts a vote and reports the stored message back.
func sendPollVote(ctx context.Context, c *http.Client, base, chatID, pollID string, options []string) tea.Cmd {
	return func() tea.Msg {
		if options == nil {
			options = []string{}
		}
		payload, _ := json.Marshal(map[string]any{"chatId": chatID, "pollMessageId": pollID, "options": options})
		res, err := doAPIRequest(ctx, c, http.MethodPost, base+"/messages/poll/vote", bytes.NewReader(payload), apiTokenFromURL(base))
		if err != nil {
			return pollVotedMsg{chatID: chatID, err: err}
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode/100 != 2 {
			return pollVotedMsg{chatID: chatID, err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		var out struct {
			Message wireMsg `json:"message"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return pollVotedMsg{chatID: chatID, err: err}
		}
		return pollVotedMsg{chatID: chatID, msg: out.Message, withdrawn: len(options) == 0}
	}
}

// applyPollVoted shows a vote in its chat, or reports why it failed.
func (x *m) applyPollVoted(v pollVotedMsg) tea.Cmd {
	if v.err != nil {
		return x.setTopBar("Vote failed: " + v.err.Error())
	}
	if x.messageIndex(v.chatID, v.msg.Key.ID) < 0 {
		x.msgs[v.chatID] = append(x.msgs[v.chatID], v.msg)
	}
	x.invalidate()
	if v.withdrawn {
		return x.setTopBar("Vote withdrawn")
	}
	return x.setTopBar("Vote sent")
}

// renderVoteForm draws the vote panel in the same look as the pickers.
func (x m) renderVoteForm(w, h int) string {
	f := x.voteForm
	p := pickerStyle().NewPanel("Vote", w, h, 60)
	for _, line := range capLines(wrapLines(f.question, p.InnerW), 2, p.InnerW) {
		p.Text(line, true)
	}
	p.Blank()

	// Show a window of the options when the terminal is short.
	visible := max(3, min(len(f.options), h-17))
	start := 0
	if f.cursor < len(f.options) {
		start = max(0, min(f.cursor-visible/2, len(f.options)-visible))
	}
	if start > 0 {
		p.Muted("  ▲ more above")
	}
	for i := start; i < min(len(f.options), start+visible); i++ {
		box, on := "[ ]", "[x]"
		if f.single {
			box, on = "( )", "(●)"
		}
		if f.ticks[i] {
			box = on
		}
		note := ""
		if f.counts[i] > 0 {
			note = fmt.Sprintf("%d", f.counts[i])
		}
		// A long option wraps onto a second row, lined up under its text.
		room := max(8, p.InnerW-4-runeDisplayWidth(box+" ")-runeDisplayWidth("  "+note))
		for j, line := range capLines(wrapLines(f.options[i], room), 2, room) {
			row := picker.RowOpts{Label: box + " " + line, Selected: f.cursor == i}
			if j > 0 {
				row.Label = strings.Repeat(" ", runeDisplayWidth(box+" ")) + line
			} else {
				row.Note = note
			}
			if f.ticks[i] {
				row.Color, row.Bold = accent, true
			}
			p.Row(row)
		}
	}
	if start+visible < len(f.options) {
		p.Muted("  ▼ more below")
	}
	p.Blank()
	p.Row(picker.RowOpts{Label: "Withdraw my vote", Selected: f.cursor == f.withdrawRow(), Dim: !f.hadVote})
	if f.msg != "" {
		p.Blank()
		p.Note(f.msg, v2Color(amber, red))
	}
	if f.single {
		p.Hint("↑↓", "move", "Enter", "vote", "Esc", "close")
	} else {
		p.Hint("↑↓", "move", "Space", "tick", "Enter", "vote", "Esc", "close")
	}
	return p.Render()
}
