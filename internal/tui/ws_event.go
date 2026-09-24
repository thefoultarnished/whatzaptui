package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// wsTraceSkip are high-volume WS event types left out of the trace log.
var wsTraceSkip = map[string]bool{"message": true, "receipt": true, "typing": true}

func (x m) handleWSEvent(v wsEvtMsg) (tea.Model, tea.Cmd) {
	if !v.ok {
		tlog("ws.closed")
	} else if !wsTraceSkip[v.evt.Type] {
		tlog("ws.event", "type", v.evt.Type)
	}
	if !v.ok {
		x.wsDisconnected = true
		delay := x.nextReconnectDelay()
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
		x.qrReceivedAt = time.Now()
	case "ready":
		x.sessionReady = true
		x.qrRaw = ""
		cmds = append(cmds,
			getChats(x.reqCtx(), x.client, x.baseURL),
			getContacts(x.reqCtx(), x.client, x.baseURL),
			getWhitelist(x.reqCtx(), x.client, x.baseURL),
		)
		if splashHoldDuration > 0 {
			cmds = append(cmds, tea.Tick(splashHoldDuration, func(time.Time) tea.Msg { return splashDoneMsg{} }))
		} else {
			cmds = append(cmds, func() tea.Msg { return splashDoneMsg{} })
		}
	case "chats:loaded":
		cmds = append(cmds, getChats(x.reqCtx(), x.client, x.baseURL))
		if x.active != "" && len(x.msgs[x.active]) == 0 {
			cmds = append(cmds, getMsgs(x.reqCtx(), x.client, x.baseURL, x.active, 120))
		}
	case "contacts:updated":
		cmds = append(cmds, getContacts(x.reqCtx(), x.client, x.baseURL))
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
			x.invalidate()
			if !wm.Key.FromMe && wm.Key.ID != "" {
				if !activeViewing {
					for i := range x.chats {
						if x.chats[i].ID == wm.Key.RemoteJID {
							x.chats[i].UnreadCount++
							break
						}
					}
				} else if !x.demoMode {
					cmds = append(cmds, postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/read", map[string]string{"chatId": wm.Key.RemoteJID}, func([]byte) tea.Msg { return dataErr{} }))
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
			x.invalidate()
		} else {
			log.Printf("ws message:edited unmarshal: %v", err)
		}
	case "receipt":
		var rm receiptMsg
		if err := json.Unmarshal(v.evt.Payload, &rm); err == nil {
			if msgs, ok := x.msgs[rm.ChatID]; ok {
				idSet := make(map[string]bool, len(rm.MessageIDs))
				for _, id := range rm.MessageIDs {
					idSet[id] = true
				}
				changed := false
				for i := range msgs {
					if !msgs[i].Key.FromMe {
						continue
					}
					for _, id := range rm.MessageIDs {
						if msgs[i].Key.ID == id {
							msgs[i].ReceiptStatus = rm.ReceiptStatus
							changed = true
							break
						}
					}
				}
				if changed {
					x.msgs[rm.ChatID] = msgs
					x.invalidate()
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
			x.invalidate()
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
}
