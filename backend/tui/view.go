package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const successLoadingScreenName = "Success Loading Screen"

func (x m) View() string {
	if x.w == 0 || x.h == 0 {
		return "loading..."
	}
	var frameW int
	if currentConfig.Borderless {
		frameW = x.w
	} else {
		frameW = max(1, x.w-2)
	}
	// Success Loading Screen: shown after login until status becomes "ready".
	if x.status != "ready" {
		var outerW, outerH int
		if currentConfig.Borderless {
			outerW = x.w
			outerH = x.h
		} else {
			outerW = frameW
			outerH = max(3, x.h-2)
		}
		var innerW, innerH int
		if currentConfig.Borderless {
			innerW = outerW
			innerH = outerH
		} else {
			innerW = max(1, outerW-2)
			innerH = max(1, outerH-2)
		}
		statusBody := x.status
		title := logoStyle.Render("WhatZap")
		subtitle := mutedStyle.Render("Private WhatsApp in your terminal")
		hint := mutedStyle.Render("Keep this window open")
		statusMsgTemplate := func(body, progress string) string {
			return title + "\n" + subtitle + "\n\n" + body + "\n" + progress + "\n\n" + hint
		}
		if x.status == "qr" {
			if x.qrRaw != "" {
				hint = accentStyle.Copy().Bold(false).Render("WhatsApp > Linked Devices > Link a Device")
				waiting := logoStyle.Render(spinnerFrames[x.spinnerFrame] + " waiting for scan")
				qrMaxW := min(max(12, innerW-6), 56)
				qrMaxH := min(max(8, innerH-6), 28)
				qrBody := renderQR(x.qrRaw, qrMaxW, qrMaxH)
				if qrBody == "" {
					qrBody = x.qrRaw
				}
				body := lipgloss.JoinVertical(
					lipgloss.Center,
					waiting,
					"",
					lipgloss.PlaceHorizontal(innerW, lipgloss.Center, qrBody),
					"",
					hint,
				)
				content := lipgloss.Place(innerW, innerH, lipgloss.Center, lipgloss.Center, body)
				if currentConfig.Borderless {
					return lipgloss.NewStyle().Width(outerW).Height(outerH).Background(background).Render(content)
				}
				return lipgloss.NewStyle().
					Width(outerW).
					Height(outerH).
					Border(lipgloss.RoundedBorder()).
					BorderForeground(brand).
					Render(content)
			} else {
				statusBody = "Generating QR..."
				hint = mutedStyle.Render("Preparing login QR")
			}
		} else if strings.HasPrefix(strings.ToLower(x.status), "logged out") {
			statusBody = accentStyle.Copy().Bold(false).Render(x.status)
			hint = mutedStyle.Render("Restart and scan a QR code to sign in again")
		} else if strings.HasPrefix(x.status, "Error:") {
			errMsg := strings.TrimPrefix(x.status, "Error:")
			errMsg = strings.TrimSpace(errMsg)
			statusBody = lipgloss.NewStyle().Foreground(red).Bold(true).Render("Error: " + errMsg)
			hint = mutedStyle.Render("Press ctrl+c to exit, then re-run whatzap")
		} else {
			statusBody = accentStyle.Copy().Bold(false).Render(spinnerFrames[x.spinnerFrame] + " Connecting your session...")
			progress := mutedStyle.Render("Syncing chats, contacts, and recent messages")
			pulse := x.loadingPulse()
			body := statusMsgTemplate(statusBody, progress+"\n"+pulse)
			content := lipgloss.Place(innerW, innerH, lipgloss.Center, lipgloss.Center, body)
			if currentConfig.Borderless {
				return lipgloss.NewStyle().Width(outerW).Height(outerH).Background(background).Render(content)
			}
			return lipgloss.NewStyle().
				Width(outerW).
				Height(outerH).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(brand).
				Render(content)
		}
		msg := statusMsgTemplate(statusBody, "")
		content := lipgloss.Place(innerW, innerH, lipgloss.Center, lipgloss.Center, msg)
		if currentConfig.Borderless {
			return lipgloss.NewStyle().Width(outerW).Height(outerH).Background(background).Render(content)
		}
		return lipgloss.NewStyle().
			Width(outerW).
			Height(outerH).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(brand).
			Render(content)
	}
	outerW := frameW
	var outerH int
	if currentConfig.Borderless {
		outerH = x.h
	} else {
		outerH = x.h - 2
	}
	contentW := outerW
	leftW := min(28, max(24, contentW/3))
	rightW := contentW - leftW
	head := x.renderHeaderContainer(contentW, leftW)
	replyBarH := 0
	if x.replyTo != nil {
		replyBarH = 1
	}
	attachmentBarH := 0
	if x.pendingAttachmentPath != "" {
		attachmentBarH = 1
	}
	typedInput := x.input + x.inputBuf

	// Calculate extra input lines from text width directly (stable, no render dependency).
	trimmedInput := strings.TrimSpace(typedInput)
	isInputCmd := strings.HasPrefix(trimmedInput, "/")
	inputRightFocused := x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused
	inputLocked := x.chatInputLocked()
	inputSendVisible := inputRightFocused && !inputLocked && !isInputCmd
	inputSendW := 0
	inputSendGap := 0
	if inputSendVisible {
		inputSendW = lipgloss.Width(lipgloss.NewStyle().Foreground(buttonInk).Background(brand).Bold(true).Render(" SEND "))
		inputSendGap = 1
	}
	inputTextAreaW := max(1, rightW-1-inputSendW-inputSendGap)
	inputLines := draftLineCount(typedInput, inputTextAreaW)
	extraInputH := max(0, inputLines-1)

	replyBar := x.renderReplyBar(contentW, rightW)
	attachmentBar := x.renderAttachmentBar(rightW)

	// Left column: sidebar + command box.
	sideH := outerH - 4
	side := x.renderSide(leftW, sideH)
	cmdBox := x.renderCommandBox(leftW)
	leftCol := lipgloss.JoinVertical(lipgloss.Left, side, cmdBox)

	// Right column: main pane shrinks to accommodate multiline input.
	mainH := max(1, outerH-4-replyBarH-attachmentBarH-extraInputH)

	hasFlash := false
	now := time.Now()
	for _, until := range x.flashUntil {
		if now.Before(until) {
			hasFlash = true
			break
		}
	}
	lastMsgID := ""
	if msgs := x.msgs[x.active]; len(msgs) > 0 {
		lastMsgID = msgs[len(msgs)-1].Key.ID
	}
	replyToID := ""
	if x.replyTo != nil {
		replyToID = x.replyTo.Key.ID
	}
	cacheKey := mainCacheKey{
		active:           x.active,
		themeName:        currentConfig.ThemeName,
		msgCount:         len(x.msgs[x.active]),
		lastMsgID:        lastMsgID,
		scroll:           x.scroll,
		w:                x.w,
		h:                x.h,
		atInput:          "",
		replyToID:        replyToID,
		selectedMsg:      x.selectedMsgID,
		contactCount:     len(x.contacts) + len(x.names),
		identityVer:      x.identityVersion,
		spinnerFrame:     x.spinnerFrame,
		inputH:           extraInputH,
		pulseOn:          (x.replyPickMode || x.editPickMode) && x.pulseOn,
		timestampNewLine: currentConfig.TimestampNewLine,
		mediaIconStyle:   currentConfig.MediaIconStyle,
		pointerIcon:      currentConfig.PointerIcon,
		mediaViewStyle:   currentConfig.MediaViewStyle,
	}
	var main string
	if x.themePicker.open {
		main = x.themePicker.RenderTheme(rightW, mainH)
	} else if x.pointerPicker.open {
		main = x.pointerPicker.Render(rightW, mainH)
	} else if x.typingAnimationPicker.open {
		main = x.typingAnimationPicker.RenderTypingAnimation(rightW, mainH, x.shineFrame)
	} else if x.mediaIconPicker.open {
		main = x.mediaIconPicker.Render(rightW, mainH)
	} else if x.mediaViewPicker.open {
		main = x.mediaViewPicker.Render(rightW, mainH)
	} else if x.userlistIconPicker.open {
		main = x.userlistIconPicker.Render(rightW, mainH)
	} else if x.helpPicker.open {
		main = x.helpPicker.RenderHelp(rightW, mainH)
	} else if x.settingsPicker.open {
		main = x.settingsPicker.RenderSettings(rightW, mainH)
	} else if x.confirmDialog.open {
		main = x.confirmDialog.Render(rightW, mainH)
	} else if x.fontTestOpen {
		main = renderFontTest(rightW, mainH)
	} else if x.fileBrowserOpen {
		main = x.renderFileBrowser(rightW, mainH)
	} else if x.emojiPickerOpen {
		main = x.renderEmojiPickerPane(rightW, mainH)
	} else if !hasFlash && x.mainCache.result != "" && x.mainCache.key == cacheKey {
		main = x.mainCache.result
	} else {
		main = x.renderMain(rightW, mainH)
		if !hasFlash {
			x.mainCache.key = cacheKey
			x.mainCache.result = main
		}
	}

	chatInput := x.renderChatInput(rightW, typedInput)
	rightParts := []string{main}
	if replyBar != "" {
		rightParts = append(rightParts, replyBar)
	}
	if attachmentBar != "" {
		rightParts = append(rightParts, attachmentBar)
	}
	rightParts = append(rightParts, chatInput)
	rightCol := lipgloss.JoinVertical(lipgloss.Left, rightParts...)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)
	inner := lipgloss.JoinVertical(lipgloss.Left, head, body)
	var frame string
	if currentConfig.Borderless {
		frame = lipgloss.NewStyle().
			Width(outerW).
			Height(outerH).
			Background(background).
			Render(inner)
	} else {
		frame = lipgloss.NewStyle().
			Width(outerW).
			Height(outerH).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).
			Render(inner)
	}
	if x.mode == "msgsearch" {
		return x.renderSearchOverlay(frame, outerW, outerH)
	}
	return frame
}

