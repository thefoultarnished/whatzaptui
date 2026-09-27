package tui

import (
	"fmt"
	"strconv"
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
	{"squares", "Squares", []string{
		"··◼◼◼◼◼◼",
		"·◼◼◼◼◼◼·",
		"◼◼◼◼◼◼··",
		"◼◼◼◼◼◼··",
		"·◼◼◼◼◼◼·",
		"··◼◼◼◼◼◼",
	}},
}

var squaresShadesDark = []lipgloss.Color{
	"#58A6FF", // 0: Front face (brightest electric blue)
	"#3B82F6", // 1: Vivid cobalt
	"#2563EB", // 2: Royal blue
	"#1D4ED8", // 3: Deep blue
	"#1E3A8A", // 4: Dark navy
	"#101E3C", // 5: Deep shadow navy (darkest)
}

const squaresDotDark = lipgloss.Color("#283042")

var squaresShadesLight = []lipgloss.Color{
	"#0F172A", // 0: Front face (darkest / most contrast on light bg)
	"#1E3A8A", // 1
	"#1D4ED8", // 2
	"#2563EB", // 3
	"#60A5FA", // 4
	"#93C5FD", // 5: Back tail (faint)
}

const squaresDotLight = lipgloss.Color("#CBD5E1")

// renderSquaresIcon renders an 8-slot scanner animation of 6 squares that bounce
// left and right, with the leading square brightest and fading to dark towards the back,
// with dim dots for empty slots (matching Claude CLI / progress animation).
func renderSquaresIcon(frame int, bg lipgloss.Color) string {
	f := (frame%6 + 6) % 6
	slotsTable := [6][8]int{
		{-1, -1, 0, 1, 2, 3, 4, 5}, // f=0: moving left,  front=slot 2 (brightest), back=slot 7 (darkest)
		{-1, 0, 1, 2, 3, 4, 5, -1}, // f=1: moving left,  front=slot 1 (brightest), back=slot 6 (darkest)
		{0, 1, 2, 3, 4, 5, -1, -1}, // f=2: moving left,  front=slot 0 (brightest), back=slot 5 (darkest)
		{5, 4, 3, 2, 1, 0, -1, -1}, // f=3: moving right, front=slot 5 (brightest), back=slot 0 (darkest)
		{-1, 5, 4, 3, 2, 1, 0, -1}, // f=4: moving right, front=slot 6 (brightest), back=slot 1 (darkest)
		{-1, -1, 5, 4, 3, 2, 1, 0}, // f=5: moving right, front=slot 7 (brightest), back=slot 2 (darkest)
	}
	slots := slotsTable[f]

	shades := squaresShadesDark
	dotColor := squaresDotDark
	if relLuminance(string(background)) >= 0.5 {
		shades = squaresShadesLight
		dotColor = squaresDotLight
	}

	dotStyle := lipgloss.NewStyle().Foreground(dotColor)
	if bg != "" {
		dotStyle = dotStyle.Background(bg)
	}
	dotStr := dotStyle.Render("·")

	var sb strings.Builder
	for _, s := range slots {
		if s == -1 {
			sb.WriteString(dotStr)
		} else {
			sqStyle := lipgloss.NewStyle().Foreground(shades[s])
			if bg != "" {
				sqStyle = sqStyle.Background(bg)
			}
			sb.WriteString(sqStyle.Render("◼"))
		}
	}
	return sb.String()
}

func renderTypingItemCell(def struct {
	key         string
	displayName string
	icons       []string
}, isSelected bool, shineFrame int, colFill lipgloss.Style, indent, dot string, dotSt lipgloss.Style, itemBg lipgloss.Color, baseSt, shineSt, nameSt lipgloss.Style) string {
	if def.key == "squares" {
		var iconStr string
		var labelStr string
		if isSelected {
			iconStr = renderSquaresIcon(shineFrame, itemBg)
			labelStr = renderShine(def.displayName, baseSt, shineSt, shineFrame)
		} else {
			iconStr = renderSquaresIcon(0, itemBg)
			labelStr = nameSt.Render(def.displayName)
		}
		return colFill.Render(indent + dotSt.Render(dot) + iconStr + "  " + labelStr)
	}

	if isSelected {
		icon := def.icons[shineFrame%len(def.icons)]
		label := fmt.Sprintf("%s  %s", icon, def.displayName)
		return colFill.Render(indent + dotSt.Render(dot) + renderShine(label, baseSt, shineSt, shineFrame))
	}
	label := fmt.Sprintf("%s  %s", def.icons[0], def.displayName)
	return colFill.Render(indent + dotSt.Render(dot) + nameSt.Render(label))
}

