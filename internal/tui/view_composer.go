package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (x m) renderCommandBox(leftW int) string {
	cmdBadge := cmdBadgeStyle.Render(" CMD ")
	cmdContent := cmdBadge + lipgloss.NewStyle().Foreground(text).Render(x.leftInput)
	if x.leftInputFocused {
		trimmedLeft := strings.TrimSpace(x.leftInput)
		ghost := ""
		if x.leftInput != "" {
			if best := commandBestMatch(x.leftInput); best != "" && strings.HasPrefix(best, strings.ToLower(trimmedLeft)) {
				ghost = best[len(trimmedLeft):]
			}
		}
		if ghost != "" {
			firstGhost, restGhost := graphemeSplitFirst(ghost)
			if x.cursorOn {
				cmdContent += cursorStyle.Render(firstGhost)
			} else {
				cmdContent += ghostStyle.Render(firstGhost)
			}
			cmdContent += ghostStyle.Render(restGhost)
		} else if x.cursorOn {
			cmdContent += lipgloss.NewStyle().Foreground(cursorColor).Render("|")
		} else {
			cmdContent += " "
		}
	}
	if !x.leftInputFocused && x.leftInput == "" {
		cmdContent = cmdBadge + ghostStyle.Render(" Ctrl+K for commands")
	}
	cmdBorder := borderSubtle
	if themeV2 && x.leftInputFocused {
		cmdBorder = borderFocus
	}
	topRow := lipgloss.NewStyle().Foreground(cmdBorder).
		Render(strings.Repeat("─", leftW) + "┼")
	contentRow := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(borderSubtle).
		Width(leftW).
		Foreground(text).
		Render(cmdContent)
	return lipgloss.JoinVertical(lipgloss.Left, topRow, contentRow)
}

type composerShortcut struct {
	icon  string
	key   string
	label string
}

func (x m) composerBorderShortcuts(inputLocked bool) ([]composerShortcut, string) {
	nerd := currentConfig.MediaIconStyle == "nerd" || currentConfig.MediaViewStyle == "glyph"
	var emojiIcon, fileIcon, replyIcon, editIcon string
	if nerd {
		emojiIcon = "\U000F0785" // nf-md-sticker_emoji
		fileIcon = "\uF115"      // nf-fa-folder_open
		replyIcon = "\U000F0311" // nf-md-reply
		editIcon = "\U000F03EB"  // nf-md-pencil
	}

	if inputLocked {
		return []composerShortcut{
			{icon: "", label: "blacklisted", key: "[/whitelist]"},
		}, "status"
	}
	if x.replyPickMode {
		return []composerShortcut{
			{icon: replyIcon, label: "quote reply", key: ""},
			{icon: "", label: "select", key: "[enter]"},
			{icon: "", label: "cancel", key: "[esc]"},
		}, "mode"
	}
	if x.editPickMode {
		return []composerShortcut{
			{icon: editIcon, label: "edit", key: ""},
			{icon: "", label: "select", key: "[enter]"},
			{icon: "", label: "cancel", key: "[esc]"},
		}, "mode"
	}
	if x.pendingAttachmentPath != "" {
		return []composerShortcut{
			{icon: fileIcon, label: "attachment", key: ""},
			{icon: "", label: "send", key: "[enter]"},
			{icon: "", label: "cancel", key: "[esc]"},
		}, "mode"
	}

	return []composerShortcut{
		{icon: emojiIcon, label: "emoji", key: "[alt+e]"},
		{icon: fileIcon, label: "file", key: "[alt+f]"},
		{icon: replyIcon, label: "reply", key: "[alt+r]"},
		{icon: editIcon, label: "edit", key: "[alt+a]"},
	}, "normal"
}

// composerHeaderPrefix is the least that goes before the shortcuts in the
// message box header: a short rule, the wall where it rises, and a space.
const composerHeaderPrefix = "─────── "

// composerChipWidth is the width of one shortcut in the header, including the
// "  ·  " separator that goes before every shortcut but the first.
func composerChipWidth(sc composerShortcut, withSep bool) int {
	w := 0
	if withSep {
		w += runeDisplayWidth("  ·  ")
	}
	if sc.icon != "" {
		w += runeDisplayWidth(sc.icon) + 1
	}
	if sc.label != "" {
		w += runeDisplayWidth(sc.label)
		if sc.key != "" {
			w++
		}
	}
	if sc.key != "" {
		w += runeDisplayWidth(sc.key)
	}
	return w
}

// composerBorderColor is the colour of the message box's top rule and roof.
func composerBorderColor(rightFocused bool) lipgloss.Color {
	if themeV2 && rightFocused {
		return borderFocus
	}
	if rightFocused {
		return accent
	}
	return borderSubtle
}

