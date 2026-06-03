package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var typingAnimationList = []struct {
	key         string
	displayName string
	icons       []string
}{
	{"dots", "Animated dots", []string{"●○○", "○●○", "○○●"}},
	{"braille", "Braille", []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}},
	{"sparkle", "Sparkle", []string{"✿", "✦", "◇", "☆", "⌘"}},
	{"bars", "Loading bars", []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"}},
	{"pulse", "Pulse", []string{"○", "◉", "○"}},
	{"arrows", "Rotating arrows", []string{"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"}},
}

func buildTypingAnimationPickerItems() []pickerItem {
	items := make([]pickerItem, len(typingAnimationList))
	for i, a := range typingAnimationList {
		items[i] = pickerItem{key: a.key, label: fmt.Sprintf("%s  %s", a.icons[0], a.displayName)}
	}
	return items
}

func getTypingIcons(style string) []string {
	for _, a := range typingAnimationList {
		if a.key == style {
			return a.icons
		}
	}
	return typingAnimationList[2].icons
}

func (p *picker) RenderTypingAnimation(w, h int) string {
	const padH = 3

	pickerW := min(w-4, 80)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / 2

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(muted))

	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln := func(s string) string { return fill.Render(s) }
	colFill := bg(lipgloss.NewStyle().Width(colW))

	divLine := ln(divSt.Render(strings.Repeat("─", innerW)))

	titleRunes := len([]rune(p.title))
	gapW := max(0, innerW-titleRunes-3)
	titleRow := ln(titleSt.Render(p.title) +
		bg(lipgloss.NewStyle().Width(gapW)).Render("") +
		hintSt.Render("esc"))

	lines := []string{titleRow, divLine, ln("")}

	indent := bg(lipgloss.NewStyle()).Render("  ")

	activePanelBg := lipgloss.Color(currentTheme.ShortcutActive)
	activeBg := lipgloss.NewStyle().Background(activePanelBg)
	activeLn := func(s string) string { return lipgloss.NewStyle().Background(activePanelBg).Width(innerW).Render(s) }
	activeIndent := activeBg.Render("  ")

	rows := p.rows()
	rightLen := p.rightColLen()

	for r := range rows {
		var row strings.Builder
		rowHasActive := false

		leftIdx := r
		item := p.items[leftIdx]
		dotColor := muted
		dot := "✨ "

		var leftCell string
		if leftIdx == p.idx {
			rowHasActive = true
			dotColor = accent
			dotSt := activeBg.Foreground(dotColor).Bold(true)
			nameSt := activeBg.Foreground(accent).Bold(true).Underline(true)
			leftCell = colFill.Render(activeIndent + dotSt.Render(dot) + nameSt.Render(item.label))
		} else {
			dotSt := bg(lipgloss.NewStyle().Foreground(dotColor).Bold(true))
			nameSt := bg(lipgloss.NewStyle().Foreground(text).Bold(true))
			leftCell = colFill.Render(indent + dotSt.Render(dot) + nameSt.Render(item.label))
		}

		var rightCell string
		if r < rightLen {
			rightIdx := p.fromColRow(1, r)
			item2 := p.items[rightIdx]
			dotColor2 := muted
			dot2 := "✨ "

			if rightIdx == p.idx {
				rowHasActive = true
				dotColor2 = accent
				dotSt := activeBg.Foreground(dotColor2).Bold(true)
				nameSt := activeBg.Foreground(accent).Bold(true).Underline(true)
				rightCell = colFill.Render(activeIndent + dotSt.Render(dot2) + nameSt.Render(item2.label))
			} else {
				dotSt := bg(lipgloss.NewStyle().Foreground(dotColor2).Bold(true))
				nameSt := bg(lipgloss.NewStyle().Foreground(text).Bold(true))
				rightCell = colFill.Render(indent + dotSt.Render(dot2) + nameSt.Render(item2.label))
			}
		}

		row.WriteString(leftCell)
		row.WriteString(rightCell)

		if rowHasActive {
			lines = append(lines, activeLn(row.String()))
		} else {
			lines = append(lines, ln(row.String()))
		}
	}

	lines = append(lines, ln(""))
	lines = append(lines, ln(divLine))

	hint := ln(keySt.Render("↑→↓←") + hintSt.Render(" navigate  ") +
		keySt.Render("Enter") + hintSt.Render(" confirm  ") +
		keySt.Render("Esc") + hintSt.Render(" cancel"))
	lines = append(lines, hint)

	box := lipgloss.NewStyle().
		Background(panelBg).
		Padding(1, padH).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
