package picker

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// VisibleItems is the entries whose label contains Query (ignoring case). The
// selection Idx points into this list.
func (p *Picker) VisibleItems() []Item {
	q := strings.ToLower(strings.TrimSpace(p.Query))
	if q == "" {
		return p.Items
	}
	var out []Item
	for _, it := range p.Items {
		if strings.Contains(strings.ToLower(it.Label), q) {
			out = append(out, it)
		}
	}
	return out
}

// HandleFilterList handles a list you can type into to narrow it. It returns
// ("confirm", true) on Enter, ("cancel", true) on Esc, and ("", false) when the
// list only moved or the filter changed.
func (p *Picker) HandleFilterList(k tea.KeyMsg) (action string, done bool) {
	switch k.Type {
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	case tea.KeyUp:
		if n := len(p.VisibleItems()); n > 0 {
			p.Idx = (p.Idx - 1 + n) % n
		}
	case tea.KeyDown:
		if n := len(p.VisibleItems()); n > 0 {
			p.Idx = (p.Idx + 1) % n
		}
	case tea.KeyBackspace:
		if r := []rune(p.Query); len(r) > 0 {
			p.Query = string(r[:len(r)-1])
			p.Idx = 0
		}
	case tea.KeyRunes, tea.KeySpace:
		if typed := strings.ReplaceAll(string(k.Runes), "\n", ""); typed != "" && !k.Alt {
			p.Query += typed
			p.Idx = 0
		} else if k.Type == tea.KeySpace {
			p.Query += " "
			p.Idx = 0
		}
	}
	return "", false
}

// RenderFilterListBox draws the filterable list as a raised panel: a search
// line, the visible entries (scrolling around the selection) and a hint row.
// Entries with Dim set are drawn muted, with Desc as a note on the right.
func (p *Picker) RenderFilterListBox(s Style, w, h int) string {
	const padH = 3

	pickerW := min(w-4, 60)
	if pickerW < 40 {
		pickerW = min(w, 40)
	}
	innerW := pickerW - padH*2

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	selBg := s.pick(s.BgSelected, s.Accent)
	textSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Text)))
	dimSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
	moreSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
	queryPlain := bg(lipgloss.NewStyle().Foreground(s.Text))
	indent := bg(lipgloss.NewStyle()).Render("  ")

	items := p.VisibleItems()
	idx := min(max(p.Idx, 0), max(0, len(items)-1))

	search := bg(lipgloss.NewStyle().Foreground(s.Muted)).Render("⌕ ")
	if p.Query == "" {
		search += dimSt.Render("type to filter")
	} else {
		search += queryPlain.Render(p.Query)
	}
	search += bg(lipgloss.NewStyle().Foreground(s.Accent)).Render("|")

	// Rows for entries: the pane minus the title, search line, dividers, hint,
	// blank lines, panel padding, outline and a spare row above and below.
	const chrome = 9 + 2 + 2 + 2
	maxRows := max(3, h-chrome)
	start := 0
	if len(items) > maxRows {
		start = min(max(idx-maxRows/2, 0), len(items)-maxRows)
	}
	end := min(len(items), start+maxRows)

	var body []string
	if len(items) == 0 {
		empty := "no matching chats"
		if p.EmptyText != "" {
			empty = p.EmptyText
		}
		body = append(body, ln(dimSt.Render(empty)))
	}
	for i := start; i < end; i++ {
		it := items[i]
		note := ""
		if it.Desc != "" {
			note = "  " + it.Desc
		}
		label := Truncate(it.Label, max(1, innerW-4-Width(note)))
		st := textSt
		if it.Dim {
			st = dimSt
		}
		var row string
		if i == idx {
			sel := lipgloss.NewStyle().Background(selBg)
			if s.V2 {
				row = sel.Width(innerW).Render(s.marker(selBg) + sel.Render(" ") + sel.Foreground(s.Text).Bold(!it.Dim).Render(label) + sel.Foreground(s.Muted).Render(note))
			} else {
				row = sel.Width(innerW).Render(sel.Render("  ") + sel.Foreground(s.BadgeInk).Bold(true).Render(label) + sel.Render(note))
			}
		} else {
			row = ln(indent + st.Render(label) + dimSt.Render(note))
		}
		body = append(body, row)
	}
	if start > 0 {
		body[0] = ln(moreSt.Render("▲ more above"))
	}
	if end < len(items) {
		body[len(body)-1] = ln(moreSt.Render("▼ more below"))
	}

	lines := []string{titleRow, divLine, ln(""), ln(search), ln("")}
	lines = append(lines, body...)
	enter := "send"
	if p.EnterLabel != "" {
		enter = p.EnterLabel
	}
	hint := ln(s.hintRow(bg, "↑↓", "choose", "Enter", enter, "Esc", "cancel"))
	lines = append(lines, ln(""), divLine, hint)
	return s.panelBox(lines, pickerW, padH, w, h)
}
