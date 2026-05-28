package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (x m) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		return x, tea.Batch(openWS(x.wsURL, x.apiToken), postEmpty(x.client, x.baseURL+"/start", nil))
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
		x.ws, x.wsCh = v.conn, v.ch
		return x, readWS(x.wsCh)
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
			_ = json.Unmarshal(v.evt.Payload, &qr)
			x.status = "qr"
			x.qrRaw = qr
		case "ready":
			x.status = "ready"
			x.qrRaw = ""
			cmds = append(cmds, getChats(x.client, x.baseURL), getContacts(x.client, x.baseURL), getWhitelist(x.client, x.baseURL))
		case "chats:loaded":
			cmds = append(cmds, getChats(x.client, x.baseURL))
			if x.active != "" {
				cmds = append(cmds, getMsgs(x.client, x.baseURL, x.active, 120))
			}
		case "contacts:updated":
			cmds = append(cmds, getContacts(x.client, x.baseURL))
		case "message":
			var wm wireMsg
			if err := json.Unmarshal(v.evt.Payload, &wm); err == nil {
				notify := false
				notifyTitle := ""
				notifyBody := ""
				activeViewing := x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused && x.active == wm.Key.RemoteJID
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
					if x.shouldNotifyIncoming(wm) {
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
					cmds = append(cmds, tea.Batch(
						x.setTopBar(notifyTitle+": "+notifyBody),
						soundCmd,
						flashTaskbarCmd(),
					))
				}
				if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
					cmds = append(cmds, titleCmd)
				}
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
			}
		case "call":
			var cm callMsg
			if err := json.Unmarshal(v.evt.Payload, &cm); err == nil {
				banner := x.callBanner(cm)
				cmds = append(cmds, tea.Batch(x.setTopBar(banner), flashTaskbarCmd()))
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
		for chatID, since := range x.typingChats {
			if now.Sub(since) > 15*time.Second {
				delete(x.typingChats, chatID)
			}
		}
		return x, nextCursorBlink()
	case spinnerTickMsg:
		x.spinnerFrame = (x.spinnerFrame + 1) % len(spinnerFrames)
		x.shineFrame++
		x.advanceSidebarHighlight()
		x.advanceSidebarMarquee()
		return x, nextSpinnerTick()
	case chatsMsg:
		if v.err != nil {
			x.err = ""
			return x, x.setTopBar(v.err.Error())
		}
		x.chats = v.chats
		sort.Slice(x.chats, func(i, j int) bool { return x.chats[i].ConversationTimestamp > x.chats[j].ConversationTimestamp })
		x.ensureSideVisible(x.sideViewRows())
		cmds := []tea.Cmd{}
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
	case sentMsg:
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
	case mediaDownloadMsg:
		if v.err != nil {
			return x, x.setTopBar(v.err.Error())
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
			x.clearPendingAttachment()
			x.replyTo = nil
			return x, sendFile(x.client, x.baseURL, x.active, kind, path, txt, "")
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
		for i := range x.chats {
			if x.chats[i].ID == x.active {
				x.chats[i].ConversationTimestamp = now.Unix()
				break
			}
		}
		sort.Slice(x.chats, func(i, j int) bool { return x.chats[i].ConversationTimestamp > x.chats[j].ConversationTimestamp })
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
		if x.mode == "chat" && x.active != "" && !x.demoMode && x.lastComposingChat != x.active {
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

func deferComposerSend(seq int) tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(time.Time) tea.Msg {
		return composerSendMsg{seq: seq}
	})
}

func (x m) callBanner(cm callMsg) string {
	name := ""
	switch {
	case cm.GroupID != "":
		name = x.nameFor(cm.GroupID)
	case cm.CallerID != "":
		name = x.nameFor(cm.CallerID)
	}
	if strings.TrimSpace(name) == "" {
		if cm.GroupID != "" {
			name = num(cm.GroupID)
		} else if cm.CallerID != "" {
			name = num(cm.CallerID)
		} else {
			name = "unknown"
		}
	}
	callType := "call"
	if cm.Media != "" {
		callType = cm.Media + " call"
	}
	switch cm.Status {
	case "incoming":
		if cm.GroupID != "" {
			return "Incoming " + callType + " in " + name
		}
		return "Incoming " + callType + " from " + name
	case "ended":
		if cm.Reason != "" {
			return "Call ended: " + name + " (" + cm.Reason + ")"
		}
		return "Call ended: " + name
	default:
		return "Call update: " + name
	}
}

func messagePreviewForNotification(msg wireMsg) string {
	body := strings.TrimSpace(renderMessageBody(msg.Message))
	body = strings.Join(strings.Fields(body), " ")
	if body == "" {
		body = "(message)"
	}
	r := []rune(body)
	if len(r) > 90 {
		body = string(r[:90]) + "..."
	}
	return body
}

func setTerminalBg(color string) {
	if color == "" {
		fmt.Printf("\033]111\a") // reset to terminal default
	} else {
		fmt.Printf("\033]11;%s\a", color)
	}
}

func setTerminalBgCmd(color string) tea.Cmd {
	return func() tea.Msg {
		setTerminalBg(color)
		return nil
	}
}

func setTerminalTitleCmd(title string) tea.Cmd {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(title), "\x1b", ""), "\a", "")
	if clean == "" {
		clean = "WhatZap"
	}
	legacy := func() tea.Msg {
		fmt.Printf("\033]0;%s\a", clean)
		fmt.Printf("\033]2;%s\a", clean)
		return nil
	}
	return tea.Batch(tea.SetWindowTitle(clean), legacy)
}

