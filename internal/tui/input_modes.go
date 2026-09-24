package tui

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (x *m) markPasteLikeInput() {
	x.lastPasteLikeAt = time.Now()
}

func (x m) recentPasteLikeEnter() bool {
	if x.lastPasteLikeAt.IsZero() {
		return false
	}
	return time.Since(x.lastPasteLikeAt) <= 80*time.Millisecond
}

func (x *m) cancelPendingSend() {
	x.pendingSendArmed = false
}

func (x *m) materializePendingSendAsNewline() {
	if !x.pendingSendArmed {
		return
	}
	x.pendingSendArmed = false
	x.input += x.inputBuf
	x.inputBuf = ""
	x.inputFlushScheduled = false
	x.input = appendComposerText(x.input, "\n")
	x.inputAllSelected = false
}

func (x m) handleNavKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "/":
		x.mode, x.search, x.searchInput, x.sel = "search", "", "", 0
		x.sidebarFocused = true
		x.ensureSideVisible(x.sideViewRows())
	case "up":
		f := x.filtered()
		if len(f) > 0 {
			x.sel = wrappedIndex(x.sel, len(f), -1)
		}
		x.ensureSideVisible(x.sideViewRows())
	case "down":
		f := x.filtered()
		if len(f) > 0 {
			x.sel = wrappedIndex(x.sel, len(f), 1)
		}
		x.ensureSideVisible(x.sideViewRows())
	case "enter":
		x.sidebarFocused = false
		return x.openSelectedChat()
	case "tab":
		x.sidebarFocused = false
		return x.openSelectedChat()
	}
	return x, nil
}

func (x m) handleSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		if x.active != "" {
			x.mode = "chat"
		} else {
			x.mode = "nav"
		}
		x.sidebarFocused = false
		x.search, x.searchInput, x.sel = "", "", 0
		x.ensureSideVisible(x.sideViewRows())
	case tea.KeyBackspace:
		if x.searchInput != "" {
			x.searchInput = graphemeDeleteLast(x.searchInput)
			x.search = x.searchInput
			x.ensureSideVisible(x.sideViewRows())
		}
	case tea.KeyUp:
		f := x.filtered()
		if len(f) > 0 {
			x.sel = wrappedIndex(x.sel, len(f), -1)
		}
		x.ensureSideVisible(x.sideViewRows())
	case tea.KeyDown:
		f := x.filtered()
		if len(f) > 0 {
			x.sel = wrappedIndex(x.sel, len(f), 1)
		}
		x.ensureSideVisible(x.sideViewRows())
	case tea.KeyEnter:
		q := strings.TrimSpace(x.searchInput)
		if q == "/logout" {
			x.confirmDialog.OpenWithWarning("Log out?", "Are you sure you want to log out?", "This will delete all your data from this computer.", "logout")
			return x, nil
		}
		x.sidebarFocused = false
		x.search = q
		return x.openSelectedChat()
	default:
		if k.Type == tea.KeyTab {
			x.sidebarFocused = false
			x.search = strings.TrimSpace(x.searchInput)
			return x.openSelectedChat()
		}
		if len(k.Runes) > 0 && k.Type != tea.KeyTab {
			var b strings.Builder
			for _, r := range k.Runes {
				if unicode.IsLetter(r) || unicode.IsNumber(r) {
					b.WriteRune(r)
				}
			}
			if b.Len() > 0 {
				x.searchInput += b.String()
				x.search = x.searchInput
				x.ensureSideVisible(x.sideViewRows())
			}
		}
	}
	return x, nil
}