func (x m) renderSearchOverlay(frame string, outerW, outerH int) string {
	popupW := min(80, max(40, outerW-8))
	popupH := min(20, max(10, outerH-6))

	prompt := "🔍 "
	cursor := ""
	if x.cursorOn {
		cursor = "▏"
	}
	inputLine := prompt + x.msgSearchInput + cursor
	if x.msgSearchInput == "" {
		inputLine = prompt + mutedStyle.Render("type query, Enter to search, Esc to cancel") + cursor
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(inputLine))
	lines = append(lines, mutedStyle.Render(strings.Repeat("─", popupW-4)))

	switch {
	case x.msgSearchLoading:
		lines = append(lines, mutedStyle.Render("Searching..."))
	case x.msgSearchErr != "":
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(x.msgSearchErr))
	case len(x.msgSearchResults) == 0 && strings.TrimSpace(x.msgSearchInput) != "":
		// User has typed but not pressed Enter yet (or got 0 results after pressing Enter).
		lines = append(lines, mutedStyle.Render("Press Enter to search."))
	case len(x.msgSearchResults) == 0:
		lines = append(lines, mutedStyle.Render("No history yet."))
	default:
		maxResults := popupH - 5
		if maxResults < 1 {
			maxResults = 1
		}
		start := 0
		if x.msgSearchSel >= maxResults {
			start = x.msgSearchSel - maxResults + 1
		}
		end := min(len(x.msgSearchResults), start+maxResults)
		for i := start; i < end; i++ {
			r := x.msgSearchResults[i]
			chatName := x.nameFor(r.ChatID)
			prefix := chatName + ": "
			maxLineW := max(10, popupW-6)
			snippetW := max(4, maxLineW-len([]rune(prefix)))
			rendered := renderSnippet(r.Snippet, snippetW)
			line := prefix + rendered
			if i == x.msgSearchSel {
				line = lipgloss.NewStyle().Reverse(true).Render(prefix + stripSnippetTags(r.Snippet, snippetW))
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "")
	lines = append(lines, mutedStyle.Render("↑/↓ navigate · Enter open · Esc cancel"))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	popup := lipgloss.NewStyle().
		Width(popupW).
		Height(popupH).
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(muted).
		Background(lipgloss.Color(currentTheme.Background)).
		Render(body)
	return lipgloss.Place(outerW+2, outerH+2, lipgloss.Center, lipgloss.Center, popup, lipgloss.WithWhitespaceChars(" "))
}

func (x m) loadingPulse() string {
	steps := []string{
		"●○○",
		"○●○",
		"○○●",
	}
	idx := x.spinnerFrame % len(steps)
	return accentStyle.Copy().Bold(false).Render(steps[idx])
}

func (x m) renderHeaderContainer(contentW, leftW int) string {
	totalUnread := 0
	for _, c := range x.chats {
		totalUnread += c.UnreadCount
	}

	logo := " " + logoStyle.Render("WhatZap")
	statusPart := ""
	if totalUnread > 0 {
		statusPart = amberStyle.Render(strconv.Itoa(totalUnread)+" unread") + " "
	} else {
		wifi := lipgloss.NewStyle().Foreground(brand).Render("ᯤ")
		connText := lipgloss.NewStyle().Foreground(brand).Render(" connected")
		if x.demoMode {
			connText = lipgloss.NewStyle().Foreground(brand).Render(" demo")
		}
		statusPart = " " + wifi + connText + " "
	}

	statusW := lipgloss.Width(statusPart)
	padW := leftW - statusW
	if padW < 0 {
		padW = 0
	}
	logoBlock := lipgloss.NewStyle().Width(padW).Render(logo)
	leftContent := logoBlock + statusPart
	leftStr := leftContent + lipgloss.NewStyle().Foreground(muted).Render("│")

	themeMood := lipgloss.NewStyle().
		Foreground(badgeInk).
		Background(anomalyTag).
		Bold(true).
		Render(" ◉ MOOD ")
	themeName := lipgloss.NewStyle().
		Foreground(badgeInk).
		Background(accent).
		Bold(true).
		Render(" " + strings.ToUpper(currentConfig.ThemeName) + " ")
	rightStr := themeMood + themeName + " "
	rightVW := lipgloss.Width(rightStr)
	centerW := max(0, contentW-(leftW+1)-rightVW)

	centerContent := " "
	if x.topBarMsg != "" && x.topBarShown > 0 {
		centerContent = " " + purpleStyle.Render(graphemeSliceN(x.topBarMsg, x.topBarShown))
	} else if x.syncingContacts {
		shineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
		centerContent = " " + renderShine(spinnerFrames[x.spinnerFrame]+" syncing contacts...", purpleStyle, shineStyle, x.shineFrame)
	} else if x.syncingGroups {
		shineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
		centerContent = " " + renderShine(spinnerFrames[x.spinnerFrame]+" syncing groups...", purpleStyle, shineStyle, x.shineFrame)
	} else if x.active != "" {
		displayName := x.nameFor(x.active)
		var avatarStr string
		avatarW := 0
		if centerW >= 24 {
			avatarStr = renderHeaderAvatar(displayName, x.active)
			avatarW = lipgloss.Width(avatarStr) + 2 // leading " " + avatar + trailing " "
		}
		// Use all available center width for the name; preview takes whatever's left.
		nameLimit := centerW - 1 - avatarW // 1 for the leading " "
		if nameLimit < 8 {
			nameLimit = 8
		}
		if avatarStr != "" {
			centerContent = " " + avatarStr + " " + accentStyle.Render(truncate(displayName, nameLimit))
		} else {
			centerContent = " " + accentStyle.Render(truncate(displayName, nameLimit))
		}
		if time.Now().Before(x.msgActivityUntil) && x.msgActivityType == "sent" {
			centerContent += accentStyle.Copy().Bold(false).Render("  " + spinnerFrames[x.spinnerFrame] + " message sent")
		}
		if strings.HasSuffix(x.active, "@g.us") {
			if gp, ok := x.groupPreviews[x.active]; ok && len(gp.members) > 0 {
				preview := strings.Join(gp.members, ", ")
				rest := gp.total - len(gp.members)
				if rest > 0 {
					preview += fmt.Sprintf(" +%d", rest)
				}
				const sep = "  · "
				previewLimit := centerW - lipgloss.Width(centerContent) - len([]rune(sep))
				if previewLimit > 0 {
					centerContent += mutedStyle.Render(sep + truncate(preview, previewLimit))
				}
			}
		} else if phone := num(x.active); !currentConfig.HidePhoneNumber && displayName != phone {
			const sep = "  · "
			phoneText := "+" + phone
			previewLimit := centerW - lipgloss.Width(centerContent) - len([]rune(sep))
			if previewLimit > 0 {
				centerContent += mutedStyle.Render(sep + truncate(phoneText, previewLimit))
			}
		}
	}

	centerStr := lipgloss.NewStyle().Width(centerW).Render(centerContent)
	headLine := leftStr + centerStr + rightStr
	headBlock := lipgloss.NewStyle().Width(contentW).Render(headLine)
	// Use a "┬" junction where the sidebar's right border meets the header's
	// bottom border, so the vertical divider reads as continuous rather than
	// breaking into a plain "─" for this one row.
	borderRow := lipgloss.NewStyle().Foreground(muted).
		Render(strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", max(0, contentW-leftW-1)))
	return lipgloss.JoinVertical(lipgloss.Left, headBlock, borderRow)
}

func renderHeaderAvatar(name, id string) string {
	initials := headerAvatarInitials(name, id)
	return lipgloss.NewStyle().
		Foreground(buttonInk).
		Background(brand).
		Bold(true).
		Render(" " + initials + " ")
}

func headerAvatarInitials(name, id string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	initials := make([]rune, 0, 2)
	for _, part := range parts {
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				initials = append(initials, unicode.ToUpper(r))
				break
			}
		}
		if len(initials) == 2 {
			break
		}
	}
	if len(initials) == 0 {
		n := num(id)
		rs := []rune(n)
		if len(rs) >= 2 {
			initials = append(initials, rs[len(rs)-2], rs[len(rs)-1])
		} else if len(rs) == 1 {
			initials = append(initials, rs[0])
		}
	}
	if len(initials) == 1 {
		initials = append(initials, ' ')
	}
	if len(initials) == 0 {
		return "??"
	}
	return string(initials[:2])
}

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
	leftBorderColor := muted
	if x.leftInputFocused {
		leftBorderColor = accent
	}
	// Use a "┤" junction for the top-right corner, so the sidebar's right
	// border continues straight down into this box's right border instead
	// of breaking at a plain "┐" corner.
	topRow := lipgloss.NewStyle().Foreground(leftBorderColor).
		Render(strings.Repeat("─", leftW) + "┤")
	contentRow := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(leftBorderColor).
		Width(leftW).
		Foreground(text).
		Render(cmdContent)
	return lipgloss.JoinVertical(lipgloss.Left, topRow, contentRow)
}

