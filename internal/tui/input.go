package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func mouseModeCmd(enabled bool) tea.Cmd {
	if enabled {
		return func() tea.Msg { return tea.EnableMouseCellMotion() }
	}
	return func() tea.Msg { return tea.DisableMouse() }
}

func (x m) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		x.leftInputFocused = false
		if x.mode == "chat" || x.mode == "search" {
			x.mode = "nav"
		}
		x.search = ""
		x.searchInput = ""
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
		x.search = ""
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
			return x, tea.Batch(x.setTopBar("Mouse on"), mouseModeCmd(true))
		}
		return x, tea.Batch(x.setTopBar("Mouse off - zoom restored"), mouseModeCmd(false))
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
		switch k.String() {
		case "q", "Q":
			return x, tea.Quit
		case "r", "R":
			// Expired QR while waiting for a scan: re-open the socket and
			// ask the backend for a fresh QR handshake (/start is a no-op
			// if a connect is already in flight). A new "qr" event resets
			// qrReceivedAt and the panel flips back to the countdown.
			if x.status == "qr" && x.qrExpired() {
				x.status = "Connecting..."
				x.sessionReady = false
				x.invalidate()
				return x, tea.Batch(
					openWS(x.reqCtx(), x.wsURL, x.apiToken),
					postEmpty(x.reqCtx(), x.client, x.baseURL+"/start", nil),
				)
			}
			x.status = "Connecting..."
			x.sessionReady = false
			x.invalidate()
			return x, openWS(x.reqCtx(), x.wsURL, x.apiToken)
		case "enter":
			if x.sessionReady && !strings.HasPrefix(x.status, "Error:") && !strings.HasPrefix(strings.ToLower(x.status), "logged out") {
				x.status = "ready"
				x.invalidate()
				return x, x.refreshWindowTitleCmd()
			}
		default:
			if x.sessionReady && !strings.HasPrefix(x.status, "Error:") && !strings.HasPrefix(strings.ToLower(x.status), "logged out") {
				x.status = "ready"
				x.invalidate()
				return x, x.refreshWindowTitleCmd()
			}
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
				if x.settingsPicker.idx >= 0 && x.settingsPicker.idx < len(settingsDefs) && settingsDefs[x.settingsPicker.idx].name == "Mouse support" {
					x.mouseEnabled = currentConfig.MouseEnabled
					cmd = mouseModeCmd(x.mouseEnabled)
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
				case "Media icon style":
					x.mediaIconPicker = picker{title: "Media Icon Style", items: buildMediaIconPickerItems()}
					x.mediaIconPicker.Open(currentConfig.MediaIconStyle)
				case "Media preview":
					x.mediaViewPicker = picker{title: "Media Preview", items: buildMediaViewPickerItems()}
					x.mediaViewPicker.Open(currentConfig.MediaViewStyle)
				case "Chat list icons":
					x.userlistIconPicker = picker{title: "Chat List Icons", items: buildUserlistIconPickerItems()}
					style := currentConfig.UserlistIconStyle
					if style == "" {
						style = "numbers"
					}
					x.userlistIconPicker.Open(style)
				case "Startup speed":
					x.splashSpeedPicker = picker{title: "Startup Speed", items: buildSplashSpeedPickerItems()}
					speed := currentConfig.SplashStageSpeed
					if speed == "" {
						speed = "normal"
					}
					x.splashSpeedPicker.Open(speed)
				default:
					x.typingAnimationPicker = picker{title: "Typing Style", items: buildTypingAnimationPickerItems()}
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
	if x.splashSpeedPicker.open {
		return x.handleSettingsSubPicker(&x.splashSpeedPicker, &currentConfig.SplashStageSpeed, k)
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
		return x.handleNavKey(k)
	case "search":
		return x.handleSearchKey(k)
	case "msgsearch":
		return x.handleMsgSearchKey(k)
	case "chat":
		return x.handleChatKey(k)
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
