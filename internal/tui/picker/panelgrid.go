package picker

import "github.com/charmbracelet/lipgloss"

// renderPanelBox draws a plain list picker as a raised panel with the same
// look as the theme and help pickers: two columns filled left to right, one
// row at a time, or one column for a short list.
func (p *Picker) renderPanelBox(s Style, w, h int) string {
	const padH = 3

	cols := 2
	if p.isSingleCol() {
		cols = 1
	}
	// Wide enough for the longest label in every column, plus its indent and gap.
	maxLabelW := 0
	for _, it := range p.Items {
		maxLabelW = max(maxLabelW, Width(it.Label))
	}
	pickerW := min(max((maxLabelW+4)*cols+padH*2, 44), max(40, w-4))
	pickerW = min(pickerW, w)
	innerW := pickerW - padH*2
	colW := innerW / cols

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	colFill := bg(lipgloss.NewStyle().Width(colW))
	indent := bg(lipgloss.NewStyle()).Render("  ")
	selBg := s.pick(s.BgSelected, s.Accent)
	textSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Text)))

	cell := func(i int) string {
		label := Truncate(p.Items[i].Label, max(1, colW-4))
		if i != p.Idx {
			return colFill.Render(indent + textSt.Render(label))
		}
		sel := lipgloss.NewStyle().Background(selBg)
		if s.V2 {
			return sel.Width(colW).Render(s.marker(selBg) + sel.Render(" ") + sel.Foreground(s.Text).Bold(true).Render(label))
		}
		return sel.Width(colW).Render(sel.Render("  ") + sel.Foreground(s.BadgeInk).Bold(true).Underline(true).Render(label))
	}

	lines := []string{titleRow, divLine, ln("")}
	// Row by row, like the theme picker: item 0 and 1 share the first row.
	for start := 0; start < len(p.Items); start += cols {
		row := ""
		for c := range cols {
			if start+c < len(p.Items) {
				row += cell(start + c)
			} else {
				row += colFill.Render("")
			}
		}
		lines = append(lines, ln(row))
	}

	keys := "↑↓"
	if cols == 2 {
		keys = "↑→↓←"
	}
	hint := ln(s.hintRow(bg, keys, "navigate", "Enter", "confirm", "Esc", "cancel"))
	lines = append(lines, ln(""), divLine, hint)
	return s.panelBox(lines, pickerW, padH, w, h)
}
