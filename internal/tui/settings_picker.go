package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var settingsDefs = []struct {
	name       string
	isSelector bool
	get        func() bool
	set        func(bool)
	getStr     func() string
}{
	{"Send typing status", false, func() bool { return currentConfig.SendTypingIndicator }, func(v bool) { currentConfig.SendTypingIndicator = v }, nil},
	{"Message sounds", false, func() bool { return currentConfig.SoundEnabled }, func(v bool) { currentConfig.SoundEnabled = v }, nil},
	{"Mouse support", false, func() bool { return currentConfig.MouseEnabled }, func(v bool) { currentConfig.MouseEnabled = v }, nil},
	{"Flash taskbar", false, func() bool { return currentConfig.FlashTaskbar }, func(v bool) { currentConfig.FlashTaskbar = v }, nil},
	{"Tab Alerts", false, func() bool { return currentConfig.NotificationsEnabled }, func(v bool) { currentConfig.NotificationsEnabled = v }, nil},
	{"Time on new line", false, func() bool { return currentConfig.TimestampNewLine }, func(v bool) { currentConfig.TimestampNewLine = v }, nil},
	{"Hide borders", false, func() bool { return currentConfig.Borderless }, func(v bool) { currentConfig.Borderless = v }, nil},
	{"Menu borders", false, func() bool { return !currentConfig.HideMenuBorder }, func(v bool) { currentConfig.HideMenuBorder = !v }, nil},
	{"Show phone number", false, func() bool { return !currentConfig.HidePhoneNumber }, func(v bool) { currentConfig.HidePhoneNumber = !v }, nil},
	{"Typing style", true, nil, nil, func() string {
		return typingAnimationList[typingAnimationIndex(currentConfig.TypingAnimationStyle)].displayName
	}},
	{"Media icon style", true, nil, nil, func() string {
		if currentConfig.MediaIconStyle == "nerd" {
			return "Nerd"
		}
		return "Text"
	}},
	{"Media preview", true, nil, nil, func() string {
		for _, e := range mediaViewList {
			if e.key == currentConfig.MediaViewStyle {
				return strings.ToUpper(e.key[:1]) + e.key[1:]
			}
		}
		return "Text"
	}},
	{"Chat list icons", true, nil, nil, func() string {
		if icon := strings.TrimSpace(userlistIconPrefix(currentConfig.UserlistIconStyle)); icon != "" {
			return icon
		}
		return "Numbers"
	}},
	{"Startup speed", true, nil, nil, func() string {
		switch currentConfig.SplashStageSpeed {
		case "off":
			return "Off"
		case "fast":
			return "Fast"
		case "slow":
			return "Slow"
		case "extremely_slow":
			return "Extra slow"
		default:
			return "Normal"
		}
	}},
	{"Theme", true, nil, nil, func() string {
		for _, t := range themeList {
			if t.name == currentConfig.ThemeName {
				return t.displayName
			}
		}
		if len(themeList) > 0 {
			return themeList[0].displayName
		}
		return ""
	}},
}

var mediaIconList = []struct {
	key, label string
}{
	{"text", "Text   •  [image]  [video]  [file]  [audio]"},
	{"nerd", "Nerd   •  " + nerdIconFor("image") + " image  " + nerdIconFor("video") + " video  " + nerdIconFor("file") + " file  " + nerdIconFor("audio") + " audio"},
}

var mediaViewList = []struct {
	key, label string
}{
	{"text", "Text   •  [image] tag"},
	{"glyph", "Glyph  •  " + nerdIconFor("image") + " image tag"},
	{"pixel", "Pixel  •  inline terminal art"},
	{"full", "Full   •  terminal graphics (Kitty/iTerm)"},
}

func buildMediaIconPickerItems() []pickerItem {
	items := make([]pickerItem, len(mediaIconList))
	for i, e := range mediaIconList {
		items[i] = pickerItem{key: e.key, label: e.label}
	}
	return items
}

func buildMediaViewPickerItems() []pickerItem {
	items := make([]pickerItem, len(mediaViewList))
	for i, e := range mediaViewList {
		items[i] = pickerItem{key: e.key, label: e.label}
	}
	return items
}

func buildUserlistIconPickerItems() []pickerItem {
	items := []pickerItem{{key: "numbers", label: "Numbers"}}
	for _, a := range typingAnimationList {
		if a.key == "sparkle" {
			for i, icon := range a.icons {
				items = append(items, pickerItem{key: fmt.Sprintf("sparkle-%d", i), label: icon})
			}
		}
	}
	return items
}

