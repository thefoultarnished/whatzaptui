package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// pollVotes maps a voter to the option names they have picked. It holds each
// person's latest vote only.
type pollVotes map[string][]string

// pollBarCells is the width of the little bar drawn next to each option.
const pollBarCells = 5

// voteNames reads the option names of a vote message. known is false when the
// vote could not be read (it was not decrypted), so it must not change the
// count. An empty list with known=true is a removed vote.
func voteNames(update map[string]any) (names []string, known bool) {
	switch v := update["selectedOptionNames"].(type) {
	case []string:
		return v, true
	case []any:
		names = make([]string, 0, len(v))
		for _, o := range v {
			if s, ok := o.(string); ok {
				names = append(names, s)
			}
		}
		return names, true
	}
	return nil, false
}

// collectPollVotes gathers the votes cast on every poll in a chat: poll message
// ID to voter to picked options. Messages are in chat order, so a later vote
// from the same person replaces the earlier one, and a removed vote takes the
// person out of the count. Votes that were not decrypted are skipped.
func (x m) collectPollVotes(items []wireMsg) map[string]pollVotes {
	out := map[string]pollVotes{}
	for _, msg := range items {
		update, ok := msg.Message["pollUpdateMessage"].(map[string]any)
		if !ok {
			continue
		}
		pollID, _ := update["pollMsgID"].(string)
		names, known := voteNames(update)
		if pollID == "" || !known {
			continue
		}
		voter := "me"
		if !msg.Key.FromMe {
			voter = num(x.senderIDForMsg(msg))
			if voter == "" {
				voter = msg.Key.ID
			}
		}
		if out[pollID] == nil {
			out[pollID] = pollVotes{}
		}
		if len(names) == 0 {
			delete(out[pollID], voter)
			continue
		}
		out[pollID][voter] = names
	}
	return out
}

// pollTally counts the votes for each option. Only names that match an option
// are counted. voters is how many people have a vote on the poll, and mine marks
// the options this account picked.
func pollTally(options []string, votes pollVotes) (counts []int, voters int, mine []bool) {
	counts = make([]int, len(options))
	mine = make([]bool, len(options))
	for voter, picked := range votes {
		counted := false
		for i, opt := range options {
			for _, name := range picked {
				if name != opt {
					continue
				}
				counts[i]++
				counted = true
				if voter == "me" {
					mine[i] = true
				}
				break
			}
		}
		if counted {
			voters++
		}
	}
	return counts, voters, mine
}

// pollCardOptions reads the option names of a poll payload.
func pollCardOptions(v map[string]any) []string {
	var opts []string
	switch raw := v["options"].(type) {
	case []any:
		for _, o := range raw {
			if s, ok := o.(string); ok && s != "" {
				opts = append(opts, s)
			}
		}
	case []string:
		for _, s := range raw {
			if s != "" {
				opts = append(opts, s)
			}
		}
	}
	return opts
}

// pollBar draws a bar of pollBarCells cells with count/total of it filled.
func pollBar(count, total int) string {
	filled := 0
	if total > 0 {
		filled = (count*pollBarCells + total/2) / total
	}
	filled = min(max(filled, 0), pollBarCells)
	return strings.Repeat("█", filled) + strings.Repeat("░", pollBarCells-filled)
}

// fitCell cuts s to w characters with "..." or pads it with spaces to w.
func fitCell(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		if w > 3 {
			return string(r[:w-3]) + "..."
		}
		return string(r[:max(w, 0)])
	}
	return s + strings.Repeat(" ", w-len(r))
}