// composerRoofHeight is the number of rows in the raised part above the message
// box's top rule: the roof edge and, under it, the row the shortcuts sit on.
const composerRoofHeight = 2

// composerRoofRows is the number of rows reserved above the message box's top
// rule for the raised part: composerRoofHeight whenever a chat is open and the
// box is wide enough for shortcuts (blank while they are hidden), so the
// message pane keeps a steady height. chatPaneGeometry and renderChatInput both
// use it.
func (x m) composerRoofRows(rightW int) int {
	if x.active != "" && rightW-1 >= 24 {
		return composerRoofHeight
	}
	return 0
}

// renderComposerTopBorder is the rule at the base of the message box. When the
// shortcuts show, it rises into the raised part at a wall and the right-hand
// end is open, up to the frame.
func (x m) renderComposerTopBorder(borderW int, rightFocused, inputLocked bool) string {
	base, _, _ := x.composerHeader(borderW, rightFocused, inputLocked)
	return base
}

// renderComposerRoof is the raised part above the rule: the roof edge, which
// runs from the wall all the way to the right frame, and under it the row the
// shortcuts sit on. Both are blank when there are no shortcuts. The edge ends in
// a rule character, so the frame draws a junction there and the raised part
// connects to the right side.
func (x m) renderComposerRoof(borderW int, rightFocused, inputLocked bool) string {
	if borderW <= 0 {
		return ""
	}
	_, text, left := x.composerHeader(borderW, rightFocused, inputLocked)
	blank := strings.Repeat(" ", borderW)
	if left < 0 || left >= borderW-1 {
		return blank + "\n" + blank
	}
	edge := lipgloss.NewStyle().Foreground(composerBorderColor(rightFocused)).Render("╭" + strings.Repeat("─", borderW-left-1))
	return strings.Repeat(" ", left) + edge + "\n" + text
}

// composerHeader draws the raised part of the message box's top edge and
// returns its rows. The shortcuts are pushed all the way right, so the raised
// part ends at the right frame:
//
//	                          ╭─────────────────────────────────────┤   <- edge (roof)
//	                          │ emoji [alt+e]  ·  file [alt+f]      │   <- text row
//	──────────────────────────╯                                     │   <- base (rule)
//
// base is the rule row and text the row of shortcuts; wallLeft is the column of
// the wall, or -1 (with a plain rule and no text) when the shortcuts are hidden.
func (x m) composerHeader(borderW int, rightFocused, inputLocked bool) (base, text string, wallLeft int) {
	if borderW <= 0 {
		return "", "", -1
	}

	borderCol := composerBorderColor(rightFocused)
	ruleStyle := lipgloss.NewStyle().Foreground(borderCol)

	// Only show shortcuts when the chat is opened and the composer is focused.
	// When unfocused (e.g. sidebar navigation or no chat opened) or narrow (< 24),
	// render a plain horizontal rule like before.
	if !rightFocused || x.active == "" || borderW < 24 {
		return ruleStyle.Render(strings.Repeat("─", borderW)), "", -1
	}

	keyStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(v2Color(textSecondary, muted)).Italic(true)
	iconStyle := lipgloss.NewStyle().Foreground(accent)
	dotStyle := lipgloss.NewStyle().Foreground(borderCol)

	shortcuts, modeType := x.composerBorderShortcuts(inputLocked)

	// How many shortcuts fit: the least rule and wall on the left
	// (composerHeaderPrefix) and one space before the frame on the right.
	avail := borderW - runeDisplayWidth(composerHeaderPrefix) - 1
	n, used := 0, 0
	for i, sc := range shortcuts {
		w := composerChipWidth(sc, i > 0)
		if used+w > avail {
			break
		}
		used += w
		n++
	}
	if n == 0 {
		// Nothing fits: a plain rule, no raised part.
		return ruleStyle.Render(strings.Repeat("─", borderW)), "", -1
	}

	// Wall, space, shortcuts, space: the shortcuts end one cell short of the
	// frame, and the rule takes up whatever is left on the left.
	wallLeft = borderW - used - 3

	// The rule rises at the wall; the right-hand end is open, so the row ends
	// in a space and the frame draws a plain wall there.
	base = ruleStyle.Render(strings.Repeat("─", wallLeft)+"╯") + strings.Repeat(" ", borderW-wallLeft-1)

	var sb strings.Builder
	sb.WriteString(strings.Repeat(" ", wallLeft))
	sb.WriteString(ruleStyle.Render("│"))
	sb.WriteString(" ")
	for i, sc := range shortcuts[:n] {
		if i > 0 {
			sb.WriteString(dotStyle.Render("  ·  "))
		}
		if sc.icon != "" {
			sb.WriteString(iconStyle.Render(sc.icon))
			sb.WriteString(" ")
		}
		if sc.label != "" {
			if sc.key == "" {
				sb.WriteString(lipgloss.NewStyle().Foreground(accent).Bold(true).Italic(true).Render(sc.label))
			} else {
				sb.WriteString(labelStyle.Render(sc.label))
			}
			if sc.key != "" {
				sb.WriteString(" ")
			}
		}
		if sc.key != "" {
			if modeType == "status" {
				sb.WriteString(lipgloss.NewStyle().Foreground(v2Color(amber, red)).Bold(true).Render(sc.key))
			} else {
				sb.WriteString(keyStyle.Render(sc.key))
			}
		}
	}
	sb.WriteString(" ")
	return base, sb.String(), wallLeft
}

