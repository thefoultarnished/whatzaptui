package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (x m) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	markPasteLikeInput := func() {
		x.lastPasteLikeAt = time.Now()
	}
	recentPasteLikeEnter := func() bool {
		if x.lastPasteLikeAt.IsZero() {
			return false
		}
		return time.Since(x.lastPasteLikeAt) <= 80*time.Millisecond
	}
	cancelPendingSend := func() {
		x.pendingSendArmed = false
	}
	materializePendingSendAsNewline := func() {
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
	if x.fileBrowserOpen {
		return x.handleFileBrowser(k)
	}

	if x.confirmDialog.open {
		action, done := x.confirmDialog.Handle(k)
		if !done {
			x.invalidate()
			return x, nil
		}
		pending := x.confirmDialog.action
		x.confirmDialog.Close()
		x.invalidate()
		if action != "confirm" {
			return x, x.setTopBar("Cancelled")
		}
		switch pending {
		case "logout":
			return x, logout(x.reqCtx(), x.client, x.baseURL)
		case "whitelistall":
			return x, x.doWhitelistAll()
		case "blacklistall":
			return x, x.doBlacklistAll()
		case "audioplayer":
			path := x.audioFallbackPath
			x.audioFallbackPath = ""
			x.confirmDialog.Close()
			x.invalidate()
			if path == "" {
				return x, nil
			}
			return x, openFile(path)
		}
		return x, nil
	}

	switch k.String() {
	case "ctrl+c":
		x.cancelRequests()
		return x, tea.Quit
	case "alt+c":
		x.replyPickMode = false
		x.selectedMsgID = ""
		x.scroll = 0
		x.sidebarTab = "chats"
		x.sidebarFocused = true
		if x.mode == "chat" {
			x.mode = "nav"
		}
		x.sel = 0
		x.sideScroll = 0
		x.invalidate()
		x.ensureSideVisible(x.sideViewRows())
		return x, nil
	case "alt+p":
		x.replyPickMode = false
		x.selectedMsgID = ""
		x.scroll = 0
		x.sidebarTab = "contacts"
		x.sidebarFocused = true
		x.mode = "search"
		x.searchInput = ""
		x.sel = 0
		x.sideScroll = 0
		x.invalidate()
		x.ensureSideVisible(x.sideViewRows())
		return x, nil
	case "alt+s":
		if x.status != "ready" {
			return x, nil
		}
		if x.mode == "search" {
			x.sidebarFocused = false
			if x.active != "" {
				x.mode = "chat"
			} else {
				x.mode = "nav"
			}
			x.search = ""
			x.searchInput = ""
			x.sel = 0
			x.ensureSideVisible(x.sideViewRows())
			return x, nil
		}
		x.leftInputFocused = false
		x.sidebarFocused = true
		x.mode = "search"
		x.ensureSideVisible(x.sideViewRows())
		return x, nil
	case "alt+m":
		x.mouseEnabled = !x.mouseEnabled
		currentConfig.MouseEnabled = x.mouseEnabled
		saveConfig()
		if x.mouseEnabled {
			return x, tea.Batch(x.setTopBar("Mouse on"), func() tea.Msg { return tea.EnableMouseCellMotion() })
		}
		return x, tea.Batch(x.setTopBar("Mouse off - zoom restored"), func() tea.Msg { return tea.DisableMouse() })
	case "alt+b", "alt+w":
		return x, x.toggleWhitelistForSelection()
	case "alt+o":
		if x.mode == "chat" && x.active != "" {
			msgs := x.msgs[x.active]
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].MediaProto != "" {
					return x, tea.Batch(
						x.setTopBar("Opening media..."),
						downloadMedia(x.reqCtx(), x.client, x.baseURL, x.active, msgs[i].Key.ID, false),
					)
				}
			}
			return x, x.setTopBar("No media in this chat")
		}
	case "ctrl+k":
		x.leftInputFocused = true
		x.sidebarFocused = false
		if x.leftInput == "" {
			x.leftInput = "/"
		}
		if x.mode == "search" {
			x.mode = "nav"
		}
		return x, nil
	case "alt+v", "ctrl+v":
		if x.status == "ready" && x.mode == "chat" && x.active != "" {
			return x, tea.Batch(x.setTopBar("Reading clipboard…"), pasteFromClipboard())
		}
		return x, nil
	case "alt+e":
		if x.status == "ready" && x.mode == "chat" && x.active != "" && !x.chatInputLocked() {
			x.openEmojiPicker()
		}
		return x, nil
	case "alt+a":
		if x.status == "ready" && x.mode == "chat" && x.active != "" && !x.chatInputLocked() {
			if x.editPickMode {
				x.editPickMode = false
				x.selectedMsgID = ""
				x.invalidate()
				return x, nil
			}
			cands := x.editPickCandidates()
			if len(cands) == 0 {
				return x, x.setTopBar("No editable messages (own, text, last 20 min)")
			}
			x.replyPickMode = false
			x.editPickMode = true
			x.editPickIndex = len(cands) - 1
			x.selectedMsgID = cands[x.editPickIndex].Key.ID
			x.invalidate()
			return x, nil
		}
		return x, nil
	case "alt+r":
		if x.status == "ready" && x.mode == "chat" && x.active != "" && !x.chatInputLocked() {
			if x.replyPickMode {
				x.replyPickMode = false
				x.selectedMsgID = ""
				return x, nil
			}
			cands := x.replyPickCandidates()
			if len(cands) == 0 {
				return x, x.setTopBar("No messages to reply to")
			}
			x.editPickMode = false
			x.replyPickMode = true
			// find the last visible candidate based on current scroll
			if x.scroll == 0 {
				x.replyPickIndex = len(cands) - 1
			} else {
				// count rows from bottom to find which message is at the bottom of the visible area
				paneW := max(10, x.w-30)
				rowsFromBottom := 0
				picked := len(cands) - 1
				for i := len(cands) - 1; i >= 0; i-- {
					rowsFromBottom += msgRowHeight(cands[i], paneW)
					if rowsFromBottom > x.scroll {
						picked = i
						break
					}
				}
				x.replyPickIndex = picked
			}
			x.selectedMsgID = cands[x.replyPickIndex].Key.ID
			x.invalidate()
			return x, nil
		}
		return x, nil
	}
	if x.leftInputFocused {
		return x.handleLeftInput(k)
	}
	if x.status != "ready" {
		if x.sessionReady && !strings.HasPrefix(x.status, "Error:") && !strings.HasPrefix(strings.ToLower(x.status), "logged out") {
			x.status = "ready"
			x.invalidate()
			return x, x.refreshWindowTitleCmd()
		}
		return x, nil
	}
	if x.fontTestOpen {
		if k.Type == tea.KeyEsc {
			x.fontTestOpen = false
			x.invalidate()
		}
		return x, nil
	}
	if x.themePicker.open {
		action, done := x.themePicker.HandleTheme(k)
		if !done {
			applyThemeByName(x.themePicker.SelectedKey())
			x.invalidate()
		} else if action == "confirm" {
			applyThemeByName(x.themePicker.Close(true))
			saveConfig()
		} else {
			applyThemeByName(x.themePicker.Close(false))
			x.invalidate()
		}
		return x, nil
	}
	if x.pointerPicker.open {
		action, done := x.pointerPicker.Handle(k)
		if !done {
			receivedMsgIcon = x.pointerPicker.SelectedKey()
			x.invalidate()
		} else if action == "confirm" {
			receivedMsgIcon = x.pointerPicker.Close(true)
			currentConfig.PointerIcon = receivedMsgIcon
			saveConfig()
		} else {
			receivedMsgIcon = x.pointerPicker.Close(false)
			x.invalidate()
		}
		return x, nil
	}
	if x.helpPicker.open {
		_, done := x.helpPicker.HandleHelp(k)
		if done {
			x.helpPicker.Close(false)
			x.invalidate()
		}
		return x, nil
	}
	if x.settingsPicker.open {
		action, done := x.settingsPicker.HandleSettings(k)
		if done {
			if action == "confirm" {
				msg := x.settingsPicker.toggleSetting()
				x.invalidate()

				// Handle mouse enable/disable command for the toggle
				var cmd tea.Cmd
				if x.settingsPicker.idx >= 0 && x.settingsPicker.idx < len(settingsDefs) && settingsDefs[x.settingsPicker.idx].name == "Mouse" {
					x.mouseEnabled = currentConfig.MouseEnabled
					if x.mouseEnabled {
						cmd = func() tea.Msg { return tea.EnableMouseCellMotion() }
					} else {
						cmd = func() tea.Msg { return tea.DisableMouse() }
					}
					return x, tea.Batch(x.setTopBar(msg), cmd)
				}
				return x, x.setTopBar(msg)
			} else if action == "selector" {
				selName := ""
				if x.settingsPicker.idx >= 0 && x.settingsPicker.idx < len(settingsDefs) {
					selName = settingsDefs[x.settingsPicker.idx].name
				}
				x.settingsPicker.Close(false)
				switch selName {
				case "Media icons":
					x.mediaIconPicker = picker{title: "Media Icons", items: buildMediaIconPickerItems()}
					x.mediaIconPicker.Open(currentConfig.MediaIconStyle)
				case "Media view":
					x.mediaViewPicker = picker{title: "Media View", items: buildMediaViewPickerItems()}
					x.mediaViewPicker.Open(currentConfig.MediaViewStyle)
				case "Userlist icons":
					x.userlistIconPicker = picker{title: "Userlist Icons", items: buildUserlistIconPickerItems()}
					style := currentConfig.UserlistIconStyle
					if style == "" {
						style = "numbers"
					}
					x.userlistIconPicker.Open(style)
				default:
					x.typingAnimationPicker = picker{title: "Typing Animation", items: buildTypingAnimationPickerItems()}
					x.typingAnimationPicker.Open(currentConfig.TypingAnimationStyle)
				}
				x.invalidate()
				return x, nil
			}
			x.settingsPicker.Close(false)
			x.invalidate()
		}
		return x, nil
	}
	if x.typingAnimationPicker.open {
		return x.handleSettingsSubPicker(&x.typingAnimationPicker, &currentConfig.TypingAnimationStyle, k)
	}
	if x.mediaIconPicker.open {
		return x.handleSettingsSubPicker(&x.mediaIconPicker, &currentConfig.MediaIconStyle, k)
	}
	if x.mediaViewPicker.open {
		return x.handleSettingsSubPicker(&x.mediaViewPicker, &currentConfig.MediaViewStyle, k)
	}
	if x.userlistIconPicker.open {
		return x.handleSettingsSubPicker(&x.userlistIconPicker, &currentConfig.UserlistIconStyle, k)
	}
	if x.emojiPickerOpen {
		return x.handleEmojiPicker(k)
	}
	if k.Type == tea.KeyCtrlF && x.mode != "msgsearch" {
		x.mode = "msgsearch"
		x.msgSearchInput = ""
		x.msgSearchResults = nil
		x.msgSearchSel = 0
		x.msgSearchLoading = false
		x.msgSearchErr = ""
		x.invalidate()
		return x, nil
	}
	switch x.mode {
	case "nav":
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
	case "search":
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
				x.confirmDialog.Open("Log out?", "Are you sure you want to log out?", "logout")
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
	case "msgsearch":
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
			// Fetch 100 messages centred on the hit (50 before + target + 50 after).
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
	case "chat":
		inputLocked := x.chatInputLocked()
		if x.pendingSendArmed {
			switch k.Type {
			case tea.KeyRunes:
				materializePendingSendAsNewline()
			case tea.KeyCtrlJ:
				materializePendingSendAsNewline()
			case tea.KeyBackspace, tea.KeyEsc, tea.KeyTab, tea.KeyUp, tea.KeyDown, tea.KeyCtrlA:
				cancelPendingSend()
			case tea.KeyEnter:
				if k.Paste || k.Alt || recentPasteLikeEnter() {
					materializePendingSendAsNewline()
				}
			default:
				if len(k.Runes) > 0 {
					materializePendingSendAsNewline()
				}
			}
		}
		if x.replyPickMode {
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
					// Remove from local state immediately
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
							// A-16: backend's messages table PK is
							// (chat_id, id, from_me); delete must
							// include from_me or it would wipe both
							// the incoming and outgoing row when the
							// same chat has two messages with the
							// same id. The TUI already gates on
							// cp.Key.FromMe above, so this is always
							// true at the call site.
							"fromMe": cp.Key.FromMe,
						}, nil),
					)
				}
				return x, nil
			}
		}
		if x.editPickMode {
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
		// Alt+B / Alt+W: toggle whitelist for the selected / active contact.
		// Mirrors the alt+f pattern above so this works even if the terminal
		// delivers Alt+letter in a way that k.String() doesn't see as "alt+b".
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
				// Clear local unread count immediately.
				for i := range x.chats {
					if x.chats[i].ID == x.active {
						x.chats[i].UnreadCount = 0
						break
					}
				}
				// Reload most-recent page and always mark chat as read so the
				// phone's unread badge clears even when local count was stale.
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
			cancelPendingSend()
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
			cancelPendingSend()
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
				cancelPendingSend()
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
				// Space outside the composer toggles audio playback for
				// the selected/latest audio message; every other key is
				// swallowed so typing can't start from the sidebar.
				if k.String() == " " {
					return x, x.toggleAudioPlayback()
				}
				cancelPendingSend()
				return x, nil
			}
			if inputLocked {
				return x, nil
			}
			if len(k.Runes) > 0 {
				if k.Paste || len(k.Runes) > 1 || strings.ContainsRune(string(k.Runes), '\n') || strings.ContainsRune(string(k.Runes), '\r') {
					markPasteLikeInput()
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
	}
	return x, nil
}

