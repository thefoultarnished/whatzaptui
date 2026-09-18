package main

import (
	"context"
	"database/sql"
	"log"
	"maps"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (a *App) bindEvents() {
	a.client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			a.mu.Lock()
			a.connected = true
			a.connState = "ready"
			a.lastQR = ""
			a.mu.Unlock()
			go func() {
				presCtx, presCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer presCancel()
				_ = a.client.SendPresence(presCtx, types.PresenceAvailable)
			}()
			a.broadcast(EventEnvelope{Type: "ready"})
			a.broadcast(EventEnvelope{Type: "chats:loaded"})
		case *events.Disconnected:
			a.mu.Lock()
			a.connected = false
			a.connState = "disconnected"
			a.mu.Unlock()
			a.broadcast(EventEnvelope{Type: "disconnected", Payload: "connection closed"})
		case *events.LoggedOut:
			a.mu.Lock()
			a.connected = false
			a.started = false
			a.connState = "logged-out"
			a.mu.Unlock()
			a.broadcast(EventEnvelope{Type: "status", Payload: "Logged out. Start again to scan QR."})
			if a.client != nil && a.client.Store != nil {
				_ = a.client.Store.Delete(context.Background())
				a.client.Store.ID = nil
			}
		case *events.PushName:
			jid := a.canonicalizeChatID(v.JID.String())
			a.mu.Lock()
			ct := a.state.Contacts[jid]
			ct.ID = jid
			ct.Notify = v.NewPushName
			a.state.Contacts[jid] = ct
			a.mu.Unlock()
			a.persistState()
			a.broadcast(EventEnvelope{Type: "contacts:updated"})
		case *events.Message:
			if v == nil || v.Message == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.Info.Chat.String())
			if chatID == "" || chatID == "status@broadcast" {
				return
			}
			if pm := v.Message.GetProtocolMessage(); pm != nil && pm.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT && pm.GetEditedMessage() != nil {
				targetID := pm.GetKey().GetID()
				if targetID == "" {
					return
				}
				edited := pm.GetEditedMessage()
				newMsg, _ := a.wireMessagePayload(edited, effectiveMessage(edited), chatID, v.Info.IsGroup)
				wire, ok, err := a.editMessageInDB(chatID, targetID, v.Info.IsFromMe, func(m map[string]any) {
					clear(m)
					maps.Copy(m, newMsg)
				})
				if err != nil {
					log.Printf("edit message: %v", err)
					return
				}
				if ok {
					a.broadcast(EventEnvelope{Type: "message:edited", Payload: wire})
				}
				return
			}
			// Sync plumbing (history sync notifications, key shares, ...) is
			// not chat content: drop it before it reaches storage or the UI.
			if isInvisibleProtocolMessage(v.Message) {
				return
			}
			msg := a.toWireMessage(v)
			msg.Key.RemoteJID = chatID
			if msg.Key.Participant != "" {
				msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
			}
			a.upsertMessage(chatID, msg)
			a.broadcast(EventEnvelope{Type: "message", Payload: msg})
			a.broadcast(EventEnvelope{Type: "chats:loaded"})
		case *events.Receipt:
			if v == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.Chat.String())
			status := receiptStatusFromType(v.Type)
			if chatID == "" || status == "" || len(v.MessageIDs) == 0 {
				return
			}
			ids := make([]string, 0, len(v.MessageIDs))
			for _, id := range v.MessageIDs {
				if id != "" {
					ids = append(ids, string(id))
				}
			}
			if len(ids) == 0 {
				return
			}
			if a.updateReceiptStatus(chatID, ids, status) {
				a.broadcast(EventEnvelope{Type: "receipt", Payload: WireReceiptUpdate{
					ChatID:        chatID,
					MessageIDs:    ids,
					ReceiptStatus: status,
				}})
			}
			if v.Type == types.ReceiptTypeReadSelf {
				a.mu.Lock()
				chat := a.state.Chats[chatID]
				chat.ID = chatID
				chat.UnreadCount = 0
				a.state.Chats[chatID] = chat
				a.mu.Unlock()
				a.persistState()
				a.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		case *events.MarkChatAsRead:
			if v == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.JID.String())
			if chatID == "" {
				return
			}
			if v.Action != nil && v.Action.GetRead() {
				a.mu.Lock()
				chat := a.state.Chats[chatID]
				chat.ID = chatID
				chat.UnreadCount = 0
				a.state.Chats[chatID] = chat
				a.mu.Unlock()
				a.persistState()
				a.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		case *events.HistorySync:
			a.applyHistorySync(v.Data)
		case *events.CallOffer:
			a.broadcast(EventEnvelope{Type: "call", Payload: a.toWireCallEvent("incoming", v.BasicCallMeta, "")})
		case *events.CallOfferNotice:
			a.broadcast(EventEnvelope{Type: "call", Payload: a.toWireCallEvent("incoming", v.BasicCallMeta, v.Media)})
		case *events.CallTerminate:
			ev := a.toWireCallEvent("ended", v.BasicCallMeta, "")
			ev.Reason = strings.TrimSpace(v.Reason)
			a.broadcast(EventEnvelope{Type: "call", Payload: ev})
		case *events.ChatPresence:
			chatID := a.canonicalizeChatID(v.Chat.String())
			senderID := a.canonicalizeChatID(v.Sender.String())
			if chatID != "" {
				a.broadcast(EventEnvelope{Type: "typing", Payload: map[string]string{
					"chatId": chatID,
					"sender": senderID,
					"state":  string(v.State),
				}})
			}
		}
	})
}