func (x m) renderChatInput(rightW int, typedInput string) string {
	trimmedInput := strings.TrimSpace(typedInput)
	isCmd := strings.HasPrefix(trimmedInput, "/")
	rightFocused := x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused
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
		inputDisplay = lipgloss.NewStyle().Foreground(muted).Render(" blacklisted | Ctrl+K then /whitelist")
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
		inputDisplay = lipgloss.NewStyle().Foreground(muted).Render(" blacklisted | Ctrl+K then /whitelist")
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
	rightBorderColor := muted
	if rightFocused && x.active != "" {
		if _, ok := x.whitelist[num(x.active)]; ok {
			rightBorderColor = brand
		} else {
			rightBorderColor = red
		}
	}
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(rightBorderColor).
		Foreground(text).
		Render(rightContent)
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
	rText := strings.ReplaceAll(renderMessageBody(displayReplyTo.Message), "\n", " ")
	quoteTextColor := quotedReceivedText
	nameColor := receivedName
	if displayReplyTo.Key.FromMe {
		quoteTextColor = quotedSentText
		nameColor = sentName
	}
	prefixText := " â•­â”€ "
	senderText := rSender + ": "
	suffixText := "  Esc cancel"
	textWidth := rightW - runeDisplayWidth(prefixText) - runeDisplayWidth(senderText) - runeDisplayWidth(suffixText)
	if textWidth < 0 {
		textWidth = 0
	}
	rText = truncateDisplayWidth(rText, textWidth)
	bar := lipgloss.NewStyle().Foreground(accent).Render(" ╭─ ") +
		lipgloss.NewStyle().Foreground(nameColor).Bold(true).Render(senderText) +
		lipgloss.NewStyle().Foreground(quoteTextColor).Render(rText) +
		lipgloss.NewStyle().Foreground(muted).Render(suffixText)
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
	bar := lipgloss.NewStyle().Foreground(brand).Bold(true).Render(" 📎 ") +
		lipgloss.NewStyle().Foreground(text).Render(label) +
		lipgloss.NewStyle().Foreground(muted).Render("  Esc cancel")
	return lipgloss.NewStyle().Width(rightW).Render(bar)
}

func (x m) emptyComposerHint() string {
	if x.pendingAttachmentPath != "" {
		return "Type a caption | Enter send file | Esc cancel"
	}
	return "Type a message | Alt+E emoji | Alt+F file"
}

func (x m) renderSide(w, h int) string {
	f := x.filtered()
	tabW := max(8, (w-3)/2)
	inactiveTabStyle := lipgloss.NewStyle().
		Width(tabW).
		Align(lipgloss.Center).
		Foreground(muted)
	activeTabStyle := lipgloss.NewStyle().
		Width(tabW).
		Align(lipgloss.Center).
		Foreground(buttonInk).
		Background(accent).
		Bold(true)
	labelStyle := lipgloss.NewStyle().Bold(true)
	shortcutStyle := lipgloss.NewStyle().Foreground(muted)
	activeShortcutStyle := lipgloss.NewStyle().Foreground(shortcutActive)
	chatsTab := inactiveTabStyle.Render(labelStyle.Render("Chats") + " " + shortcutStyle.Render("alt+c"))
	contactsTab := inactiveTabStyle.Render(labelStyle.Render("People") + " " + shortcutStyle.Render("alt+p"))
	if x.sidebarTab == "chats" {
		chatsTab = activeTabStyle.Render("Chats " + activeShortcutStyle.Render("alt+c"))
	} else {
		contactsTab = activeTabStyle.Render("People " + activeShortcutStyle.Render("alt+p"))
	}
	tabsLine := chatsTab + " " + contactsTab
	underlineColor := muted
	if x.mode == "search" {
		underlineColor = accent
	}
	tabsUnderline := lipgloss.NewStyle().Foreground(muted).Render(strings.Repeat("─", max(8, w-2)))
	underline := lipgloss.NewStyle().Foreground(underlineColor).Render(strings.Repeat("─", max(8, w-2)))
	lines := []string{tabsLine, tabsUnderline, x.renderSearchBox(), underline}
	viewRows := max(1, h-4)
	maxStart := max(0, len(f)-viewRows)
	start := x.sideScroll
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	end := min(len(f), start+viewRows)
	lines = append(lines, x.renderUserList(f, start, end, w)...)
	if len(f) == 0 && strings.TrimSpace(x.search) != "" {
		lines = append(lines, mutedStyle.Render("  no results"))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(muted).
		Render(strings.Join(lines, "\n"))
}

func (x m) renderSearchBox() string {
	searchFocused := x.mode == "search"
	searchValue := x.search
	if searchFocused {
		searchValue = x.searchInput
	}
	searchIconText := "⌕ "
	searchIcon := mutedStyle.Render(searchIconText)
	searchLine := searchIcon
	placeholder := "search [Alt+S]"
	if x.sidebarTab == "contacts" {
		placeholder = "type to search users"
	}
	if searchFocused && x.sidebarTab == "contacts" && searchValue == "" {
		if true {
			searchLine += inputCursorStyle.Render(inputCursorGlyph)
		} else {
			searchLine += " "
		}
		searchLine += mutedStyle.Render(placeholder)
		return searchLine
	}
	if searchValue == "" && !searchFocused {
		searchLine += mutedStyle.Render(placeholder)
	} else if searchValue != "" {
		searchLine += lipgloss.NewStyle().Foreground(text).Render(searchValue)
	}
	if searchFocused {
		if true {
			searchLine += inputCursorStyle.Render(inputCursorGlyph)
		} else {
			searchLine += " "
		}
	}
	return searchLine
}

func (x m) renderUserList(f []chat, start, end, w int) []string {
	lines := []string{}
	for i := start; i < end; i++ {
		c := f[i]
		hasUnread := c.UnreadCount > 0
		isSel := i == x.sel
		navActive := x.mode != "chat" || x.sidebarFocused
		isActive := x.active != "" && num(x.active) == num(c.ID)
		highlighted := isSel && navActive
		_, whitelisted := x.whitelist[num(c.ID)]

		rowBase := lipgloss.NewStyle()
		bg := lipgloss.Color("")
		fg := lipgloss.Color("")
		if highlighted {
			bg = sidebarWhitelistActiveBg
			fg = buttonInk
			if !whitelisted {
				bg = sidebarBlacklistActiveBg
				fg = buttonInk
			}
			rowBase = rowBase.Background(bg).Foreground(fg).Bold(isSel && navActive)
		} else if isActive {
			bg = sidebarWhitelistActiveBg
			fg = buttonInk
			if !whitelisted {
				bg = sidebarBlacklistActiveBg
				fg = buttonInk
			}
			rowBase = rowBase.Background(bg).Foreground(fg)
		}

		rowWidth := max(1, w-2)
		nameWidth := max(1, rowWidth-2)

		n := i + 1
		var numLabel string
		if icon := userlistIconPrefix(currentConfig.UserlistIconStyle); icon != "" {
			numLabel = icon
		} else {
			switch {
			case n >= 100:
				numLabel = fmt.Sprintf("%d ", n)
			case n >= 10:
				numLabel = fmt.Sprintf("%d. ", n)
			default:
				numLabel = fmt.Sprintf("0%d. ", n)
			}
		}
		nameText := numLabel + x.name(c)
		isMarqueeRow := highlighted || (isActive && x.mode == "chat" && !x.sidebarFocused)
		if isMarqueeRow && graphemeCount(nameText) > nameWidth {
			offset := x.sidebarMarqueeOffset
			maxOffset := graphemeCount(nameText) - nameWidth
			if offset < 0 {
				offset = 0
			}
			if offset > maxOffset {
				offset = maxOffset
			}
			nameText = graphemeWindow(nameText, offset, nameWidth)
		} else {
			nameText = truncate(nameText, nameWidth)
		}
		var content string
		switch {
		case hasUnread:
			// Shine plays for unread rows in every state (highlighted/active too).
			// The wave travels only across the name, so it bounces at the last
			// character instead of drifting through the trailing padding.
			var shineBase, shineHigh lipgloss.Style
			if highlighted || isActive {
				shineBase = lipgloss.NewStyle().Foreground(fg).Background(bg)
				shineHigh = lipgloss.NewStyle().Foreground(accent).Background(bg).Bold(true)
			} else {
				shineBase = lipgloss.NewStyle().Foreground(muted)
				shineHigh = lipgloss.NewStyle().Foreground(accent).Bold(true)
			}
			if highlighted {
				shineBase = shineBase.Bold(isSel && navActive)
			}
			// Clamp the label to the row so the wave's end matches the visible name.
			label := nameText
			if runeDisplayWidth(label) > nameWidth {
				label = padRight(label, nameWidth)
			}
			content = renderShine(label, shineBase, shineHigh, x.shineFrame+i*4)
			if pad := nameWidth - runeDisplayWidth(label); pad > 0 {
				padStyle := lipgloss.NewStyle()
				if highlighted || isActive {
					padStyle = padStyle.Background(bg)
				}
				content += padStyle.Render(strings.Repeat(" ", pad))
			}
		case highlighted || isActive:
			contentStyle := lipgloss.NewStyle().Foreground(fg).Background(bg)
			if highlighted {
				contentStyle = contentStyle.Bold(isSel && navActive)
			}
			content = contentStyle.Render(padRight(nameText, nameWidth))
		default:
			content = padRight(nameText, nameWidth)
		}
		unreadStyle := lipgloss.NewStyle()
		if highlighted || isActive {
			unreadStyle = unreadStyle.Background(bg)
		}
		if hasUnread {
			content += unreadStyle.Foreground(accent).Render("\u25cf")
		} else {
			content += unreadStyle.Render(" ")
		}
		leftPad := " "
		if highlighted || isActive {
			leftPad = lipgloss.NewStyle().Background(bg).Render(" ")
		}
		line := rowBase.Width(rowWidth).Render(leftPad + content)
		lines = append(lines, line)
	}
	return lines
}

func chatMessageWrapWidth(w int, msg string) int {
	// Keep message content comfortably inside the pane so inline timestamps and long links don't spill.
	contentW := max(8, w-2)
	seventyPct := (contentW * 7) / 10
	base := max(8, min(seventyPct, contentW-18))
	return max(8, base-countEmojiHeuristic(msg))
}

func wrapMessageLines(msgBody string, availableW int, fromMe bool, senderName string) []string {
	if fromMe {
		return strings.Split(wrapTextBalanced(msgBody, availableW), "\n")
	}
	prefixWidth := runeDisplayWidth(senderName + ": ")
	return strings.Split(wrapTextWithPrefix(msgBody, availableW, prefixWidth), "\n")
}

var urlRegex = regexp.MustCompile(`https?://[^\s]+`)

func renderTextWithLinks(s string, baseStyle lipgloss.Style, original ...string) string {
	matches := urlRegex.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return baseStyle.Render(s)
	}
	var origURLs []string
	if len(original) > 0 && original[0] != "" {
		origURLs = urlRegex.FindAllString(original[0], -1)
	}
	linkStyle := baseStyle.Copy().
		Foreground(lipgloss.Color("#89b4fa")).
		Underline(true)
	var sb strings.Builder
	lastIdx := 0
	for _, match := range matches {
		sb.WriteString(baseStyle.Render(s[lastIdx:match[0]]))
		matchText := s[match[0]:match[1]]
		targetURL := matchText
		prefix := strings.TrimRight(matchText, ".")
		for _, oURL := range origURLs {
			if strings.HasPrefix(oURL, prefix) {
				targetURL = oURL
				break
			}
		}
		sb.WriteString("\x1b]8;;" + targetURL + "\x1b\\" + linkStyle.Render(matchText) + "\x1b]8;;\x1b\\")
		lastIdx = match[1]
	}
	sb.WriteString(baseStyle.Render(s[lastIdx:]))
	return sb.String()
}

