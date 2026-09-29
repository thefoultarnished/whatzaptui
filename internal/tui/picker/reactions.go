package picker

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HandleReactions scrolls the reactions list one entry at a time. It returns
// true when the panel should close (Esc, Enter or q).
func (p *Picker) HandleReactions(k tea.KeyMsg) (done bool) {
	switch k.Type {
	case tea.KeyEsc, tea.KeyEnter:
		return true
	case tea.KeyUp:
		if p.Idx > 0 {
			p.Idx--
		}
	case tea.KeyDown:
		if p.Idx < len(p.Items)-1 {
			p.Idx++
		}
	case tea.KeyRunes:
		if string(k.Runes) == "q" {
			return true
		}
	}
	return false
}

// wrapList word-wraps a comma separated list to at most width cells per line,
// breaking only after a comma. A single item wider than width is left whole.
func wrapList(s string, width int) []string {
	parts := strings.Split(s, ", ")
	var lines []string
	cur := ""
	for i, part := range parts {
		piece := part
		if i < len(parts)-1 {
			piece += ","
		}
		if cur == "" {
			cur = piece
			continue
		}
		if Width(cur+" "+piece) > width {
			lines = append(lines, cur)
			cur = piece
			continue
		}
		cur += " " + piece
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// RenderReactionsBox draws who reacted to a message: one entry per emoji, with
// Key (emoji and count) on the left and Desc (the people) wrapped on the right.
// It returns just the box so the caller can draw it over the chat instead of
// replacing it.
func (p *Picker) RenderReactionsBox(s Style, w, h int) string {
	const padH = 3

	pickerW := min(w-4, 60)
	if pickerW < 36 {
		pickerW = min(w, 36)
	}
	innerW := pickerW - padH*2

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	keySt := bg(lipgloss.NewStyle().Foreground(s.Text).Bold(true))
	descSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Muted)))
	moreSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
	sp := func(n int) string { return bg(lipgloss.NewStyle()).Render(strings.Repeat(" ", max(0, n))) }

	keyW := 0
	for _, it := range p.Items {
		keyW = max(keyW, Width(it.Key))
	}
	keyW = min(keyW+2, innerW/3)
	descW := max(8, innerW-keyW)

	// Rows that fit: the panel adds a title, dividers, a hint row and padding.
	maxLines := max(3, h-10)

	start := min(max(p.Idx, 0), max(0, len(p.Items)-1))
	var body []string
	shown := start
	for i := start; i < len(p.Items); i++ {
		it := p.Items[i]
		wrapped := wrapList(it.Desc, descW)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		if len(body)+len(wrapped) > maxLines && len(body) > 0 {
			break
		}
		for j, seg := range wrapped {
			lead := sp(keyW)
			if j == 0 {
				key := it.Key
				lead = keySt.Render(key) + sp(keyW-Width(key))
			}
			body = append(body, ln(lead+descSt.Render(seg)))
		}
		shown = i + 1
	}

	lines := []string{titleRow, divLine, ln("")}
	if start > 0 {
		lines = append(lines, ln(moreSt.Render("▲ more above")))
	}
	lines = append(lines, body...)
	if shown < len(p.Items) {
		lines = append(lines, ln(moreSt.Render("▼ more below")))
	}
	hint := ln(s.hintRow(bg, "↑↓", "scroll", "Esc", "close"))
	lines = append(lines, ln(""), divLine, hint)
	return s.panelBox(lines, pickerW, padH, w, h)
}
