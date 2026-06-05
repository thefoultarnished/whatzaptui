package main

import (
	"strings"

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
	{"Typing indicator", false, func() bool { return currentConfig.SendTypingIndicator }, func(v bool) { currentConfig.SendTypingIndicator = v }, nil},
	{"Sound", false, func() bool { return currentConfig.SoundEnabled }, func(v bool) { currentConfig.SoundEnabled = v }, nil},
	{"Mouse", false, func() bool { return currentConfig.MouseEnabled }, func(v bool) { currentConfig.MouseEnabled = v }, nil},
	{"Taskbar flash", false, func() bool { return currentConfig.FlashTaskbar }, func(v bool) { currentConfig.FlashTaskbar = v }, nil},
	{"Notifications", false, func() bool { return currentConfig.NotificationsEnabled }, func(v bool) { currentConfig.NotificationsEnabled = v }, nil},
	{"Typing animation", true, nil, nil, func() string { return currentConfig.TypingAnimationStyle }},
	{"Timestamp 2-line", false, func() bool { return currentConfig.TimestampNewLine }, func(v bool) { currentConfig.TimestampNewLine = v }, nil},
	{"Media icons", true, nil, nil, func() string {
		if currentConfig.MediaIconStyle == "nerd" {
			return "nerd"
		}
		return "text"
	}},
}

var mediaIconList = []struct {
	key, label string
}{
	{"text", "Text  [image] [video] [file] [audio]"},
	{"nerd", "Nerd  " + nerdIconFor("image") + " " + nerdIconFor("video") + " " + nerdIconFor("file") + " " + nerdIconFor("audio")},
}

func buildMediaIconPickerItems() []pickerItem {
	items := make([]pickerItem, len(mediaIconList))
	for i, e := range mediaIconList {
		items[i] = pickerItem{key: e.key, label: e.label}
	}
	return items
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

func (p *picker) settingsGroupOffset(g int) int {
	return 0
}

func settingsVisualPos(idx int) (g, row, col int) {
	return 0, idx / 2, idx % 2
}

func (p *picker) HandleSettings(k tea.KeyMsg) (action string, done bool) {
	const numCols = 2
	switch k.Type {
	case tea.KeyEnter:
		if p.idx >= 0 && p.idx < len(settingsDefs) && settingsDefs[p.idx].isSelector {
			return "selector", true
		}
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}

	g, row, col := settingsVisualPos(p.idx)
	total := len(p.items)
	rows := (total + numCols - 1) / numCols

	switch k.Type {
	case tea.KeyRight:
		localIdx := row*numCols + col
		if col < numCols-1 && localIdx+1 < total {
			p.idx++
		}
	case tea.KeyLeft:
		if col > 0 {
			p.idx--
		}
	case tea.KeyDown:
		if row < rows-1 {
			newLocal := (row+1)*numCols + col
			if newLocal >= total {
				newLocal = total - 1
			}
			p.idx = newLocal
		}
	case tea.KeyUp:
		if row > 0 {
			p.idx = (row-1)*numCols + col
		}
	}
	_ = g
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

func (p *picker) RenderSettings(w, h int) string {
	const numCols = 2
	const padH = 3

	pickerW := min(w-4, 80)
	if pickerW < 60 {
		pickerW = min(w, 60)
	}
	innerW := pickerW - padH*2
	colW := innerW / numCols

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

	rows := (len(p.items) + numCols - 1) / numCols
	for r := range rows {
		var row strings.Builder
		rowHasActive := false
		for c := range numCols {
			localIdx := r*numCols + c
			if localIdx >= len(p.items) {
				row.WriteString(colFill.Render(""))
				continue
			}
			item := p.items[localIdx]
			isSelector := localIdx < len(settingsDefs) && settingsDefs[localIdx].isSelector

			var state string
			var dotColor lipgloss.Color

			if isSelector {
				state = settingsDefs[localIdx].getStr()
				if state == "" {
					state = "Default"
				}
				dotColor = accent
			} else {
				isOn := strings.HasSuffix(item.label, "ON")
				state = "OFF"
				if isOn {
					state = "ON"
					dotColor = accent
				} else {
					dotColor = muted
				}
			}

			var cell string
			dot := "● "
			if isSelector {
				dot = "→ "
			}
			if localIdx == p.idx {
				rowHasActive = true
				dotSt := activeBg.Foreground(dotColor).Bold(true)
				nameSt := activeBg.Foreground(accent).Bold(true).Underline(true)
				stateSt := activeBg.Foreground(dotColor).Bold(true)
				cell = colFill.Render(activeIndent + dotSt.Render(dot) + nameSt.Render(item.key) + stateSt.Render("  "+state))
			} else {
				dotSt := bg(lipgloss.NewStyle().Foreground(dotColor).Bold(true))
				nameSt := bg(lipgloss.NewStyle().Foreground(text).Bold(true))
				stateSt := bg(lipgloss.NewStyle().Foreground(dotColor))
				cell = colFill.Render(indent + dotSt.Render(dot) + nameSt.Render(item.key) + stateSt.Render("  "+state))
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

	hint := ln(keySt.Render("↑→↓←") + hintSt.Render(" navigate  ") +
		keySt.Render("Enter") + hintSt.Render(" toggle  ") +
		keySt.Render("Esc") + hintSt.Render(" close"))
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