// renderPollCard draws a poll as a card. With votes it shows the count and a
// bar for each option, a filled bullet on the options this account picked, and
// the number of voters at the bottom. Notes, the latest vote lines, go in the
// same card under a horizontal rule. limit is the widest the card may be, or 0
// for no limit. Without votes or notes it is the plain card.
func renderPollCard(v map[string]any, votes pollVotes, notes []pollNote, limit int) string {
	name, _ := v["name"].(string)
	opts := pollCardOptions(v)
	counts, voters, mine := pollTally(opts, votes)
	showVotes := voters > 0

	minW, maxW, extra := 38, 50, 0
	if showVotes {
		// Room for a gap, the bar, a gap and a two-digit count.
		extra = 1 + pollBarCells + 1 + 2
	}
	if limit > 0 {
		maxW = min(maxW, limit)
		minW = min(minW, maxW)
	}
	cardWidth := len(name) + 6
	for _, o := range opts {
		if len(o)+8+extra > cardWidth {
			cardWidth = len(o) + 8 + extra
		}
	}
	for _, n := range notes {
		cardWidth = max(cardWidth, utf8.RuneCountInString(n.plain())+6)
	}
	cardWidth = min(max(cardWidth, minW), maxW)

	accentCol := lipgloss.Color(currentTheme.Accent)
	questionCol := v2Color(text, accentCol)
	tagStyle := mediaTagStyle("poll")
	borderStyle := lipgloss.NewStyle().Foreground(borderSubtle)
	accentBorderStyle := lipgloss.NewStyle().Foreground(accentCol)

	var sb strings.Builder
	badge := tagStyle.Render(" " + mediaIconLabel("poll") + " ")
	topLen := max(cardWidth-lipgloss.Width(badge)-4, 2)
	sb.WriteString(borderStyle.Render(" ╭─") + badge + borderStyle.Render(strings.Repeat("─", topLen)+"╮") + "\n")
	// Long text wraps onto more rows inside the card.
	for _, line := range wrapLines(name, cardWidth-5) {
		qText := " " + line
		qText += strings.Repeat(" ", max(0, cardWidth-4-runeDisplayWidth(qText)))
		sb.WriteString(borderStyle.Render(" │ ") + lipgloss.NewStyle().Bold(true).Foreground(questionCol).Render(qText) + borderStyle.Render("│") + "\n")
	}
	sb.WriteString(borderStyle.Render(" ├"+strings.Repeat("─", cardWidth-3)+"┤") + "\n")
	textW := cardWidth - 8 - extra
	for i, opt := range opts {
		bullet := accentBorderStyle.Render("◯")
		if showVotes && mine[i] {
			bullet = accentBorderStyle.Render("●")
		}
		for j, line := range wrapLines(opt, textW) {
			pad := strings.Repeat(" ", max(0, textW-runeDisplayWidth(line)))
			if j > 0 {
				// A continuation row lines up under the option's text.
				sb.WriteString(borderStyle.Render(" │  ") + "  " + line + pad + strings.Repeat(" ", extra) + borderStyle.Render(" │") + "\n")
				continue
			}
			row := borderStyle.Render(" │  ") + bullet + " " + line + pad
			if showVotes {
				row += " " + accentBorderStyle.Render(pollBar(counts[i], voters)) + fmt.Sprintf(" %2d", counts[i])
			}
			sb.WriteString(row + borderStyle.Render(" │") + "\n")
		}
	}
	if showVotes {
		label := fmt.Sprintf("%d votes", voters)
		if voters == 1 {
			label = "1 vote"
		}
		sb.WriteString(borderStyle.Render(" │ ") + mutedStyle.Render(fitCell(" "+label, cardWidth-4)) + borderStyle.Render("│") + "\n")
	}
	if len(notes) > 0 {
		sb.WriteString(borderStyle.Render(" ├"+strings.Repeat("─", cardWidth-3)+"┤") + "\n")
		for _, n := range notes {
			for j, line := range n.wrap(cardWidth - 6) {
				if j > 0 {
					line = append(pollNote{{text: "  "}}, line...)
				}
				sb.WriteString(borderStyle.Render(" │ ") + line.render(cardWidth-4) + borderStyle.Render("│") + "\n")
			}
		}
	}
	sb.WriteString(borderStyle.Render(" ╰" + strings.Repeat("─", cardWidth-3) + "╯"))
	return sb.String()
}

// wrapLines wraps text into lines of at most w cells, breaking at spaces and
// splitting a word that is longer than a line. Text that is empty is one empty
// line, so a row is always drawn.
func wrapLines(text string, w int) []string {
	text = strings.TrimSpace(text)
	if text == "" || w < 4 {
		return []string{text}
	}
	return strings.Split(wrapText(text, w), "\n")
}

// wrap splits a vote line into lines of at most w characters, breaking at spaces
// where it can and keeping the colour of every piece.
func (n pollNote) wrap(w int) []pollNote {
	type glyph struct {
		r   rune
		seg int
	}
	var glyphs []glyph
	for i, s := range n {
		for _, r := range s.text {
			glyphs = append(glyphs, glyph{r, i})
		}
	}
	build := func(part []glyph) pollNote {
		var out pollNote
		for _, g := range part {
			if len(out) > 0 && out[len(out)-1].color == n[g.seg].color && out[len(out)-1].bold == n[g.seg].bold {
				out[len(out)-1].text += string(g.r)
				continue
			}
			out = append(out, noteSeg{text: string(g.r), color: n[g.seg].color, bold: n[g.seg].bold})
		}
		return out
	}
	if w < 4 || len(glyphs) <= w {
		return []pollNote{build(glyphs)}
	}
	var lines []pollNote
	for len(glyphs) > w {
		cut, skip := w, 0
		for i := w; i > 0; i-- {
			if glyphs[i].r == ' ' {
				cut, skip = i, 1
				break
			}
		}
		lines = append(lines, build(glyphs[:cut]))
		glyphs = glyphs[cut+skip:]
	}
	return append(lines, build(glyphs))
}

