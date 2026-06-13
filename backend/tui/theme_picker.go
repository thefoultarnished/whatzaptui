package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var themeList = []struct {
	name        string
	displayName string
	theme       Theme
}{
	{"halo", "Halo", Halo},
	{"cornflower", "Cornflower", Cornflower},
	{"whatsapp", "WhatsApp", WhatsApp},
	{"linen", "Linen", Linen},
	{"tokyonight", "Tokyo Night", TokyoNight},
	{"catppuccin", "Catppuccin", Catppuccin},
	{"monokai", "Monokai", Monokai},
	{"charcoal", "Charcoal", Charcoal},
	{"aurora", "Aurora", Aurora},
	{"sakura", "Sakura", Sakura},
	{"abyssal", "Abyssal", Abyssal},
	{"cyberpunk", "Cyberpunk", Cyberpunk},
	{"lilac", "Lilac", Lilac},
	{"ember", "Ember", Ember},
	{"glacier", "Glacier", Glacier},
	{"verdant", "Verdant", Verdant},
	{"dusk", "Dusk", Dusk},
	{"fossil", "Fossil", Fossil},
	{"nord", "Nord", Nord},
}

var themeGroupOrder = []struct {
	name        string
	displayName string
	theme       Theme
}{
	// Dark
	{"whatsapp", "WhatsApp", WhatsApp},
	{"tokyonight", "Tokyo Night", TokyoNight},
	{"catppuccin", "Catppuccin", Catppuccin},
	{"monokai", "Monokai", Monokai},
	{"charcoal", "Charcoal", Charcoal},
	{"aurora", "Aurora", Aurora},
	{"abyssal", "Abyssal", Abyssal},
	{"cyberpunk", "Cyberpunk", Cyberpunk},
	// Warm
	{"ember", "Ember", Ember},
	{"sakura", "Sakura", Sakura},
	{"dusk", "Dusk", Dusk},
	{"fossil", "Fossil", Fossil},
	// Cool
	{"glacier", "Glacier", Glacier},
	{"verdant", "Verdant", Verdant},
	{"halo", "Halo", Halo},
	{"cornflower", "Cornflower", Cornflower},
	{"linen", "Linen", Linen},
	{"lilac", "Lilac", Lilac},
	{"nord", "Nord", Nord},
}

var themeGroupDefs = []struct {
	name  string
	count int
}{
	{"Dark", 8},
	{"Warm", 4},
	{"Cool", 7},
}

func applyThemeByName(name string) {
	for _, t := range themeList {
		if t.name == name {
			currentTheme = t.theme
			currentConfig.ThemeName = name
			rehashStyles()
			return
		}
	}
	currentTheme = themeList[0].theme
	currentConfig.ThemeName = themeList[0].name
	rehashStyles()
}

func buildThemePickerItems() []pickerItem {
	items := make([]pickerItem, len(themeGroupOrder))
	for i, t := range themeGroupOrder {
		items[i] = pickerItem{key: t.name, label: t.displayName}
	}
	return items
}

func (p *picker) themeGroupOffset(g int) int {
	offset := 0
	for i := 0; i < g; i++ {
		offset += themeGroupDefs[i].count
	}
	return offset
}

func themeVisualPos(idx int) (g, row, col int) {
	offset := 0
	for gi, grp := range themeGroupDefs {
		if idx < offset+grp.count {
			local := idx - offset
			return gi, local / 2, local % 2
		}
		offset += grp.count
	}
	return len(themeGroupDefs) - 1, 0, 0
}

