package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmDialog is a small "are you sure?" modal: a title, a message, and
// a Yes/No choice. Used for A-6 (confirm before /logout, /whitelistall,
// /blacklistall).
type confirmDialog struct {
	open    bool
	title   string
	message string
	action  string // identifies what to do on confirm: "logout" | "whitelistall" | "blacklistall"
	idx     int    // 0 = Yes, 1 = No
}

// Open shows the dialog with the given title/message, defaulting the
// selection to "No" so an accidental Enter cancels.
func (d *confirmDialog) Open(title, message, action string) {
	d.open = true
	d.title = title
	d.message = message
	d.action = action
	d.idx = 1
}

func (d *confirmDialog) Close() {
	d.open = false
}

// Handle processes a key event. Returns ("confirm", true), ("cancel", true),
// or ("", false) if the dialog just moved focus between Yes/No.
func (d *confirmDialog) Handle(k tea.KeyMsg) (action string, done bool) {
	switch k.Type {
	case tea.KeyLeft, tea.KeyRight, tea.KeyTab:
		d.idx = 1 - d.idx
		return "", false
	case tea.KeyEnter:
		if d.idx == 0 {
			return "confirm", true
		}
		return "cancel", true
	case tea.KeyEsc:
		return "cancel", true
	}
	switch k.String() {
	case "y", "Y":
		return "confirm", true
	case "n", "N":
		return "cancel", true
	}
	return "", false
}

func (d *confirmDialog) Render(w, h int) string {
	const padH = 3

	pickerW := min(w-4, 64)
	if pickerW < 46 {
		pickerW = min(w, 46)
	}
	innerW := pickerW - padH*2

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(muted))
	msgSt := bg(lipgloss.NewStyle().Foreground(text))

	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln := func(s string) string { return fill.Render(s) }

	divLine := ln(divSt.Render(strings.Repeat("─", innerW)))

	lines := []string{ln(titleSt.Render(d.title)), divLine, ln("")}

	for _, line := range strings.Split(wrapText(d.message, innerW), "\n") {
		lines = append(lines, ln(msgSt.Render(line)))
	}
	lines = append(lines, ln(""))

	activePanelBg := lipgloss.Color(currentTheme.ShortcutActive)
	activeBg := lipgloss.NewStyle().Background(activePanelBg)

	renderButton := func(label string, active bool) string {
		if active {
			return activeBg.Foreground(accent).Bold(true).Padding(0, 2).Render(label)
		}
		return bg(lipgloss.NewStyle().Foreground(text)).Padding(0, 2).Render(label)
	}

	yesBtn := renderButton("Yes", d.idx == 0)
	noBtn := renderButton("No", d.idx == 1)
	buttonsRow := lipgloss.JoinHorizontal(lipgloss.Top, yesBtn, bg(lipgloss.NewStyle()).Render("   "), noBtn)
	buttonsLine := ln(lipgloss.PlaceHorizontal(innerW, lipgloss.Center, buttonsRow, lipgloss.WithWhitespaceBackground(panelBg)))
	lines = append(lines, buttonsLine, ln(""))

	lines = append(lines, divLine)

	hint := ln(keySt.Render("←→") + hintSt.Render(" select  ") +
		keySt.Render("Enter") + hintSt.Render(" confirm  ") +
		keySt.Render("y/n") + hintSt.Render(" quick choice  ") +
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