// noteSeg is a piece of a vote line with its own colour. An empty colour is the
// muted text colour.
type noteSeg struct {
	text  string
	color lipgloss.Color
	bold  bool
}

func (s noteSeg) style() lipgloss.Style {
	st := mutedStyle
	if s.color != "" {
		st = lipgloss.NewStyle().Foreground(s.color)
	}
	return st.Bold(s.bold)
}

// pollNote is one line about a vote, made of coloured pieces.
type pollNote []noteSeg

// plain is the line without colours.
func (n pollNote) plain() string {
	var sb strings.Builder
	for _, s := range n {
		sb.WriteString(s.text)
	}
	return sb.String()
}

// styled draws the line in its colours.
func (n pollNote) styled() string {
	var sb strings.Builder
	for _, s := range n {
		sb.WriteString(s.style().Render(s.text))
	}
	return sb.String()
}

// render draws the line in exactly w characters: cut with "..." when it is too
// long, padded with spaces when it is short.
func (n pollNote) render(w int) string {
	total := utf8.RuneCountInString(n.plain())
	limit := w
	if total > w {
		limit = max(w-3, 0)
	}
	var sb strings.Builder
	used := 0
	for _, s := range n {
		if used >= limit {
			break
		}
		part := []rune(s.text)
		if len(part) > limit-used {
			part = part[:limit-used]
		}
		sb.WriteString(s.style().Render(string(part)))
		used += len(part)
	}
	if total > w {
		sb.WriteString(mutedStyle.Render(strings.Repeat(".", min(3, w))))
		used += min(3, w)
	}
	if used < w {
		sb.WriteString(strings.Repeat(" ", w-used))
	}
	return sb.String()
}

// voterColor is the colour the voter's name has in the chat: the sent colour for
// you, the sender's own colour in a group and the received colour in a one to one
// chat.
func (x m) voterColor(msg wireMsg) lipgloss.Color {
	switch {
	case msg.Key.FromMe:
		return sentName
	case strings.HasSuffix(x.active, "@g.us"):
		return senderColor(x.senderIDForMsg(msg))
	}
	return receivedName
}

// voterKey names a voter the same way collectPollVotes does, so a person's votes
// can be followed through the chat.
func (x m) voterKey(msg wireMsg) string {
	if msg.Key.FromMe {
		return "me"
	}
	if voter := num(x.senderIDForMsg(msg)); voter != "" {
		return voter
	}
	return msg.Key.ID
}

// priorChoice is what the voter of votes[i] had picked just before that vote: the
// options of their latest earlier vote on the same poll, or nil when they had
// none, or had already withdrawn it. votes must all be for one poll, oldest first.
func (x m) priorChoice(votes []wireMsg, i int) []string {
	voter := x.voterKey(votes[i])
	for j := i - 1; j >= 0; j-- {
		if x.voterKey(votes[j]) != voter {
			continue
		}
		update, _ := votes[j].Message["pollUpdateMessage"].(map[string]any)
		if names, known := voteNames(update); known {
			return names
		}
	}
	return nil
}

// pollVoteNote builds the line for a vote: who voted, in their colour, and the
// option or options they picked, in the accent colour. A withdrawn vote names
// the options that were withdrawn (prev, what the person had picked before).
func (x m) pollVoteNote(msg wireMsg, prev []string) pollNote {
	who := "You"
	if !msg.Key.FromMe {
		who = strings.TrimSpace(x.senderNameForMsg(msg))
		if who == "" {
			who = "Someone"
		}
	}
	note := pollNote{{text: who, color: x.voterColor(msg), bold: true}}
	update, _ := msg.Message["pollUpdateMessage"].(map[string]any)
	names, known := voteNames(update)
	switch {
	case known && len(names) == 0 && len(prev) > 0:
		return append(note, quotedOptions(" withdrew ", prev)...)
	case known && len(names) == 0:
		their := "their"
		if msg.Key.FromMe {
			their = "your"
		}
		return append(note, noteSeg{text: " withdrew " + their + " vote"})
	case known && len(prev) > 0:
		return append(note, changedOptions(prev, names)...)
	case known:
		return append(note, quotedOptions(" voted ", names)...)
	}
	return append(note, noteSeg{text: " voted"})
}

