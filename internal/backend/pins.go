package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"whatzap/internal/store"
	"whatzap/internal/whatsapp"
)

// Pins live in WhatsApp's own app state, so a pin made on the phone shows up
// here and a pin made here shows up on the phone. The local chat_pins table is
// only a cache of that state, so the chat list can be served without a
// connection.

// pinStore returns the store holding the pin cache, or nil when no database is
// open.
func (a *App) pinStore() *store.Store {
	st, db := a.dbHandles()
	if st != nil {
		return st
	}
	if db != nil {
		return store.Wrap(db)
	}
	return nil
}

// pinnedChats is the set of pinned chat IDs (canonical form).
func (a *App) pinnedChats() map[string]bool {
	pins, err := a.pinStore().LoadPinnedChats()
	if err != nil {
		a.actionLog.Event("pins.load.error", map[string]string{"err": truncateErr(err)})
		return map[string]bool{}
	}
	return pins
}

// applyPin records a pin or unpin reported by WhatsApp (or just sent by us) and
// tells the TUI to refetch the chat list when something actually changed.
// fromFullSync events are also collected so resyncPins can drop stale pins.
func (a *App) applyPin(jid types.JID, pinned, fromFullSync bool) {
	chatID := a.canonicalizeChatID(jid.String())
	if chatID == "" || chatID == "status@broadcast" {
		return
	}
	if fromFullSync && pinned {
		a.flagSeenMu.Lock()
		if a.pinSeen != nil {
			a.pinSeen[chatID] = true
		}
		a.flagSeenMu.Unlock()
	}
	changed, err := a.pinStore().SetChatPinned(chatID, pinned)
	if err != nil {
		a.actionLog.Event("pins.save.error", map[string]string{"err": truncateErr(err)})
		return
	}
	if changed {
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
	}
}

// resyncChatFlags does one full download of the app state that holds pins and
// archives, so ones made before this feature existed are picked up, then drops
// any local pin or archive the phone no longer has. It never drops anything
// when the download failed.
func (a *App) resyncChatFlags(client *whatsapp.Client) {
	if client == nil || client.Store == nil || client.Store.AppState == nil {
		return
	}
	a.flagSeenMu.Lock()
	a.pinSeen = map[string]bool{}
	a.archiveSeen = map[string]bool{}
	a.flagSeenMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := client.FetchAppState(ctx, appstate.WAPatchRegularLow, true, false)

	a.flagSeenMu.Lock()
	pinsSeen, archivesSeen := a.pinSeen, a.archiveSeen
	a.pinSeen, a.archiveSeen = nil, nil
	a.flagSeenMu.Unlock()

	if err != nil {
		a.actionLog.Event("chatflags.resync.error", map[string]string{"err": truncateErr(err)})
		return
	}
	st := a.pinStore()
	pinsChanged, err := st.KeepOnlyPinned(pinsSeen)
	if err != nil {
		a.actionLog.Event("chatflags.resync.error", map[string]string{"err": truncateErr(err)})
		return
	}
	archivesChanged, err := st.KeepOnlyArchived(archivesSeen)
	if err != nil {
		a.actionLog.Event("chatflags.resync.error", map[string]string{"err": truncateErr(err)})
		return
	}
	if pinsChanged || archivesChanged {
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
	}
}

// resyncChatFlagsOnce runs resyncChatFlags the first time the client connects
// in this process. Later changes arrive as ordinary app state updates.
func (a *App) resyncChatFlagsOnce(client *whatsapp.Client) {
	a.mu.Lock()
	done := a.flagsResynced
	a.flagsResynced = true
	a.mu.Unlock()
	if !done {
		a.resyncChatFlags(client)
	}
}

// handlePinChat pins or unpins a chat on WhatsApp (and so on the phone).
// Body: {"chatId": string, "pinned": bool}.
func (a *App) handlePinChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.connectedClient(w)
	if client == nil {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
		Pinned *bool  `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pinned == nil {
		writeErr(w, http.StatusBadRequest, "chatId and pinned are required")
		return
	}
	chatID := a.canonicalizeChatID(strings.TrimSpace(req.ChatID))
	if chatID == "" || chatID == "status@broadcast" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	ctx, cancel := waCallCtx()
	defer cancel()
	if err := client.SendAppState(ctx, appstate.BuildPin(jid, *req.Pinned)); err != nil {
		writeInternalErr(w, err)
		return
	}
	a.applyPin(jid, *req.Pinned, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