func renderStyledMessageText(
	ln string,
	bodyStyle lipgloss.Style,
	tokenStyle lipgloss.Style,
	isMediaMsg bool,
	original string,
) string {
	if isMediaMsg && strings.HasPrefix(ln, "[") {
		if end := strings.Index(ln, "]"); end > 0 {
			token := ln[:end+1]
			rest := ln[end+1:]
			return tokenStyle.Render(token) + renderTextWithLinks(rest, bodyStyle, original)
		}
	}
	return renderTextWithLinks(ln, bodyStyle, original)
}

func outgoingMessageIndent(paneW int, blockW int, fromMe bool) string {
	if !fromMe {
		return ""
	}
	paneW = max(1, paneW)
	blockW = max(1, min(blockW, paneW))
	return strings.Repeat(" ", max(0, paneW-blockW))
}

// renderUploadProgress draws a horizontal progress bar of the form
// "████████░░░░░░░░░ 47%". availableW caps the total width; the bar
// shrinks on narrow panes and the percent label is dropped if even
// that doesn't fit.
func renderUploadProgress(pct, availableW int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	// Reserve " 100%" worst-case (5 chars).
	barW := 16
	if availableW > 0 {
		// Total = 1 ('[') + barW + 1 (']') + 1 (space) + 4 ("100%") = barW + 7
		maxBarW := availableW - 7
		if maxBarW < 4 {
			// No room for a bar; fall back to "47%".
			return fmt.Sprintf("%d%%", pct)
		}
		if maxBarW < barW {
			barW = maxBarW
		}
	}
	filled := pct * barW / 100
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	filledStr := strings.Repeat("█", filled)
	emptyStr := strings.Repeat("░", empty)
	return "[" + filledStr + emptyStr + "] " + fmt.Sprintf("%d%%", pct)
}

func (x m) renderWelcomePane(w, h int) string {
	keyStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(text)
	secStyle := lipgloss.NewStyle().Foreground(purple).Bold(true)
	divColor := lipgloss.NewStyle().Foreground(muted)

	// content column width
	col := 38
	if col > w-4 {
		col = w - 4
	}
	if col < 20 {
		col = 20
	}

	// header centered
	star := lipgloss.NewStyle().Foreground(brand).Bold(true).Render(spinnerFrames[x.spinnerFrame])
	title := lipgloss.NewStyle().Foreground(text).Bold(true).Render("WhatZap")
	tagline := mutedStyle.Render("Terminal WhatsApp client")
	header := lipgloss.JoinVertical(lipgloss.Center, star, title, tagline)
	header = lipgloss.PlaceHorizontal(col, lipgloss.Center, header)

	div := divColor.Render(strings.Repeat("─", col))

	kw := 15
	shortcut := func(key, desc string) string {
		rk := keyStyle.Render(key)
		pad := kw - len([]rune(key))
		if pad < 1 {
			pad = 1
		}
		return rk + strings.Repeat(" ", pad) + descStyle.Render(desc)
	}

	section := func(title string, items []string) string {
		h := secStyle.Render("  " + title)
		return h + "\n" + strings.Join(items, "\n")
	}

	nav := section("Navigation", []string{
		shortcut("↑ / ↓", "Browse chats"),
		shortcut("Enter", "Open chat"),
		shortcut("Esc", "Close / go back"),
		shortcut("Tab", "Toggle sidebar"),
	})

	chat := section("In Chat", []string{
		shortcut("Alt+↑ / ↓", "Switch chats"),
		shortcut("↑ / ↓", "Scroll messages"),
		shortcut("Alt+R", "Pick msg to reply"),
		shortcut("Alt+E", "Emoji picker"),
		shortcut("Alt+F", "File picker"),
		shortcut("Tab / Esc", "Exit chat"),
	})

	quick := section("Quick Actions", []string{
		shortcut("Alt+S", "Search"),
		shortcut("Alt+C", "Chats tab"),
		shortcut("Alt+P", "Contacts tab"),
		shortcut("Ctrl+K", "Command palette"),
		shortcut("Alt+M", "Toggle mouse"),
	})

	hint := lipgloss.PlaceHorizontal(col, lipgloss.Center,
		mutedStyle.Render("Select a chat to start messaging"))

	body := lipgloss.JoinVertical(lipgloss.Left,
		"", header, "", div, "",
		nav, "", chat, "", quick,
		"", div, hint,
	)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body)
}

