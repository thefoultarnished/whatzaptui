package backend

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
	"whatzap/internal/whatsapp"
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
			a.actionLog.Event("client.connected", nil)
			go func() {
				presCtx, presCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer presCancel()
				if err := a.client.SendPresence(presCtx, types.PresenceAvailable); err != nil {
					a.actionLog.Event("client.presence.error", map[string]string{
						"err": truncateErr(err),
					})
				}
			}()
			a.mu.RLock()
			bootstrap := a.needsBootstrapSync
			a.mu.RUnlock()
			if bootstrap {
				go a.bootstrapFromStore()
			}
			go a.refreshGroupMetadata()
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
			// Check both the raw envelope and the unwrapped effective message —
			// some protocol messages arrive nested inside DeviceSentMessage.
			if isInvisibleProtocolMessage(v.Message) || isInvisibleProtocolMessage(effectiveMessage(v.Message)) {
				a.actionLog.Event("message.protocol.dropped", map[string]string{
					"chat": redactChatID(chatID),
				})
				return
			}
			msg := a.toWireMessage(v)
			msg.Key.RemoteJID = chatID
			if msg.Key.Participant != "" {
				msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
			}
			a.upsertMessage(chatID, msg)
			a.actionLog.Event("message.received", map[string]string{
				"chat": redactChatID(chatID),
				"kind": redactMessageKind(msg.Message),
				"from": redactChatID(strings.TrimSpace(msg.Key.Participant)),
				"name": redactPushName(msg.PushName),
			})
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
		case *events.AppStateSyncComplete:
			if v != nil {
				a.handleAppStateSyncComplete(v.Name)
			}
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
	m := whatsapp.BuildCallMeta(status, meta, media, "", a.canonicalizeChatID)
	return WireCallEvent{
		Status:   m.Status,
		CallerID: m.CallerID,
		GroupID:  m.GroupID,
		CallID:   m.CallID,
		Media:    m.Media,
	}
}

