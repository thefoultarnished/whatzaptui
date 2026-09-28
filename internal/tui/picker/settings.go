package picker

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const settingsCols = 2

// settingsToggleCount returns how many leading items are ON/OFF toggles;
// every selector is listed after them.
func (p *Picker) settingsToggleCount() int {
	n := 0
	for n < len(p.Items) && !p.Items[n].Selector {
		n++
	}
	return n
}

func settingsSectionRows(n int) int {
	return (n + settingsCols - 1) / settingsCols
}

// SettingsPos maps a setting to its grid cell. Toggles fill the rows under
// TOGGLES; selectors start on a fresh row under OPTIONS, so the two sections
// never share a row.
func (p *Picker) SettingsPos(idx int) (row, col int) {
	nT := p.settingsToggleCount()
	if idx < nT {
		return idx / settingsCols, idx % settingsCols
	}
	j := idx - nT
	return settingsSectionRows(nT) + j/settingsCols, j % settingsCols
}

// SettingsIdxAt is the inverse of SettingsPos; it returns -1 for empty or
// out-of-range cells.
func (p *Picker) SettingsIdxAt(row, col int) int {
	if row < 0 || col < 0 || col >= settingsCols {
		return -1
	}
	nT := p.settingsToggleCount()
	togRows := settingsSectionRows(nT)
	if row < togRows {
		if i := row*settingsCols + col; i < nT {
			return i
		}
		return -1
	}
	if i := nT + (row-togRows)*settingsCols + col; i < len(p.Items) {
		return i
	}
	return -1
}

// HandleSettings returns ("selector", true) when Enter lands on a selector
// so the caller can open its sub-picker, ("confirm", true) for a toggle.
func (p *Picker) HandleSettings(k tea.KeyMsg) (action string, done bool) {
	switch k.Type {
	case tea.KeyEnter:
		if p.Idx >= 0 && p.Idx < len(p.Items) && p.Items[p.Idx].Selector {
			return "selector", true
		}
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	row, col := p.SettingsPos(p.Idx)
	// Vertical moves into a shorter row land on its first cell.
	moveTo := func(r, c int) {
		if i := p.SettingsIdxAt(r, c); i >= 0 {
			p.Idx = i
		} else if i := p.SettingsIdxAt(r, 0); i >= 0 {
			p.Idx = i
		}
	}

	switch k.Type {
	case tea.KeyRight:
		if i := p.SettingsIdxAt(row, col+1); i >= 0 {
			p.Idx = i
		}
	case tea.KeyLeft:
		if i := p.SettingsIdxAt(row, col-1); i >= 0 {
			p.Idx = i
		}
	case tea.KeyDown:
		moveTo(row+1, col)
	case tea.KeyUp:
		moveTo(row-1, col)
	}
	return "", false
}

func (p *Picker) RenderSettings(s Style, w, h int) string {
	const padH = 3

	pickerW := min(w-4, 84)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / settingsCols

	bg, ln, divLine, titleRow := s.panelFrame(p.Title, innerW)
	headSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.Purple, s.Muted)).Bold(true))
	headBarSt := bg(lipgloss.NewStyle().Foreground(s.pick(s.Purple, s.Accent)).Bold(true))
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

	// Pad every name to the longest one so the ON/OFF and value text start
	// at the same column in every cell.
	nameW := 0
	for _, item := range p.Items {
		nameW = max(nameW, lipgloss.Width(item.Key))
	}

	nT := p.settingsToggleCount()
	numDigits := len(fmt.Sprintf("%d", max(nT, len(p.Items)-nT)))
	numW := numDigits + 2 // digits + "." + " "

	stateW := colW - lipgloss.Width(indent) - numW - lipgloss.Width("● ") - nameW - 2

	sectionHeader := func(label string) string {
		return indent + headBarSt.Render("│ ") + headSt.Render(label)
	}

	togRows := settingsSectionRows(nT)
	rows := togRows + settingsSectionRows(len(p.Items)-nT)
	for r := range rows {
		switch r {
		case 0:
			lines = append(lines, ln(sectionHeader("TOGGLES")))
		case togRows:
			lines = append(lines, ln(""), ln(sectionHeader("OPTIONS")))
		}
		var row strings.Builder
		rowHasActive := false
		for c := range settingsCols {
			localIdx := p.SettingsIdxAt(r, c)
			if localIdx < 0 || localIdx >= len(p.Items) {
				row.WriteString(colFill.Render(""))
				continue
			}
			item := p.Items[localIdx]

			var state string
			var dotColor lipgloss.Color
			dot := "  " // reserve the same width as "● " so selector rows
			// still fill the full column when highlighted.

			if item.Selector {
				state = item.Value
				dotColor = s.Accent
			} else {
				state = "OFF"
				if item.Value == "ON" {
					state = "ON"
					dotColor = s.StatusSuccess
				} else {
					// OFF is a normal state, not an error (V2: TextMuted).
					dotColor = s.pick(s.Muted, lipgloss.Color("#ef4444"))
				}
				dot = "● "
			}

			state = Truncate(state, stateW)
			// Pad the value to the column's fixed width so the selected
			// row's highlight always spans the full column, regardless of
			// how short or long this option's value text is.
			statePad := strings.Repeat(" ", max(0, stateW-lipgloss.Width(state)))
			pad := strings.Repeat(" ", nameW-lipgloss.Width(item.Key))

			num := localIdx + 1
			if item.Selector {
				num = localIdx - nT + 1
			}
			numLabel := fmt.Sprintf("%d.", num)
			numStr := numLabel + strings.Repeat(" ", numW-lipgloss.Width(numLabel))

			var cell string
			if localIdx == p.Idx {
				rowHasActive = true
				numSt := activeBg.Foreground(s.Muted).Bold(true)
				nameSt := activeBg.Foreground(s.Accent).Bold(true).Underline(true)
				if s.V2 {
					nameSt = activeBg.Foreground(s.Text).Bold(true)
				}
				dotSt := activeBg.Foreground(dotColor).Bold(true)
				stateSt := activeBg.Foreground(dotColor).Bold(true)
				cell = colFill.Render(activeIndent + numSt.Render(numStr) + nameSt.Render(item.Key) + activeBg.Render(pad) + stateSt.Render("  ") + dotSt.Render(dot) + stateSt.Render(state+statePad))
			} else {
				numSt := bg(lipgloss.NewStyle().Foreground(s.Muted))
				nameSt := bg(lipgloss.NewStyle().Foreground(s.Text).Bold(true))
				if s.V2 {
					nameSt = bg(lipgloss.NewStyle().Foreground(s.TextSecondary))
				}
				dotSt := bg(lipgloss.NewStyle().Foreground(dotColor).Bold(true))
				stateSt := bg(lipgloss.NewStyle().Foreground(dotColor))
				cell = colFill.Render(indent + numSt.Render(numStr) + nameSt.Render(item.Key) + bg(lipgloss.NewStyle()).Render(pad) + stateSt.Render("  ") + dotSt.Render(dot) + stateSt.Render(state+statePad))
			}
			row.WriteString(cell)
		}
		if rowHasActive {
			lines = append(lines, activeLn(row.String()))
		} else {
			lines = append(lines, ln(row.String()))
		}
	}

	lines = append(lines, ln(""))
	lines = append(lines, ln(divLine))
	lines = append(lines, ln(s.hintRow(bg, "↑↓", "navigate", "Enter", "toggle/open", "Esc", "close")))
	return s.placePanel(lines, pickerW, padH, w, h)
}