func wrappedIndex(cur, n, delta int) int {
	if n <= 0 {
		return 0
	}
	cur = ((cur % n) + n) % n
	next := (cur + delta) % n
	if next < 0 {
		next += n
	}
	return next
}

func (x *m) handleSettingsSubPicker(p *picker, cfgField *string, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	action, done := p.Handle(k)
	if !done {
		*cfgField = p.SelectedKey()
		x.invalidate()
	} else if action == "confirm" {
		*cfgField = p.Close(true)
		saveConfig()
		x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
		x.settingsPicker.Open("")
		x.invalidate()
	} else {
		*cfgField = p.Close(false)
		x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
		x.settingsPicker.Open("")
		x.invalidate()
	}
	return *x, nil
}


func (x m) handleFileBrowser(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := x.fileBrowserVisibleRows(max(1, x.h-8))

	if x.fileBrowserPathMode {
		switch k.Type {
		case tea.KeyEsc:
			x.fileBrowserPathMode = false
			x.fileBrowserPathBuf = ""
			x.invalidate()
		case tea.KeyEnter:
			target := strings.TrimSpace(x.fileBrowserPathBuf)
			target = strings.Trim(target, `"'`)
			target = strings.TrimRight(target, `\/`)
			x.fileBrowserPathMode = false
			x.fileBrowserPathBuf = ""
			if target != "" {
				if err := x.loadFileBrowserDir(target); err != nil {
					return x, x.setTopBar(fmt.Sprintf("Path not found: %s", target))
				}
			}
			x.invalidate()
		case tea.KeyBackspace:
			runes := []rune(x.fileBrowserPathBuf)
			if len(runes) > 0 {
				x.fileBrowserPathBuf = string(runes[:len(runes)-1])
			}
			x.invalidate()
		case tea.KeyRunes:
			for _, r := range k.Runes {
				if r >= 32 && r != 127 {
					x.fileBrowserPathBuf += string(r)
				}
			}
			x.invalidate()
		}
		return x, nil
	}

	if k.String() == "alt+f" {
		x.fileBrowserPathMode = true
		x.fileBrowserPathBuf = ""
		x.invalidate()
		return x, nil
	}

	switch k.Type {
	case tea.KeyEsc:
		if x.fileBrowserFilter != "" {
			x.fileBrowserFilter = ""
			x.rebuildFileBrowserFiltered()
			x.fileBrowserIndex = 0
			x.fileBrowserScroll = 0
			x.invalidate()
			return x, nil
		}
		x.closeFileBrowser()
		return x, nil
	case tea.KeyUp:
		x.fileBrowserIndex = wrappedIndex(x.fileBrowserIndex, len(x.fileBrowserFiltered), -1)
		x.ensureFileBrowserVisible(rows)
		x.invalidate()
		return x, nil
	case tea.KeyDown:
		x.fileBrowserIndex = wrappedIndex(x.fileBrowserIndex, len(x.fileBrowserFiltered), 1)
		x.ensureFileBrowserVisible(rows)
		x.invalidate()
		return x, nil
	case tea.KeyTab:
		x.fileBrowserSortRecent = !x.fileBrowserSortRecent
		entries, err := readFileBrowserEntries(x.fileBrowserDir, x.fileBrowserSortRecent)
		if err == nil {
			x.fileBrowserEntries = entries
			x.rebuildFileBrowserFiltered()
			x.fileBrowserIndex = 0
			x.fileBrowserScroll = 0
		}
		x.invalidate()
		return x, nil
	case tea.KeyBackspace:
		if x.fileBrowserFilter != "" {
			runes := []rune(x.fileBrowserFilter)
			x.fileBrowserFilter = string(runes[:len(runes)-1])
			x.rebuildFileBrowserFiltered()
			x.fileBrowserIndex = 0
			x.fileBrowserScroll = 0
			x.invalidate()
			return x, nil
		}
		if x.fileBrowserDir == "" {
			return x, nil
		}
		parent := filepath.Dir(x.fileBrowserDir)
		if parent == x.fileBrowserDir {
			return x, nil
		}
		if err := x.loadFileBrowserDir(parent); err != nil {
			return x, x.setTopBar(fmt.Sprintf("File browser: %v", err))
		}
		return x, nil
	case tea.KeyEnter:
		entry, ok := x.selectedFileBrowserEntry()
		if !ok || entry.isPlaceholder {
			return x, nil
		}
		if entry.isDir {
			if err := x.loadFileBrowserDir(entry.path); err != nil {
				return x, x.setTopBar(fmt.Sprintf("File browser: %v", err))
			}
			return x, nil
		}
		x.closeFileBrowser()
		x.input = ""
		x.inputBuf = ""
		x.inputFlushScheduled = false
		x.inputAllSelected = false
		x.replyTo = nil
		x.setPendingAttachment(entry.path)
		x.sidebarFocused = false
		return x, nil
	case tea.KeyRunes:
		if k.Alt {
			return x, nil
		}
		x.fileBrowserFilter += k.String()
		x.rebuildFileBrowserFiltered()
		x.fileBrowserIndex = 0
		x.fileBrowserScroll = 0
		x.invalidate()
		return x, nil
	}
	// swallow all other keys (ctrl, alt combos, etc.)
	return x, nil
}