func (x m) renderMain(w, h int) string {
	if x.active == "" {
		return x.renderWelcomePane(w, h)
	}
	items := x.msgs[x.active]
	type reactionRender struct {
		emoji     string
		name      string
		isMe      bool
		isContact bool
	}
	reactionsFor := map[string][]reactionRender{}
	for _, msg := range items {
		if rxn, ok := msg.Message["reactionMessage"].(map[string]any); ok {
			targetID, _ := rxn["targetMsgID"].(string)
			emoji, _ := rxn["emoji"].(string)
			if targetID != "" && emoji != "" {
				rSender := "Me"
				rIsMe := msg.Key.FromMe
				sid := x.senderIDForMsg(msg)
				fullName := x.nameFor(sid)
				if fullName == num(sid) && msg.PushName != "" {
					fullName = msg.PushName
				}
				senderNum := num(sid)
				isKnown := rIsMe || strings.TrimSpace(fullName) != "" || x.names[senderNum] != "" || x.whitelist[senderNum] != ""
				if !msg.Key.FromMe {
					rSender = truncate(fullName, 10)
				}
				reactionsFor[targetID] = append(reactionsFor[targetID], reactionRender{
					emoji:     emoji,
					name:      rSender,
					isMe:      rIsMe,
					isContact: isKnown,
				})
			}
		}
	}
	needed := h + x.scroll
	msgBlocks := [][]string{}
	msgTimestamps := []int64{}
	msgBlockMsgs := []wireMsg{}
	msgBlockTimeLine := []int{}
	for i := len(items) - 1; i >= 0; i-- {
		msg := items[i]
		msgBody := renderMessageBody(msg.Message)
		if msgBody == "" {
			msgBody = "[media]"
		}

		timeStr := formatExactTime(msg.MessageTimestamp)
		if edited, _ := msg.Message["edited"].(bool); edited {
			timeStr += " (edited)"
		}
		receiptText := ""
		if msg.Key.FromMe {
			switch msg.ReceiptStatus {
			case "delivered":
				receiptText = "  ✓✓ "
			case "read", "played":
				receiptText = "  ✓✓ "
			default:
				receiptText = "  ✓ "
			}
		}
		senderName := "Me"
		if !msg.Key.FromMe {
			senderName = truncate(x.senderNameForMsg(msg), 40)
		}

		isFlashing := !msg.Key.FromMe && msg.Key.ID != "" && x.flashUntil[msg.Key.ID].After(time.Now())

		timeColor := muted
		if isFlashing {
			timeColor = accent
		}

		if _, ok := msg.Message["reactionMessage"]; ok {
			continue
		}

		isGroup := strings.HasSuffix(x.active, "@g.us")
		isMediaMsg := isMediaWire(msg)
		availableW := chatMessageWrapWidth(w, msgBody)
		// In 2-line mode, non-media outgoing lines need room for the right icon.
		// Reduce availableW so text wraps before the icon column; without this,
		// long lines fill the full pane and indent collapses to 0 (left-aligned).
		if msg.Key.FromMe && !isMediaMsg && currentConfig.TimestampNewLine {
			availableW = max(8, availableW-runeDisplayWidth(receivedMsgIcon+" "))
		}

		wrappedSender := senderName
		if !isGroup {
			wrappedSender = receivedMsgIcon
		}
		isImageMsg := false
		isPollCreation := false
		var imgPayload map[string]any
		if msg.Message != nil {
			if p, ok := msg.Message["imageMessage"].(map[string]any); ok {
				isImageMsg = true
				imgPayload = p
			}
			if _, ok := msg.Message["pollCreationMessage"]; ok {
				isPollCreation = true
			}
		}
		numPixelLines := 0
		var wrapped []string
		if currentConfig.MediaViewStyle == "pixel" && isImageMsg {
			localPath := x.downloadedMedia[msg.Key.ID]
			if localPath != "" {
				pixelArt := renderPixelArt(localPath, availableW)
				var pixelLines []string
				if pixelArt != "" {
					pixelLines = strings.Split(pixelArt, "\n")
				} else {
					pixelLines = []string{"[image]"}
				}
				numPixelLines = len(pixelLines)
				wrapped = pixelLines
			} else {
				wrapped = []string{"[downloading preview...]"}
			}
			if caption, _ := imgPayload["caption"].(string); caption != "" {
				wrappedCaption := wrapMessageLines(caption, availableW, msg.Key.FromMe, wrappedSender)
				wrapped = append(wrapped, wrappedCaption...)
			}
		} else if isPollCreation {
			var prefixWidth int
			if !msg.Key.FromMe {
				if isGroup {
					prefixWidth = runeDisplayWidth(senderName + ": ")
				} else {
					prefixWidth = 0
				}
			}
			rawLines := strings.Split(msgBody, "\n")
			wrapped = make([]string, len(rawLines))
			wrapped[0] = rawLines[0]
			for i := 1; i < len(rawLines); i++ {
				if !msg.Key.FromMe {
					wrapped[i] = strings.Repeat(" ", prefixWidth) + rawLines[i]
				} else {
					wrapped[i] = rawLines[i]
				}
			}
		} else {
			wrapped = wrapMessageLines(msgBody, availableW, msg.Key.FromMe, wrappedSender)
		}

		bodyColor := sentText
		if !msg.Key.FromMe {
			bodyColor = receivedText
		}

		// For multi-line outgoing text messages, try re-wrapping at a slightly
		// narrower width so the first (widest) line doesn't reach the right edge
		// while the last line's text sits well to the left of the timestamp.
		if msg.Key.FromMe && !isMediaMsg && len(wrapped) > 1 {
			tsApprox := runeDisplayWidth("  " + timeStr + receiptText + " ")
			narrowW := max(8, availableW-tsApprox/2)
			if narrowW < availableW {
				if attempt := wrapMessageLines(msgBody, narrowW, msg.Key.FromMe, wrappedSender); len(attempt) == len(wrapped) {
					wrapped = attempt
				}
			}
		}
		mediaTokenBG := mediaTokenBg
		if x.pulseOn {
			mediaTokenBG = mediaTokenPulseBg
		}
		if isFlashing {
			bodyColor = accent
		}

		isClickedSelected := x.selectedMsgID != "" && msg.Key.ID == x.selectedMsgID
		selectColor := lipgloss.Color("")
		if isClickedSelected && (x.replyPickMode || x.editPickMode) {
			if x.pulseOn {
				selectColor = accent
			} else {
				selectColor = lipgloss.Color(blendHex(string(accent), string(muted), 0.45))
			}
		} else if isClickedSelected {
			selectColor = brand
		}
		replySelected := isClickedSelected && (x.replyPickMode || x.editPickMode)
		applySelectedBG := func(st lipgloss.Style) lipgloss.Style {
			if selectColor != "" {
				st = st.Foreground(selectColor)
				if replySelected {
					st = st.Bold(true)
				}
			}
			return st
		}
		applyBodyBG := func(st lipgloss.Style) lipgloss.Style {
			if selectColor != "" {
				st = st.Foreground(selectColor)
				if replySelected {
					st = st.Bold(true)
				}
			}
			return st
		}

		timeStyled := mutedStyle.Copy().Foreground(timeColor).Render("  " + timeStr)
		progressActive := false
		if msg.Key.FromMe && isMediaMsg {
			if _, ok := x.uploadProgress[msg.Key.ID]; ok {
				progressActive = true
				// During upload, replace the timestamp slot with a live
				// progress bar. Same gutter width as the timestamp+receipt
				// it stands in for, so the right edge stays anchored.
				progressW := runeDisplayWidth(timeStr + receiptText) + 2
				if progressW < 4 {
					progressW = 16
				}
				timeStyled = mutedStyle.Copy().Foreground(timeColor).Render(renderUploadProgress(x.uploadProgress[msg.Key.ID], progressW))
			}
		}
		if receiptText != "" && !progressActive {
			receiptColor := muted
			if msg.ReceiptStatus == "read" || msg.ReceiptStatus == "played" {
				receiptColor = accent
			}
			timeStyled += mutedStyle.Copy().Foreground(receiptColor).Bold(true).Render(receiptText)
		}
		block := []string{}
		hasQuoteLine := false

		reactionLinePlain := ""
		reactionLine := ""
		reactionMerged := false
		if rxns, ok := reactionsFor[msg.Key.ID]; ok && len(rxns) > 0 {
			// Split into known contacts vs others.
			var contactRxns []reactionRender
			otherEmojis := map[string]int{}
			for _, rxn := range rxns {
				if rxn.isContact {
					contactRxns = append(contactRxns, rxn)
				} else {
					otherEmojis[rxn.emoji]++
				}
			}

			reactionPlainParts := []string{}
			reactionStyledParts := []string{}
			firstReactionColor := receivedName
			for i, rxn := range contactRxns {
				reactionColor := receivedName
				if rxn.isMe {
					reactionColor = sentName
				}
				if i == 0 {
					firstReactionColor = reactionColor
				}
				// 1-to-1 chats: emoji alone is enough; the other party is
				// implicit. Groups keep the reactor's name so multiple
				// reactions from different people stay distinguishable.
				label := rxn.emoji
				if isGroup {
					label = rxn.emoji + " " + rxn.name
				}
				reactionPlainParts = append(reactionPlainParts, label)
				reactionStyledParts = append(reactionStyledParts,
					applySelectedBG(lipgloss.NewStyle().Foreground(reactionColor).Bold(true)).Render(label))
			}

			// Build summary for non-contact reactions.
			if len(otherEmojis) > 0 {
				sortedEmojis := make([]string, 0, len(otherEmojis))
				for e := range otherEmojis {
					sortedEmojis = append(sortedEmojis, e)
				}
				sort.Slice(sortedEmojis, func(i, j int) bool {
					if otherEmojis[sortedEmojis[i]] != otherEmojis[sortedEmojis[j]] {
						return otherEmojis[sortedEmojis[i]] > otherEmojis[sortedEmojis[j]]
					}
					return sortedEmojis[i] < sortedEmojis[j]
				})
				summaryPlainParts := []string{}
				summaryStyledParts := []string{}
				for _, emoji := range sortedEmojis {
					count := otherEmojis[emoji]
					summaryPlainParts = append(summaryPlainParts, fmt.Sprintf("%s %d", emoji, count))
					summaryStyledParts = append(summaryStyledParts,
						applySelectedBG(mutedStyle).Render(fmt.Sprintf("%s %d", emoji, count)))
				}
				if len(contactRxns) > 0 {
					reactionPlainParts = append(reactionPlainParts, "│ "+strings.Join(summaryPlainParts, " "))
					reactionStyledParts = append(reactionStyledParts,
						applySelectedBG(mutedStyle).Render("│ ")+strings.Join(summaryStyledParts, applySelectedBG(mutedStyle).Render(" ")))
				} else {
					reactionPlainParts = append(reactionPlainParts, strings.Join(summaryPlainParts, " "))
					reactionStyledParts = append(reactionStyledParts, strings.Join(summaryStyledParts, applySelectedBG(mutedStyle).Render(" ")))
					firstReactionColor = muted
				}
			}

			reactionLinePlain = "  ╰─ " + strings.Join(reactionPlainParts, ", ")
			reactionPrefix := applySelectedBG(lipgloss.NewStyle().Foreground(firstReactionColor)).Render("  ╰─ ")
			if len(contactRxns) > 0 {
				reactionLine = reactionPrefix + strings.Join(reactionStyledParts, applySelectedBG(mutedStyle).Render(", "))
			} else {
				reactionLine = reactionPrefix + strings.Join(reactionStyledParts, applySelectedBG(mutedStyle).Render(" "))
			}
		}

		quoteStyled := ""
		quoteStyledRight := ""
		quotePlainRight := ""
		if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
			if qText, _ := ext["quotedText"].(string); qText != "" {
				hasQuoteLine = true
				qParticipant, _ := ext["quotedParticipant"].(string)
				qFromMe, _ := ext["quotedFromMe"].(bool)
				qSender := "Me"
				if !qFromMe && strings.TrimSpace(qParticipant) != "" {
					qSender = x.nameFor(qParticipant)
				}
				if !qFromMe && strings.TrimSpace(qSender) == "" {
					qSender = num(qParticipant)
				}
				origQuote := strings.ReplaceAll(qText, "\n", " ")
				qText = origQuote
				if len([]rune(qText)) > 50 {
					qText = string([]rune(qText)[:50]) + "..."
				}
				qSenderColor := receivedName
				qTextColor := receivedText
				if qFromMe {
					qSenderColor = sentName
					qTextColor = sentText
				}
				quotePrefix := "  ╭─ "
				quoteSuffix := " ─╮ "
				quoteStyled = applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor)).Render(quotePrefix) +
					applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor).Bold(true)).Render(qSender+": ") +
					renderTextWithLinks(qText, applySelectedBG(lipgloss.NewStyle().Foreground(qTextColor)), origQuote)
				quoteStyledRight = applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor).Bold(true)).Render(qSender+": ") +
					renderTextWithLinks(qText, applySelectedBG(lipgloss.NewStyle().Foreground(qTextColor)), origQuote) +
					applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor)).Render(quoteSuffix)
				quotePlainRight = qSender + ": " + qText + quoteSuffix
			}
		}
		outgoingBlockW := 0
		outgoingBodyW := 0
		// In 2-line mode the icon appears at a fixed right column on every body
		// line and the timestamp line. Reserve its width in both suffix and body.
		outgoingIconW := 0
		if currentConfig.TimestampNewLine {
			outgoingIconW = runeDisplayWidth(receivedMsgIcon + " ")
		}
		timeSuffixPlain := "  " + timeStr + receiptText + " "

		timeSuffixW := runeDisplayWidth(timeSuffixPlain)
		if msg.Key.FromMe {
			for _, ln := range wrapped {
				extra := outgoingIconW
				if isMediaMsg {
					extra = 0
				}
				spacing := 1
				if currentConfig.TimestampNewLine && !isMediaMsg {
					spacing = 2
				}
				outgoingBodyW = max(outgoingBodyW, lipgloss.Width(ln)+spacing+extra)
			}
			// Block must fit the widest text line OR the timestamp, whichever is wider.
			lastLineW := runeDisplayWidth(wrapped[len(wrapped)-1] + " ")
			if !currentConfig.TimestampNewLine && lastLineW+timeSuffixW <= max(1, w-2) {
				// Timestamp fits on the last line (and 2-line mode is off).
				outgoingBlockW = max(outgoingBodyW, lastLineW+timeSuffixW)
			} else if currentConfig.TimestampNewLine {
				// Timestamp goes on its own line. Reserve outgoingIconW extra
				// columns so the timestamp line's pad never needs to go
				// negative to align its receipt tick with the last character
				// of the message text (which sits behind the right-side icon).
				outgoingBlockW = max(outgoingBodyW, timeSuffixW+outgoingIconW)
			} else {
				// Timestamp goes on its own line (too wide for the last line).
				outgoingBlockW = max(outgoingBodyW, timeSuffixW)
			}
			if reactionLinePlain != "" {
				if currentConfig.TimestampNewLine {
					// Reaction shares the timestamp line (reaction on the left,
					// timestamp on the right), so reserve room for both plus a
					// 1-space gap between them.
					outgoingBlockW = max(outgoingBlockW, runeDisplayWidth(reactionLinePlain)+1+timeSuffixW+outgoingIconW)
				} else {
					outgoingBlockW = max(outgoingBlockW, runeDisplayWidth(reactionLinePlain))
				}
			}
			outgoingBlockW = min(outgoingBlockW, max(1, w-2))
		}
		indent := outgoingMessageIndent(max(1, w-2), outgoingBlockW, msg.Key.FromMe)
		// For multi-line outgoing media, the icon and filename lines should
		// be right-aligned within the last text line's width (not the full
		// bubble width) so their right edges match the caption/name's right
		// edge, with the timestamp extending further right. The last line
		// itself (caption or name) right-aligns within the full width.
		// Use the caption width WITHOUT the trailing space (which outgoing
		// rendering appends to every line) so the content right edges line
		// up after the trailing space is stripped.
		var lastTextW int
		var mediaName string
		if msg.Key.FromMe && isMediaMsg && len(wrapped) > 1 {
			lastTextW = runeDisplayWidth(wrapped[len(wrapped)-1])
			if msg.Message != nil {
				for _, key := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage"} {
					if v, ok := msg.Message[key].(map[string]any); ok {
						mediaName, _ = v["fileName"].(string)
						break
					}
				}
			}
		}
		if quoteStyled != "" {
			if msg.Key.FromMe {
				qPlainW := runeDisplayWidth(quotePlainRight)
				targetW := outgoingBodyW
				if currentConfig.TimestampNewLine && !isMediaMsg {
					targetW -= 3
				}
				qIndent := max(0, len(indent)+targetW-qPlainW)
				block = append(block, strings.Repeat(" ", qIndent)+quoteStyledRight)
			} else {
				block = append(block, quoteStyled)
			}
		}
		lastBodyPlainW := 0
		for i, ln := range wrapped {
			// Media layout: line 1 is the kind tag (pre-styled in saturated
			// color), line 2 is the filename (muted), line 3 is the caption
			// (body color, with timestamp appended). Render the filename
			// muted and the caption in body color; for 2-line media where
			// only one of name/caption is present, apply the matching style.
			lineFg := bodyColor
			if isMediaMsg && i == 1 && mediaName != "" {
				lineFg = muted
			}
			bodyStyle := applyBodyBG(lipgloss.NewStyle().Foreground(lineFg).Bold(isFlashing))
			tokenStyle := applyBodyBG(lipgloss.NewStyle().
				Foreground(tagInk).
				Background(mediaTokenBG).
				Bold(true))
			lineParts := []string{}
			if !msg.Key.FromMe && i == 0 && isGroup {
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(receivedName).
					Bold(true)).
					Render(senderName+": "))
			} else if !msg.Key.FromMe && i == 0 && !isGroup {
				isLast := i == len(wrapped)-1
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(receivedName)).
					Render(incomingLeftIcon(0, isLast)+" "))
			} else if !msg.Key.FromMe && i > 0 && !isGroup {
				isLast := i == len(wrapped)-1
				var iconPad string
				switch receivedMsgIcon {
				case "│", "┃", "║":
					iconPad = incomingLeftIcon(i, isLast) + " "
				default:
					iconPad = strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon+" "))
				}
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(receivedName)).
					Render(iconPad))
			}
			if currentConfig.MediaViewStyle == "pixel" && isImageMsg && i < numPixelLines {
				lineParts = append(lineParts, ln)
			} else {
				lineParts = append(lineParts, renderStyledMessageText(ln, bodyStyle, tokenStyle, isMediaMsg, msgBody))
			}
			if msg.Key.FromMe && i == len(wrapped)-1 {
				// Float timestamp to the right of the last line if it fits and
				// 2-line mode is off; otherwise it goes on its own line below.
				lastW := runeDisplayWidth(ln + " ")
				if !currentConfig.TimestampNewLine && lastW+timeSuffixW <= outgoingBlockW {
					pad := outgoingBlockW - lastW - timeSuffixW
					lineParts = append(lineParts, bodyStyle.Render(strings.Repeat(" ", pad+1)))
					lineParts = append(lineParts, timeStyled)
				}
			} else if !msg.Key.FromMe && i == len(wrapped)-1 && !currentConfig.TimestampNewLine {
				lineParts = append(lineParts, timeStyled)
			}
			if msg.Key.FromMe {
				if currentConfig.TimestampNewLine && !isMediaMsg {
					contentW := lipgloss.Width(strings.Join(lineParts, ""))
					fillW := max(0, outgoingBlockW-outgoingIconW-2-contentW)
					lineParts = append([]string{strings.Repeat(" ", fillW)}, lineParts...)
					lineParts = append(lineParts, "  ", lipgloss.NewStyle().Foreground(muted).Render(outgoingRightIcon(i, i == len(wrapped)-1)), " ")
				} else {
					lineParts = append(lineParts, " ")
				}
			}
			bodyContent := strings.Join(lineParts, "")
			if msg.Key.FromMe {
				if isMediaMsg && i < len(wrapped)-1 {
					// Non-last lines of multi-line media (icon + filename):
					// right-align within the last text line's width so their
					// right edges match the caption/name's right edge, with
					// the timestamp extending further right. Manually pad
					// with spaces (stripping ANSI for width measurement) to
					// avoid lipgloss.Align quirks with pre-styled content.
					visualW := lipgloss.Width(bodyContent)
					targetW := max(1, (w-2)-outgoingBlockW+lastTextW+1-outgoingIconW)
					if currentConfig.TimestampNewLine {
						targetW = max(1, w-2-outgoingIconW)
					}
					if visualW < targetW {
						bodyContent = strings.Repeat(" ", targetW-visualW) + bodyContent
					}
					if currentConfig.TimestampNewLine {
						bodyContent += lipgloss.NewStyle().Foreground(muted).Render(outgoingRightIcon(i, i == len(wrapped)-1)) + " "
					}
				} else if isMediaMsg {
					// Last line of media (caption/name + timestamp):
					// right-align within the full chat width.
					visualW := lipgloss.Width(bodyContent)
					targetW := max(1, w-2-outgoingIconW)
					if visualW < targetW {
						bodyContent = strings.Repeat(" ", targetW-visualW) + bodyContent
					}
					if currentConfig.TimestampNewLine {
						bodyContent += lipgloss.NewStyle().Foreground(muted).Render(outgoingRightIcon(i, i == len(wrapped)-1)) + " "
					}
				} else {
					bodyContent = indent + bodyContent
				}
			}
			lastBodyPlainW = runeDisplayWidth(ln)
			if !msg.Key.FromMe && i == 0 && isGroup {
				lastBodyPlainW += runeDisplayWidth(senderName + ": ")
			} else if !msg.Key.FromMe && !isGroup {
				lastBodyPlainW += runeDisplayWidth(receivedMsgIcon + " ")
			}
			block = append(block, bodyContent)
		}
		if msg.Key.FromMe {
			lastW := runeDisplayWidth(wrapped[len(wrapped)-1] + " ")
			if currentConfig.TimestampNewLine || lastW+timeSuffixW > outgoingBlockW {
				if currentConfig.TimestampNewLine {
					// Body lines reserve outgoingIconW columns on the right for
					// the right-side icon, so the message text's last character
					// sits outgoingIconW+2 columns before the line's right edge.
					// Pad the timestamp line so its last visible glyph (the
					// second receipt tick, before receiptText's own trailing
					// space) lands in that same column.
					pad := max(0, outgoingBlockW-outgoingIconW-timeSuffixW)
					if reactionLine != "" {
						// Put the reaction on the left of the same line, with
						// the timestamp staying right-aligned as usual.
						reactionW := runeDisplayWidth(reactionLinePlain)
						fillW := max(0, pad-reactionW)
						timeLine := indent + reactionLine + strings.Repeat(" ", fillW) + timeStyled
						block = append(block, timeLine)
						reactionMerged = true
					} else {
						timeLine := indent + strings.Repeat(" ", pad) + timeStyled
						block = append(block, timeLine)
					}
				} else {
					pad := max(0, outgoingBlockW-timeSuffixW)
					timeLine := indent + strings.Repeat(" ", pad) + timeStyled + " "
					block = append(block, timeLine)
				}
			}
		} else if currentConfig.TimestampNewLine {
			var prefix string
			var prefixWidth int
			if isGroup {
				prefixWidth = runeDisplayWidth(senderName + ": ")
				prefix = strings.Repeat(" ", prefixWidth)
			} else {
				prefixWidth = runeDisplayWidth(receivedMsgIcon + " ")
				icon := strings.Repeat(" ", prefixWidth)
				prefix = applyBodyBG(lipgloss.NewStyle().Foreground(receivedName)).Render(icon)
			}
			// timeStyled carries a "  " lead-in meant for the same-line layout;
			// drop it here so the timestamp starts directly under the message
			// text instead of leaving an extra gap after the icon/prefix.
			timeOnly := mutedStyle.Foreground(timeColor).Render(timeStr)
			if reactionLine != "" {
				// Reaction shares the timestamp line, right-aligned to the
				// message bubble's right edge while the timestamp stays left.
				reactionW := runeDisplayWidth(reactionLinePlain)
				timeW := prefixWidth + runeDisplayWidth(timeStr)
				targetW := max(lastBodyPlainW, timeW+1+reactionW)
				fillW := max(1, targetW-timeW-reactionW)
				timeLine := prefix + timeOnly + strings.Repeat(" ", fillW) + reactionLine
				block = append(block, timeLine)
				reactionMerged = true
			} else {
				timeLine := prefix + timeOnly
				block = append(block, timeLine)
			}
		}
		if reactionLine != "" && !reactionMerged {
			reactionOffset := max(0, lastBodyPlainW-runeDisplayWidth(reactionLinePlain))
			if msg.Key.FromMe {
				reactionLine = indent + strings.Repeat(" ", reactionOffset) + reactionLine
			} else {
				reactionLine = strings.Repeat(" ", reactionOffset) + reactionLine
			}
			block = append(block, reactionLine)
		}

		timeLine := len(wrapped)
		if hasQuoteLine {
			timeLine++
		}
		if replySelected && len(block) > 0 {
			marker := lipgloss.NewStyle().Foreground(selectColor).Bold(true).Render("│ ")
			if x.pulseOn {
				block[0] = marker + block[0]
			} else {
				block[0] = "  " + block[0]
			}
		}
		msgBlocks = append(msgBlocks, block)
		msgTimestamps = append(msgTimestamps, msg.MessageTimestamp)
		msgBlockMsgs = append(msgBlockMsgs, msg)
		msgBlockTimeLine = append(msgBlockTimeLine, timeLine)

		total := 0
		for _, b := range msgBlocks {
			total += len(b)
		}
		if total >= needed {
			break
		}
	}
	for i, j := 0, len(msgBlocks)-1; i < j; i, j = i+1, j-1 {
		msgBlocks[i], msgBlocks[j] = msgBlocks[j], msgBlocks[i]
		msgTimestamps[i], msgTimestamps[j] = msgTimestamps[j], msgTimestamps[i]
		msgBlockMsgs[i], msgBlockMsgs[j] = msgBlockMsgs[j], msgBlockMsgs[i]
		msgBlockTimeLine[i], msgBlockTimeLine[j] = msgBlockTimeLine[j], msgBlockTimeLine[i]
	}
	all := []string{}
	allDates := []string{}
	allBlockIdx := []int{}
	allTimeLine := []bool{}
	lastDay := ""
	for idx, b := range msgBlocks {
		dayLabel := dateSeparatorLabel(msgTimestamps[idx])
		if dayLabel != lastDay {
			lastDay = dayLabel
			all = append(all, dateSeparatorLine(dayLabel, w))
			allDates = append(allDates, "")
			allBlockIdx = append(allBlockIdx, 0)
			allTimeLine = append(allTimeLine, false)
		}
		timeLineInBlock := msgBlockTimeLine[idx]
		for j, ln := range b {
			all = append(all, ln)
			allDates = append(allDates, dayLabel)
			allBlockIdx = append(allBlockIdx, idx+1)
			allTimeLine = append(allTimeLine, j == timeLineInBlock)
		}
	}
	// When the other party is typing, reserve the bottom row of the chat
	// area for the typing indicator and let messages shift up by one row.
	// Otherwise the typing row would overwrite the last message line.
	messageH := h
	if _, typing := x.typingChats[x.active]; typing {
		messageH = h - 1
	}

	start := len(all) - messageH - x.scroll
	if start < 0 {
		start = 0
	}
	end := start + messageH
	if end > len(all) {
		end = len(all)
	}
	lines := make([]string, end-start)
	copy(lines, all[start:end])
	if len(lines) > 0 && start < len(allDates) {
		pinLabel := ""
		for i := start; i >= 0 && pinLabel == ""; i-- {
			if i < len(allDates) && allDates[i] != "" {
				pinLabel = allDates[i]
			}
		}
		if pinLabel != "" {
			firstIsSep := start < len(allDates) && allDates[start] == ""
			if !firstIsSep {
				lines[0] = dateSeparatorLine(pinLabel, w)
			}
		}
	}
	for len(lines) < messageH {
		lines = append(lines, "")
	}
	if _, typing := x.typingChats[x.active]; typing {
		name := x.nameFor(x.active)
		typingIcons := getTypingIcons(currentConfig.TypingAnimationStyle)
		icon := typingIcons[x.shineFrame%len(typingIcons)]
		text := icon + " " + name + " is typing..."
		baseSt := lipgloss.NewStyle().Foreground(anomalyTag)
		shineSt := lipgloss.NewStyle().Foreground(accent).Bold(true)
		lines = append(lines, renderShine(text, shineSt, baseSt, x.shineFrame))
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (x m) name(c chat) string {
	if n, ok := x.names[num(c.ID)]; ok && strings.TrimSpace(n) != "" {
		return n
	}
	if strings.HasSuffix(c.ID, "@g.us") {
		if c.Name != "" {
			return c.Name
		}
		if c.Subject != "" {
			return c.Subject
		}
		return num(c.ID)
	}
	if n, ok := x.whitelist[num(c.ID)]; ok && strings.TrimSpace(n) != "" {
		return n
	}
	if c.Name != "" {
		return c.Name
	}
	if c.Subject != "" {
		return c.Subject
	}
	if ct, ok := x.contacts[c.ID]; ok {
		if ct.Notify != "" {
			return ct.Notify
		}
		if ct.Name != "" {
			return ct.Name
		}
	}
	n := num(c.ID)
	if ct, ok := x.contactsByNumber[n]; ok {
		if strings.TrimSpace(ct.Notify) != "" {
			return ct.Notify
		}
		if strings.TrimSpace(ct.Name) != "" {
			return ct.Name
		}
	}
	return num(c.ID)
}

func (x m) nameFor(id string) string {
	for _, c := range x.chats {
		if c.ID == id {
			return x.name(c)
		}
	}
	return x.name(chat{ID: id})
}

func (x m) senderIDForMsg(msg wireMsg) string {
	if msg.Key.Participant != "" {
		return msg.Key.Participant
	}
	return msg.Key.RemoteJID
}

func (x m) senderNameForMsg(msg wireMsg) string {
	sid := x.senderIDForMsg(msg)
	name := x.nameFor(sid)
	if name == num(sid) && msg.PushName != "" {
		return msg.PushName
	}
	return name
}

func (x m) msgIDAtLine(lineIdx, w, h int) string {
	items := x.msgs[x.active]
	reactionsForLine := map[string]int{}
	for _, msg := range items {
		if rxn, ok := msg.Message["reactionMessage"].(map[string]any); ok {
			if tid, _ := rxn["targetMsgID"].(string); tid != "" {
				reactionsForLine[tid]++
			}
		}
	}
	needed := h + x.scroll
	blocks := [][]string{}
	ids := [][]string{}
	for i := len(items) - 1; i >= 0; i-- {
		msg := items[i]
		if _, ok := msg.Message["reactionMessage"]; ok {
			continue
		}
		mb := renderMessageBody(msg.Message)
		if mb == "" {
			mb = "[media]"
		}
		availableW := chatMessageWrapWidth(w, mb)
		senderName := "Me"
		if !msg.Key.FromMe {
			senderName = truncate(x.senderNameForMsg(msg), 40)
		}
		wrapped := wrapMessageLines(mb, availableW, msg.Key.FromMe, senderName)
		hasQuote := false
		if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
			if qt, _ := ext["quotedText"].(string); qt != "" {
				hasQuote = true
			}
		}
		lineCount := len(wrapped)
		if hasQuote {
			lineCount++
		}
		if reactionsForLine[msg.Key.ID] > 0 {
			lineCount++
		}
		block := make([]string, lineCount)
		idBlock := make([]string, lineCount)
		for j := range block {
			block[j] = ""
			idBlock[j] = msg.Key.ID
		}
		blocks = append(blocks, block)
		ids = append(ids, idBlock)
		total := 0
		for _, b := range blocks {
			total += len(b)
		}
		if total >= needed {
			break
		}
	}
	for i, j := 0, len(blocks)-1; i < j; i, j = i+1, j-1 {
		blocks[i], blocks[j] = blocks[j], blocks[i]
		ids[i], ids[j] = ids[j], ids[i]
	}
	allIDs := []string{}
	for _, id := range ids {
		allIDs = append(allIDs, id...)
	}
	start := len(allIDs) - h - x.scroll
	if start < 0 {
		start = 0
	}
	visible := allIDs[start:]
	if len(visible) > h {
		visible = visible[:h]
	}
	if lineIdx < 0 || lineIdx >= len(visible) {
		return ""
	}
	return visible[lineIdx]
}

