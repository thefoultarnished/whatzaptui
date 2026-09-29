package picker

import "github.com/charmbracelet/lipgloss"

// placeBox centres box in a w×h pane. The *Box renderers return the box alone
// so the TUI can draw it over the chat; the plain Render* functions below keep
// the old "fill the pane" behaviour.
func placeBox(box string, w, h int) string {
	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}

// Render draws the plain list picker filling a w×h pane.
func (p *Picker) Render(s Style, w, h int) string { return placeBox(p.RenderBox(s, w, h), w, h) }

// RenderHelp draws the help picker filling a w×h pane.
func (p *Picker) RenderHelp(s Style, w, h int) string {
	return placeBox(p.RenderHelpBox(s, w, h), w, h)
}

// RenderSettings draws the settings picker filling a w×h pane.
func (p *Picker) RenderSettings(s Style, w, h int) string {
	return placeBox(p.RenderSettingsBox(s, w, h), w, h)
}

// RenderTypingAnimation draws the typing style picker filling a w×h pane.
func (p *Picker) RenderTypingAnimation(s Style, w, h, shineFrame int) string {
	return placeBox(p.RenderTypingAnimationBox(s, w, h, shineFrame), w, h)
}