var splashSpeedList = []struct {
	key, label string
}{
	{"off", "Off             •  0s delay (finishes as tasks complete)"},
	{"fast", "Fast            •  2s total (500ms per stage)"},
	{"normal", "Normal          •  4s total (1.0s per stage)"},
	{"slow", "Slow            •  6s total (1.5s per stage)"},
	{"extremely_slow", "Extremely Slow  •  8s total (2.0s per stage)"},
}

func buildSplashSpeedPickerItems() []pickerItem {
	items := make([]pickerItem, len(splashSpeedList))
	for i, e := range splashSpeedList {
		items[i] = pickerItem{key: e.key, label: e.label}
	}
	return items
}

func splashStageHoldDuration() time.Duration {
	switch currentConfig.SplashStageSpeed {
	case "off":
		return 0
	case "fast":
		return 500 * time.Millisecond
	case "slow":
		return 1500 * time.Millisecond
	case "extremely_slow":
		return 2000 * time.Millisecond
	default:
		return time.Second
	}
}

func buildSettingsPickerItems() []pickerItem {
	items := make([]pickerItem, len(settingsDefs))
	for i, s := range settingsDefs {
		var state string
		if s.isSelector {
			state = s.getStr()
		} else {
			state = "OFF"
			if s.get() {
				state = "ON"
			}
		}
		items[i] = pickerItem{key: s.name, label: s.name + "  " + state}
	}
	return items
}

const settingsCols = 2

// settingsToggleCount returns how many leading settingsDefs are ON/OFF
// toggles; every selector is listed after them.
func settingsToggleCount() int {
	n := 0
	for n < len(settingsDefs) && !settingsDefs[n].isSelector {
		n++
	}
	return n
}

func settingsSectionRows(n int) int {
	return (n + settingsCols - 1) / settingsCols
}

// settingsVisualPos maps a setting to its grid cell. Toggles fill the rows
// under TOGGLES; selectors start on a fresh row under OPTIONS, so the two
// sections never share a row.
func settingsVisualPos(idx int) (row, col int) {
	nT := settingsToggleCount()
	if idx < nT {
		return idx / settingsCols, idx % settingsCols
	}
	j := idx - nT
	return settingsSectionRows(nT) + j/settingsCols, j % settingsCols
}

// settingsIdxAt is the inverse of settingsVisualPos; it returns -1 for empty
// or out-of-range cells.
func settingsIdxAt(row, col int) int {
	if row < 0 || col < 0 || col >= settingsCols {
		return -1
	}
	nT := settingsToggleCount()
	togRows := settingsSectionRows(nT)
	if row < togRows {
		if i := row*settingsCols + col; i < nT {
			return i
		}
		return -1
	}
	if i := nT + (row-togRows)*settingsCols + col; i < len(settingsDefs) {
		return i
	}
	return -1
}