func (x m) handleMsgSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		if x.active != "" {
			x.mode = "chat"
		} else {
			x.mode = "nav"
		}
		x.msgSearchInput = ""
		x.msgSearchResults = nil
		x.msgSearchSel = 0
		x.msgSearchLoading = false
		x.msgSearchErr = ""
		x.invalidate()
		return x, nil
	case tea.KeyBackspace:
		if x.msgSearchInput != "" {
			x.msgSearchInput = graphemeDeleteLast(x.msgSearchInput)
			x.msgSearchResults = nil
			x.msgSearchSel = 0
			x.invalidate()
		}
		return x, nil
	case tea.KeyUp:
		if len(x.msgSearchResults) > 0 {
			x.msgSearchSel = wrappedIndex(x.msgSearchSel, len(x.msgSearchResults), -1)
			x.invalidate()
		}
		return x, nil
	case tea.KeyDown:
		if len(x.msgSearchResults) > 0 {
			x.msgSearchSel = wrappedIndex(x.msgSearchSel, len(x.msgSearchResults), 1)
			x.invalidate()
		}
		return x, nil
	case tea.KeyEnter:
		q := strings.TrimSpace(x.msgSearchInput)
		// First Enter on a fresh query: fire the search.
		if q != "" && len(x.msgSearchResults) == 0 && !x.msgSearchLoading {
			x.msgSearchLoading = true
			x.msgSearchErr = ""
			x.invalidate()
			return x, searchMsgs(x.reqCtx(), x.client, x.baseURL, q)
		}
		// Second Enter (with results): jump to selected.
		if len(x.msgSearchResults) == 0 {
			return x, nil
		}
		hit := x.msgSearchResults[x.msgSearchSel]
		if hit.ChatID != x.active {
			x.saveDraft()
			x.active = hit.ChatID
			x.restoreDraft()
		}
		x.mode = "chat"
		x.sidebarFocused = false
		x.scroll = 0
		x.selectedMsgID = hit.MessageID
		x.msgSearchInput = ""
		x.msgSearchResults = nil
		x.msgSearchSel = 0
		x.msgSearchErr = ""
		x.invalidate()
		return x, getMsgsAround(x.reqCtx(), x.client, x.baseURL, hit.ChatID, hit.MessageID, 100)
	default:
		if len(k.Runes) > 0 {
			x.msgSearchInput += string(k.Runes)
			x.msgSearchResults = nil
			x.msgSearchSel = 0
			x.invalidate()
		}
		return x, nil
	}
}

