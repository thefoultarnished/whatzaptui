package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const successLoadingScreenName = "Success Loading Screen"

func (x m) View() string {
	// Flush pending terminal-graphics deletes before repainting: Bubble Tea
	// repaints the whole screen every frame, so last frame's Kitty
	// placements must be removed before new ones are issued during render.
	dels := ""
	if x.gfx != nil {
		dels = x.gfx.takePrevDeletes()
	}
	return dels + x.viewInner()
}

func (x m) viewInner() string {
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
		return x.renderStartupView(x.w)
	}
	outerW := frameW
	var outerH int
	if currentConfig.Borderless {
		outerH = x.h
	} else {
		outerH = x.h - 2
	}
	contentW := outerW
	g := x.chatPaneGeometry()
	leftW, rightW, mainH := g.leftW, g.rightW, g.mainH
	head := x.renderHeaderContainer(contentW, leftW)
	mentionPopup := ""
	if x.mentionPickerOpen && len(x.mentionMatches) > 0 {
		mentionPopup = x.renderMentionPopup(rightW)
	}
	typedInput := x.input + x.inputBuf

	replyBar := x.renderReplyBar(contentW, rightW)
	attachmentBar := x.renderAttachmentBar(rightW)

	// Left column: sidebar + command box.
	sideH := outerH - 4
	side := x.renderSide(leftW, sideH)
	cmdBox := x.renderCommandBox(leftW)
	leftCol := lipgloss.JoinVertical(lipgloss.Left, side, cmdBox)
	if themeV2 && bgSidebar != background {
		leftCol = paintLines(leftCol, bgSidebar)
	}

	main := x.renderRightMain(rightW, mainH)

	chatInput := x.renderChatInput(rightW, typedInput)
	rightParts := []string{main}
	if replyBar != "" {
		rightParts = append(rightParts, replyBar)
	}
	if attachmentBar != "" {
		rightParts = append(rightParts, attachmentBar)
	}
	if mentionPopup != "" {
		rightParts = append(rightParts, mentionPopup)
	}
	rightParts = append(rightParts, chatInput)
	rightCol := lipgloss.JoinVertical(lipgloss.Left, rightParts...)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)
	inner := lipgloss.JoinVertical(lipgloss.Left, head, body)
	frame := renderOuterAppFrame(inner, outerW, outerH, leftW)
	if x.mode == "msgsearch" {
		return x.renderSearchOverlay(frame, outerW, outerH)
	}
	return frame
}

// chatPaneGeometry is the single source of truth for where the message pane
// sits on screen and how big it is. viewInner draws with it and the mouse
// handler hit-tests with it, so the two can't drift apart.
type chatPaneGeom struct {
	leftW, rightW int // sidebar and right column widths
	mainH         int // message pane height
	paneX, paneY  int // screen column/row where the message pane starts
}

func (x m) chatPaneGeometry() chatPaneGeom {
	border := 1
	if currentConfig.Borderless {
		border = 0
	}
	outerW := max(1, x.w-2*border)
	outerH := x.h - 2*border
	leftW := min(28, max(24, outerW/3))
	rightW := outerW - leftW

	replyBarH := 0
	if x.replyTo != nil {
		replyBarH = 1
	}
	attachmentBarH := 0
	if x.pendingAttachmentPath != "" {
		attachmentBarH = 1
	}
	mentionPopupH := 0
	if x.mentionPickerOpen && len(x.mentionMatches) > 0 {
		if popup := x.renderMentionPopup(rightW); popup != "" {
			mentionPopupH = strings.Count(popup, "\n") + 1
		}
	}

	// Calculate extra input lines from text width directly (stable, no render dependency).
	typedInput := x.input + x.inputBuf
	isInputCmd := strings.HasPrefix(strings.TrimSpace(typedInput), "/")
	inputRightFocused := x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused && x.active != ""
	inputSendVisible := inputRightFocused && !x.chatInputLocked() && !isInputCmd
	inputSendW := 0
	inputSendGap := 0
	if inputSendVisible {
		inputSendW = lipgloss.Width(lipgloss.NewStyle().Foreground(buttonInk).Background(brand).Bold(true).Render(" SEND "))
		inputSendGap = 1
	}
	inputTextAreaW := max(1, rightW-1-inputSendW-inputSendGap)
	extraInputH := max(0, draftLineCount(typedInput, inputTextAreaW)-1)

	// Main pane shrinks to accommodate multiline input. The 4 rows are the
	// two-line header plus the chat input box.
	mainH := max(1, outerH-4-replyBarH-attachmentBarH-extraInputH-mentionPopupH)
	return chatPaneGeom{
		leftW:  leftW,
		rightW: rightW,
		mainH:  mainH,
		paneX:  border + leftW,
		paneY:  border + 2,
	}
}

