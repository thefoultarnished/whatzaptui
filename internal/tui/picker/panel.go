package picker

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Panel builds a popup in the same look as the pickers: the raised background,
// a title row with "esc", divider lines, the highlighted selected row and a key
// hint footer. Panels are for popups that are not a plain list, such as a form.
type Panel struct {
	s      Style
	InnerW int
	width  int
	w, h   int
	bg     func(lipgloss.Style) lipgloss.Style
	ln     func(string) string
	div    string
	title  string
	lines  []string
	hint   string
}

// NewPanel starts a panel titled title, to be placed in a w by h area. maxW is
// the widest it may be. Add rows, then call Render.
func (s Style) NewPanel(title string, w, h, maxW int) *Panel {
	const padH = 3
	pickerW := min(w-4, maxW)
	if pickerW < 40 {
		pickerW = min(w, 40)
	}
	innerW := pickerW - padH*2
	bg, ln, div, titleRow := s.panelFrame(title, innerW)
	return &Panel{s: s, InnerW: innerW, width: pickerW, w: w, h: h, bg: bg, ln: ln, div: div, title: titleRow}
}

// Blank adds an empty row.
func (p *Panel) Blank() { p.lines = append(p.lines, p.ln("")) }

// Rule adds a divider line.
func (p *Panel) Rule() { p.lines = append(p.lines, p.div) }

// Section adds a small heading such as "OPTIONS".
func (p *Panel) Section(label string) {
	st := p.bg(lipgloss.NewStyle().Foreground(p.s.pick(p.s.Purple, p.s.Muted)).Bold(true))
	p.lines = append(p.lines, p.ln(st.Render(Truncate(strings.ToUpper(label), p.InnerW))))
}

// Text adds a row of plain text, bold when bold is set.
func (p *Panel) Text(text string, bold bool) {
	st := p.bg(lipgloss.NewStyle().Foreground(p.s.Text).Bold(bold))
	p.lines = append(p.lines, p.ln(st.Render(Truncate(text, p.InnerW))))
}

// Muted adds a row of quiet text.
func (p *Panel) Muted(text string) {
	st := p.bg(lipgloss.NewStyle().Foreground(p.s.Muted))
	p.lines = append(p.lines, p.ln(st.Render(Truncate(text, p.InnerW))))
}

// Note adds a row of text in the given colour, for a warning.
func (p *Panel) Note(text string, color lipgloss.Color) {
	st := p.bg(lipgloss.NewStyle().Foreground(color).Bold(true))
	p.lines = append(p.lines, p.ln(st.Render(Truncate(text, p.InnerW))))
}

// RowOpts describes one row of a panel.
type RowOpts struct {
	Label string
	Note  string // quiet text after the label
	// Selected draws the highlighted bar, like the selected row of a picker.
	Selected bool
	Dim      bool           // quiet colour, for a hint or an unavailable choice
	Color    lipgloss.Color // label colour when the row is not selected
	Bold     bool           // bold label when Color is set
}

// Row adds a row. Selected rows look exactly like the selected row of a picker.
func (p *Panel) Row(o RowOpts) {
	s := p.s
	selBg := s.pick(s.BgSelected, s.Accent)
	textSt := p.bg(lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Text)))
	dimSt := p.bg(lipgloss.NewStyle().Foreground(s.Muted))
	indent := p.bg(lipgloss.NewStyle()).Render("  ")

	note := ""
	if o.Note != "" {
		note = "  " + o.Note
	}
	label := Truncate(o.Label, max(1, p.InnerW-4-Width(note)))
	st := textSt
	switch {
	case o.Dim:
		st = dimSt
	case o.Color != "":
		st = p.bg(lipgloss.NewStyle().Foreground(o.Color).Bold(o.Bold))
	}
	if !o.Selected {
		p.lines = append(p.lines, p.ln(indent+st.Render(label)+dimSt.Render(note)))
		return
	}
	sel := lipgloss.NewStyle().Background(selBg)
	if s.V2 {
		p.lines = append(p.lines, sel.Width(p.InnerW).Render(
			s.marker(selBg)+sel.Render(" ")+sel.Foreground(s.Text).Bold(!o.Dim).Render(label)+sel.Foreground(s.Muted).Render(note)))
		return
	}
	p.lines = append(p.lines, sel.Width(p.InnerW).Render(
		sel.Render("  ")+sel.Foreground(s.BadgeInk).Bold(true).Render(label)+sel.Render(note)))
}

// Hint sets the key legend at the bottom, as pairs of key and action.
func (p *Panel) Hint(pairs ...string) { p.hint = p.ln(p.s.hintRow(p.bg, pairs...)) }

// Render returns the finished panel.
func (p *Panel) Render() string {
	lines := []string{p.title, p.div, p.ln("")}
	lines = append(lines, p.lines...)
	lines = append(lines, p.ln(""), p.div, p.hint)
	return p.s.panelBox(lines, p.width, 3, p.w, p.h)
}
