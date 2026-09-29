package backend

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"whatzap/internal/whatsapp"
)

// setShowOnline records whether the user wants to appear online. It defaults
// to false (hidden) until the TUI reports its setting, so a fresh connection
// never shows the user online by accident.
func (a *App) setShowOnline(v bool) {
	a.mu.Lock()
	a.showOnline = v
	a.mu.Unlock()
}

func (a *App) wantsOnline() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.showOnline
}

// ownPresenceState is the presence to publish for the current setting.
func (a *App) ownPresenceState() types.Presence {
	if a.wantsOnline() {
		return types.PresenceAvailable
	}
	return types.PresenceUnavailable
}

// sendOwnPresence publishes the user's presence per the show-online setting.
func (a *App) sendOwnPresence(client *whatsapp.Client) error {
	ctx, cancel := waCallCtx()
	defer cancel()
	return client.SendPresence(ctx, a.ownPresenceState())
}

// handlePresence sets whether other people can see the user online.
// Body: {"online": bool}. The value is remembered and applied on every
// (re)connect; if already connected it is published right away.
func (a *App) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Online *bool `json:"online"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Online == nil {
		writeErr(w, http.StatusBadRequest, "online is required")
		return
	}
	a.setShowOnline(*req.Online)
	if client := a.getClient(); client != nil && client.IsConnected() && client.IsLoggedIn() {
		if err := a.sendOwnPresence(client); err != nil {
			a.actionLog.Event("client.presence.error", map[string]string{"err": truncateErr(err)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handlePresenceSubscribe asks WhatsApp to report a contact's online status.
// Body: {"chatId": string}. Only one-to-one chats have presence.
func (a *App) handlePresenceSubscribe(w http.ResponseWriter, r *http.Request) {
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	chatID := a.canonicalizeChatID(strings.TrimSpace(req.ChatID))
	if chatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	jid, err := types.ParseJID(chatID)
	if err != nil || (jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer) {
		writeErr(w, http.StatusBadRequest, "chatId must be a one-to-one chat")
		return
	}
	ctx, cancel := waCallCtx()
	defer cancel()
	if err := client.SubscribePresence(ctx, jid); err != nil {
		// Not fatal for the user: the header just shows nothing.
		a.actionLog.Event("client.presence.subscribe.error", map[string]string{
			"chat": redactChatID(chatID),
			"err":  truncateErr(err),
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// wirePresence converts a WhatsApp presence event to the WS payload.
// lastSeen is 0 when the contact hides it.
func (a *App) wirePresence(v *events.Presence) map[string]any {
	var lastSeen int64
	if !v.LastSeen.IsZero() {
		lastSeen = v.LastSeen.Unix()
	}
	return map[string]any{
		"chatId":   a.canonicalizeChatID(v.From.String()),
		"online":   !v.Unavailable,
		"lastSeen": lastSeen,
	}
}