func (a *App) toWireCallEvent(status string, meta types.BasicCallMeta, media string) WireCallEvent {
	callerID := a.canonicalizeChatID(meta.CallCreator.String())
	if callerID == "" {
		callerID = a.canonicalizeChatID(meta.CallCreatorAlt.String())
	}
	if callerID == "" {
		callerID = a.canonicalizeChatID(meta.From.String())
	}
	groupID := a.canonicalizeChatID(meta.GroupJID.String())
	return WireCallEvent{
		Status:   status,
		CallerID: callerID,
		GroupID:  groupID,
		CallID:   meta.CallID,
		Media:    strings.TrimSpace(media),
	}
}

func (a *App) applyHistorySync(data *waHistorySync.HistorySync) {
	if data == nil {
		return
	}

	a.mu.Lock()
	a.historySyncing = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.historySyncing = false
		a.mu.Unlock()
	}()

	changedChats := false

	// Phase 1: pre-compute all updates without holding a.mu.
	// canonicalizeChatID uses a.lidCacheMu (its own lock), not a.mu, so this is safe.
	type pushnameUpdate struct {
		id   string
		name string
	}
	var pushnameUpdates []pushnameUpdate
	for _, p := range data.GetPushnames() {
		id := a.canonicalizeChatID(strings.TrimSpace(p.GetID()))
		if id == "" {
			continue
		}
		if name := strings.TrimSpace(p.GetPushname()); name != "" {
			pushnameUpdates = append(pushnameUpdates, pushnameUpdate{id, name})
		}
	}

	type convMetadata struct {
		chatID string
		name   string
		ts     int64
		uc     int
	}
	var convMeta []convMetadata
	for _, conv := range data.GetConversations() {
		chatID := a.historyConversationChatID(conv)
		if chatID == "" || chatID == "status@broadcast" {
			continue
		}
		name := strings.TrimSpace(conv.GetDisplayName())
		if name == "" {
			name = strings.TrimSpace(conv.GetName())
		}
		ts := int64(conv.GetConversationTimestamp())
		if ts == 0 {
			ts = int64(conv.GetLastMsgTimestamp())
		}
		convMeta = append(convMeta, convMetadata{chatID, name, ts, int(conv.GetUnreadCount())})
		changedChats = true
	}

	// Phase 2: apply pre-computed updates under one short lock.
	a.mu.Lock()
	for _, u := range pushnameUpdates {
		contact := a.state.Contacts[u.id]
		contact.ID = u.id
		contact.Notify = u.name
		a.state.Contacts[u.id] = contact
		chat := a.state.Chats[u.id]
		chat.ID = u.id
		if chat.Name == "" {
			chat.Name = u.name
		}
		a.state.Chats[u.id] = chat
	}
	for _, c := range convMeta {
		chat := a.state.Chats[c.chatID]
		chat.ID = c.chatID
		if c.name != "" {
			chat.Name = c.name
		}
		if chat.Name == "" {
			if ct, ok := a.state.Contacts[c.chatID]; ok {
				if n := strings.TrimSpace(ct.Notify); n != "" {
					chat.Name = n
				} else if n := strings.TrimSpace(ct.Name); n != "" {
					chat.Name = n
				}
			}
		}
		if c.ts > chat.ConversationTimestamp {
			chat.ConversationTimestamp = c.ts
		}
		if c.uc > chat.UnreadCount {
			chat.UnreadCount = c.uc
		}
		a.state.Chats[c.chatID] = chat
	}
	a.mu.Unlock()

	var tx *sql.Tx
	var err error
	if a.db != nil {
		tx, err = a.db.Begin()
		if err != nil {
			log.Printf("applyHistorySync: begin tx: %v", err)
		}
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var exec dbExecutor = a.db
	if tx != nil {
		exec = tx
	}

	for _, conv := range data.GetConversations() {
		chatID := a.historyConversationChatID(conv)
		if chatID == "" || chatID == "status@broadcast" {
			continue
		}
		for _, item := range conv.GetMessages() {
			wm := item.GetMessage()
			if wm == nil {
				continue
			}
			if isInvisibleProtocolMessage(wm.GetMessage()) {
				continue
			}
			msg := a.toWireMessageFromHistory(wm)
			if msg.Key.RemoteJID == "" {
				msg.Key.RemoteJID = chatID
			}
			msg.Key.RemoteJID = a.canonicalizeChatID(msg.Key.RemoteJID)
			if msg.Key.Participant != "" {
				msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
			}
			if msg.Key.RemoteJID == "status@broadcast" {
				continue
			}
			// History sync doesn't carry a live receipt state for previously-sent
			// messages. Any FromMe row that survived into history is, by
			// definition, at minimum delivered (you can't have a history row for
			// a message WhatsApp never accepted). Default to "delivered" so the
			// TUI renders ✓✓. Live send paths set "sent" explicitly and receipt
			// events upgrade from there.
			if msg.Key.FromMe && msg.ReceiptStatus == "" {
				msg.ReceiptStatus = "delivered"
			}
			a.upsertMessageTx(exec, msg.Key.RemoteJID, msg)
			changedChats = true
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			log.Printf("applyHistorySync: commit tx: %v", err)
		} else {
			tx = nil
		}
	}

	a.reconcileLIDChats()

	if changedChats {
		a.persistState()
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
		a.broadcast(EventEnvelope{Type: "contacts:updated"})
	}
}