// changedOptions describes a vote that follows an earlier one by only what
// changed: the options added and the options taken back. Sending the same choice
// again changes nothing, so it is shown in full.
func changedOptions(prev, now []string) []noteSeg {
	has := func(list []string, name string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	var added, removed []string
	for _, n := range now {
		if !has(prev, n) {
			added = append(added, n)
		}
	}
	for _, p := range prev {
		if !has(now, p) {
			removed = append(removed, p)
		}
	}
	switch {
	case len(added) > 0 && len(removed) > 0:
		segs := quotedOptions(" voted ", added)
		segs = append(segs, noteSeg{text: ", "})
		return append(segs, quotedOptions("withdrew ", removed)...)
	case len(added) > 0:
		return quotedOptions(" voted ", added)
	case len(removed) > 0:
		return quotedOptions(" withdrew ", removed)
	}
	return quotedOptions(" voted ", now)
}

// quotedOptions is a lead-in word followed by the options in quotes, each in the
// accent colour: ` voted "Pizza, Sushi"`.
func quotedOptions(lead string, names []string) []noteSeg {
	segs := []noteSeg{{text: lead + `"`}}
	for i, name := range names {
		if i > 0 {
			segs = append(segs, noteSeg{text: ", "})
		}
		segs = append(segs, noteSeg{text: name, color: accent, bold: true})
	}
	return append(segs, noteSeg{text: `"`})
}

// pollVoteLine is the chat line for a vote that is shown on its own.
func (x m) pollVoteLine(msg wireMsg) string {
	var prev []string
	if target := pollVoteTarget(msg); target != "" {
		var same []wireMsg
		at := -1
		for _, it := range x.msgs[x.active] {
			if pollVoteTarget(it) != target {
				continue
			}
			same = append(same, it)
			if it.Key.ID == msg.Key.ID {
				at = len(same) - 1
			}
		}
		if at >= 0 {
			prev = x.priorChoice(same, at)
		}
	}
	return mediaTagStyle("poll").Render(mediaIconLabel("poll")) + " " + x.pollVoteNote(msg, prev).styled()
}

// maxPollVoteNotes is how many vote lines are shown under a poll.
const maxPollVoteNotes = 3

// pollVoteTarget returns the ID of the poll a vote message is for, or "".
func pollVoteTarget(msg wireMsg) string {
	update, ok := msg.Message["pollUpdateMessage"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := update["pollMsgID"].(string)
	return id
}

// groupPollVotes finds the vote messages that belong under a poll in this chat.
// under maps a poll's message ID to its votes, oldest first, and hidden holds the
// IDs of those vote messages, which are not drawn on their own. A vote for a
// poll that is not in the chat stays visible, so it never disappears.
func groupPollVotes(items []wireMsg) (under map[string][]wireMsg, hidden map[string]bool) {
	polls := map[string]bool{}
	for _, msg := range items {
		if _, ok := msg.Message["pollCreationMessage"].(map[string]any); ok && msg.Key.ID != "" {
			polls[msg.Key.ID] = true
		}
	}
	under, hidden = map[string][]wireMsg{}, map[string]bool{}
	for _, msg := range items {
		target := pollVoteTarget(msg)
		if target == "" || !polls[target] {
			continue
		}
		under[target] = append(under[target], msg)
		if msg.Key.ID != "" {
			hidden[msg.Key.ID] = true
		}
	}
	return under, hidden
}

// pollVoteNotes lists the votes of a poll as plain lines for inside its card: the
// latest few, with a line for how many earlier ones are folded away.
func (x m) pollVoteNotes(votes []wireMsg) []pollNote {
	if len(votes) == 0 {
		return nil
	}
	var lines []pollNote
	first := 0
	if len(votes) > maxPollVoteNotes {
		first = len(votes) - maxPollVoteNotes
		lines = append(lines, pollNote{{text: fmt.Sprintf("... %d earlier votes", first)}})
	}
	for i := first; i < len(votes); i++ {
		lines = append(lines, x.pollVoteNote(votes[i], x.priorChoice(votes, i)))
	}
	return lines
}

// capLines keeps the first n lines of a wrapped text. When lines are dropped the
// last one ends in "..." so it is clear there was more. w is the line width.
func capLines(lines []string, n, w int) []string {
	if len(lines) <= n {
		return lines
	}
	out := append([]string(nil), lines[:n]...)
	out[n-1] = truncate(out[n-1]+" ...", w)
	return out
}