func (x m) handleEmojiPicker(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := x.emojiVisibleRows()
	switch k.Type {
	case tea.KeyEsc:
		x.closeEmojiPicker()
		x.reactPickMode = false
		x.reactPickMsgID = ""
	case tea.KeyEnter:
		if x.reactPickMode {
			results := x.emojiResults()
			if len(results) == 0 {
				return x, x.setTopBar("No emoji selected")
			}
			emoji := results[x.emojiSel].Char
			chatID := x.reactPickChatID
			msgID := x.reactPickMsgID
			sender := x.reactPickSender
			x.closeEmojiPicker()
			x.reactPickMode = false
			x.reactPickMsgID = ""
			x.reactPickChatID = ""
			x.reactPickSender = ""
			// Remove any existing reaction from me on the same target before adding new one
			if msgs, ok := x.msgs[chatID]; ok {
				filtered := msgs[:0]
				for _, m := range msgs {
					if m.Key.FromMe {
						if rxn, ok := m.Message["reactionMessage"].(map[string]any); ok {
							if tid, _ := rxn["targetMsgID"].(string); tid == msgID {
								continue
							}
						}
					}
					filtered = append(filtered, m)
				}
				x.msgs[chatID] = filtered
			}
			// Optimistic local reaction message
			var rxnMsg wireMsg
			rxnMsg.Key.ID = fmt.Sprintf("local-rxn-%d", time.Now().UnixNano())
			rxnMsg.Key.RemoteJID = chatID
			rxnMsg.Key.FromMe = true
			rxnMsg.Message = map[string]any{
				"reactionMessage": map[string]any{
					"targetMsgID": msgID,
					"emoji":       emoji,
				},
			}
			rxnMsg.MessageTimestamp = time.Now().Unix()
			x.msgs[chatID] = append(x.msgs[chatID], rxnMsg)
			x.invalidate()
			return x, tea.Batch(
				x.setTopBar("Reacted "+emoji),
				postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/react", map[string]string{
					"chatId":    chatID,
					"messageId": msgID,
					"sender":    sender,
					"reaction":  emoji,
				}, nil),
			)
		}
		if !x.insertSelectedEmoji() {
			return x, x.setTopBar("No emoji selected")
		}
	case tea.KeyUp:
		x.emojiSel--
		x.ensureEmojiVisible(rows)
	case tea.KeyDown:
		x.emojiSel++
		x.ensureEmojiVisible(rows)
	case tea.KeyPgUp:
		x.emojiSel -= rows
		x.ensureEmojiVisible(rows)
	case tea.KeyPgDown:
		x.emojiSel += rows
		x.ensureEmojiVisible(rows)
	case tea.KeyBackspace:
		if x.emojiQuery != "" {
			x.emojiQuery = graphemeDeleteLast(x.emojiQuery)
			x.emojiResultsDirty = true
			x.emojiSel = 0
			x.emojiScroll = 0
			x.ensureEmojiVisible(rows)
		}
	default:
		if len(k.Runes) > 0 {
			x.emojiQuery += string(k.Runes)
			x.emojiResultsDirty = true
			x.emojiSel = 0
			x.emojiScroll = 0
			x.ensureEmojiVisible(rows)
		}
	}
	return x, nil
}

func (x m) handleLeftInput(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		x.leftInput = ""
		x.leftInputFocused = false
	case tea.KeyBackspace:
		if x.leftInput != "/" && x.leftInput != "" {
			x.leftInput = graphemeDeleteLast(x.leftInput)
		}
	case tea.KeyEnter:
		txt := strings.TrimSpace(x.leftInput)
		x.leftInput = ""
		x.leftInputFocused = false
		if txt == "" {
			break
		}
		if cmd, handled := x.handleGlobalCommand(txt); handled {
			return x, cmd
		}
		return x, x.setTopBar("unknown command: " + txt)
	case tea.KeyTab:
		if best := commandBestMatch(x.leftInput); best != "" {
			x.leftInput = best
		} else {
			x.leftInputFocused = false
		}
	default:
		if len(k.Runes) > 0 {
			x.leftInput += string(k.Runes)
		}
	}
	return x, nil
}