func (x m) renderChatInput(rightW int, typedInput string) string {
	trimmedInput := strings.TrimSpace(typedInput)
	isCmd := strings.HasPrefix(trimmedInput, "/")
	rightFocused := x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused && x.active != ""
	inputLocked := x.chatInputLocked()
	inputGhost := ""
	if rightFocused && !inputLocked && !x.inputAllSelected {
		inputGhost = chatInputGhost(typedInput)
	}

	showSend := rightFocused && !inputLocked && !isCmd
	sendBadge := ""
	sendBadgeW := 0
	if showSend {
		sendBadge = lipgloss.NewStyle().Foreground(buttonInk).Background(brand).Bold(true).Render(" SEND ")
		sendBadgeW = lipgloss.Width(sendBadge)
	}

	sendGap := 0
	if showSend {
		sendGap = 1
	}
	textAreaW := max(1, rightW-1-sendBadgeW-sendGap)
	var inputDisplay string
	if inputLocked {
		inputDisplay = lipgloss.NewStyle().Foreground(v2Color(amber, red)).Render(" blacklisted | Ctrl+K then /whitelist")
	} else if x.inputAllSelected {
		inputDisplay = lipgloss.NewStyle().Foreground(text).Render(" ") +
			lipgloss.NewStyle().Foreground(buttonInk).Background(accent).Render(typedInput)
	} else {
		trailingNL := len(typedInput) - len(strings.TrimRight(typedInput, "\n"))
		trimmed := strings.TrimRight(typedInput, "\n")
		lines := strings.Split(trimmed, "\n")
		for i, l := range lines {
			lines[i] = " " + l
		}
		for i, l := range lines {
			lines[i] = lipgloss.NewStyle().Foreground(text).Render(l)
		}
		inputDisplay = strings.Join(lines, "\n")
		if trailingNL > 0 {
			inputDisplay += strings.Repeat("\n", trailingNL)
			if rightFocused {
				inputDisplay += " "
			}
		}
		if rightFocused && inputGhost != "" {
			firstGhost, restGhost := graphemeSplitFirst(inputGhost)
			if x.cursorOn {
				inputDisplay += cursorStyle.Render(firstGhost)
			} else {
				inputDisplay += ghostStyle.Render(firstGhost)
			}
			inputDisplay += ghostStyle.Render(restGhost)
		} else if rightFocused && x.cursorOn {
			inputDisplay += inputCursorStyle.Render(inputCursorGlyph)
		} else if rightFocused {
			inputDisplay += inputCursorStyle.Render(" ")
		}
	}

	if isCmd {
		inputDisplay += lipgloss.NewStyle().Foreground(accent).Bold(true).Render("  CMD")
	}
	if x.replyPickMode {
		inputDisplay = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(" R") +
			lipgloss.NewStyle().Foreground(text).Render("/") +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Enter") +
			lipgloss.NewStyle().Foreground(text).Render(" quote reply  ") +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Esc") +
			lipgloss.NewStyle().Foreground(text).Render("/") +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Alt") +
			lipgloss.NewStyle().Foreground(text).Render(" cancel")
	} else if x.editPickMode {
		inputDisplay = lipgloss.NewStyle().Foreground(accent).Bold(true).Render(" A") +
			lipgloss.NewStyle().Foreground(text).Render("/") +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Enter") +
			lipgloss.NewStyle().Foreground(text).Render(" edit message  ") +
			lipgloss.NewStyle().Foreground(accent).Bold(true).Render("Esc") +
			lipgloss.NewStyle().Foreground(text).Render(" cancel")
	} else if x.mode == "nav" && x.active == "" {
		inputDisplay = lipgloss.NewStyle().Foreground(muted).Render(" Press Enter/Tab to start chatting")
	} else if x.mode == "nav" || (x.mode == "chat" && x.sidebarFocused) {
		inputDisplay = lipgloss.NewStyle().Foreground(muted).Render(" Press Enter/Tab to start chatting")
	} else if inputLocked {
		inputDisplay = lipgloss.NewStyle().Foreground(v2Color(amber, muted)).Render(" blacklisted | Ctrl+K then /whitelist")
	} else if rightFocused && !x.emojiPickerOpen && typedInput == "" {
		if x.cursorOn {
			inputDisplay = lipgloss.NewStyle().Foreground(muted).Render(" ") +
				inputCursorStyle.Render(inputCursorGlyph) +
				lipgloss.NewStyle().Foreground(muted).Render(x.emptyComposerHint())
		} else {
			inputDisplay = lipgloss.NewStyle().Foreground(muted).Render("  " + x.emptyComposerHint())
		}
	}

	inputDisplayLines := strings.Split(inputDisplay, "\n")
	for i, l := range inputDisplayLines {
		inputDisplayLines[i] = lipgloss.NewStyle().Width(textAreaW).Render(l)
	}
	textRendered := strings.Join(inputDisplayLines, "\n")
	rightContent := textRendered
	if showSend {
		rightContent = lipgloss.JoinHorizontal(lipgloss.Top, textRendered, " "+sendBadge)
	}
	borderW := max(1, rightW-1)
	topBorder := x.renderComposerTopBorder(borderW, rightFocused, inputLocked)
	content := lipgloss.NewStyle().Foreground(text).Render(rightContent)
	if x.composerRoofRows(rightW) > 0 {
		return x.renderComposerRoof(borderW, rightFocused, inputLocked) + "\n" + topBorder + "\n" + content
	}
	return topBorder + "\n" + content
}
func (x m) renderReplyBar(contentW, rightW int) string {
	displayReplyTo := x.replyTo
	if displayReplyTo == nil {
		return ""
	}
	_ = contentW

	rSender := x.senderNameForMsg(*displayReplyTo)
	if strings.TrimSpace(rSender) == "" {
		rSender = num(x.senderIDForMsg(*displayReplyTo))
	}
	rText := stripAnsi(strings.ReplaceAll(renderMessageBody(displayReplyTo.Message), "\n", " "))
	quoteTextColor := quotedReceivedText
	nameColor := receivedName
	if strings.HasSuffix(x.active, "@g.us") {
		nameColor = senderColor(x.senderIDForMsg(*displayReplyTo))
	}
	if displayReplyTo.Key.FromMe {
		quoteTextColor = quotedSentText
		nameColor = sentName
	}
	prefixText := " ╭─ "
	senderText := rSender + ": "
	suffixText := "  Esc cancel"
	textWidth := rightW - runeDisplayWidth(prefixText) - runeDisplayWidth(senderText) - runeDisplayWidth(suffixText)
	if textWidth < 0 {
		textWidth = 0
	}
	rText = truncateDisplayWidth(rText, textWidth)
	bar := lipgloss.NewStyle().Foreground(accent).Render(prefixText) +
		lipgloss.NewStyle().Foreground(nameColor).Bold(true).Render(senderText) +
		lipgloss.NewStyle().Foreground(quoteTextColor).Render(rText) +
		lipgloss.NewStyle().Foreground(muted).Render(suffixText)
	if themeV2 {
		// Reply bar sits on its own tinted surface (BgReply).
		if pad := rightW - lipgloss.Width(bar); pad > 0 {
			bar += strings.Repeat(" ", pad)
		}
		return applyBgToAnsiString(bar, bgReply)
	}
	return lipgloss.NewStyle().Width(rightW).Render(bar)
}

func (x m) renderAttachmentBar(rightW int) string {
	if x.pendingAttachmentPath == "" {
		return ""
	}
	label := x.pendingAttachmentLabel()
	textWidth := rightW - runeDisplayWidth(" 📎 ") - runeDisplayWidth("  Esc cancel")
	if textWidth < 0 {
		textWidth = 0
	}
	label = truncateDisplayWidth(label, textWidth)
	bar := lipgloss.NewStyle().Foreground(v2Color(accent, brand)).Bold(true).Render(" 📎 ") +
		lipgloss.NewStyle().Foreground(text).Render(label) +
		lipgloss.NewStyle().Foreground(muted).Render("  Esc cancel")
	return lipgloss.NewStyle().Width(rightW).Render(bar)
}

func (x m) emptyComposerHint() string {
	if x.pendingAttachmentPath != "" {
		return "Type a caption..."
	}
	return "Type a message..."
}