// HandleTheme processes key events with 2-column grouped navigation for the
// theme picker, matching RenderTheme's row-major layout.
func (p *picker) HandleTheme(k tea.KeyMsg) (action string, done bool) {
	const numCols = 2
	switch k.Type {
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	g, row, col := themeVisualPos(p.idx)
	grp := themeGroupDefs[g]
	rows := (grp.count + numCols - 1) / numCols

	switch k.Type {
	case tea.KeyRight:
		localIdx := row*numCols + col
		if col < numCols-1 && localIdx+1 < grp.count {
			p.idx++
		}
	case tea.KeyLeft:
		if col > 0 {
			p.idx--
		}
	case tea.KeyDown:
		if row < rows-1 {
			newLocal := (row+1)*numCols + col
			if newLocal >= grp.count {
				newLocal = grp.count - 1
			}
			p.idx = p.themeGroupOffset(g) + newLocal
		} else if g < len(themeGroupDefs)-1 {
			nextCount := themeGroupDefs[g+1].count
			newCol := col
			if newCol >= nextCount {
				newCol = nextCount - 1
			}
			p.idx = p.themeGroupOffset(g+1) + newCol
		}
	case tea.KeyUp:
		if row > 0 {
			p.idx = p.themeGroupOffset(g) + (row-1)*numCols + col
		} else if g > 0 {
			prevCount := themeGroupDefs[g-1].count
			prevRows := (prevCount + numCols - 1) / numCols
			newLocal := (prevRows-1)*numCols + col
			if newLocal >= prevCount {
				newLocal = prevCount - 1
			}
			p.idx = p.themeGroupOffset(g-1) + newLocal
		}
	}
	return "", false
}

// RenderTheme renders a 2-column grouped palette for the theme picker,
// using the same raised-surface depth effect as the help picker.
func (p *picker) RenderTheme(w, h int) string {
	const numCols = 2
	const padH = 3

	pickerW := min(w-4, 60)
	if pickerW < 40 {
		pickerW = min(w, 40)
	}
	innerW := pickerW - padH*2
	colW := innerW / numCols

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)

	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	sectionSt := bg(lipgloss.NewStyle().Foreground(purple).Bold(true))
	sectionDivSt := bg(lipgloss.NewStyle().Foreground(purple))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(muted))

	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln := func(s string) string { return fill.Render(s) }

	colFill := bg(lipgloss.NewStyle().Width(colW))

	activeBg := lipgloss.NewStyle().Background(accent).Width(innerW)
	activeLn := func(s string) string { return activeBg.Render(s) }

	divLine := ln(divSt.Render(strings.Repeat("─", innerW)))

	titleRunes := len([]rune(p.title))
	gapW := max(0, innerW-titleRunes-3)
	titleRow := ln(titleSt.Render(p.title) +
		bg(lipgloss.NewStyle().Width(gapW)).Render("") +
		hintSt.Render("esc"))

	lines := []string{titleRow, divLine, ln("")}

	indent := bg(lipgloss.NewStyle()).Render("  ")
	activeIndent := lipgloss.NewStyle().Background(accent).Render("  ")

	itemOffset := 0
	for gi, g := range themeGroupDefs {
		nameW := len([]rune(g.name))
		lines = append(lines,
			ln(sectionSt.Render(g.name)),
			ln(sectionDivSt.Render(strings.Repeat("─", nameW))),
		)

		rows := (g.count + numCols - 1) / numCols
		for r := range rows {
			var row strings.Builder
			rowHasActive := false
			for c := range numCols {
				localIdx := r*numCols + c
				if localIdx >= g.count {
					row.WriteString(colFill.Render(""))
					continue
				}
				fi := itemOffset + localIdx
				item := p.items[fi]
				anomalyColor := lipgloss.Color(themeGroupOrder[fi].theme.AnomalyTag)
				accentColor := lipgloss.Color(themeGroupOrder[fi].theme.Accent)

				var cell string
				if fi == p.idx {
					rowHasActive = true
					activeRowSt := lipgloss.NewStyle().Background(accent)
					activeSwatchSt := activeRowSt.Foreground(anomalyColor)
					themeItemSt := activeRowSt.Foreground(badgeInk).Bold(true).Underline(true)
					cell = activeRowSt.Width(colW).Render(activeIndent + activeSwatchSt.Render("■ ") + themeItemSt.Render(item.label))
				} else {
					swatchSt := bg(lipgloss.NewStyle().Foreground(anomalyColor).Background(anomalyColor))
					themeItemSt := bg(lipgloss.NewStyle().Foreground(accentColor).Bold(true))
					cell = colFill.Render(indent + swatchSt.Render("■ ") + themeItemSt.Render(item.label))
				}
				row.WriteString(cell)
			}
			if rowHasActive {
				lines = append(lines, activeLn(row.String()))
			} else {
				lines = append(lines, ln(row.String()))
			}
		}
		itemOffset += g.count
		if gi < len(themeGroupDefs)-1 {
			lines = append(lines, ln(""))
		}
	}

	hint := ln(keySt.Render("↑→↓←") + hintSt.Render(" navigate  ") +
		keySt.Render("Enter") + hintSt.Render(" apply  ") +
		keySt.Render("Esc") + hintSt.Render(" close"))

	lines = append(lines, ln(""), divLine, hint)

	box := lipgloss.NewStyle().
		Background(panelBg).
		Padding(1, padH).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
