package picker

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SquaresKey is the typing style drawn as a coloured scanner instead of
// cycling its plain-text frames.
const SquaresKey = "squares"

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

// SquaresIcon renders an 8-slot scanner animation of 6 squares that bounce
// left and right, with the leading square brightest and fading to dark
// towards the back, with dim dots for empty slots. light selects shades for
// a light app background; an empty bg leaves the cell background alone.
func SquaresIcon(frame int, bg lipgloss.Color, light bool) string {
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
	if light {
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

type typingCellStyle struct {
	colFill               lipgloss.Style
	indent, dot           string
	dotSt                 lipgloss.Style
	itemBg                lipgloss.Color
	baseSt, shineSt, name lipgloss.Style
}

func typingItemCell(s Style, item Item, isSelected bool, shineFrame int, cs typingCellStyle) string {
	prefix := cs.indent + cs.dotSt.Render(cs.dot)
	if item.Key == SquaresKey {
		if isSelected {
			return cs.colFill.Render(prefix + SquaresIcon(shineFrame, cs.itemBg, s.LightBackground) + "  " + s.shine(item.Name, cs.baseSt, cs.shineSt, shineFrame))
		}
		return cs.colFill.Render(prefix + SquaresIcon(0, cs.itemBg, s.LightBackground) + "  " + cs.name.Render(item.Name))
	}

	if isSelected {
		icon := item.Icons[shineFrame%len(item.Icons)]
		return cs.colFill.Render(prefix + s.shine(fmt.Sprintf("%s  %s", icon, item.Name), cs.baseSt, cs.shineSt, shineFrame))
	}
	return cs.colFill.Render(prefix + cs.name.Render(fmt.Sprintf("%s  %s", item.Icons[0], item.Name)))
}

func (p *Picker) RenderTypingAnimation(s Style, w, h, shineFrame int) string {
	const padH = 3

	pickerW := min(w-4, 80)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / 2

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	colFill := bg(lipgloss.NewStyle().Width(colW))

	lines := []string{titleRow, divLine, ln("")}

	indent := bg(lipgloss.NewStyle()).Render("  ")

	activeBg := lipgloss.NewStyle().Background(s.ActivePanelBg)
	activeLn := func(str string) string {
		return lipgloss.NewStyle().Background(s.ActivePanelBg).Width(innerW).Render(str)
	}
	activeIndent := activeBg.Render("  ")
	if s.V2 {
		activeIndent = s.marker(s.ActivePanelBg) + activeBg.Render(" ")
	}

	cell := func(idx int) (string, bool) {
		base := typingCellStyle{
			colFill: colFill,
			dot:     "\U000F0C52 ",
			baseSt:  activeBg.Foreground(s.pick(s.Text, s.Muted)).Bold(true).Underline(!s.V2),
			shineSt: activeBg.Foreground(s.Accent).Bold(true).Underline(!s.V2),
			name:    bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Text)).Bold(!s.V2)),
		}
		if idx == p.Idx {
			base.indent = activeIndent
			base.dotSt = activeBg.Foreground(s.Accent).Bold(true)
			base.itemBg = s.ActivePanelBg
			return typingItemCell(s, p.Items[idx], true, shineFrame, base), true
		}
		base.indent = indent
		base.dotSt = bg(lipgloss.NewStyle().Foreground(s.Muted).Bold(true))
		base.itemBg = s.PanelBg
		return typingItemCell(s, p.Items[idx], false, shineFrame, base), false
	}

	rows := p.rows()
	rightLen := p.rightColLen()
	for r := range rows {
		leftCell, leftActive := cell(r)
		rightCell, rightActive := "", false
		if r < rightLen {
			rightCell, rightActive = cell(p.fromColRow(1, r))
		}
		row := leftCell + rightCell
		if leftActive || rightActive {
			lines = append(lines, activeLn(row))
		} else {
			lines = append(lines, ln(row))
		}
	}

	lines = append(lines, ln(""))
	lines = append(lines, ln(divLine))
	lines = append(lines, ln(s.hintRow(bg, "↑→↓←", "navigate", "Enter", "confirm", "Esc", "cancel")))
	return s.placePanel(lines, pickerW, padH, w, h)
}
