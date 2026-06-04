package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// fontTestKinds is the ordered list of media kinds shown in the /fonttest
// overlay. Order is chosen to roughly match the order the user encounters
// them in a chat.
var fontTestKinds = []string{
	"image", "video", "file", "audio", "voice",
	"sticker", "contact", "poll", "location", "anomaly",
}

func renderFontTest(w, h int) string {
	const padH = 3

	panelW := min(w-4, 78)
	if panelW < 60 {
		panelW = min(w, 60)
	}
	innerW := panelW - padH*2

	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(muted))
	kindSt := bg(lipgloss.NewStyle().Foreground(brand).Bold(true))
	textSt := bg(lipgloss.NewStyle().Foreground(muted))
	nerdSt := bg(lipgloss.NewStyle().Foreground(text).Bold(true))

	fill := bg(lipgloss.NewStyle().Width(innerW))
	ln := func(s string) string { return fill.Render(s) }

	divLine := ln(divSt.Render(strings.Repeat("─", innerW)))

	title := " Media Icon Font Test "
	titleRunes := len([]rune(title))
	gapW := max(0, innerW-titleRunes-3)
	titleRow := ln(titleSt.Render(title) +
		bg(lipgloss.NewStyle().Width(gapW)).Render("") +
		hintSt.Render("esc"))

	headerKindW := 10
	headerTextW := 14
	// nerd col takes whatever's left
	headerNerdW := innerW - headerKindW - headerTextW - 4 // 4 for separator padding
	if headerNerdW < 10 {
		headerNerdW = 10
	}
	headerRow := ln(
		bg(lipgloss.NewStyle().Width(headerKindW)).Render(" ")+
			hintSt.Render(strings.Repeat(" ", headerTextW))+" "+
			hintSt.Render(padRight("Nerd Font glyph", headerNerdW)),
	)
	headerUnderline := ln(divSt.Render(strings.Repeat("─", innerW)))

	lines := []string{titleRow, divLine, ln(""), headerRow, headerUnderline, ln("")}

	for _, kind := range fontTestKinds {
		kindCell := bg(lipgloss.NewStyle().Width(headerKindW)).Render(
			" " + kindSt.Render(padRight(kind, headerKindW-1)),
		)
		textCell := bg(lipgloss.NewStyle().Width(headerTextW)).Render(
			" " + textSt.Render(padRight("["+kind+"]", headerTextW-1)),
		)
		glyph := nerdIconFor(kind)
		if glyph == "" {
			glyph = "?"
		}
		nerdCell := bg(lipgloss.NewStyle().Width(headerNerdW)).Render(
			" " + nerdSt.Render(padRight(glyph, headerNerdW-1)),
		)
		lines = append(lines, ln(kindCell+textCell+nerdCell))
	}

	lines = append(lines, ln(""), divLine, ln(""))

	hint1 := ln(hintSt.Render(" If the Nerd column shows ") +
		keySt.Render("[]") +
		hintSt.Render(" tofu, your terminal isn't using a Nerd Font."))
	hint2 := ln(hintSt.Render(" Install one: ") +
		keySt.Render("https://www.nerdfonts.com/font-downloads"))
	hint3 := ln(hintSt.Render(" Recommended: ") +
		keySt.Render("JetBrainsMono Nerd Font") +
		hintSt.Render(" or ") +
		keySt.Render("CaskaydiaCove Nerd Font") + hintSt.Render("."))
	hint4 := ln(hintSt.Render(" Then set it as your terminal's font face."))

	lines = append(lines, hint1, hint2, hint3, hint4, ln(""))

	hint := ln(keySt.Render("Esc") + hintSt.Render(" close"))
	lines = append(lines, hint)

	box := lipgloss.NewStyle().
		Background(panelBg).
		Padding(1, padH).
		Width(panelW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().
		Width(w).Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
