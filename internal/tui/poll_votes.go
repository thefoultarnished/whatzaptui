package tui

import (
	"fmt"
	"strings"

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
// the number of voters at the bottom. Without votes it is the plain card.
func renderPollCard(v map[string]any, votes pollVotes) string {
	name, _ := v["name"].(string)
	opts := pollCardOptions(v)
	counts, voters, mine := pollTally(opts, votes)
	showVotes := voters > 0

	minW, maxW, extra := 30, 38, 0
	if showVotes {
		// Room for a gap, the bar, a gap and a two-digit count.
		extra = 1 + pollBarCells + 1 + 2
		minW, maxW = 34, 44
	}
	cardWidth := len(name) + 6
	for _, o := range opts {
		if len(o)+8+extra > cardWidth {
			cardWidth = len(o) + 8 + extra
		}
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
	qText := " " + name
	if len([]rune(qText)) > cardWidth-4 {
		qText = string([]rune(qText)[:cardWidth-7]) + "..."
	} else {
		qText = qText + strings.Repeat(" ", cardWidth-4-len([]rune(qText)))
	}
	sb.WriteString(borderStyle.Render(" │ ") + lipgloss.NewStyle().Bold(true).Foreground(questionCol).Render(qText) + borderStyle.Render("│") + "\n")
	sb.WriteString(borderStyle.Render(" ├"+strings.Repeat("─", cardWidth-3)+"┤") + "\n")
	for i, opt := range opts {
		bullet := accentBorderStyle.Render("◯")
		if showVotes && mine[i] {
			bullet = accentBorderStyle.Render("●")
		}
		row := borderStyle.Render(" │  ") + bullet + " " + fitCell(opt, cardWidth-8-extra)
		if showVotes {
			row += " " + accentBorderStyle.Render(pollBar(counts[i], voters)) + fmt.Sprintf(" %2d", counts[i])
		}
		sb.WriteString(row + borderStyle.Render(" │") + "\n")
	}
	if showVotes {
		label := fmt.Sprintf("%d votes", voters)
		if voters == 1 {
			label = "1 vote"
		}
		sb.WriteString(borderStyle.Render(" │ ") + mutedStyle.Render(fitCell(" "+label, cardWidth-4)) + borderStyle.Render("│") + "\n")
	}
	sb.WriteString(borderStyle.Render(" ╰" + strings.Repeat("─", cardWidth-3) + "╯"))
	return sb.String()
}

// pollVoteLine is the chat line for a vote: who voted and for what, for example
// Bob voted "Pizza" on poll. When the vote could not be read it only says who
// voted, and a removed vote says so.
func (x m) pollVoteLine(msg wireMsg) string {
	who := "You"
	if !msg.Key.FromMe {
		who = strings.TrimSpace(x.senderNameForMsg(msg))
		if who == "" {
			who = "Someone"
		}
	}
	update, _ := msg.Message["pollUpdateMessage"].(map[string]any)
	tag := mediaTagStyle("poll").Render(mediaIconLabel("poll"))
	names, known := voteNames(update)
	switch {
	case known && len(names) == 0:
		their := "their"
		if msg.Key.FromMe {
			their = "your"
		}
		return tag + " " + who + " removed " + their + " vote on poll"
	case known:
		return tag + " " + who + " voted \"" + strings.Join(names, ", ") + "\" on poll"
	}
	return tag + " " + who + " voted on poll"
}
