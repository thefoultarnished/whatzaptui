package backend

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"whatzap/internal/whatsapp"
)

// pollPlan is a poll that passed every check and is ready to send.
type pollPlan struct {
	chatID   string
	jid      types.JID
	question string
	options  []string
	msg      *waE2E.Message
}

// resolvePoll checks a poll request and builds the message. On failure it
// returns the HTTP status and message to answer with. The chat must be
// whitelisted, like any outgoing message.
func (a *App) resolvePoll(chatRaw, question string, options []string, multiple bool) (plan pollPlan, status int, errMsg string) {
	chatID := a.canonicalizeChatID(strings.TrimSpace(chatRaw))
	if chatID == "" {
		return plan, http.StatusBadRequest, "chatId is required"
	}
	if chatID == "status@broadcast" {
		return plan, http.StatusBadRequest, "invalid chatId"
	}
	jid, err := types.ParseJID(chatID)
	if err != nil {
		return plan, http.StatusBadRequest, "invalid chatId"
	}
	allowed, err := a.isChatAllowed(chatID)
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	if !allowed {
		return plan, http.StatusForbidden, "chat not whitelisted"
	}
	cleanOptions := make([]string, len(options))
	for i, o := range options {
		cleanOptions[i] = sanitizeOutgoingText(o)
	}
	question, cleanOptions, err = whatsapp.CleanPoll(sanitizeOutgoingText(question), cleanOptions)
	if err != nil {
		return plan, http.StatusBadRequest, err.Error()
	}
	msg, err := whatsapp.BuildPollCreationMessage(question, cleanOptions, multiple)
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	return pollPlan{chatID: chatID, jid: jid, question: question, options: cleanOptions, msg: msg}, 0, ""
}

// handleSendPoll sends a poll to a chat.
// Body: {"chatId", "question", "options": [...], "multiple": bool}.
func (a *App) handleSendPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.connectedClient(w)
	if client == nil {
		return
	}
	var req struct {
		ChatID   string   `json:"chatId"`
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Multiple bool     `json:"multiple"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	plan, status, msg := a.resolvePoll(req.ChatID, req.Question, req.Options, req.Multiple)
	if status != 0 {
		writeErr(w, status, msg)
		return
	}

	ctx, cancel := waCallCtx()
	defer cancel()
	resp, err := client.SendMessage(ctx, plan.jid, plan.msg)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	wireBody, _ := a.wireMessagePayload(plan.msg, plan.msg, plan.chatID, strings.HasSuffix(plan.chatID, "@g.us"))
	wire := WireMessage{
		Key:              WireKey{ID: resp.ID, RemoteJID: plan.chatID, FromMe: true},
		Message:          wireBody,
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(plan.chatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}