func (x *m) refreshWindowTitleCmd() tea.Cmd {
	title := "WhatZap"
	names := x.unreadTitleNames(3)
	if len(names) > 0 {
		extra := x.unreadNamedChatCount() - len(names)
		if extra > 0 {
			title = fmt.Sprintf("WhatZap (🟢 %s +%d)", strings.Join(names, ","), extra)
		} else {
			title = fmt.Sprintf("WhatZap (🟢 %s)", strings.Join(names, ","))
		}
	}
	if x.windowTitle == title {
		return nil
	}
	x.windowTitle = title
	return setTerminalTitleCmd(title)
}

func (x *m) unreadNamedChatCount() int {
	count := 0
	for _, ch := range x.chats {
		if ch.UnreadCount > 0 {
			count++
		}
	}
	return count
}

func firstNameForTitle(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (x *m) unreadTitleNames(limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, ch := range x.chats {
		if ch.UnreadCount <= 0 {
			continue
		}
		n := firstNameForTitle(x.nameFor(ch.ID))
		if n == "" {
			n = firstNameForTitle(num(ch.ID))
		}
		if n == "" {
			continue
		}
		key := strings.ToLower(n)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, n)
		if len(out) == limit {
			break
		}
	}
	return out
}

func (x *m) shouldNotifyIncoming(wm wireMsg) bool {
	if wm.Key.FromMe || wm.Key.RemoteJID == "" {
		return false
	}
	// Keep alerts quiet while you're already looking at that chat.
	if x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused && x.active == wm.Key.RemoteJID {
		return false
	}
	now := time.Now()
	if !x.lastNotifyGlobal.IsZero() && now.Sub(x.lastNotifyGlobal) < 1200*time.Millisecond {
		return false
	}
	if last, ok := x.lastNotifyAt[wm.Key.RemoteJID]; ok && now.Sub(last) < 4*time.Second {
		return false
	}
	x.lastNotifyGlobal = now
	if x.lastNotifyAt == nil {
		x.lastNotifyAt = map[string]time.Time{}
	}
	x.lastNotifyAt[wm.Key.RemoteJID] = now
	return true
}

func preferredContact(a, b contact) contact {
	score := func(c contact) int {
		switch {
		case strings.TrimSpace(c.Notify) != "":
			return 2
		case strings.TrimSpace(c.Name) != "":
			return 1
		default:
			return 0
		}
	}
	if score(a) >= score(b) {
		return a
	}
	return b
}

func (x *m) rebuildContactIndex() {
	x.contactsByNumber = make(map[string]contact, len(x.contacts))
	for _, ct := range x.contacts {
		n := num(ct.ID)
		if n == "" {
			continue
		}
		if prev, ok := x.contactsByNumber[n]; ok {
			x.contactsByNumber[n] = preferredContact(ct, prev)
			continue
		}
		x.contactsByNumber[n] = ct
	}
}

func (x *m) invalidateSidebarContacts() {
	if x.sidebarCache == nil {
		x.sidebarCache = &sidebarCache{}
	}
	x.sidebarCache.contacts = nil
	x.sidebarCache.contactsValid = false
}

func (x *m) markIdentityChanged() {
	x.identityVersion++
	if x.mainCache != nil {
		x.mainCache.result = ""
	}
	x.invalidateSidebarContacts()
}

func (x m) activeChatWhitelisted() bool {
	if x.active == "" {
		return false
	}
	_, ok := x.whitelist[num(x.active)]
	return ok
}

func (x m) chatInputLocked() bool {
	return x.active != "" && !x.activeChatWhitelisted()
}

func (x m) replyPickCandidates() []wireMsg {
	msgs := x.msgs[x.active]
	out := make([]wireMsg, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := m.Message["reactionMessage"]; ok {
			continue
		}
		if pm, ok := m.Message["protocolMessage"]; ok {
			if pmm, ok := pm.(map[string]any); ok {
				if t, _ := pmm["type"].(string); t == "REVOKE" || t == "MESSAGE_EDIT" {
					continue
				}
			}
		}
		out = append(out, m)
	}
	return out
}

func msgRowHeight(msg wireMsg, w int) int {
	rows := 0
	// quote line
	if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
		if qt, _ := ext["quotedText"].(string); qt != "" {
			rows++
		}
	}
	// body lines
	body := renderMessageBody(msg.Message)
	wrapW := max(10, w-10)
	if body == "" {
		rows++
	} else {
		lines := strings.Split(wrapText(body, wrapW), "\n")
		rows += len(lines)
	}
	// reaction line
	if _, ok := msg.Message["reactionMessage"]; ok {
		rows++
	}
	return max(1, rows)
}