func (x *m) setTopBar(msg string) tea.Cmd {
	x.topBarVer++
	x.topBarMsg = msg
	x.topBarShown = 0
	ver := x.topBarVer
	d := 2200 * time.Millisecond
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "fail") || strings.Contains(lower, "err") || strings.Contains(lower, "invalid") || strings.Contains(lower, "not") {
		d = 10000 * time.Millisecond
	}
	return tea.Batch(nextTopBarTypeTick(ver), clearTopBarAfter(ver, d))
}
func nextTopBarTypeTick(ver int) tea.Cmd {
	return tea.Tick(28*time.Millisecond, func(time.Time) tea.Msg { return topBarTypeMsg{ver: ver} })
}
func nextCursorBlink() tea.Cmd {
	return tea.Tick(530*time.Millisecond, func(time.Time) tea.Msg { return cursorBlinkMsg{} })
}
func nextSpinnerTick() tea.Cmd {
	return tea.Tick(102*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
func clearTopBarAfter(ver int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return topBarClearMsg{ver: ver} })
}
func setTopBarAfter(msg string, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return topBarSetMsg{msg: msg} })
}
func padRight(s string, n int) string {
	w := runeDisplayWidth(s)
	if w == n {
		return s
	}
	if w > n {
		var b strings.Builder
		used := 0
		for _, r := range s {
			rw := runeDisplayWidth(string(r))
			if used+rw > n {
				break
			}
			b.WriteRune(r)
			used += rw
		}
		return b.String()
	}
	return s + strings.Repeat(" ", n-w)
}

