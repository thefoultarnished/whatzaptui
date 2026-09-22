package main

import (
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (x m) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	resModel, cmd := x.updateInner(msg)
	resM, ok := resModel.(m)
	if !ok {
		return resModel, cmd
	}
	var bgCmd tea.Cmd
	if currentConfig.MediaViewStyle == "pixel" && resM.active != "" {
		bgCmd = resM.triggerBackgroundDownloads()
	}
	return resM, tea.Batch(cmd, bgCmd)
}

func (x m) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		x.w, x.h = v.Width, v.Height
	case initMsg:
		if v.err != nil {
			x.err = ""
			x.status = "Error: " + v.err.Error()
			return x, nil
		}
		if v.demo {
			x.loadDemoState()
			return x, x.setTopBar("Demo mode: fake chats loaded")
		}
		x.startedBackend, x.backend = v.started, v.cmd
		x.status = "Connecting..."
		return x, tea.Batch(
			openWS(x.wsURL, x.apiToken),
			postEmpty(x.client, x.baseURL+"/start", nil),
			registerSession(x.client, x.baseURL, x.apiToken),
		)
	case wsOpenMsg:
		if v.err != nil {
			x.wsDisconnected = true
			if x.wsReconnectDelay == 0 {
				x.wsReconnectDelay = time.Second
			}
			delay := x.wsReconnectDelay
			if x.wsReconnectDelay*2 < 30*time.Second {
				x.wsReconnectDelay *= 2
			} else {
				x.wsReconnectDelay = 30 * time.Second
			}
			x.err = ""
			if x.status == "ready" {
				return x, x.setTopBar(fmt.Sprintf("Reconnecting in %s…", delay.Round(time.Second)))
			}
			x.status = "Reconnecting…"
			return x, tea.Tick(delay, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
		// Successful (re)connect — reset backoff and clear disconnect state.
		x.wsDisconnected = false
		x.wsReconnectDelay = 0
		if x.ws != nil {
			_ = x.ws.Close()
		}
		x.ws, x.wsCh = v.conn, v.ch
		return x, tea.Batch(readWS(x.wsCh), x.prefetchOnWSOpen())
	case reconnectMsg:
		return x, openWS(x.wsURL, x.apiToken)
	case wsEvtMsg:
		if !v.ok {
			x.wsDisconnected = true
			if x.wsReconnectDelay == 0 {
				x.wsReconnectDelay = time.Second
			}
			delay := x.wsReconnectDelay
			if x.wsReconnectDelay*2 < 30*time.Second {
				x.wsReconnectDelay *= 2
			} else {
				x.wsReconnectDelay = 30 * time.Second
			}
			if x.status == "ready" {
				return x, tea.Batch(
					x.setTopBar(fmt.Sprintf("Disconnected — reconnecting in %s…", delay.Round(time.Second))),
					tea.Tick(delay, func(time.Time) tea.Msg { return reconnectMsg{} }),
				)
			}
			return x, tea.Tick(delay, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
		cmds := []tea.Cmd{readWS(x.wsCh)}
		switch v.evt.Type {
		case "qr":
			var qr string
			if err := json.Unmarshal(v.evt.Payload, &qr); err != nil {
				log.Printf("ws qr unmarshal: %v", err)
			}
			x.status = "qr"
			x.qrRaw = qr
		case "ready":
			x.status = "ready"
			x.qrRaw = ""
			cmds = append(cmds, getChats(x.client, x.baseURL), getContacts(x.client, x.baseURL), getWhitelist(x.client, x.baseURL))
			if x.active != "" && !x.demoMode {
				cmds = append(cmds, postJSON(x.client, x.baseURL+"/messages/read", map[string]string{"chatId": x.active}, func([]byte) tea.Msg { return dataErr{} }))
			}
		case "chats:loaded":
			cmds = append(cmds, getChats(x.client, x.baseURL))
			if x.active != "" && len(x.msgs[x.active]) == 0 {
				cmds = append(cmds, getMsgs(x.client, x.baseURL, x.active, 120))
			}
		case "contacts:updated":
			cmds = append(cmds, getContacts(x.client, x.baseURL))
		case "status":
			var st string
			if err := json.Unmarshal(v.evt.Payload, &st); err != nil {
				log.Printf("ws status unmarshal: %v", err)
				break
			}
			st = strings.TrimSpace(st)
			if st == "" {
				break
			}
			if strings.HasPrefix(st, "connect-failed:") {
				msg := strings.TrimSpace(strings.TrimPrefix(st, "connect-failed:"))
				if msg == "" {
					msg = "connect failed"
				}
				x.status = "Error: WhatsApp connect failed: " + msg
				break
			}
			if x.status == "ready" {
				cmds = append(cmds, x.setTopBar(st))
				break
			}
			switch st {
			case "connecting":
				x.status = "Connecting..."
			case "waiting-qr":
				x.status = "Connecting..."
			case "disconnected":
				x.status = "Reconnecting…"
			case "logged-out":
				x.status = "Logged out. Start again to scan QR."
			default:
				// Backend detail strings ("QR timed out, ...",
				// "Logged out. ..."): surface logged-out, show the
				// rest as-is unless we're already past loading.
				if strings.HasPrefix(strings.ToLower(st), "logged out") {
					x.status = st
				} else {
					cmds = append(cmds, x.setTopBar(st))
				}
			}
		case "disconnected":
			if x.status == "ready" {
				cmds = append(cmds, x.setTopBar("Disconnected — waiting for backend…"))
			} else {
				x.status = "Reconnecting…"
			}
		case "message":
			var wm wireMsg
			if err := json.Unmarshal(v.evt.Payload, &wm); err == nil {
				notify := false
				notifyTitle := ""
				notifyBody := ""
				activeViewing := x.mode == "chat" && x.active == wm.Key.RemoteJID
				exists := false
				// For our own outgoing messages, replace a local-* placeholder
				// in-place to avoid the optimistic/WS race creating a duplicate.
				if wm.Key.FromMe && wm.Key.ID != "" {
					msgs := x.msgs[wm.Key.RemoteJID]
					for i, existing := range msgs {
						if strings.HasPrefix(existing.Key.ID, "local-") &&
							existing.MessageTimestamp > 0 && wm.MessageTimestamp > 0 &&
							wm.MessageTimestamp-existing.MessageTimestamp <= 10 &&
							existing.MessageTimestamp-wm.MessageTimestamp <= 10 {
							msgs[i] = wm
							x.msgs[wm.Key.RemoteJID] = msgs
							exists = true
							break
						}
					}
				}
				if !exists {
					for _, existing := range x.msgs[wm.Key.RemoteJID] {
						if wm.Key.ID != "" && existing.Key.ID == wm.Key.ID {
							exists = true
							break
						}
					}
				}
				if !exists {
					x.msgs[wm.Key.RemoteJID] = append(x.msgs[wm.Key.RemoteJID], wm)
					sort.Slice(x.msgs[wm.Key.RemoteJID], func(i, j int) bool {
						return x.msgs[wm.Key.RemoteJID][i].MessageTimestamp < x.msgs[wm.Key.RemoteJID][j].MessageTimestamp
					})
				}
				x.mainCache.result = ""
				if !wm.Key.FromMe && wm.Key.ID != "" {
					if !activeViewing {
						for i := range x.chats {
							if x.chats[i].ID == wm.Key.RemoteJID {
								x.chats[i].UnreadCount++
								break
							}
						}
					} else if !x.demoMode {
						cmds = append(cmds, postJSON(x.client, x.baseURL+"/messages/read", map[string]string{"chatId": wm.Key.RemoteJID}, func([]byte) tea.Msg { return dataErr{} }))
					}
					x.flashUntil[wm.Key.ID] = time.Now().Add(5 * time.Second)
					x.msgActivityUntil = time.Now().Add(3 * time.Second)
					x.msgActivityType = "received"
					if x.shouldNotifyIncoming(wm) && currentConfig.NotificationsEnabled {
						notify = true
						notifyTitle = "New message from " + x.nameFor(wm.Key.RemoteJID)
						notifyBody = messagePreviewForNotification(wm)
					}
				}
				if notify {
					soundCmd := tea.Cmd(nil)
					if x.soundEnabled {
						soundCmd = playSoundProfileCmd(x.soundProfile)
					}
					notifyCmds := []tea.Cmd{
						x.setTopBar(notifyTitle + ": " + notifyBody),
						soundCmd,
					}
					if currentConfig.FlashTaskbar {
						notifyCmds = append(notifyCmds, flashTaskbarCmd())
					}
					cmds = append(cmds, tea.Batch(notifyCmds...))
				}
				if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
					cmds = append(cmds, titleCmd)
				}
			} else {
				log.Printf("ws message unmarshal: %v", err)
			}
		case "message:edited":
			var wm wireMsg
			if err := json.Unmarshal(v.evt.Payload, &wm); err == nil {
				msgs := x.msgs[wm.Key.RemoteJID]
				for i := range msgs {
					if msgs[i].Key.ID == wm.Key.ID && msgs[i].Key.FromMe == wm.Key.FromMe {
						msgs[i].Message = wm.Message
						break
					}
				}
				x.msgs[wm.Key.RemoteJID] = msgs
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
			} else {
				log.Printf("ws message:edited unmarshal: %v", err)
			}
		case "receipt":
			var rm receiptMsg
			if err := json.Unmarshal(v.evt.Payload, &rm); err == nil {
				if msgs, ok := x.msgs[rm.ChatID]; ok {
					updated := false
					for i := range msgs {
						if !msgs[i].Key.FromMe {
							continue
						}
						for _, id := range rm.MessageIDs {
							if msgs[i].Key.ID == id {
								msgs[i].ReceiptStatus = rm.ReceiptStatus
								updated = true
								break
							}
						}
					}
					if updated {
						x.msgs[rm.ChatID] = msgs
						x.mainCache.result = ""
					}
				}
			} else {
				log.Printf("ws receipt unmarshal: %v", err)
			}
		case "typing":
			var tm struct {
				ChatID string `json:"chatId"`
				Sender string `json:"sender"`
				State  string `json:"state"`
			}
			if err := json.Unmarshal(v.evt.Payload, &tm); err == nil {
				if x.typingChats == nil {
					x.typingChats = map[string]time.Time{}
				}
				if tm.State == "composing" {
					x.typingChats[tm.ChatID] = time.Now()
				} else {
					delete(x.typingChats, tm.ChatID)
				}
				x.mainCache.result = ""
				if x.sidebarCache != nil {
					x.sidebarCache.contactsValid = false
				}
			} else {
				log.Printf("ws typing unmarshal: %v", err)
			}
		case "call":
			var cm callMsg
			if err := json.Unmarshal(v.evt.Payload, &cm); err == nil {
				banner := x.callBanner(cm)
				callCmds := []tea.Cmd{x.setTopBar(banner)}
				if currentConfig.FlashTaskbar {
					callCmds = append(callCmds, flashTaskbarCmd())
				}
				cmds = append(cmds, tea.Batch(callCmds...))
			} else {
				log.Printf("ws call unmarshal: %v", err)
			}
		}
		return x, tea.Batch(cmds...)
	case dataErr:
		x.syncingContacts = false
		x.syncingGroups = false
		if v.err != nil {
			x.err = ""
			if x.status == "ready" {
				return x, x.setTopBar(v.err.Error())
			}
			if strings.Contains(v.err.Error(), "not connected") {
				return x, nil
			}
			x.status = "Error: " + v.err.Error()
		}
	case topBarClearMsg:
		if v.ver == x.topBarVer {
			x.topBarMsg = ""
			x.topBarShown = 0
		}
	case topBarTypeMsg:
		if v.ver == x.topBarVer {
			n := graphemeCount(x.topBarMsg)
			if x.topBarShown < n {
				x.topBarShown++
				return x, nextTopBarTypeTick(v.ver)
			}
		}
	case topBarSetMsg:
		return x, x.setTopBar(v.msg)
	case syncContactsDoneMsg:
		x.syncingContacts = false
		return x, tea.Batch(x.setTopBar(v.msg), getChats(x.client, x.baseURL), getContacts(x.client, x.baseURL))
	case syncGroupsDoneMsg:
		x.syncingGroups = false
		return x, tea.Batch(x.setTopBar(v.msg), getChats(x.client, x.baseURL))
	case cursorBlinkMsg:
		if time.Since(x.lastTypeTime) < 1*time.Second {
			x.cursorOn = true
		} else {
			x.cursorOn = !x.cursorOn
		}
		x.pulseOn = !x.pulseOn
		now := time.Now()
		for id, until := range x.flashUntil {
			if !until.After(now) {
				delete(x.flashUntil, id)
			}
		}
		anyDeleted := false
		for chatID, since := range x.typingChats {
			if now.Sub(since) > 15*time.Second {
				delete(x.typingChats, chatID)
				anyDeleted = true
			}
		}
		if anyDeleted && x.sidebarCache != nil {
			x.sidebarCache.contactsValid = false
		}
		return x, nextCursorBlink()
	case spinnerTickMsg:
		x.spinnerFrame = (x.spinnerFrame + 1) % len(spinnerFrames)
		x.shineFrame++
		x.advanceSidebarMarquee()
		return x, nextSpinnerTick()
	case chatsMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		selectedID := x.selectedChatID()
		x.chats = v.chats
		x.resortChats(selectedID)
		x.ensureSideVisible(x.sideViewRows())
		cmds := []tea.Cmd{}
		// First paint: open the most recent chat so the pane isn't empty.
		// active is only "" on a fresh session (logout clears chats too),
		// and People-tab selection must not be hijacked.
		if x.active == "" && x.sidebarTab == "chats" && len(x.chats) > 0 {
			x.sel = 0
			mdl, openCmd := x.openSelectedChat()
			if xm, ok := mdl.(m); ok {
				x = xm
			}
			if openCmd != nil {
				cmds = append(cmds, openCmd)
			}
		}
		if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
			cmds = append(cmds, titleCmd)
		}
		if len(cmds) > 0 {
			return x, tea.Batch(cmds...)
		}
	case contactsMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		x.contacts = map[string]contact{}
		for _, c := range v.contacts {
			x.contacts[c.ID] = c
		}
		x.rebuildContactIndex()
		x.markIdentityChanged()
	case groupPreviewMsg:
		if v.err == nil {
			x.groupPreviews[v.jid] = v.preview
			x.markIdentityChanged()
		}
	case msgsMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		x.msgs[v.chatID] = v.msgs
		if x.loadingOlder == nil {
			x.loadingOlder = map[string]bool{}
		}
		if x.noMoreOlder == nil {
			x.noMoreOlder = map[string]bool{}
		}
		delete(x.loadingOlder, v.chatID)
		if v.hasMore {
			delete(x.noMoreOlder, v.chatID)
		} else {
			x.noMoreOlder[v.chatID] = true
		}
	case olderMsgsMsg:
		if x.loadingOlder == nil {
			x.loadingOlder = map[string]bool{}
		}
		if x.noMoreOlder == nil {
			x.noMoreOlder = map[string]bool{}
		}
		delete(x.loadingOlder, v.chatID)
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		if !v.hasMore {
			x.noMoreOlder[v.chatID] = true
		}
		if len(v.msgs) == 0 {
			return x, nil
		}
		// Prepend, dedupe by message key. Existing list is chronological (oldest first).
		existing := x.msgs[v.chatID]
		seen := make(map[string]struct{}, len(existing))
		for _, m := range existing {
			seen[m.Key.ID] = struct{}{}
		}
		fresh := make([]wireMsg, 0, len(v.msgs))
		for _, m := range v.msgs {
			if _, ok := seen[m.Key.ID]; ok {
				continue
			}
			fresh = append(fresh, m)
		}
		if len(fresh) == 0 {
			return x, nil
		}
		merged := make([]wireMsg, 0, len(fresh)+len(existing))
		merged = append(merged, fresh...)
		merged = append(merged, existing...)
		x.msgs[v.chatID] = merged
		if x.mainCache != nil {
			x.mainCache.result = ""
		}
	case aroundMsgsMsg:
		if v.err != nil {
			return x, x.setTopBar(v.err.Error())
		}
		if len(v.msgs) == 0 {
			return x, nil
		}
		x.msgs[v.chatID] = v.msgs
		if x.loadingOlder == nil {
			x.loadingOlder = map[string]bool{}
		}
		if x.noMoreOlder == nil {
			x.noMoreOlder = map[string]bool{}
		}
		delete(x.loadingOlder, v.chatID)
		// We don't know if there are more older pages without another query;
		// allow lazy-load to discover by clearing noMoreOlder.
		delete(x.noMoreOlder, v.chatID)
		// Set scroll so anchor is roughly centred — messages after anchor are
		// newer (lower index from bottom), each ~2 rows on average.
		newerCount := len(v.msgs) - v.anchorIndex - 1
		if newerCount < 0 {
			newerCount = 0
		}
		x.scroll = newerCount * 2
		if x.mainCache != nil {
			x.mainCache.result = ""
		}
	case searchResultsMsg:
		x.msgSearchLoading = false
		if v.err != nil {
			x.msgSearchErr = v.err.Error()
			x.msgSearchResults = nil
			x.msgSearchSel = 0
			return x, nil
		}
		x.msgSearchErr = ""
		x.msgSearchResults = v.results
		x.msgSearchSel = 0
		if x.mainCache != nil {
			x.mainCache.result = ""
		}
	case fileProgressMsg:
		if x.uploadProgress == nil {
			x.uploadProgress = map[string]int{}
		}
		x.uploadProgress[v.pendingID] = v.pct
		if x.uploadChans == nil {
			x.uploadChans = map[string]chan fileProgressMsg{}
		}
		ch, ok := x.uploadChans[v.pendingID]
		if !ok {
			return x, nil
		}
		return x, listenFileProgress(ch)
	case sentMsg:
		if v.pendingID != "" {
			delete(x.uploadProgress, v.pendingID)
			delete(x.uploadChans, v.pendingID)
		}
		if v.err != nil {
			if v.pendingID != "" {
				if msgs, ok := x.msgs[v.chatID]; ok {
					filtered := msgs[:0]
					for _, msg := range msgs {
						if msg.Key.ID == v.pendingID {
							continue
						}
						filtered = append(filtered, msg)
					}
					x.msgs[v.chatID] = filtered
				}
			}
			x.mainCache.result = ""
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		replaced := false
		if v.pendingID != "" {
			if msgs, ok := x.msgs[v.chatID]; ok {
				for i := range msgs {
					if msgs[i].Key.ID == v.pendingID {
						msgs[i] = v.msg
						replaced = true
						break
					}
				}
				x.msgs[v.chatID] = msgs
			}
		}
		if !replaced {
			// WS may have already claimed the placeholder and delivered the real
			// message. If the real ID is already present, just drop the stale
			// local placeholder instead of appending a duplicate.
			alreadyPresent := false
			if v.msg.Key.ID != "" {
				for _, existing := range x.msgs[v.chatID] {
					if existing.Key.ID == v.msg.Key.ID {
						alreadyPresent = true
						break
					}
				}
			}
			if alreadyPresent {
				if v.pendingID != "" {
					msgs := x.msgs[v.chatID]
					out := msgs[:0]
					for _, m := range msgs {
						if m.Key.ID != v.pendingID {
							out = append(out, m)
						}
					}
					x.msgs[v.chatID] = out
				}
			} else {
				x.msgs[v.chatID] = append(x.msgs[v.chatID], v.msg)
			}
		}
		x.mainCache.result = ""
		x.scroll = 0
		x.msgActivityUntil = time.Now().Add(3 * time.Second)
		x.msgActivityType = "sent"
	case whitelistLoadMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		x.whitelist = v.whitelist
		x.names = v.names
		x.markIdentityChanged()
	case whitelistSetMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
	case blockMsg:
		if v.err != nil {
			return x, x.setTopBar("Block failed: " + v.err.Error())
		}
		return x, x.setTopBar("Contact blocked successfully")
	case logoutMsg:
		if v.err != nil {
			x.err = v.err.Error()
			return x, x.setTopBar("Logout failed: " + v.err.Error())
		}
		x.active = ""
		x.chats = nil
		x.msgs = map[string][]wireMsg{}
		x.contacts = map[string]contact{}
		x.contactsByNumber = map[string]contact{}
		x.whitelist = map[string]string{}
		x.names = map[string]string{}
		x.drafts = map[string]string{}
		x.replyTo = nil
		x.selectedMsgID = ""
		x.status = v.msg
		x.err = ""
		x.mainCache.result = ""
		return x, tea.Batch(setTerminalTitleCmd("WhatZap"), x.setTopBar(v.msg), tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return tea.QuitMsg{} }))
	case tea.MouseMsg:
		if v.Action == tea.MouseActionPress && v.Button == tea.MouseButtonLeft && x.mode == "chat" && x.active != "" {
			sidePad := max(2, x.w/20)
			contentW := x.w - sidePad*2
			leftW := min(28, max(24, contentW/3))
			msgPaneX := sidePad + 1 + leftW + 1
			msgPaneTopY := 3
			msgPaneH := x.h - 6
			if v.X >= msgPaneX && v.Y >= msgPaneTopY && v.Y < msgPaneTopY+msgPaneH {
				lineIdx := v.Y - msgPaneTopY
				rightW := contentW - leftW
				if id := x.msgIDAtLine(lineIdx, rightW, msgPaneH); id != "" {
					isDouble := v.Y == x.lastClickY && time.Since(x.lastClickTime) < 350*time.Millisecond
					x.lastClickY = v.Y
					x.lastClickTime = time.Now()
					if isDouble {
						for i := range x.msgs[x.active] {
							if x.msgs[x.active][i].Key.ID == id {
								cp := x.msgs[x.active][i]
								x.replyTo = &cp
								x.selectedMsgID = ""
								return x, nil
							}
						}
					} else if x.selectedMsgID == id {
						x.selectedMsgID = ""
					} else {
						x.selectedMsgID = id
					}
				}
			}
		}
	case clipboardPasteMsg:
		if v.err != nil {
			return x, x.setTopBar("Clipboard: " + v.err.Error())
		}
		x.setPendingAttachment(v.path)
		x.input = ""
		x.inputBuf = ""
		x.sidebarFocused = false
		label := filepath.Base(v.path)
		if v.isImage {
			label = "screenshot → " + label
		}
		return x, x.setTopBar("Attached: " + label)
	case mediaDownloadMsg:
		if v.err != nil {
			if x.downloadingMedia != nil {
				delete(x.downloadingMedia, v.msgID)
			}
			return x, x.setTopBar(v.err.Error())
		}
		if x.downloadingMedia != nil {
			delete(x.downloadingMedia, v.msgID)
		}
		if v.isPreview {
			if x.downloadedMedia == nil {
				x.downloadedMedia = make(map[string]string)
			}
			x.downloadedMedia[v.msgID] = v.path
			x.mainCache.result = ""
			return x, nil
		}
		return x, openFile(v.path)
	case fileOpenMsg:
		if v.err != nil {
			return x, x.setTopBar("Open failed: " + v.err.Error())
		}
		return x, x.setTopBar("Opened: " + filepath.Base(v.path))
	case flushInputMsg:
		x.input += x.inputBuf
		x.inputBuf = ""
		x.inputFlushScheduled = false
		return x, nil
	case composerSendMsg:
		if !x.pendingSendArmed || v.seq != x.pendingSendSeq {
			return x, nil
		}
		x.pendingSendArmed = false
		if x.sidebarFocused || x.chatInputLocked() {
			return x, nil
		}
		txt := sanitizeOutgoingText(x.input)
		x.input = ""
		if x.active == "" {
			return x, nil
		}
		if x.editingMsgID != "" {
			msgID := x.editingMsgID
			x.editingMsgID = ""
			if !hasVisibleText(txt) {
				return x, x.setTopBar("Edit cancelled: text cannot be empty")
			}
			chatID := x.active
			msgs := x.msgs[chatID]
			for i := range msgs {
				if msgs[i].Key.ID == msgID && msgs[i].Key.FromMe {
					if ext, ok := msgs[i].Message["extendedTextMessage"].(map[string]any); ok {
						ext["text"] = txt
						msgs[i].Message["extendedTextMessage"] = ext
					} else {
						msgs[i].Message["conversation"] = txt
					}
					msgs[i].Message["edited"] = true
					break
				}
			}
			x.msgs[chatID] = msgs
			if x.mainCache != nil {
				x.mainCache.result = ""
			}
			return x, postJSON(x.client, x.baseURL+"/messages/edit", map[string]string{"chatId": chatID, "messageId": msgID, "text": txt}, nil)
		}
		if x.pendingAttachmentPath != "" {
			if _, ok := x.whitelist[num(x.active)]; !ok {
				return x, x.setTopBar("Not whitelisted - use /whitelist to enable")
			}
			if x.demoMode {
				x.clearPendingAttachment()
				return x, x.setTopBar("Demo mode: media send disabled")
			}
			kind := x.pendingAttachmentKind
			if kind == "" {
				var err error
				kind, err = detectMediaSendKind(x.pendingAttachmentPath)
				if err != nil {
					return x, x.setTopBar(err.Error())
				}
			}
			path := x.pendingAttachmentPath
			fileName := filepath.Base(path)
			pendingID := fmt.Sprintf("local-%d", time.Now().UnixNano())
			x.msgs[x.active] = append(x.msgs[x.active],
				optimisticOutgoingMediaMessage(x.active, kind, fileName, txt, pendingID))
			if x.mainCache != nil {
				x.mainCache.result = ""
			}
			now := time.Now()
			selectedID := x.selectedChatID()
			for i := range x.chats {
				if x.chats[i].ID == x.active {
					x.chats[i].ConversationTimestamp = now.Unix()
					break
				}
			}
			x.resortChats(selectedID)
			x.scroll = 0
			x.msgActivityUntil = time.Now().Add(3 * time.Second)
			x.msgActivityType = "sent"
			x.clearPendingAttachment()
			x.replyTo = nil
			progressCh := make(chan fileProgressMsg, 16)
			if x.uploadChans == nil {
				x.uploadChans = map[string]chan fileProgressMsg{}
			}
			x.uploadChans[pendingID] = progressCh
			return x, tea.Batch(
				sendFile(x.client, x.baseURL, x.active, kind, path, txt, pendingID, progressCh),
				listenFileProgress(progressCh),
			)
		}
		if !hasVisibleText(txt) {
			return x, nil
		}
		if cmd, handled := x.handleSlash(strings.TrimSpace(txt)); handled {
			return x, cmd
		}
		if _, ok := x.whitelist[num(x.active)]; !ok {
			return x, x.setTopBar("Not whitelisted - use /whitelist to enable")
		}
		replyTo := x.replyTo
		x.replyTo = nil
		if x.demoMode {
			return x, demoSend(x.active, txt, replyTo)
		}
		pendingID := fmt.Sprintf("local-%d", time.Now().UnixNano())
		x.msgs[x.active] = append(x.msgs[x.active], optimisticOutgoingMessage(x.active, txt, pendingID, replyTo))
		if x.mainCache != nil {
			x.mainCache.result = ""
		}
		now := time.Now()
		selectedID := x.selectedChatID()
		for i := range x.chats {
			if x.chats[i].ID == x.active {
				x.chats[i].ConversationTimestamp = now.Unix()
				break
			}
		}
		x.resortChats(selectedID)
		x.scroll = 0
		x.msgActivityUntil = time.Now().Add(3 * time.Second)
		x.msgActivityType = "sent"
		sendCmd := send(x.client, x.baseURL, x.active, txt, replyTo, pendingID)
		if x.lastComposingChat != "" && !x.demoMode {
			pauseCmd := postJSON(x.client, x.baseURL+"/typing", map[string]string{"chatId": x.lastComposingChat, "state": "paused"}, nil)
			x.lastComposingChat = ""
			return x, tea.Batch(sendCmd, pauseCmd)
		}
		x.lastComposingChat = ""
		return x, sendCmd
	case tea.KeyMsg:
		x.lastTypeTime = time.Now()
		mdl, cmd := x.key(v)
		// Send composing indicator when typing in chat mode
		if x.mode == "chat" && x.active != "" && !x.demoMode && x.lastComposingChat != x.active && currentConfig.SendTypingIndicator {
			inp := x.input + x.inputBuf
			if inp != "" {
				x.lastComposingChat = x.active
				cmd = tea.Batch(cmd, postJSON(x.client, x.baseURL+"/typing", map[string]string{"chatId": x.active, "state": "composing"}, nil))
			}
		}
		return mdl, cmd
	}
	return x, nil
}

// prefetchOnWSOpen loads cached chats/contacts the moment the socket
// opens, so when `ready` arrives the sidebar can render instantly instead
// of waiting for more round trips. Safe pre-login: the backend serves
// whatever is cached (possibly empty) without requiring a session.
func (x m) prefetchOnWSOpen() tea.Cmd {
	return tea.Batch(getChats(x.client, x.baseURL), getContacts(x.client, x.baseURL))
}