func optimisticOutgoingMessage(chatID, text, pendingID string, replyTo *wireMsg) wireMsg {
	msg := wireMsg{
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
	}
	msg.Key.ID = pendingID
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = true
	if replyTo != nil {
		participant := replyTo.Key.RemoteJID
		if replyTo.Key.Participant != "" {
			participant = replyTo.Key.Participant
		}
		quotedFromMe := replyTo.Key.FromMe
		if quotedFromMe {
			participant = ""
		}
		msg.Message = map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              text,
				"quotedText":        renderMessageBody(replyTo.Message),
				"quotedParticipant": participant,
				"quotedFromMe":      quotedFromMe,
			},
		}
		return msg
	}
	msg.Message = map[string]any{"conversation": text}
	return msg
}

func (x *m) clearChatComposer() {
	x.input = ""
	x.inputBuf = ""
	x.inputFlushScheduled = false
	x.inputAllSelected = false
	x.clearPendingAttachment()
	x.replyTo = nil
	x.replyPickMode = false
	x.replyPickIndex = 0
	x.selectedMsgID = ""
	x.closeEmojiPicker()
}

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
	switch k.String() {
	case "ctrl+c":
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
		x.mainCache.result = ""
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
		x.mainCache.result = ""
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
	case "alt+o":
		if x.mode == "chat" && x.active != "" {
			msgs := x.msgs[x.active]
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].MediaProto != "" {
					return x, tea.Batch(
						x.setTopBar("Opening media..."),
						downloadMedia(x.client, x.baseURL, x.active, msgs[i].Key.ID),
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
	case "alt+e":
		if x.status == "ready" && x.mode == "chat" && x.active != "" && !x.chatInputLocked() {
			x.openEmojiPicker()
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
			x.mainCache.result = ""
			return x, nil
		}
		return x, nil
	}
	if x.leftInputFocused {
		return x.handleLeftInput(k)
	}
	if x.status != "ready" {
		return x, nil
	}
	if x.themePicker.open {
		action, done := x.themePicker.Handle(k)
		if !done {
			applyThemeByName(x.themePicker.SelectedKey())
			x.mainCache.result = ""
		} else if action == "confirm" {
			applyThemeByName(x.themePicker.Close(true))
			saveConfig()
		} else {
			applyThemeByName(x.themePicker.Close(false))
			x.mainCache.result = ""
		}
		return x, nil
	}
	if x.pointerPicker.open {
		action, done := x.pointerPicker.Handle(k)
		if !done {
			receivedMsgIcon = x.pointerPicker.SelectedKey()
			x.mainCache.result = ""
		} else if action == "confirm" {
			receivedMsgIcon = x.pointerPicker.Close(true)
			currentConfig.PointerIcon = receivedMsgIcon
			saveConfig()
		} else {
			receivedMsgIcon = x.pointerPicker.Close(false)
			x.mainCache.result = ""
		}
		return x, nil
	}
	if x.helpPicker.open {
		_, done := x.helpPicker.Handle(k)
		if done {
			x.helpPicker.Close(false)
			x.mainCache.result = ""
		}
		return x, nil
	}
	if x.fileBrowserOpen {
		rows := x.fileBrowserVisibleRows(max(1, x.h-6))
		switch k.Type {
		case tea.KeyEsc:
			x.closeFileBrowser()
			return x, nil
		case tea.KeyUp:
			x.fileBrowserIndex = wrappedIndex(x.fileBrowserIndex, len(x.fileBrowserEntries), -1)
			x.ensureFileBrowserVisible(rows)
			return x, nil
		case tea.KeyDown:
			x.fileBrowserIndex = wrappedIndex(x.fileBrowserIndex, len(x.fileBrowserEntries), 1)
			x.ensureFileBrowserVisible(rows)
			return x, nil
		case tea.KeyBackspace:
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
		}
		return x, nil
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
		if x.mainCache != nil {
			x.mainCache.result = ""
		}
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
				return x, logout(x.client, x.baseURL)
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
			if x.mainCache != nil {
				x.mainCache.result = ""
			}
			return x, nil
		case tea.KeyBackspace:
			if x.msgSearchInput != "" {
				x.msgSearchInput = graphemeDeleteLast(x.msgSearchInput)
				x.msgSearchResults = nil
				x.msgSearchSel = 0
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
			}
			return x, nil
		case tea.KeyUp:
			if len(x.msgSearchResults) > 0 {
				x.msgSearchSel = wrappedIndex(x.msgSearchSel, len(x.msgSearchResults), -1)
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
			}
			return x, nil
		case tea.KeyDown:
			if len(x.msgSearchResults) > 0 {
				x.msgSearchSel = wrappedIndex(x.msgSearchSel, len(x.msgSearchResults), 1)
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
			}
			return x, nil
		case tea.KeyEnter:
			q := strings.TrimSpace(x.msgSearchInput)
			// First Enter on a fresh query: fire the search.
			if q != "" && len(x.msgSearchResults) == 0 && !x.msgSearchLoading {
				x.msgSearchLoading = true
				x.msgSearchErr = ""
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
				return x, searchMsgs(x.client, x.baseURL, q)
			}
			// Second Enter (with results): jump to selected.
			if len(x.msgSearchResults) == 0 {
				return x, nil
			}
			hit := x.msgSearchResults[x.msgSearchSel]
			x.active = hit.ChatID
			x.mode = "chat"
			x.sidebarFocused = false
			x.scroll = 0
			x.selectedMsgID = hit.MessageID
			x.msgSearchInput = ""
			x.msgSearchResults = nil
			x.msgSearchSel = 0
			x.msgSearchErr = ""
			if x.mainCache != nil {
				x.mainCache.result = ""
			}
			// Fetch 100 messages centred on the hit (50 before + target + 50 after).
			return x, getMsgsAround(x.client, x.baseURL, hit.ChatID, hit.MessageID, 100)
		default:
			if len(k.Runes) > 0 {
				x.msgSearchInput += string(k.Runes)
				x.msgSearchResults = nil
				x.msgSearchSel = 0
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
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
				x.mainCache.result = ""
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
				x.mainCache.result = ""
				return x, nil
			case tea.KeyEnter:
				cp := cands[x.replyPickIndex]
				x.replyTo = &cp
				x.replyPickMode = false
				x.selectedMsgID = ""
				x.scroll = 0
				x.mainCache.result = ""
				return x, nil
			case tea.KeyEsc, tea.KeyTab:
				x.replyPickMode = false
				x.selectedMsgID = ""
				x.scroll = 0
				x.mainCache.result = ""
				return x, nil
			default:
				if k.String() == "r" {
					cp := cands[x.replyPickIndex]
					x.replyTo = &cp
					x.replyPickMode = false
					x.selectedMsgID = ""
					x.scroll = 0
					x.mainCache.result = ""
					return x, nil
				}
				if k.String() == "o" {
					cp := cands[x.replyPickIndex]
					if cp.MediaProto != "" {
						x.replyPickMode = false
						x.selectedMsgID = ""
						x.mainCache.result = ""
						return x, tea.Batch(
							x.setTopBar("Opening media..."),
							downloadMedia(x.client, x.baseURL, x.active, cp.Key.ID),
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
					x.mainCache.result = ""
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
					x.mainCache.result = ""
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
						postJSON(x.client, x.baseURL+"/messages/delete", map[string]string{
							"chatId":    x.active,
							"messageId": cp.Key.ID,
						}, nil),
					)
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
				if x.mainCache != nil {
					x.mainCache.result = ""
				}
				// Clear local unread count immediately.
				hadUnread := false
				for i := range x.chats {
					if x.chats[i].ID == x.active {
						hadUnread = x.chats[i].UnreadCount > 0
						x.chats[i].UnreadCount = 0
						break
					}
				}
				// Reload most-recent page and mark chat as read.
				cmds := []tea.Cmd{getMsgs(x.client, x.baseURL, x.active, 120)}
				if hadUnread {
					cmds = append(cmds, postJSON(x.client, x.baseURL+"/messages/read", map[string]string{"chatId": x.active}, func([]byte) tea.Msg { return dataErr{} }))
				}
				return x, tea.Batch(cmds...)
			}
		case tea.KeyEsc:
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

func (x *m) handleSlash(txt string) (tea.Cmd, bool) {
	return x.runCommand(txt, false)
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
			x.mainCache.result = ""
			return x, tea.Batch(
				x.setTopBar("Reacted "+emoji),
				postJSON(x.client, x.baseURL+"/messages/react", map[string]string{
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
		if x.leftInput != "" {
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

func (x *m) handleGlobalCommand(txt string) (tea.Cmd, bool) {
	return x.runCommand(txt, true)
}

func (x *m) runCommand(txt string, includeGlobal bool) (tea.Cmd, bool) {
	switch {
	case includeGlobal && txt == "/exit":
		return tea.Quit, true
	case includeGlobal && txt == "/restart":
		x.restartRequested = true
		return tea.Quit, true
	case txt == "/logout":
		return logout(x.client, x.baseURL), true
	case includeGlobal && txt == "/synccontacts":
		if x.demoMode {
			return x.setTopBar("Demo mode: contacts already fake"), true
		}
		x.syncingContacts = true
		return tea.Batch(x.setTopBar("Syncing contacts..."), syncContacts(x.client, x.baseURL)), true
	case includeGlobal && txt == "/syncgroups":
		if x.demoMode {
			return x.setTopBar("Demo mode: groups already fake"), true
		}
		x.syncingGroups = true
		return tea.Batch(x.setTopBar("Syncing groups..."), syncGroups(x.client, x.baseURL)), true
	case txt == "/whitelistall":
		if len(x.chats) == 0 {
			if includeGlobal {
				return x.setTopBar("No chats loaded yet"), true
			}
			x.err = "no chats loaded yet"
			return nil, true
		}
		added := 0
		cmds := []tea.Cmd{}
		for _, c := range x.chats {
			n := num(c.ID)
			if _, exists := x.whitelist[n]; !exists {
				added++
			}
			name := x.nameFor(c.ID)
			x.whitelist[n] = name
			cmds = append(cmds, setWhitelistEntry(x.client, x.baseURL, n, name, 1))
		}
		x.markIdentityChanged()
		msg := fmt.Sprintf("Whitelisted %d chats (%d new)", len(x.whitelist), added)
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		cmds = append(cmds, x.setTopBar(msg))
		return tea.Batch(cmds...), true
	case txt == "/blacklistall":
		count := len(x.whitelist)
		if count == 0 {
			return x.setTopBar("Whitelist already empty"), true
		}
		cmds := []tea.Cmd{}
		for n := range x.whitelist {
			cmds = append(cmds, setWhitelistEntry(x.client, x.baseURL, n, "", 0))
		}
		x.whitelist = map[string]string{}
		x.markIdentityChanged()
		if x.active != "" {
			x.clearChatComposer()
		}
		msg := fmt.Sprintf("Removed %d from whitelist", count)
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		cmds = append(cmds, x.setTopBar(msg))
		return tea.Batch(cmds...), true
	case txt == "/whitelist":
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		_, already := x.whitelist[n]
		name := x.nameFor(x.active)
		x.whitelist[n] = name
		x.markIdentityChanged()
		msg := "Added to whitelist"
		if already {
			msg = "Already in whitelist"
		}
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		return tea.Batch(setWhitelistEntry(x.client, x.baseURL, n, name, 1), x.setTopBar(msg)), true
	case txt == "/blacklist":
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		_, was := x.whitelist[n]
		delete(x.whitelist, n)
		x.markIdentityChanged()
		if x.active != "" && num(x.active) == n {
			x.clearChatComposer()
		}
		msg := "Removed from whitelist"
		if !was {
			msg = "Not in whitelist"
		}
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		return tea.Batch(setWhitelistEntry(x.client, x.baseURL, n, "", 0), x.setTopBar(msg)), true
	case strings.HasPrefix(txt, "/rename "):
		name := strings.TrimSpace(strings.TrimPrefix(txt, "/rename "))
		if name == "" {
			return x.setTopBar("usage: /rename <name>"), true
		}
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		x.names[n] = name
		if _, ok := x.whitelist[n]; ok {
			x.whitelist[n] = name
		}
		x.markIdentityChanged()
		if x.demoMode {
			return x.setTopBar("Renamed"), true
		}
		return tea.Batch(setName(x.client, x.baseURL, n, name), x.setTopBar("Renamed")), true
	case txt == "/rename":
		return x.setTopBar("usage: /rename <name>"), true
	case strings.HasPrefix(txt, "/send "), txt == "/send", strings.HasPrefix(txt, "/sendimage"), strings.HasPrefix(txt, "/sendvideo"), strings.HasPrefix(txt, "/sendfile"):
		cmd, usage, matched := parseMediaSendCommand(txt)
		if !matched {
			break
		}
		if usage != "" {
			return x.setTopBar(usage), true
		}
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		if _, ok := x.whitelist[num(x.active)]; !ok {
			return x.setTopBar("Not whitelisted - use /whitelist to enable"), true
		}
		if x.demoMode {
			return x.setTopBar("Demo mode: media send disabled"), true
		}
		kind := cmd.kind
		if kind == "" {
			var err error
			kind, err = detectMediaSendKind(cmd.path)
			if err != nil {
				return x.setTopBar(err.Error()), true
			}
		}
		x.replyTo = nil
		return sendFile(x.client, x.baseURL, x.active, kind, cmd.path, cmd.caption, ""), true
	case txt == "/emoji":
		x.openEmojiPicker()
		return nil, true
	case txt == "/theme":
		x.themePicker.Open(currentConfig.ThemeName)
		x.leftInput = ""
		x.leftInputFocused = false
		x.mainCache.result = ""
		return nil, true
	case txt == "/pointer":
		x.pointerPicker.Open(receivedMsgIcon)
		x.leftInput = ""
		x.leftInputFocused = false
		x.mainCache.result = ""
		return nil, true
	case txt == "/help":
		x.helpPicker.Open("")
		x.leftInput = ""
		x.leftInputFocused = false
		x.mainCache.result = ""
		return nil, true
	case strings.HasPrefix(txt, "/theme") && txt != "/theme":
		suffix := txt[len("/theme"):]
		// Strip optional leading digit (e.g. "/theme1linen" → "linen").
		if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
			suffix = suffix[1:]
		}
		for _, t := range themeList {
			if t.name == suffix {
				applyThemeByName(t.name)
				saveConfig()
				x.mainCache.result = ""
				return x.setTopBar("Theme: " + t.displayName), true
			}
		}
	case txt == "/mouseon":
		x.mouseEnabled = true
		currentConfig.MouseEnabled = true
		saveConfig()
		return x.setTopBar("Mouse: Enabled"), true
	case txt == "/mouseoff":
		x.mouseEnabled = false
		currentConfig.MouseEnabled = false
		saveConfig()
		return x.setTopBar("Mouse: Disabled"), true
	case txt == "/sound1", txt == "/sound2", txt == "/sound3", txt == "/sound4", txt == "/sound5":
		profile := int(txt[len(txt)-1] - '0')
		profile = normalizeSoundProfile(profile)
		x.soundProfile = profile
		x.soundEnabled = true
		currentConfig.SoundProfile = profile
		currentConfig.SoundEnabled = true
		saveConfig()
		// Quick preview helps you pick a tone without guesswork.
		return tea.Batch(x.setTopBar("Sound: "+soundName(profile)), playSoundProfileCmd(profile)), true
	case txt == "/soundoff":
		x.soundEnabled = false
		currentConfig.SoundEnabled = false
		saveConfig()
		return x.setTopBar("Sound: Off"), true
	case txt == "/soundon":
		x.soundEnabled = true
		x.soundProfile = normalizeSoundProfile(x.soundProfile)
		currentConfig.SoundEnabled = true
		currentConfig.SoundProfile = x.soundProfile
		saveConfig()
		return tea.Batch(x.setTopBar("Sound: "+soundName(x.soundProfile)), playSoundProfileCmd(x.soundProfile)), true
	case strings.HasPrefix(txt, "/"):
		return x.setTopBar("unknown command: " + txt), true
	}
	return nil, false
}

func rehashStyles() {
	brand = lipgloss.Color(currentTheme.Brand)
	accent = lipgloss.Color(currentTheme.Accent)
	purple = lipgloss.Color(currentTheme.Purple)
	amber = lipgloss.Color(currentTheme.Amber)
	red = lipgloss.Color(currentTheme.Red)
	muted = lipgloss.Color(currentTheme.Muted)
	text = lipgloss.Color(currentTheme.Text)
	imageTag = lipgloss.Color(currentTheme.ImageTag)
	videoTag = lipgloss.Color(currentTheme.VideoTag)
	audioTag = lipgloss.Color(currentTheme.AudioTag)
	fileTag = lipgloss.Color(currentTheme.FileTag)
	stickerTag = lipgloss.Color(currentTheme.StickerTag)
	contactTag = lipgloss.Color(currentTheme.ContactTag)
	pollTag = lipgloss.Color(currentTheme.PollTag)
	locationTag = lipgloss.Color(currentTheme.LocationTag)
	anomalyTag = lipgloss.Color(currentTheme.AnomalyTag)
	sentText = lipgloss.Color(currentTheme.SentText)
	receivedText = lipgloss.Color(currentTheme.ReceivedText)
	sentName = lipgloss.Color(currentTheme.SentName)
	receivedName = lipgloss.Color(currentTheme.ReceivedName)
	quotedSentText = lipgloss.Color(currentTheme.QuotedSentText)
	quotedReceivedText = lipgloss.Color(currentTheme.QuotedReceivedText)
	badgeInk = lipgloss.Color(currentTheme.BadgeInk)
	buttonInk = lipgloss.Color(currentTheme.ButtonInk)
	tagInk = lipgloss.Color(currentTheme.TagInk)
	cursorColor = lipgloss.Color(currentTheme.Cursor)
	qrLight = lipgloss.Color(currentTheme.QRLight)
	qrDark = lipgloss.Color(currentTheme.QRDark)
	shortcutActive = lipgloss.Color(currentTheme.ShortcutActive)
	sidebarActiveBg = lipgloss.Color(currentTheme.SidebarActiveBg)
	sidebarActiveUnreadBg = lipgloss.Color(currentTheme.SidebarActiveUnreadBg)
	replyPreviewBg = lipgloss.Color(currentTheme.ReplyPreviewBg)
	messageSelectedBg = lipgloss.Color(currentTheme.MessageSelectedBg)
	mediaTokenBg = lipgloss.Color(currentTheme.MediaTokenBg)
	mediaTokenPulseBg = lipgloss.Color(currentTheme.MediaTokenPulseBg)

	baseBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(brand).
		Foreground(text).
		Padding(1, 2).
		Align(lipgloss.Center, lipgloss.Center)

	logoStyle = lipgloss.NewStyle().Bold(true).Foreground(brand)
	accentStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	amberStyle = lipgloss.NewStyle().Foreground(amber).Bold(true)
	purpleStyle = lipgloss.NewStyle().Foreground(purple).Bold(true)
	redStyle = lipgloss.NewStyle().Foreground(red).Bold(true)

	cmdBadgeStyle = lipgloss.NewStyle().Foreground(badgeInk).Background(accent).Bold(true)
	ghostStyle = lipgloss.NewStyle().Foreground(muted)
	cursorStyle = lipgloss.NewStyle().Foreground(cursorColor).Background(cursorColor)
	inputCursorStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	sidebarStyle = lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(muted)

	msgPaneStyle = lipgloss.NewStyle().Padding(0, 1)
	dateSepStyle = lipgloss.NewStyle().Foreground(muted).Bold(true)

	bColor = qrDark
	wColor = qrLight
	bb = lipgloss.NewStyle().Foreground(bColor).Background(bColor).Render("▀")
	bw = lipgloss.NewStyle().Foreground(bColor).Background(wColor).Render("▀")
	wb = lipgloss.NewStyle().Foreground(wColor).Background(bColor).Render("▀")
	ww = lipgloss.NewStyle().Foreground(wColor).Background(wColor).Render("▀")

	imageTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(imageTag).Bold(true)
	videoTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(videoTag).Bold(true)
	audioTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(audioTag).Bold(true)
	fileTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(fileTag).Bold(true)
	stickerTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(stickerTag).Bold(true)
	contactTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(contactTag).Bold(true)
	pollTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(pollTag).Bold(true)
	locationTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(locationTag).Bold(true)
	anomalyTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(anomalyTag).Bold(true)

	setTerminalBg(currentTheme.Background)
}

func (x m) sidebarItems() []chat {
	if x.sidebarTab == "contacts" {
		if x.sidebarCache != nil && x.sidebarCache.contactsValid {
			return x.sidebarCache.contacts
		}
		out := make([]chat, 0, len(x.contacts))
		for _, ct := range x.contacts {
			if ct.ID == "" || strings.HasSuffix(ct.ID, "@g.us") || ct.ID == "status@broadcast" {
				continue
			}
			// Contacts tab should only show named contacts, not raw unresolved numbers.
			if strings.TrimSpace(ct.Notify) == "" && strings.TrimSpace(ct.Name) == "" {
				continue
			}
			out = append(out, chat{ID: ct.ID, Name: ct.Notify, Subject: ct.Name})
		}
		sort.Slice(out, func(i, j int) bool {
			classI, keyI := contactSortKey(x.name(out[i]))
			classJ, keyJ := contactSortKey(x.name(out[j]))
			if classI != classJ {
				return classI < classJ
			}
			if keyI == keyJ {
				return out[i].ID < out[j].ID
			}
			return keyI < keyJ
		})
		if x.sidebarCache != nil {
			x.sidebarCache.contacts = out
			x.sidebarCache.contactsValid = true
			return x.sidebarCache.contacts
		}
		return out
	}
	out := make([]chat, 0, len(x.chats))
	for _, ch := range x.chats {
		if ch.ID == "" || ch.ID == "status@broadcast" {
			continue
		}
		if ch.ConversationTimestamp == 0 && len(x.msgs[ch.ID]) == 0 {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func contactSortKey(name string) (class int, key string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 2, ""
	}
	first := rune(0)
	for _, r := range trimmed {
		first = r
		break
	}
	lower := strings.ToLower(trimmed)
	if (first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') {
		return 0, lower
	}
	return 1, lower
}

func (x m) filtered() []chat {
	items := x.sidebarItems()
	if strings.TrimSpace(x.search) == "" {
		return items
	}
	q := strings.ToLower(strings.TrimSpace(x.search))
	out := make([]chat, 0, len(items))
	for _, c := range items {
		if strings.Contains(strings.ToLower(x.name(c)), q) || strings.Contains(strings.ToLower(c.ID), q) {
			out = append(out, c)
		}
	}
	return out
}
func (x *m) ensureSideVisible(viewRows int) {
	f := x.filtered()
	if len(f) == 0 {
		x.sel = 0
		x.sideScroll = 0
		return
	}
	if x.sel < 0 {
		x.sel = 0
	}
	if x.sel >= len(f) {
		x.sel = len(f) - 1
	}
	if viewRows <= 0 {
		return
	}
	maxStart := max(0, len(f)-viewRows)
	if x.sideScroll > maxStart {
		x.sideScroll = maxStart
	}
	if x.sideScroll < 0 {
		x.sideScroll = 0
	}
	if x.sel < x.sideScroll {
		x.sideScroll = x.sel
	}
	if x.sel >= x.sideScroll+viewRows {
		x.sideScroll = x.sel - viewRows + 1
	}
}

func (x m) sideViewRows() int {
	outerH := x.h - 2
	if outerH <= 0 {
		return 1
	}
	sideH := outerH - 5
	return max(1, sideH-3)
}

func (x m) sidePaneWidth() int {
	frameW := max(1, x.w-2)
	outerW := frameW
	contentW := outerW
	return min(28, max(24, contentW/3))
}

func (x m) currentSidebarMarqueeKey() string {
	f := x.filtered()
	if len(f) == 0 {
		return ""
	}
	if x.mode != "chat" || x.sidebarFocused {
		if x.sel >= 0 && x.sel < len(f) {
			return x.sidebarTab + ":" + f[x.sel].ID
		}
	} else if x.active != "" {
		return x.sidebarTab + ":" + x.active
	}
	return ""
}

func (x *m) syncSidebarHighlight() {
	key := x.currentSidebarMarqueeKey()
	if key == "" {
		x.sidebarHighlightKey = ""
		x.sidebarHighlightInset = 0
		return
	}
	if key != x.sidebarHighlightKey {
		x.sidebarHighlightKey = key
		x.sidebarHighlightInset = 0
	}
}

func (x *m) advanceSidebarHighlight() {
	x.syncSidebarHighlight()
	if x.sidebarHighlightKey == "" {
		return
	}
	if x.sidebarHighlightInset < 1 {
		x.sidebarHighlightInset++
	}
}

func (x m) currentSidebarMarqueeLabel() (string, int) {
	f := x.filtered()
	if len(f) == 0 {
		return "", 0
	}
	var target chat
	var targetIdx int
	found := false
	if x.mode != "chat" || x.sidebarFocused {
		if x.sel >= 0 && x.sel < len(f) {
			target = f[x.sel]
			targetIdx = x.sel
			found = true
		}
	} else if x.active != "" {
		for i, c := range f {
			if num(c.ID) == num(x.active) {
				target = c
				targetIdx = i
				found = true
				break
			}
		}
	}
	if !found {
		return "", 0
	}
	sideW := x.sidePaneWidth()
	rowWidth := max(1, sideW-2)
	nameWidth := max(1, rowWidth-1)
	n := targetIdx + 1
	var numLabel string
	switch {
	case n >= 100:
		numLabel = fmt.Sprintf("%d ", n)
	case n >= 10:
		numLabel = fmt.Sprintf("%d. ", n)
	default:
		numLabel = fmt.Sprintf("0%d. ", n)
	}
	return numLabel + x.name(target), nameWidth
}

func (x *m) resetSidebarMarquee() {
	x.sidebarMarqueeOffset = 0
	x.sidebarMarqueePause = 8
	x.sidebarMarqueeDir = 1
	x.sidebarMarqueeTick = 0
}

func (x *m) advanceSidebarMarquee() {
	x.syncSidebarHighlight()
	key := x.currentSidebarMarqueeKey()
	if key == "" {
		x.sidebarMarqueeKey = ""
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueePause = 0
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueeTick = 0
		return
	}
	if key != x.sidebarMarqueeKey {
		x.sidebarMarqueeKey = key
		x.resetSidebarMarquee()
		return
	}
	label, width := x.currentSidebarMarqueeLabel()
	if graphemeCount(label) <= width {
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueePause = 0
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueeTick = 0
		return
	}
	maxOffset := graphemeCount(label) - width
	if maxOffset <= 0 {
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueeTick = 0
		return
	}
	if x.sidebarMarqueePause > 0 {
		x.sidebarMarqueePause--
		return
	}
	x.sidebarMarqueeTick = (x.sidebarMarqueeTick + 1) % 3
	if x.sidebarMarqueeTick != 0 {
		return
	}
	if x.sidebarMarqueeDir <= 0 {
		if x.sidebarMarqueeOffset > 0 {
			x.sidebarMarqueeOffset--
			if x.sidebarMarqueeOffset == 0 {
				x.sidebarMarqueeDir = 1
				x.sidebarMarqueePause = 8
			}
			return
		}
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueePause = 8
		return
	}
	if x.sidebarMarqueeOffset < maxOffset {
		x.sidebarMarqueeOffset++
		if x.sidebarMarqueeOffset == maxOffset {
			x.sidebarMarqueeDir = -1
			x.sidebarMarqueePause = 8
		}
		return
	}
	x.sidebarMarqueeDir = -1
	x.sidebarMarqueePause = 8
}

func (x m) openSelectedChat() (tea.Model, tea.Cmd) {
	f := x.filtered()
	if len(f) == 0 {
		return x, nil
	}
	if x.sel < 0 || x.sel >= len(f) {
		x.sel = 0
	}
	x.active, x.mode, x.scroll = f[x.sel].ID, "chat", 0
	if x.chatInputLocked() {
		x.clearChatComposer()
	}

	// Clear the unread dot right away.
	hadUnread := false
	for i := range x.chats {
		if x.chats[i].ID == x.active {
			hadUnread = x.chats[i].UnreadCount > 0
			x.chats[i].UnreadCount = 0
			break
		}
	}

	if x.demoMode {
		if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
			return x, titleCmd
		}
		return x, nil
	}

	batch := []tea.Cmd{getMsgs(x.client, x.baseURL, x.active, 120)}
	if hadUnread {
		batch = append(batch, postJSON(x.client, x.baseURL+"/messages/read", map[string]string{"chatId": x.active}, func([]byte) tea.Msg { return dataErr{} }))
	}
	if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
		batch = append(batch, titleCmd)
	}
	return x, tea.Batch(batch...)
}

// maybeLoadOlder fires a lazy-load fetch for older messages if the user has
// scrolled within `lazyLoadTriggerRows` rows of the top of the loaded window.
// Returns nil if no fetch is needed (already loading, exhausted, or far from top).
func (x *m) maybeLoadOlder() tea.Cmd {
	if x.demoMode || x.active == "" {
		return nil
	}
	chatID := x.active
	if x.loadingOlder[chatID] || x.noMoreOlder[chatID] {
		return nil
	}
	msgs := x.msgs[chatID]
	if len(msgs) == 0 {
		return nil
	}
	// scroll is rows-from-bottom. Total visible content rows ≈ len(msgs) (one row per msg
	// is a lower bound; multi-line bubbles take more). Trigger when scroll is within
	// `lazyLoadTriggerRows` of len(msgs).
	const lazyLoadTriggerRows = 10
	if x.scroll < len(msgs)-lazyLoadTriggerRows {
		return nil
	}
	oldest := msgs[0].MessageTimestamp
	if oldest <= 0 {
		return nil
	}
	if x.loadingOlder == nil {
		x.loadingOlder = map[string]bool{}
	}
	x.loadingOlder[chatID] = true
	return getMsgsBefore(x.client, x.baseURL, chatID, 100, oldest)
}