func buildTypingAnimationPickerItems() []pickerItem {
	items := make([]pickerItem, len(typingAnimationList))
	for i, a := range typingAnimationList {
		items[i] = pickerItem{key: a.key, label: fmt.Sprintf("%s  %s", a.icons[0], a.displayName)}
	}
	return items
}

func getTypingIcons(style string) []string {
	return typingAnimationList[typingAnimationIndex(style)].icons
}

// typingAnimationIndex returns the typingAnimationList index for style,
// falling back to Sparkle when style is unset or unknown.
func typingAnimationIndex(style string) int {
	for i, a := range typingAnimationList {
		if a.key == style {
			return i
		}
	}
	return 2
}

// userlistIconPrefix returns the static icon glyph (plus trailing space) used
// in place of the row number when UserlistIconStyle names a sparkle icon
// (e.g. "sparkle-2"). Returns "" for "numbers" (or unset), which keeps the
// existing numbering.
func userlistIconPrefix(style string) string {
	idx, ok := strings.CutPrefix(style, "sparkle-")
	if !ok {
		return ""
	}
	i, err := strconv.Atoi(idx)
	if err != nil {
		return ""
	}
	for _, a := range typingAnimationList {
		if a.key == "sparkle" && i >= 0 && i < len(a.icons) {
			return a.icons[i] + " "
		}
	}
	return ""
}

func (p *picker) RenderTypingAnimation(w, h, shineFrame int) string {
	const padH = 3

	pickerW := min(w-4, 80)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / 2

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(v2Color(text, accent)).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(borderSubtle))

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
	if themeV2 {
		activeIndent = selectionMarker(activePanelBg) + activeBg.Render(" ")
	}

	rows := p.rows()
	rightLen := p.rightColLen()

	for r := range rows {
		var row strings.Builder
		rowHasActive := false

		leftIdx := r
		leftDef := typingAnimationList[leftIdx]
		leftIsActive := leftIdx == p.idx
		if leftIsActive {
			rowHasActive = true
		}

		dot := "\U000F0C52 "
		baseSt := activeBg.Foreground(v2Color(text, muted)).Bold(true).Underline(!themeV2)
		shineSt := activeBg.Foreground(accent).Bold(true).Underline(!themeV2)
		nameSt := bg(lipgloss.NewStyle().Foreground(v2Color(textSecondary, text)).Bold(!themeV2))

		var leftCell string
		if leftIsActive {
			dotSt := activeBg.Foreground(accent).Bold(true)
			leftCell = renderTypingItemCell(leftDef, true, shineFrame, colFill, activeIndent, dot, dotSt, activePanelBg, baseSt, shineSt, nameSt)
		} else {
			dotSt := bg(lipgloss.NewStyle().Foreground(muted).Bold(true))
			leftCell = renderTypingItemCell(leftDef, false, shineFrame, colFill, indent, dot, dotSt, panelBg, baseSt, shineSt, nameSt)
		}

		var rightCell string
		if r < rightLen {
			rightIdx := p.fromColRow(1, r)
			rightDef := typingAnimationList[rightIdx]
			rightIsActive := rightIdx == p.idx
			if rightIsActive {
				rowHasActive = true
			}

			if rightIsActive {
				dotSt := activeBg.Foreground(accent).Bold(true)
				rightCell = renderTypingItemCell(rightDef, true, shineFrame, colFill, activeIndent, dot, dotSt, activePanelBg, baseSt, shineSt, nameSt)
			} else {
				dotSt := bg(lipgloss.NewStyle().Foreground(muted).Bold(true))
				rightCell = renderTypingItemCell(rightDef, false, shineFrame, colFill, indent, dot, dotSt, panelBg, baseSt, shineSt, nameSt)
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
	box = withPanelOutline(box, w, h)

	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
