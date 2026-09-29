package backend

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

// Archives live in WhatsApp's own app state, like pins: an archive made on the
// phone shows up here and one made here shows up on the phone. WhatsApp also
// brings a chat back out of the archive itself when a new message arrives (the
// phone sends that as an ordinary unarchive), so no local rule is needed for it.

// archivedChats is the set of archived chat IDs (canonical form).
func (a *App) archivedChats() map[string]bool {
	archived, err := a.pinStore().LoadArchivedChats()
	if err != nil {
		a.actionLog.Event("archives.load.error", map[string]string{"err": truncateErr(err)})
		return map[string]bool{}
	}
	return archived
}

// applyArchive records an archive or unarchive reported by WhatsApp (or just
// sent by us) and tells the TUI to refetch when something changed. Archiving
// also unpins the chat, as WhatsApp does.
func (a *App) applyArchive(jid types.JID, archived, fromFullSync bool) {
	chatID := a.canonicalizeChatID(jid.String())
	if chatID == "" || chatID == "status@broadcast" {
		return
	}
	if fromFullSync && archived {
		a.flagSeenMu.Lock()
		if a.archiveSeen != nil {
			a.archiveSeen[chatID] = true
		}
		a.flagSeenMu.Unlock()
	}
	st := a.pinStore()
	changed, err := st.SetChatArchived(chatID, archived)
	if err != nil {
		a.actionLog.Event("archives.save.error", map[string]string{"err": truncateErr(err)})
		return
	}
	if archived && !fromFullSync {
		if unpinned, err := st.SetChatPinned(chatID, false); err == nil && unpinned {
			changed = true
		}
	}
	if changed {
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
	}
}

// handleArchiveChat archives or unarchives a chat on WhatsApp (and so on the
// phone). Body: {"chatId": string, "archived": bool}.
func (a *App) handleArchiveChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.connectedClient(w)
	if client == nil {
		return
	}
	var req struct {
		ChatID   string `json:"chatId"`
		Archived *bool  `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Archived == nil {
		writeErr(w, http.StatusBadRequest, "chatId and archived are required")
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
	var lastMsg time.Time
	a.mu.RLock()
	if c, ok := a.state.Chats[chatID]; ok && c.ConversationTimestamp > 0 {
		lastMsg = time.Unix(c.ConversationTimestamp, 0)
	}
	a.mu.RUnlock()

	ctx, cancel := waCallCtx()
	defer cancel()
	if err := client.SendAppState(ctx, appstate.BuildArchive(jid, *req.Archived, lastMsg, nil)); err != nil {
		writeInternalErr(w, err)
		return
	}
	a.applyArchive(jid, *req.Archived, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