// incomingLeftIcon returns the left-side icon glyph for a given line of a
// received message. Pipe-like icons use half-height box-drawing chars so each
// message forms a self-contained capsule: ╷ on the first line (open at top),
// │ on middle lines, ╵ on the last body line (open at bottom). Single-line
// messages use ╷ only — adjacent ╷ chars don't visually connect because each
// leaves the cell's top half empty, creating a gap between messages.
func incomingLeftIcon(lineIdx int, isLastLine bool) string {
	switch receivedMsgIcon {
	case "│":
		if lineIdx == 0 {
			return "╷"
		}
		if isLastLine {
			return "╵"
		}
		return "│"
	case "┃":
		if lineIdx == 0 {
			return "╻"
		}
		if isLastLine {
			return "╹"
		}
		return "┃"
	case "║":
		if lineIdx == 0 {
			return "╷"
		}
		if isLastLine {
			return "╵"
		}
		return "║"
	}
	if lineIdx == 0 {
		return receivedMsgIcon
	}
	return strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon))
}

// outgoingRightIcon returns the right-side icon glyph for a given line of an
// outgoing message in 2-line mode. Pipe-like icons use half-height box-drawing
// chars (╷ top, ╵ bottom) so each message forms a self-contained capsule and
// adjacent messages' bars don't appear connected. Other icons are used as-is.
func outgoingRightIcon(lineIdx int, isTimestamp bool) string {
	switch receivedMsgIcon {
	case "│":
		if isTimestamp {
			return "╵"
		}
		return "│"
	case "┃":
		if isTimestamp {
			return "╹"
		}
		return "┃"
	case "║":
		if isTimestamp {
			return "╵"
		}
		return "║"
	}
	if isTimestamp {
		return receivedMsgIcon
	}
	return strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon))
}

