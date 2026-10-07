package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"whatzap/internal/whatsapp"
)

// pollVotePlan is a vote that passed every check and is ready to be built.
type pollVotePlan struct {
	chatID  string
	chat    types.JID
	pollID  string
	options []string // the choice in the poll's own spelling, empty to withdraw
	fromMe  bool     // the poll was sent by this account
	// participant is who sent the poll in a group, as stored.
	participant string
	isGroup     bool
}

// resolvePollVote checks a vote request against the stored poll. On failure it
// returns the HTTP status and message to answer with. The chat must be
// whitelisted, like any outgoing message.
func (a *App) resolvePollVote(chatRaw, pollID string, chosen []string) (plan pollVotePlan, status int, errMsg string) {
	chatID := a.canonicalizeChatID(strings.TrimSpace(chatRaw))
	pollID = strings.TrimSpace(pollID)
	if chatID == "" || pollID == "" {
		return plan, http.StatusBadRequest, "chatId and pollMessageId are required"
	}
	if chatID == "status@broadcast" {
		return plan, http.StatusBadRequest, "invalid chatId"
	}
	chat, err := types.ParseJID(chatID)
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
	st := a.pinStore()
	raw, err := st.GetMessageJSON(chatID, pollID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return plan, http.StatusNotFound, "poll not found"
	}
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	poll, _ := stored["pollCreationMessage"].(map[string]any)
	if poll == nil {
		return plan, http.StatusBadRequest, "that message is not a poll"
	}
	var pollOptions []string
	if list, ok := poll["options"].([]any); ok {
		for _, o := range list {
			if s, ok := o.(string); ok {
				pollOptions = append(pollOptions, s)
			}
		}
	}
	// Polls saved before the limit was recorded have none: allow any number.
	selectable := -1
	if n, ok := poll["selectableCount"].(float64); ok {
		selectable = int(n)
	}
	options, err := whatsapp.ValidateVote(pollOptions, selectable, chosen)
	if err != nil {
		return plan, http.StatusBadRequest, err.Error()
	}
	fromMe, participant, err := st.GetMessageOrigin(chatID, pollID)
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	return pollVotePlan{
		chatID:      chatID,
		chat:        chat,
		pollID:      pollID,
		options:     options,
		fromMe:      fromMe,
		participant: participant,
		isGroup:     chat.Server == types.GroupServer,
	}, 0, ""
}

// jidForms lists the ways WhatsApp can name a person: the address as given and,
// when known, its phone number or LID twin. The poll's secret is stored under
// one of them.
func (a *App) jidForms(client *whatsmeow.Client, jid types.JID) []types.JID {
	forms := []types.JID{jid}
	if client == nil || client.Store == nil || client.Store.LIDs == nil {
		return forms
	}
	switch jid.Server {
	case types.HiddenUserServer:
		if pn, err := a.getPNForLID(jid); err == nil && pn.User != "" {
			forms = append(forms, pn)
		}
	case types.DefaultUserServer:
		if lid, err := client.Store.LIDs.GetLIDForPN(context.Background(), jid); err == nil && lid.User != "" {
			forms = append(forms, lid)
		}
	}
	return forms
}

// buildPollVote builds the encrypted vote. The vote is keyed to the poll's chat
// and sender, and the poll's secret was saved under one spelling of each, so the
// combinations are tried until one is found.
func (a *App) buildPollVote(client *whatsmeow.Client, plan pollVotePlan) (*waE2E.Message, error) {
	chats := a.jidForms(client, plan.chat)
	var senders []types.JID
	switch {
	case plan.fromMe:
		for _, own := range []types.JID{client.Store.GetLID(), client.Store.GetJID()} {
			if !own.IsEmpty() {
				senders = append(senders, own.ToNonAD())
			}
		}
	case plan.isGroup:
		if sender, err := types.ParseJID(plan.participant); err == nil {
			senders = a.jidForms(client, sender)
		}
	default:
		senders = a.jidForms(client, plan.chat)
	}
	if len(senders) == 0 {
		return nil, whatsmeow.ErrOriginalMessageSecretNotFound
	}
	ctx := context.Background()
	for _, chat := range chats {
		for _, sender := range senders {
			info := &types.MessageInfo{
				MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: plan.fromMe, IsGroup: plan.isGroup},
				ID:            plan.pollID,
			}
			msg, err := client.BuildPollVote(ctx, info, plan.options)
			if err == nil {
				return msg, nil
			}
			if !errors.Is(err, whatsmeow.ErrOriginalMessageSecretNotFound) {
				return nil, err
			}
		}
	}
	return nil, whatsmeow.ErrOriginalMessageSecretNotFound
}

// handleVotePoll sends this account's vote on a poll, or withdraws it when no
// options are given. Body: {"chatId", "pollMessageId", "options": [...]}.
func (a *App) handleVotePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.connectedClient(w)
	if client == nil {
		return
	}
	var req struct {
		ChatID        string   `json:"chatId"`
		PollMessageID string   `json:"pollMessageId"`
		Options       []string `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	plan, status, msg := a.resolvePollVote(req.ChatID, req.PollMessageID, req.Options)
	if status != 0 {
		writeErr(w, status, msg)
		return
	}
	vote, err := a.buildPollVote(client.Client, plan)
	if errors.Is(err, whatsmeow.ErrOriginalMessageSecretNotFound) {
		a.actionLog.Event("poll.vote.unsendable", map[string]string{"why": "secret-not-found"})
		writeErr(w, http.StatusUnprocessableEntity, "this poll cannot be voted on here: its vote key was not saved")
		return
	}
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	ctx, cancel := waCallCtx()
	defer cancel()
	resp, err := client.SendMessage(ctx, plan.chat, vote)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	wire := WireMessage{
		Key: WireKey{ID: resp.ID, RemoteJID: plan.chatID, FromMe: true},
		Message: map[string]any{"pollUpdateMessage": map[string]any{
			"pollChatID":          plan.chatID,
			"pollMsgID":           plan.pollID,
			"selectedOptionNames": plan.options,
		}},
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(plan.chatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}
