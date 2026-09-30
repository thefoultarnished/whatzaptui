package backend

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"whatzap/internal/whatsapp"
)

// forwardableText returns the text of a stored message, or false when it has
// none.
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

// hasForwardableMedia reports whether a stored message holds an image, video,
// file or audio.
func hasForwardableMedia(msg map[string]any) bool {
	for _, kind := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage"} {
		if _, ok := msg[kind].(map[string]any); ok {
			return true
		}
	}
	return false
}

// withForwardedMark adds the forwarded flag to a media entry of the wire payload
// when the message carries WhatsApp's forwarded mark.
func withForwardedMark(entry map[string]any, ctx *waE2E.ContextInfo) map[string]any {
	if ctx.GetIsForwarded() {
		entry["forwarded"] = true
	}
	return entry
}

// forwardPlan is what to send for a forward request: text, or a media message
// built from the stored one. stale is set when the media link is too old to
// reuse, so the file has to be downloaded and uploaded again.
type forwardPlan struct {
	to    string
	toJID types.JID
	text  string
	media *waE2E.Message
	kind  string
	stale bool
	// source is the stored message, used to download the file for a stale link.
	source *waE2E.Message
}

// resolveForward checks a forward request and returns what to send. On
// failure it returns the HTTP status and message to answer with. The target
// chat must be whitelisted, like any outgoing message.
func (a *App) resolveForward(fromRaw, messageID, toRaw string) (plan forwardPlan, status int, errMsg string) {
	from := a.canonicalizeChatID(strings.TrimSpace(fromRaw))
	to := a.canonicalizeChatID(strings.TrimSpace(toRaw))
	msgID := strings.TrimSpace(messageID)
	if from == "" || to == "" || msgID == "" {
		return plan, http.StatusBadRequest, "fromChatId, messageId and toChatId are required"
	}
	if to == "status@broadcast" {
		return plan, http.StatusBadRequest, "invalid toChatId"
	}
	toJID, err := types.ParseJID(to)
	if err != nil {
		return plan, http.StatusBadRequest, "invalid toChatId"
	}
	allowed, err := a.isChatAllowed(to)
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	if !allowed {
		return plan, http.StatusForbidden, "chat not whitelisted"
	}
	st := a.pinStore()
	raw, err := st.GetMessageJSON(from, msgID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return plan, http.StatusNotFound, "message not found"
	}
	if err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return plan, http.StatusInternalServerError, "internal error"
	}
	plan.to, plan.toJID = to, toJID
	if text, ok := forwardableText(stored); ok {
		plan.text = sanitizeOutgoingText(text)
		return plan, 0, ""
	}
	if !hasForwardableMedia(stored) {
		return forwardPlan{}, http.StatusBadRequest, whatsapp.ErrNoForwardableMedia.Error()
	}
	mediaB64, err := st.GetMediaProto(from, msgID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && mediaB64 == "") {
		return forwardPlan{}, http.StatusNotFound, "media not available"
	}
	if err != nil {
		return forwardPlan{}, http.StatusInternalServerError, "internal error"
	}
	b, err := base64.StdEncoding.DecodeString(mediaB64)
	if err != nil {
		return forwardPlan{}, http.StatusInternalServerError, "internal error"
	}
	var source waE2E.Message
	if err := proto.Unmarshal(b, &source); err != nil {
		return forwardPlan{}, http.StatusInternalServerError, "internal error"
	}
	media, kind, err := whatsapp.BuildForwardedMediaMessage(&source)
	if err != nil {
		return forwardPlan{}, http.StatusBadRequest, err.Error()
	}
	ts, _ := st.GetMessageTimestamp(from, msgID)
	plan.media, plan.kind, plan.source = media, kind, &source
	plan.stale = whatsapp.MediaLinkStale(ts, time.Now())
	return plan, 0, ""
}

// handleForwardMessage sends a copy of a stored message to another chat, marked
// as forwarded. Text is copied as text. Media reuses the stored link and key, or
// is uploaded again when the link is too old. Body: {"fromChatId", "messageId",
// "toChatId"}.
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
	plan, status, msg := a.resolveForward(req.FromChatID, req.MessageID, req.ToChatID)
	if status != 0 {
		writeErr(w, status, msg)
		return
	}

	out := whatsapp.BuildForwardedTextMessage(plan.text)
	wireBody := map[string]any{"extendedTextMessage": map[string]any{"text": plan.text, "forwarded": true}}
	mediaProto := ""
	if plan.media != nil {
		out = plan.media
		if plan.stale && !a.reuploadForwardedMedia(w, plan) {
			return
		}
		wireBody, mediaProto = a.wireMessagePayload(out, out, plan.to, false)
	}

	ctx, cancel := waCallCtx()
	defer cancel()
	resp, err := client.SendMessage(ctx, plan.toJID, out)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	wire := WireMessage{
		Key:              WireKey{ID: resp.ID, RemoteJID: plan.to, FromMe: true},
		Message:          wireBody,
		MessageTimestamp: time.Now().Unix(),
		MediaProto:       mediaProto,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(plan.to, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}

// reuploadForwardedMedia downloads the original file and uploads it again, then
// points the forwarded message at the new upload. It writes the error reply and
// returns false when the file can no longer be fetched.
func (a *App) reuploadForwardedMedia(w http.ResponseWriter, plan forwardPlan) bool {
	client := a.getClient()
	if client == nil {
		writeErr(w, http.StatusConflict, "not connected")
		return false
	}
	mediaType, err := mediaTypeForSendKind(plan.kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	dlCtx, dlCancel := waDownloadCtx()
	data, err := client.DownloadAny(dlCtx, plan.source)
	dlCancel()
	if err != nil {
		writeErr(w, http.StatusGone, "media is no longer available, it cannot be forwarded")
		return false
	}
	upCtx, upCancel := waUploadCtx()
	upload, err := client.Upload(upCtx, data, mediaType)
	upCancel()
	if err != nil {
		writeInternalErr(w, err)
		return false
	}
	if err := whatsapp.ReplaceMediaUpload(plan.media, upload); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}