func (x m) handleChatKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	inputLocked := x.chatInputLocked()
	if x.pendingSendArmed {
		switch k.Type {
		case tea.KeyRunes:
			x.materializePendingSendAsNewline()
		case tea.KeyCtrlJ:
			x.materializePendingSendAsNewline()
		case tea.KeyBackspace, tea.KeyEsc, tea.KeyTab, tea.KeyUp, tea.KeyDown, tea.KeyCtrlA:
			x.cancelPendingSend()
		case tea.KeyEnter:
			if k.Paste || k.Alt || x.recentPasteLikeEnter() {
				x.materializePendingSendAsNewline()
			}
		default:
			if len(k.Runes) > 0 {
				x.materializePendingSendAsNewline()
			}
		}
	}
	if x.replyPickMode {
		return x.handleChatReplyPickKey(k)
	}
	if x.editPickMode {
		return x.handleChatEditPickKey(k)
	}
	if k.Alt && (k.Type == tea.KeyUp || k.Type == tea.KeyDown) {
		f := x.filtered()
		if len(f) == 0 {
			return x, nil
		}
		cur := x.sel
		if x.active != "" {
			for i, c := range f {
				if c.ID == x.active {
					cur = i
					break
				}
			}
		}
		next := cur
		if k.Type == tea.KeyUp {
			next = wrappedIndex(cur, len(f), -1)
		}
		if k.Type == tea.KeyDown {
			next = wrappedIndex(cur, len(f), 1)
		}
		if next != cur {
			x.sel = next
			x.sidebarFocused = false
			x.ensureSideVisible(x.sideViewRows())
			return x.openSelectedChat()
		}
		return x, nil
	}
	if k.Alt && len(k.Runes) > 0 && (k.Runes[0] == 'f' || k.Runes[0] == 'F') {
		if x.active == "" || inputLocked {
			return x, nil
		}
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		return x, x.openFileBrowser()
	}
	if k.Alt && len(k.Runes) > 0 {
		r := k.Runes[0]
		if r == 'b' || r == 'B' || r == 'w' || r == 'W' {
			return x, x.toggleWhitelistForSelection()
		}
	}
	switch k.Type {
	case tea.KeyTab:
		if !x.sidebarFocused && !inputLocked {
			x.input += x.inputBuf
			x.inputBuf = ""
			x.inputFlushScheduled = false
			if completed, ok := completeChatInputStep(x.input); ok {
				x.input = completed
				x.inputAllSelected = false
				return x, nil
			}
		}
		x.sidebarFocused = !x.sidebarFocused
		x.leftInputFocused = false
		return x, nil
	case tea.KeyEnd:
		if !x.sidebarFocused && x.active != "" {
			x.scroll = 0
			x.selectedMsgID = ""
			x.invalidate()
			for i := range x.chats {
				if x.chats[i].ID == x.active {
					x.chats[i].UnreadCount = 0
					break
				}
			}
			return x, tea.Batch(
				getMsgs(x.reqCtx(), x.client, x.baseURL, x.active, 120),
				postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/read", map[string]string{"chatId": x.active}, func([]byte) tea.Msg { return dataErr{} }),
			)
		}
	case tea.KeyEsc:
		if x.editingMsgID != "" {
			x.editingMsgID = ""
			x.input = ""
			x.inputBuf = ""
			return x, nil
		}
		if x.inputAllSelected {
			x.inputAllSelected = false
			return x, nil
		}
		if x.pendingAttachmentPath != "" {
			x.clearPendingAttachment()
			return x, nil
		}
		if strings.HasPrefix(x.input, "@") {
			x.input = ""
			return x, nil
		}
		if x.sidebarFocused {
			x.sidebarFocused = false
			return x, nil
		}
		if x.replyTo != nil {
			x.replyTo = nil
			return x, nil
		}
		x.saveDraft()
		x.mode, x.active = "nav", ""
		x.ensureSideVisible(x.sideViewRows())
	case tea.KeyUp:
		if x.sidebarFocused {
			f := x.filtered()
			if len(f) > 0 {
				x.sel = wrappedIndex(x.sel, len(f), -1)
			}
			x.ensureSideVisible(x.sideViewRows())
			return x.openSelectedChat()
		}
		x.scroll++
		if cmd := x.maybeLoadOlder(); cmd != nil {
			return x, cmd
		}
	case tea.KeyDown:
		if x.sidebarFocused {
			f := x.filtered()
			if len(f) > 0 {
				x.sel = wrappedIndex(x.sel, len(f), 1)
			}
			x.ensureSideVisible(x.sideViewRows())
			return x.openSelectedChat()
		}
		if x.scroll > 0 {
			x.scroll--
		}
	case tea.KeyCtrlJ:
		if x.sidebarFocused || inputLocked {
			return x, nil
		}
		x.cancelPendingSend()
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		x.input = appendComposerText(x.input, "\n")
		x.inputAllSelected = false
	case tea.KeyCtrlA:
		if inputLocked {
			return x, nil
		}
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		if !x.sidebarFocused && x.input != "" {
			x.inputAllSelected = true
		}
	case tea.KeyBackspace:
		if x.sidebarFocused || inputLocked {
			return x, nil
		}
		x.cancelPendingSend()
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		if x.inputAllSelected {
			x.input = ""
			x.inputAllSelected = false
			return x, nil
		}
		if x.input != "" {
			x.input = graphemeDeleteLast(x.input)
		}
	case tea.KeyEnter:
		if k.Paste {
			if x.sidebarFocused || inputLocked {
				return x, nil
			}
			x.input += x.inputBuf
			x.inputBuf = ""
			x.inputFlushScheduled = false
			x.input = appendComposerText(x.input, "\n")
			x.inputAllSelected = false
			return x, nil
		}
		if k.Alt {
			if x.sidebarFocused || inputLocked {
				return x, nil
			}
			x.input += x.inputBuf
			x.inputBuf = ""
			x.inputFlushScheduled = false
			if x.inputAllSelected {
				x.input = ""
			}
			x.input = appendComposerText(x.input, "\n")
			x.inputAllSelected = false
			return x, nil
		}
		if x.sidebarFocused {
			x.cancelPendingSend()
			x.sidebarFocused = false
			return x.openSelectedChat()
		}
		if inputLocked {
			return x, nil
		}
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		if !hasVisibleText(x.input) || x.active == "" {
			return x, nil
		}
		x.pendingSendSeq++
		x.pendingSendArmed = true
		return x, deferComposerSend(x.pendingSendSeq)
	default:
		if x.sidebarFocused {
			if k.String() == " " {
				return x, x.toggleAudioPlayback()
			}
			x.cancelPendingSend()
			return x, nil
		}
		if inputLocked {
			return x, nil
		}
		if len(k.Runes) > 0 {
			if k.Paste || len(k.Runes) > 1 || strings.ContainsRune(string(k.Runes), '\n') || strings.ContainsRune(string(k.Runes), '\r') {
				x.markPasteLikeInput()
			}
			if x.inputAllSelected {
				x.input = sanitizeOutgoingText(string(k.Runes))
				x.inputBuf = ""
				x.inputFlushScheduled = false
				x.inputAllSelected = false
			} else {
				x.inputBuf += sanitizeOutgoingText(string(k.Runes))
				if !x.inputFlushScheduled {
					x.inputFlushScheduled = true
					return x, tea.Tick(2*time.Millisecond, func(time.Time) tea.Msg { return flushInputMsg{} })
				}
			}
		}
	}
	return x, nil
}

