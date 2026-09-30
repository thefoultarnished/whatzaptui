package backend

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"whatzap/internal/whatsapp"
)

// forwardableText returns the text of a stored message, or false when it has
// none. Only text messages can be forwarded for now.
func forwardableText(msg map[string]any) (string, bool) {
	if t, ok := msg["conversation"].(string); ok {
		return t, hasVisibleText(t)
	}
	if ext, ok := msg["extendedTextMessage"].(map[string]any); ok {
		if t, ok := ext["text"].(string); ok {
			return t, hasVisibleText(t)
		}
	}
	return "", false
}

// resolveForward checks a forward request and returns what to send. On
// failure it returns the HTTP status and message to answer with. The target
// chat must be whitelisted, like any outgoing message.
func (a *App) resolveForward(fromRaw, messageID, toRaw string) (text, to string, toJID types.JID, status int, errMsg string) {
	from := a.canonicalizeChatID(strings.TrimSpace(fromRaw))
	to = a.canonicalizeChatID(strings.TrimSpace(toRaw))
	msgID := strings.TrimSpace(messageID)
	if from == "" || to == "" || msgID == "" {
		return "", "", toJID, http.StatusBadRequest, "fromChatId, messageId and toChatId are required"
	}
	if to == "status@broadcast" {
		return "", "", toJID, http.StatusBadRequest, "invalid toChatId"
	}
	toJID, err := types.ParseJID(to)
	if err != nil {
		return "", "", toJID, http.StatusBadRequest, "invalid toChatId"
	}
	allowed, err := a.isChatAllowed(to)
	if err != nil {
		return "", "", toJID, http.StatusInternalServerError, "internal error"
	}
	if !allowed {
		return "", "", toJID, http.StatusForbidden, "chat not whitelisted"
	}
	raw, err := a.pinStore().GetMessageJSON(from, msgID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return "", "", toJID, http.StatusNotFound, "message not found"
	}
	if err != nil {
		return "", "", toJID, http.StatusInternalServerError, "internal error"
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return "", "", toJID, http.StatusInternalServerError, "internal error"
	}
	text, ok := forwardableText(stored)
	if !ok {
		return "", "", toJID, http.StatusBadRequest, "only text messages can be forwarded"
	}
	return sanitizeOutgoingText(text), to, toJID, 0, ""
}

// handleForwardMessage sends a copy of a stored text message to another chat,
// marked as forwarded. Body: {"fromChatId", "messageId", "toChatId"}.
func (a *App) handleForwardMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.connectedClient(w)
	if client == nil {
		return
	}
	var req struct {
		FromChatID string `json:"fromChatId"`
		MessageID  string `json:"messageId"`
		ToChatID   string `json:"toChatId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	text, to, toJID, status, msg := a.resolveForward(req.FromChatID, req.MessageID, req.ToChatID)
	if status != 0 {
		writeErr(w, status, msg)
		return
	}

	ctx, cancel := waCallCtx()
	defer cancel()
	resp, err := client.SendMessage(ctx, toJID, whatsapp.BuildForwardedTextMessage(text))
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	wire := WireMessage{
		Key:              WireKey{ID: resp.ID, RemoteJID: to, FromMe: true},
		Message:          map[string]any{"extendedTextMessage": map[string]any{"text": text, "forwarded": true}},
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(to, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}
