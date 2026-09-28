package picker

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Style is the theme snapshot a picker draws with. The TUI builds a fresh
// one per render, so live theme previews repaint the picker immediately.
type Style struct {
	V2 bool // role-token (V2) theme: selection marker, no underlines

	Text, TextSecondary, Muted lipgloss.Color
	Accent, Brand, Purple      lipgloss.Color
	BorderSubtle, BorderFocus  lipgloss.Color
	BgSelected, BadgeInk       lipgloss.Color
	StatusSuccess              lipgloss.Color
	PanelBg, ActivePanelBg     lipgloss.Color // raised panel / highlighted row
	LightBackground            bool           // app background is light

	// Outline draws the V2 focus border around a floating panel.
	Outline func(box string, w, h int) string
	// Shine animates a highlight sweeping across text.
	Shine func(text string, base, shine lipgloss.Style, frame int) string
}

func (s Style) pick(v2, legacy lipgloss.Color) lipgloss.Color {
	if s.V2 {
		return v2
	}
	return legacy
}

// marker is the "▌" cursor bar that starts every selected row in V2 themes.
func (s Style) marker(bg lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(s.Accent).Background(bg).Render("▌")
}

func (s Style) outline(box string, w, h int) string {
	if s.Outline == nil {
		return box
	}
	return s.Outline(box, w, h)
}

func (s Style) shine(text string, base, shine lipgloss.Style, frame int) string {
	if s.Shine == nil {
		return base.Render(text)
	}
	return s.Shine(text, base, shine, frame)
}

// panelFrame returns the shared pieces every raised-surface picker uses:
// a background-applying helper, a full-width line filler, the divider and
// the title row with the "esc" hint.
func (s Style) panelFrame(title string, innerW int) (bg func(lipgloss.Style) lipgloss.Style, ln func(string) string, divLine, titleRow string) {
	bg = func(st lipgloss.Style) lipgloss.Style { return st.Background(s.PanelBg) }
	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln = func(str string) string { return fill.Render(str) }

	titleSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.Text, s.Accent)).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
	divSt := bg(lipgloss.NewStyle().Foreground(s.BorderSubtle))

	divLine = ln(divSt.Render(strings.Repeat("─", innerW)))
	gapW := max(0, innerW-len([]rune(title))-3) // 3 = len("esc")
	titleRow = ln(titleSt.Render(title) +
		bg(lipgloss.NewStyle().Width(gapW)).Render("") +
		hintSt.Render("esc"))
	return bg, ln, divLine, titleRow
}

// placePanel wraps lines in the raised panel, adds the outline and centres
// it in the w×h pane.
func (s Style) placePanel(lines []string, pickerW, padH, w, h int) string {
	box := lipgloss.NewStyle().
		Background(s.PanelBg).
		Padding(1, padH).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))
	box = s.outline(box, w, h)
	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}

// hintRow renders the key-legend footer, e.g. "↑→↓← navigate  Enter apply".
func (s Style) hintRow(bg func(lipgloss.Style) lipgloss.Style, pairs ...string) string {
	keySt := bg(lipgloss.NewStyle().Foreground(s.Accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
	var out strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		sep := "  "
		if i+2 >= len(pairs) {
			sep = ""
		}
		out.WriteString(keySt.Render(pairs[i]) + hintSt.Render(" "+pairs[i+1]+sep))
	}
	return out.String()
}