func (x m) handleChatReplyPickKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cands := x.replyPickCandidates()
	if len(cands) == 0 {
		x.replyPickMode = false
		x.selectedMsgID = ""
		return x, nil
	}
	if x.replyPickIndex >= len(cands) {
		x.replyPickIndex = len(cands) - 1
	}
	paneW := max(10, x.w-30)
	threshold := 6
	switch k.Type {
	case tea.KeyUp:
		if x.replyPickIndex > 0 {
			x.replyPickIndex--
			stepsFromBottom := len(cands) - 1 - x.replyPickIndex
			if stepsFromBottom > threshold {
				rows := msgRowHeight(cands[x.replyPickIndex], paneW)
				x.scroll += rows
			}
		}
		x.selectedMsgID = cands[x.replyPickIndex].Key.ID
		x.invalidate()
		return x, nil
	case tea.KeyDown:
		if x.replyPickIndex < len(cands)-1 {
			rows := msgRowHeight(cands[x.replyPickIndex], paneW)
			x.replyPickIndex++
			if x.scroll > 0 {
				x.scroll -= rows
				if x.scroll < 0 {
					x.scroll = 0
				}
			}
		}
		x.selectedMsgID = cands[x.replyPickIndex].Key.ID
		x.invalidate()
		return x, nil
	case tea.KeyEnter:
		cp := cands[x.replyPickIndex]
		x.replyTo = &cp
		x.replyPickMode = false
		x.selectedMsgID = ""
		x.scroll = 0
		x.invalidate()
		return x, nil
	case tea.KeyEsc, tea.KeyTab:
		x.replyPickMode = false
		x.selectedMsgID = ""
		x.scroll = 0
		x.invalidate()
		return x, nil
	default:
		if k.String() == "r" {
			cp := cands[x.replyPickIndex]
			x.replyTo = &cp
			x.replyPickMode = false
			x.selectedMsgID = ""
			x.scroll = 0
			x.invalidate()
			return x, nil
		}
		if k.String() == "o" {
			cp := cands[x.replyPickIndex]
			if cp.MediaProto != "" {
				x.replyPickMode = false
				x.selectedMsgID = ""
				x.invalidate()
				return x, tea.Batch(
					x.setTopBar("Opening media..."),
					downloadMedia(x.reqCtx(), x.client, x.baseURL, x.active, cp.Key.ID, false),
				)
			}
			return x, x.setTopBar("No media on this message")
		}
		if k.String() == "e" {
			cp := cands[x.replyPickIndex]
			x.reactPickMode = true
			x.reactPickMsgID = cp.Key.ID
			x.reactPickChatID = x.active
			x.reactPickSender = cp.Key.Participant
			if x.reactPickSender == "" {
				x.reactPickSender = cp.Key.RemoteJID
			}
			if cp.Key.FromMe {
				x.reactPickSender = ""
			}
			x.replyPickMode = false
			x.selectedMsgID = ""
			x.invalidate()
			x.openEmojiPicker()
			return x, nil
		}
		if k.String() == "d" {
			cp := cands[x.replyPickIndex]
			if !cp.Key.FromMe {
				return x, x.setTopBar("Can only delete your own messages")
			}
			x.replyPickMode = false
			x.selectedMsgID = ""
			x.invalidate()
			if msgs, ok := x.msgs[x.active]; ok {
				for i, msg := range msgs {
					if msg.Key.ID == cp.Key.ID {
						x.msgs[x.active] = append(msgs[:i], msgs[i+1:]...)
						break
					}
				}
			}
			return x, tea.Batch(
				x.setTopBar("Message deleted"),
				postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/delete", map[string]any{
					"chatId":    x.active,
					"messageId": cp.Key.ID,
					"fromMe":    cp.Key.FromMe,
				}, nil),
			)
		}
		return x, nil
	}
}

func (x m) handleChatEditPickKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cands := x.editPickCandidates()
	if len(cands) == 0 {
		x.editPickMode = false
		x.selectedMsgID = ""
		return x, nil
	}
	if x.editPickIndex >= len(cands) {
		x.editPickIndex = len(cands) - 1
	}
	switch k.Type {
	case tea.KeyUp:
		if x.editPickIndex > 0 {
			x.editPickIndex--
		}
		x.selectedMsgID = cands[x.editPickIndex].Key.ID
		x.invalidate()
		return x, nil
	case tea.KeyDown:
		if x.editPickIndex < len(cands)-1 {
			x.editPickIndex++
		}
		x.selectedMsgID = cands[x.editPickIndex].Key.ID
		x.invalidate()
		return x, nil
	case tea.KeyEnter:
		cp := cands[x.editPickIndex]
		x.editingMsgID = cp.Key.ID
		x.input = renderMessageBody(cp.Message)
		x.inputBuf = ""
		x.editPickMode = false
		x.selectedMsgID = ""
		x.invalidate()
		return x, nil
	case tea.KeyEsc, tea.KeyTab:
		x.editPickMode = false
		x.selectedMsgID = ""
		x.invalidate()
		return x, nil
	default:
		if k.String() == "a" {
			cp := cands[x.editPickIndex]
			x.editingMsgID = cp.Key.ID
			x.input = renderMessageBody(cp.Message)
			x.inputBuf = ""
			x.editPickMode = false
			x.selectedMsgID = ""
			x.invalidate()
			return x, nil
		}
		return x, nil
	}
}
