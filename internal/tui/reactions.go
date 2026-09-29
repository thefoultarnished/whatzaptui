package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"whatzap/internal/tui/picker"
)

// reactionGroup is everyone who reacted to a message with one emoji.
type reactionGroup struct {
	emoji string
	names []string
}

// reactionGroups groups the reactions on a message by emoji, most used first
// (ties keep the order they were first seen). Names are full display names.
func (x m) reactionGroups(items []wireMsg, targetID string) []reactionGroup {
	var groups []reactionGroup
	index := map[string]int{}
	for _, r := range x.collectReactions(items)[targetID] {
		i, ok := index[r.emoji]
		if !ok {
			i = len(groups)
			index[r.emoji] = i
			groups = append(groups, reactionGroup{emoji: r.emoji})
		}
		groups[i].names = append(groups[i].names, r.full)
	}
	sort.SliceStable(groups, func(i, j int) bool { return len(groups[i].names) > len(groups[j].names) })
	return groups
}

// openReactionList shows who reacted to the selected message (Alt+G). It needs
// a message picked with Alt+R or a click, and closes with Esc.
func (x *m) openReactionList() tea.Cmd {
	if x.selectedMsgID == "" {
		return x.setTopBar("Select a message first (Alt+R or click), then Alt+G")
	}
	groups := x.reactionGroups(x.msgs[x.active], x.selectedMsgID)
	if len(groups) == 0 {
		return x.setTopBar("No reactions on this message")
	}
	items := make([]picker.Item, len(groups))
	for i, g := range groups {
		items[i] = picker.Item{
			Key:  fmt.Sprintf("%s %d", g.emoji, len(g.names)),
			Desc: strings.Join(g.names, ", "),
		}
	}
	// Leave the reply picker without starting a reply; the message stays
	// selected until the list closes.
	x.replyPickMode = false
	x.editPickMode = false
	x.reactionPicker = picker.New("Reactions", items)
	x.reactionPicker.Open("")
	x.invalidate()
	return nil
}

// closeReactionList closes the list and clears the message selection.
func (x *m) closeReactionList() {
	x.reactionPicker.Close(false)
	x.selectedMsgID = ""
	x.invalidate()
}