func (x m) renderRightMain(rightW, mainH int) string {
	hasFlash := false
	now := time.Now()
	for _, until := range x.flashUntil {
		if now.Before(until) {
			hasFlash = true
			break
		}
	}
	if x.themePicker.IsOpen {
		return x.themePicker.RenderTheme(pickerStyle(), rightW, mainH)
	}
	if x.pointerPicker.IsOpen {
		return x.pointerPicker.Render(pickerStyle(), rightW, mainH)
	}
	if x.typingAnimationPicker.IsOpen {
		return x.typingAnimationPicker.RenderTypingAnimation(pickerStyle(), rightW, mainH, x.shineFrame)
	}
	if x.mediaIconPicker.IsOpen {
		return x.mediaIconPicker.Render(pickerStyle(), rightW, mainH)
	}
	if x.mediaViewPicker.IsOpen {
		return x.mediaViewPicker.Render(pickerStyle(), rightW, mainH)
	}
	if x.userlistIconPicker.IsOpen {
		return x.userlistIconPicker.Render(pickerStyle(), rightW, mainH)
	}
	if x.splashSpeedPicker.IsOpen {
		return x.splashSpeedPicker.Render(pickerStyle(), rightW, mainH)
	}
	if x.helpPicker.IsOpen {
		return x.helpPicker.RenderHelp(pickerStyle(), rightW, mainH)
	}
	if x.reactionPicker.IsOpen {
		return x.reactionPicker.RenderReactions(pickerStyle(), rightW, mainH)
	}
	if x.settingsPicker.IsOpen {
		return x.settingsPicker.RenderSettings(pickerStyle(), rightW, mainH)
	}
	if x.confirmDialog.open {
		return x.confirmDialog.Render(rightW, mainH)
	}
	if x.fontTestOpen {
		return renderFontTest(rightW, mainH)
	}
	if x.fileBrowserOpen {
		return x.renderFileBrowser(rightW, mainH)
	}
	if x.emojiPickerOpen {
		return x.renderEmojiPickerPane(rightW, mainH)
	}
	if !hasFlash && x.mainCache != nil {
		if cached, ok := x.mainCache.get(x.revision, rightW, mainH); ok {
			return cached
		}
	}
	main := x.renderMain(rightW, mainH)
	if !hasFlash && x.mainCache != nil {
		x.mainCache.set(x.revision, rightW, mainH, main)
	}
	return main
}

func renderOuterAppFrame(inner string, outerW, outerH, leftW int) string {
	if currentConfig.Borderless {
		return lipgloss.NewStyle().
			Width(outerW).
			Height(outerH).
			Background(background).
			Render(inner)
	}
	framedBody := lipgloss.NewStyle().
		Width(outerW).
		Height(outerH).
		Border(lipgloss.RoundedBorder(), false, true, false, true).
		BorderForeground(borderSubtle).
		Render(inner)
	framedBody = connectFrameJunctions(framedBody)
	// Use a "┬" junction in the top border and a "┴" junction in the
	// bottom border where the vertical divider meets them, so the divider
	// reads as continuous from the very top to the very bottom of the frame.
	topRow := lipgloss.NewStyle().Foreground(borderSubtle).Render(
		"╭" + strings.Repeat("─", leftW) + "┬" + strings.Repeat("─", max(0, outerW-leftW-1)) + "╮")
	botRow := lipgloss.NewStyle().Foreground(borderSubtle).Render(
		"╰" + strings.Repeat("─", leftW) + "┴" + strings.Repeat("─", max(0, outerW-leftW-1)) + "╯")
	return lipgloss.JoinVertical(lipgloss.Left, topRow, framedBody, botRow)
}

func renderStatusBox(body string, innerW, innerH, outerW, outerH int) string {
	content := lipgloss.Place(outerW, outerH, lipgloss.Center, lipgloss.Center, body)
	return lipgloss.NewStyle().Width(outerW).Height(outerH).Background(background).Render(content)
}

func connectFrameJunctions(framedBody string) string {
	lines := strings.Split(framedBody, "\n")
	for i, l := range lines {
		plain := stripAnsi(l)
		plainRunes := []rune(plain)
		if len(plainRunes) < 2 {
			continue
		}
		needLeft := plainRunes[0] == '│' && plainRunes[1] == '─'
		needRight := plainRunes[len(plainRunes)-1] == '│' && plainRunes[len(plainRunes)-2] == '─'
		if !needLeft && !needRight {
			continue
		}

		runes := []rune(l)
		if needLeft {
			for j, r := range runes {
				if r == '│' {
					runes[j] = '├'
					break
				}
			}
		}
		if needRight {
			for j := len(runes) - 1; j >= 0; j-- {
				if runes[j] == '│' {
					runes[j] = '┤'
					break
				}
			}
		}
		lines[i] = string(runes)
	}
	return strings.Join(lines, "\n")
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
	lines = append(lines, lipgloss.NewStyle().Foreground(borderSubtle).Render(strings.Repeat("─", popupW-4)))

	switch {
	case x.msgSearchLoading:
		lines = append(lines, mutedStyle.Render("Searching..."))
	case x.msgSearchErr != "":
		lines = append(lines, lipgloss.NewStyle().Foreground(v2Color(red, lipgloss.Color("9"))).Render(x.msgSearchErr))
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
				if themeV2 {
					line = selectionMarker(bgSelected) + lipgloss.NewStyle().Background(bgSelected).Foreground(text).Bold(true).Render(prefix+stripSnippetTags(r.Snippet, snippetW))
				} else {
					line = lipgloss.NewStyle().Reverse(true).Render(prefix + stripSnippetTags(r.Snippet, snippetW))
				}
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
		BorderForeground(v2Color(borderFocus, muted)).
		Background(lipgloss.Color(currentTheme.Background)).
		Render(body)
	return lipgloss.Place(outerW+2, outerH+2, lipgloss.Center, lipgloss.Center, popup, lipgloss.WithWhitespaceChars(" "))
}
