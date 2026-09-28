package picker

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// groupOffset returns the flat-index start of group g.
func (p *Picker) groupOffset(g int) int {
	offset := 0
	for i := 0; i < g; i++ {
		offset += p.Groups[i].Count
	}
	return offset
}

// groupPos returns the group, visual row and visual column of a flat index
// in a row-major grid of cols columns per group.
func (p *Picker) groupPos(idx, cols int) (g, row, col int) {
	offset := 0
	for gi, grp := range p.Groups {
		if idx < offset+grp.Count {
			local := idx - offset
			return gi, local / cols, local % cols
		}
		offset += grp.Count
	}
	return len(p.Groups) - 1, 0, 0
}

// handleGrouped moves through titled groups laid out cols-wide: left/right
// stay within a row, up/down cross into neighbouring groups keeping the
// column (clamped to that group's size).
func (p *Picker) handleGrouped(k tea.KeyMsg, cols int) (action string, done bool) {
	switch k.Type {
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	g, row, col := p.groupPos(p.Idx, cols)
	grp := p.Groups[g]
	rows := (grp.Count + cols - 1) / cols

	switch k.Type {
	case tea.KeyRight:
		localIdx := row*cols + col
		if col < cols-1 && localIdx+1 < grp.Count {
			p.Idx++
		}
	case tea.KeyLeft:
		if col > 0 {
			p.Idx--
		}
	case tea.KeyDown:
		if row < rows-1 {
			newLocal := (row+1)*cols + col
			if newLocal >= grp.Count {
				newLocal = grp.Count - 1
			}
			p.Idx = p.groupOffset(g) + newLocal
		} else if g < len(p.Groups)-1 {
			nextCount := p.Groups[g+1].Count
			newCol := col
			if newCol >= nextCount {
				newCol = nextCount - 1
			}
			p.Idx = p.groupOffset(g+1) + newCol
		}
	case tea.KeyUp:
		if row > 0 {
			p.Idx = p.groupOffset(g) + (row-1)*cols + col
		} else if g > 0 {
			prevCount := p.Groups[g-1].Count
			prevRows := (prevCount + cols - 1) / cols
			newLocal := (prevRows-1)*cols + col
			if newLocal >= prevCount {
				newLocal = prevCount - 1
			}
			p.Idx = p.groupOffset(g-1) + newLocal
		}
	}
	return "", false
}

const (
	themeCols = 2
	helpCols  = 3
)

// HandleTheme navigates the 2-column grouped theme grid.
func (p *Picker) HandleTheme(k tea.KeyMsg) (action string, done bool) {
	return p.handleGrouped(k, themeCols)
}

// HandleHelp navigates the 3-column grouped command grid.
func (p *Picker) HandleHelp(k tea.KeyMsg) (action string, done bool) {
	return p.handleGrouped(k, helpCols)
}

// groupHeader renders a group title with its underline.
func groupHeader(s Style, bg func(lipgloss.Style) lipgloss.Style, ln func(string) string, name string) []string {
	sectionSt := bg(lipgloss.NewStyle().Foreground(s.Purple).Bold(true))
	sectionDivSt := bg(lipgloss.NewStyle().Foreground(s.Purple))
	return []string{
		ln(sectionSt.Render(name)),
		ln(sectionDivSt.Render(strings.Repeat("─", len([]rune(name))))),
	}
}

// RenderTheme renders a 2-column grouped palette for the theme picker,
// using the same raised-surface depth effect as the help picker.
func (p *Picker) RenderTheme(s Style, w, h int) string {
	const padH = 3

	pickerW := min(w-4, 60)
	if pickerW < 40 {
		pickerW = min(w, 40)
	}
	innerW := pickerW - padH*2
	colW := innerW / themeCols

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	colFill := bg(lipgloss.NewStyle().Width(colW))

	// V2: the selected cell uses the shared selection surface, not an
	// Action fill.
	selBg := s.pick(s.BgSelected, s.Accent)
	activeBg := lipgloss.NewStyle().Background(selBg).Width(innerW)
	activeLn := func(str string) string { return activeBg.Render(str) }

	lines := []string{titleRow, divLine, ln("")}

	indent := bg(lipgloss.NewStyle()).Render("  ")
	activeIndent := lipgloss.NewStyle().Background(selBg).Render("  ")
	if s.V2 {
		activeIndent = s.marker(selBg) + lipgloss.NewStyle().Background(selBg).Render(" ")
	}

	itemOffset := 0
	for gi, g := range p.Groups {
		lines = append(lines, groupHeader(s, bg, ln, g.Name)...)

		rows := (g.Count + themeCols - 1) / themeCols
		for r := range rows {
			var row strings.Builder
			rowHasActive := false
			for c := range themeCols {
				localIdx := r*themeCols + c
				if localIdx >= g.Count {
					row.WriteString(colFill.Render(""))
					continue
				}
				fi := itemOffset + localIdx
				item := p.Items[fi]
				swatch, tint := item.Swatch, item.Tint
				if s.V2 {
					// Swatch + name preview each theme's identity pair.
					swatch, tint = item.SwatchV2, item.TintV2
				}

				var cell string
				if fi == p.Idx {
					rowHasActive = true
					activeRowSt := lipgloss.NewStyle().Background(selBg)
					activeSwatchSt := activeRowSt.Foreground(swatch)
					themeItemSt := activeRowSt.Foreground(s.BadgeInk).Bold(true).Underline(true)
					if s.V2 {
						themeItemSt = activeRowSt.Foreground(s.Text).Bold(true)
					}
					cell = activeRowSt.Width(colW).Render(activeIndent + activeSwatchSt.Render("■ ") + themeItemSt.Render(item.Label))
				} else {
					swatchSt := bg(lipgloss.NewStyle().Foreground(swatch).Background(swatch))
					themeItemSt := bg(lipgloss.NewStyle().Foreground(tint).Bold(true))
					cell = colFill.Render(indent + swatchSt.Render("■ ") + themeItemSt.Render(item.Label))
				}
				row.WriteString(cell)
			}
			if rowHasActive {
				lines = append(lines, activeLn(row.String()))
			} else {
				lines = append(lines, ln(row.String()))
			}
		}
		itemOffset += g.Count
		if gi < len(p.Groups)-1 {
			lines = append(lines, ln(""))
		}
	}

	hint := ln(s.hintRow(bg, "↑→↓←", "navigate", "Enter", "apply", "Esc", "close"))
	lines = append(lines, ln(""), divLine, hint)
	return s.placePanel(lines, pickerW, padH, w, h)
}

// RenderHelp renders a 3-column grouped command palette for the help picker.
// Depth is achieved via a raised surface colour applied to every style - no
// border characters, so the panel floats cleanly.
func (p *Picker) RenderHelp(s Style, w, h int) string {
	const padH = 3

	pickerW := min(w-4, 100)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / helpCols

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	cmdSt := bg(lipgloss.NewStyle().Foreground(s.Text).Bold(true))
	descSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Muted)))
	activeCmdSt := bg(lipgloss.NewStyle().Foreground(s.Accent).Bold(true).Underline(true))
	activeDescSt := bg(lipgloss.NewStyle().Foreground(s.Text))
	colFill := bg(lipgloss.NewStyle().Width(colW))

	lines := []string{titleRow, divLine, ln("")}

	// sp and indent are background-safe: plain spaces would bleed terminal black.
	sp := bg(lipgloss.NewStyle()).Render(" ")
	indent := bg(lipgloss.NewStyle()).Render("  ")

	itemOffset := 0
	for gi, g := range p.Groups {
		lines = append(lines, groupHeader(s, bg, ln, g.Name)...)

		rows := (g.Count + helpCols - 1) / helpCols
		for r := range rows {
			var row strings.Builder
			for c := range helpCols {
				localIdx := r*helpCols + c
				if localIdx >= g.Count {
					row.WriteString(colFill.Render(""))
					continue
				}
				fi := itemOffset + localIdx
				item := p.Items[fi]
				desc := item.Desc

				maxDesc := max(0, colW-len([]rune(item.Key))-4)
				if len([]rune(desc)) > maxDesc {
					desc = string([]rune(desc)[:maxDesc])
				}

				var cell string
				if fi == p.Idx && s.V2 {
					// Shared selection style: marker + BgSelected + TextPrimary.
					selSt := lipgloss.NewStyle().Background(s.BgSelected)
					cell = selSt.Width(colW).Render(s.marker(s.BgSelected) + selSt.Render(" ") +
						selSt.Foreground(s.Text).Bold(true).Render(item.Key) + selSt.Render(" ") + selSt.Foreground(s.Text).Render(desc))
				} else if fi == p.Idx {
					cell = colFill.Render(indent + activeCmdSt.Render(item.Key) + sp + activeDescSt.Render(desc))
				} else {
					cell = colFill.Render(indent + cmdSt.Render(item.Key) + sp + descSt.Render(desc))
				}
				row.WriteString(cell)
			}
			lines = append(lines, ln(row.String()))
		}
		itemOffset += g.Count
		if gi < len(p.Groups)-1 {
			lines = append(lines, ln(""))
		}
	}

	hint := ln(s.hintRow(bg, "↑→↓←", "navigate", "Enter", "run", "Esc", "close"))
	lines = append(lines, ln(""), divLine, hint)
	return s.placePanel(lines, pickerW, padH, w, h)
}
