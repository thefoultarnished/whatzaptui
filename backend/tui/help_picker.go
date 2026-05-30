package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var helpCommands = []struct {
	cmd  string
	desc string
}{
	// Interface
	{"/help", "Show commands"},
	{"/theme", "Change color theme"},
	{"/pointer", "Change message icon"},
	{"/settings", "Open settings panel"},
	{"/emoji", "Open emoji picker"},
	{"/mouseon", "Enable mouse"},
	{"/mouseoff", "Disable mouse"},
	// Contacts
	{"/rename", "Rename a contact"},
	{"/whitelist", "Allow a contact"},
	{"/whitelistall", "Allow all"},
	{"/blacklist", "Block a contact"},
	{"/blacklistall", "Block all"},
	{"/block", "Block on WhatsApp"},
	{"/synccontacts", "Sync contacts"},
	{"/syncgroups", "Sync groups"},
	// Sounds
	{"/soundon", "Enable sounds"},
	{"/soundoff", "Disable sounds"},
	{"/sound1", "Sound profile 1"},
	{"/sound2", "Sound profile 2"},
	{"/sound3", "Sound profile 3"},
	{"/sound4", "Sound profile 4"},
	{"/sound5", "Sound profile 5"},
	// Session
	{"/logout", "Log out"},
	{"/restart", "Restart app"},
	{"/exit", "Exit"},
}

var helpGroupDefs = []struct {
	name  string
	count int
}{
	{"Interface", 7},
	{"Contacts", 8},
	{"Sounds", 7},
	{"Session", 3},
}

func buildHelpPickerItems() []pickerItem {
	items := make([]pickerItem, len(helpCommands))
	for i, c := range helpCommands {
		items[i] = pickerItem{key: c.cmd, label: c.cmd + "  " + c.desc}
	}
	return items
}

// helpGroupOffset returns the flat-index start of group g.
func helpGroupOffset(g int) int {
	offset := 0
	for i := 0; i < g; i++ {
		offset += helpGroupDefs[i].count
	}
	return offset
}

// helpVisualPos returns the group index, visual row, and visual column for a flat index.
func helpVisualPos(idx int) (g, row, col int) {
	offset := 0
	for gi, grp := range helpGroupDefs {
		if idx < offset+grp.count {
			local := idx - offset
			return gi, local / 3, local % 3
		}
		offset += grp.count
	}
	return len(helpGroupDefs) - 1, 0, 0
}