func renderShine(text string, baseStyle, shineStyle lipgloss.Style, frame int) string {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return ""
	}
	cycle := 2 * (n - 1)
	if cycle < 1 {
		cycle = 1
	}
	f := frame % cycle
	center := f
	if f >= n {
		center = cycle - f
	}
	baseFg, _ := baseStyle.GetForeground().(lipgloss.Color)
	shineFg, _ := shineStyle.GetForeground().(lipgloss.Color)
	mid1 := lerpColor(baseFg, shineFg, 0.15)
	mid2 := lerpColor(baseFg, shineFg, 0.35)
	mid3 := lerpColor(baseFg, shineFg, 0.70)
	// Inherit the base background so the wave doesn't punch holes in a
	// highlighted row's background as it travels.
	bg := baseStyle.GetBackground()
	m3 := lipgloss.NewStyle().Foreground(mid3).Bold(true).Background(bg)
	m2 := lipgloss.NewStyle().Foreground(mid2).Background(bg)
	m1 := lipgloss.NewStyle().Foreground(mid1).Background(bg)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		dist := i - center
		if dist < 0 {
			dist = -dist
		}
		ch := string(runes[i])
		switch {
		case dist == 0:
			sb.WriteString(shineStyle.Render(ch))
		case dist == 1:
			sb.WriteString(m3.Render(ch))
		case dist == 2:
			sb.WriteString(m2.Render(ch))
		case dist == 3:
			sb.WriteString(m1.Render(ch))
		default:
			sb.WriteString(baseStyle.Render(ch))
		}
	}
	return sb.String()
}

func hexToRGB(c lipgloss.Color) (r, g, b uint8, ok bool) {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	rv, e1 := strconv.ParseUint(s[1:3], 16, 8)
	gv, e2 := strconv.ParseUint(s[3:5], 16, 8)
	bv, e3 := strconv.ParseUint(s[5:7], 16, 8)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false
	}
	return uint8(rv), uint8(gv), uint8(bv), true
}

func lerpColor(a, b lipgloss.Color, t float64) lipgloss.Color {
	ar, ag, ab, aok := hexToRGB(a)
	br, bg, bb, bok := hexToRGB(b)
	if !aok || !bok {
		if t < 0.5 {
			return a
		}
		return b
	}
	r := uint8(float64(ar) + t*(float64(br)-float64(ar)))
	g := uint8(float64(ag) + t*(float64(bg)-float64(ag)))
	bv := uint8(float64(ab) + t*(float64(bb)-float64(ab)))
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, bv))
}