func (p *picker) HandleSettings(k tea.KeyMsg) (action string, done bool) {
	switch k.Type {
	case tea.KeyEnter:
		if p.idx >= 0 && p.idx < len(settingsDefs) && settingsDefs[p.idx].isSelector {
			return "selector", true
		}
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	row, col := settingsVisualPos(p.idx)
	// Vertical moves into a shorter row land on its first cell.
	moveTo := func(r, c int) {
		if i := settingsIdxAt(r, c); i >= 0 {
			p.idx = i
		} else if i := settingsIdxAt(r, 0); i >= 0 {
			p.idx = i
		}
	}

	switch k.Type {
	case tea.KeyRight:
		if i := settingsIdxAt(row, col+1); i >= 0 {
			p.idx = i
		}
	case tea.KeyLeft:
		if i := settingsIdxAt(row, col-1); i >= 0 {
			p.idx = i
		}
	case tea.KeyDown:
		moveTo(row+1, col)
	case tea.KeyUp:
		moveTo(row-1, col)
	}
	return "", false
}

func (p *picker) toggleSetting() string {
	if p.idx < 0 || p.idx >= len(settingsDefs) {
		return ""
	}
	s := settingsDefs[p.idx]
	newVal := !s.get()
	s.set(newVal)
	saveConfig()
	state := "OFF"
	if newVal {
		state = "ON"
	}
	p.items[p.idx] = pickerItem{key: s.name, label: s.name + "  " + state}
	return s.name + ": " + state
}

// reopenSettings rebuilds the settings picker after a sub-picker closes,
// restoring the selection that was active when the sub-picker was opened
// instead of jumping back to the first item.
func (x *m) reopenSettings() {
	x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
	x.settingsPicker.Open("")
	if x.settingsReturnIdx >= 0 && x.settingsReturnIdx < len(settingsDefs) {
		x.settingsPicker.idx = x.settingsReturnIdx
	}
	x.invalidate()
}

func (p *picker) RenderSettings(w, h int) string {
	const numCols = settingsCols
	const padH = 3

	pickerW := min(w-4, 84)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / numCols

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(v2Color(text, accent)).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(borderSubtle))
	headSt := bg(lipgloss.NewStyle().Foreground(v2Color(purple, muted)).Bold(true))
	headBarSt := bg(lipgloss.NewStyle().Foreground(v2Color(purple, accent)).Bold(true))

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

	// Pad every name to the longest one so the ON/OFF and value text start
	// at the same column in every cell.
	nameW := 0
	for _, item := range p.items {
		nameW = max(nameW, lipgloss.Width(item.key))
	}

	nT := settingsToggleCount()
	numDigits := len(fmt.Sprintf("%d", max(nT, len(settingsDefs)-nT)))
	numW := numDigits + 2 // digits + "." + " "

	stateW := colW - lipgloss.Width(indent) - numW - lipgloss.Width("● ") - nameW - 2

	sectionHeader := func(label string) string {
		return indent + headBarSt.Render("│ ") + headSt.Render(label)
	}

	togRows := settingsSectionRows(nT)
	rows := togRows + settingsSectionRows(len(settingsDefs)-nT)
	for r := range rows {
		switch r {
		case 0:
			lines = append(lines, ln(sectionHeader("TOGGLES")))
		case togRows:
			lines = append(lines, ln(""), ln(sectionHeader("OPTIONS")))
		}
		var row strings.Builder
		rowHasActive := false
		for c := range numCols {
			localIdx := settingsIdxAt(r, c)
			if localIdx < 0 || localIdx >= len(p.items) {
				row.WriteString(colFill.Render(""))
				continue
			}
			item := p.items[localIdx]
			isSelector := settingsDefs[localIdx].isSelector

			var state string
			var dotColor lipgloss.Color
			dot := "  " // reserve the same width as "● " so selector rows
			// still fill the full column when highlighted.

			if isSelector {
				state = settingsDefs[localIdx].getStr()
				dotColor = accent
			} else {
				isOn := strings.HasSuffix(item.label, "ON")
				state = "OFF"
				if isOn {
					state = "ON"
					dotColor = statusSuccess
				} else {
					// OFF is a normal state, not an error (V2: TextMuted).
					dotColor = v2Color(muted, lipgloss.Color("#ef4444"))
				}
				dot = "● "
			}

			state = truncate(state, stateW)
			// Pad the value to the column's fixed width so the selected
			// row's highlight always spans the full column, regardless of
			// how short or long this option's value text is.
			statePad := strings.Repeat(" ", max(0, stateW-lipgloss.Width(state)))
			pad := strings.Repeat(" ", nameW-lipgloss.Width(item.key))

			num := localIdx + 1
			if isSelector {
				num = localIdx - nT + 1
			}
			numLabel := fmt.Sprintf("%d.", num)
			numStr := numLabel + strings.Repeat(" ", numW-lipgloss.Width(numLabel))

			var cell string
			if localIdx == p.idx {
				rowHasActive = true
				numSt := activeBg.Foreground(muted).Bold(true)
				nameSt := activeBg.Foreground(accent).Bold(true).Underline(true)
				if themeV2 {
					nameSt = activeBg.Foreground(text).Bold(true)
				}
				dotSt := activeBg.Foreground(dotColor).Bold(true)
				stateSt := activeBg.Foreground(dotColor).Bold(true)
				cell = colFill.Render(activeIndent + numSt.Render(numStr) + nameSt.Render(item.key) + activeBg.Render(pad) + stateSt.Render("  ") + dotSt.Render(dot) + stateSt.Render(state+statePad))
			} else {
				numSt := bg(lipgloss.NewStyle().Foreground(muted))
				nameSt := bg(lipgloss.NewStyle().Foreground(text).Bold(true))
				if themeV2 {
					nameSt = bg(lipgloss.NewStyle().Foreground(textSecondary))
				}
				dotSt := bg(lipgloss.NewStyle().Foreground(dotColor).Bold(true))
				stateSt := bg(lipgloss.NewStyle().Foreground(dotColor))
				cell = colFill.Render(indent + numSt.Render(numStr) + nameSt.Render(item.key) + bg(lipgloss.NewStyle()).Render(pad) + stateSt.Render("  ") + dotSt.Render(dot) + stateSt.Render(state+statePad))
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

	hint := ln(keySt.Render("↑↓") + hintSt.Render(" navigate  ") +
		keySt.Render("Enter") + hintSt.Render(" toggle/open  ") +
		keySt.Render("Esc") + hintSt.Render(" close"))
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