// HandleHelp processes key events with proper row-major 2D navigation for the
// 3-column grouped layout rendered by RenderHelp.
func (p *picker) HandleHelp(k tea.KeyMsg) (action string, done bool) {
	switch k.Type {
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	g, row, col := helpVisualPos(p.idx)
	grp := helpGroupDefs[g]
	rows := (grp.count + 2) / 3

	switch k.Type {
	case tea.KeyRight:
		// Move right within the current row; stop at row edge.
		localIdx := row*3 + col
		if col < 2 && localIdx+1 < grp.count {
			p.idx++
		}
	case tea.KeyLeft:
		// Move left within the current row; stop at row edge.
		if col > 0 {
			p.idx--
		}
	case tea.KeyDown:
		if row < rows-1 {
			// Same column, next row within group (clamp if partial row).
			newLocal := (row+1)*3 + col
			if newLocal >= grp.count {
				newLocal = grp.count - 1
			}
			p.idx = helpGroupOffset(g) + newLocal
		} else if g < len(helpGroupDefs)-1 {
			// Cross into first row of next group, same column (clamped).
			nextCount := helpGroupDefs[g+1].count
			newCol := col
			if newCol >= nextCount {
				newCol = nextCount - 1
			}
			p.idx = helpGroupOffset(g+1) + newCol
		}
	case tea.KeyUp:
		if row > 0 {
			// Same column, previous row within group.
			p.idx = helpGroupOffset(g) + (row-1)*3 + col
		} else if g > 0 {
			// Cross into last row of previous group, same column (clamped).
			prevCount := helpGroupDefs[g-1].count
			prevRows := (prevCount + 2) / 3
			newLocal := (prevRows-1)*3 + col
			if newLocal >= prevCount {
				newLocal = prevCount - 1
			}
			p.idx = helpGroupOffset(g-1) + newLocal
		}
	}
	return "", false
}

// RenderHelp renders a 3-column grouped command palette for the help picker.
// Depth is achieved via a raised surface color (SidebarActiveBg) applied to
// every style — no border characters, so the panel floats cleanly.
func (p *picker) RenderHelp(w, h int) string {
	const numCols = 3
	const padH = 3 // horizontal padding inside the panel

	pickerW := min(w-4, 100)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / numCols

	// Elevated surface: slightly lighter than the app background — this IS the depth.
	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)

	// bg adds panelBg to any style so every character cell is on the raised surface.
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt   := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt    := bg(lipgloss.NewStyle().Foreground(muted))
	sectionSt    := bg(lipgloss.NewStyle().Foreground(purple).Bold(true))
	sectionDivSt := bg(lipgloss.NewStyle().Foreground(purple))
	cmdSt        := bg(lipgloss.NewStyle().Foreground(text).Bold(true))
	descSt       := bg(lipgloss.NewStyle().Foreground(muted))
	activeCmdSt  := bg(lipgloss.NewStyle().Foreground(accent).Bold(true).Underline(true))
	activeDescSt := bg(lipgloss.NewStyle().Foreground(text))
	keySt     := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt     := bg(lipgloss.NewStyle().Foreground(muted))

	// fill pads a rendered string to full innerW on the panel surface.
	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln := func(s string) string { return fill.Render(s) }

	// colFill pads each item cell to colW on the panel surface.
	colFill := bg(lipgloss.NewStyle().Width(colW))

	divLine := ln(divSt.Render(strings.Repeat("─", innerW)))

	titleRunes := len([]rune(p.title))
	gapW := max(0, innerW-titleRunes-3) // 3 = len("esc")
	titleRow := ln(titleSt.Render(p.title) +
		bg(lipgloss.NewStyle().Width(gapW)).Render("") +
		hintSt.Render("esc"))

	lines := []string{titleRow, divLine, ln("")}

	descOf := make(map[string]string, len(helpCommands))
	for _, hc := range helpCommands {
		descOf[hc.cmd] = hc.desc
	}

	// sp and indent are background-safe: plain spaces would bleed terminal black.
	sp     := bg(lipgloss.NewStyle()).Render(" ")
	indent := bg(lipgloss.NewStyle()).Render("  ")

	itemOffset := 0
	for gi, g := range helpGroupDefs {
		nameW := len([]rune(g.name))
		lines = append(lines,
			ln(sectionSt.Render(g.name)),
			ln(sectionDivSt.Render(strings.Repeat("─", nameW))),
		)

		rows := (g.count + numCols - 1) / numCols
		for r := range rows {
			var row strings.Builder
			for c := range numCols {
				localIdx := r*numCols + c
				if localIdx >= g.count {
					row.WriteString(colFill.Render(""))
					continue
				}
				fi := itemOffset + localIdx
				item := p.items[fi]
				desc := descOf[item.key]

				maxDesc := colW - len([]rune(item.key)) - 4
				if maxDesc < 0 {
					maxDesc = 0
				}
				if len([]rune(desc)) > maxDesc {
					desc = string([]rune(desc)[:maxDesc])
				}

				var cell string
				if fi == p.idx {
					cell = colFill.Render(indent + activeCmdSt.Render(item.key) + sp + activeDescSt.Render(desc))
				} else {
					cell = colFill.Render(indent + cmdSt.Render(item.key) + sp + descSt.Render(desc))
				}
				row.WriteString(cell)
			}
			lines = append(lines, ln(row.String()))
		}
		itemOffset += g.count
		if gi < len(helpGroupDefs)-1 {
			lines = append(lines, ln(""))
		}
	}

	hint := ln(keySt.Render("↑→↓←") + hintSt.Render(" navigate  ") +
		keySt.Render("Enter") + hintSt.Render(" run  ") +
		keySt.Render("Esc") + hintSt.Render(" close"))

	lines = append(lines, ln(""), divLine, hint)

	// No border — the surface color contrast creates the floating depth effect.
	box := lipgloss.NewStyle().
		Background(panelBg).
		Padding(1, padH).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