func (a *App) applyHistorySync(data *waHistorySync.HistorySync) {
	if data == nil {
		return
	}
	a.dbLifecycleMu.RLock()
	defer a.dbLifecycleMu.RUnlock()
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	a.mu.RUnlock()
	if shuttingDown {
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

	// Pre-flight counts: how much HistorySync did WhatsApp push this
	// time? Conversations / Pushnames come from the protobuf header;
	// the message total is summed inline so the line can show "we got
	// N messages but only managed to insert M" without a second pass.
	totalConvs := 0
	totalMessages := 0
	for _, conv := range data.GetConversations() {
		totalConvs++
		totalMessages += len(conv.GetMessages())
	}
	totalPushnames := len(data.GetPushnames())
	progress := int(data.GetProgress())
	startedAt := time.Now()
	a.actionLog.Event("historysync.received", map[string]string{
		"conversations": intStr(totalConvs),
		"messages":      intStr(totalMessages),
		"pushnames":     intStr(totalPushnames),
		"progress":      intStr(progress),
	})

	// The watchdog logs the current phase (and dumps goroutine stacks once)
	// if this chunk stops making progress, so a hang names its own spot.
	watch := a.startStallWatch("historysync")
	defer watch.Stop()

	watch.Phase("metadata")
	changedChats := a.syncPushnamesAndConversations(data)
	a.actionLog.Event("historysync.phase", map[string]string{
		"phase": "metadata", "changed": boolStr(changedChats), "elapsedMs": durMs(time.Since(startedAt)),
	})

	// Convert every message BEFORE opening the transaction. Conversion does
	// DB lookups on the pool (e.g. quotedStanzaFromMe for replies) and LID
	// lookups in whatsmeow's store; with a single-connection pool, running
	// them while the tx holds the connection waits on itself forever.
	watch.Phase("prepare")
	pending := a.prepareHistoryMessages(data)
	a.actionLog.Event("historysync.phase", map[string]string{
		"phase": "prepare", "messages": intStr(len(pending)), "elapsedMs": durMs(time.Since(startedAt)),
	})

	watch.Phase("begin-tx")
	var tx *sql.Tx
	var err error
	if a.db != nil && len(pending) > 0 {
		tx, err = a.db.Begin()
		if err != nil {
			log.Printf("applyHistorySync: begin tx: %v", err)
			a.actionLog.Event("historysync.tx.begin.fail", map[string]string{
				"err": truncateErr(err),
			})
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

	watch.Phase("insert")
	if a.insertHistoryMessages(exec, pending, watch) {
		changedChats = true
	}

	watch.Phase("commit")
	commitErr := ""
	if tx != nil {
		if cErr := tx.Commit(); cErr != nil {
			log.Printf("applyHistorySync: commit tx: %v", cErr)
			commitErr = truncateErr(cErr)
			a.actionLog.Event("historysync.tx.commit.fail", map[string]string{
				"err": commitErr,
			})
		} else {
			tx = nil
		}
	}

	a.actionLog.Event("historysync.applied", map[string]string{
		"conversations":  intStr(totalConvs),
		"messagesSeen":   intStr(totalMessages),
		"messagesStored": intStr(len(pending)),
		"elapsedMs":      intStr(int(time.Since(startedAt).Milliseconds())),
		"commitOk":       boolStr(commitErr == ""),
	})

	watch.Phase("reconcile-lid")
	a.reconcileLIDChats()

	watch.Phase("persist")
	if changedChats {
		if err := a.persistStateWithErrUnlocked(); err != nil {
			log.Printf("persistState: %v", err)
			a.actionLog.Event("historysync.persist.fail", map[string]string{"err": truncateErr(err)})
		}
	}
	// History sync arrives in chunks and may contain pushnames or messages
	// without conversation metadata. Refresh both views after every chunk so
	// a new account does not remain stuck on the empty pre-sync snapshot.
	a.broadcast(EventEnvelope{Type: "chats:loaded"})
	a.broadcast(EventEnvelope{Type: "contacts:updated"})
	a.mu.RLock()
	chatCount := len(a.state.Chats)
	a.mu.RUnlock()
	a.actionLog.Event("historysync.done", map[string]string{
		"chatsInState": intStr(chatCount),
		"elapsedMs":    durMs(time.Since(startedAt)),
	})
}

type pushnameUpdate struct {
	id   string
	name string
}

type convMetadata struct {
	chatID string
	name   string
	ts     int64
	uc     int
}

func (a *App) syncPushnamesAndConversations(data *waHistorySync.HistorySync) bool {
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

	var convMeta []convMetadata
	changedState := false
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
		changedState = true
	}

	a.mu.Lock()
	for _, u := range pushnameUpdates {
		// Pushnames-only entries: WhatsApp sends one for nearly anyone
		// who has ever appeared in a synced group or chat, most of whom
		// you have no real 1:1 conversation with (a batch of 2000
		// pushnames is normal for one history-sync chunk). This must
		// only touch Contacts — writing a Chats entry here used to turn
		// every one of those names into a phantom zero-message "chat"
		// (reported: 943 of 1000 chats had conv_ts=0). A real chat's
		// name is already filled in from Contacts at serve time
		// (handleChats), so a Chats write here was always redundant on
		// top of being wrong for everyone else.
		contact := a.state.Contacts[u.id]
		contact.ID = u.id
		contact.Notify = u.name
		a.state.Contacts[u.id] = contact
		changedState = true
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
	return changedState
}

// pendingHistoryMessage is one history message converted to wire form and
// ready to insert under the sync transaction.
type pendingHistoryMessage struct {
	chatID string
	msg    WireMessage
}

// prepareHistoryMessages converts a chunk's messages to wire form. It must
// run with no transaction open (see applyHistorySync).
func (a *App) prepareHistoryMessages(data *waHistorySync.HistorySync) []pendingHistoryMessage {
	var pending []pendingHistoryMessage
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
			if msg.Key.FromMe && msg.ReceiptStatus == "" {
				msg.ReceiptStatus = "delivered"
			}
			pending = append(pending, pendingHistoryMessage{chatID: msg.Key.RemoteJID, msg: msg})
		}
	}
	return pending
}

// historyInsertProgressEvery is how often (in messages) the insert phase
// logs progress, so a slow or stuck insert shows how far it got.
const historyInsertProgressEvery = 250

// insertHistoryMessages writes prepared messages through exec (the sync
// transaction when one is open). Reports true when anything was written.
func (a *App) insertHistoryMessages(exec dbExecutor, pending []pendingHistoryMessage, watch *stallWatch) bool {
	for i, p := range pending {
		a.upsertMessageTx(exec, p.chatID, p.msg)
		if n := i + 1; n%historyInsertProgressEvery == 0 && n < len(pending) {
			watch.Phase("insert " + intStr(n) + "/" + intStr(len(pending)))
			a.actionLog.Event("historysync.insert.progress", map[string]string{
				"done": intStr(n), "total": intStr(len(pending)),
			})
		}
	}
	return len(pending) > 0
}
