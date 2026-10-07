package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func deferComposerSend(seq int) tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(time.Time) tea.Msg {
		return composerSendMsg{seq: seq}
	})
}

func (x m) replyPickCandidates() []wireMsg {
	msgs := x.msgs[x.active]
	out := make([]wireMsg, 0, len(msgs))
	_, hiddenVotes := groupPollVotes(msgs)
	for _, m := range msgs {
		if _, ok := m.Message["reactionMessage"]; ok {
			continue
		}
		if hiddenVotes[m.Key.ID] {
			continue
		}
		if pm, ok := m.Message["protocolMessage"]; ok {
			if pmm, ok := pm.(map[string]any); ok {
				if t, _ := pmm["type"].(string); t == "REVOKE" || t == "MESSAGE_EDIT" {
					continue
				}
			}
		}
		out = append(out, m)
	}
	return out
}

// editPickCandidates returns the user's own text messages sent within the
// last 20 minutes (whatsmeow's EditWindow), eligible for Alt+A editing.
func (x m) editPickCandidates() []wireMsg {
	msgs := x.msgs[x.active]
	out := make([]wireMsg, 0, len(msgs))
	cutoff := time.Now().Add(-20 * time.Minute).Unix()
	for _, msg := range msgs {
		if !msg.Key.FromMe {
			continue
		}
		if msg.MessageTimestamp < cutoff {
			continue
		}
		if _, ok := msg.Message["conversation"]; !ok {
			if _, ok2 := msg.Message["extendedTextMessage"]; !ok2 {
				continue
			}
		}
		out = append(out, msg)
	}
	return out
}

func msgRowHeight(msg wireMsg, w int) int {
	rows := 0
	// quote line
	if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
		if qt, _ := ext["quotedText"].(string); qt != "" {
			rows++
		}
	}
	// body lines
	body := renderMessageBody(msg.Message)
	wrapW := max(10, w-10)
	if body == "" {
		rows++
	} else {
		lines := strings.Split(wrapText(body, wrapW), "\n")
		rows += len(lines)
	}
	// reaction line
	if _, ok := msg.Message["reactionMessage"]; ok {
		rows++
	}
	return max(1, rows)
}

// newOutgoingMessageID picks the real WhatsApp message ID up front, in the
// same "3EB0" + uppercase-hex shape WhatsApp uses. The placeholder, the send
// response and WhatsApp's echo then all share it, so they match exactly.
func newOutgoingMessageID() string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	return "3EB0" + strings.ToUpper(hex.EncodeToString(b))
}

// isClientMessageID reports whether id was made by newOutgoingMessageID and
// can be sent to the backend as the message's real ID.
func isClientMessageID(id string) bool {
	if len(id) != 22 || !strings.HasPrefix(id, "3EB0") {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func optimisticOutgoingMessage(chatID, text, pendingID string, replyTo *wireMsg) wireMsg {
	msg := wireMsg{
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
		pending:          true,
	}
	msg.Key.ID = pendingID
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = true
	if replyTo != nil {
		participant := replyTo.Key.RemoteJID
		if replyTo.Key.Participant != "" {
			participant = replyTo.Key.Participant
		}
		quotedFromMe := replyTo.Key.FromMe
		if quotedFromMe {
			participant = ""
		}
		msg.Message = map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              text,
				"quotedText":        renderMessageBody(replyTo.Message),
				"quotedParticipant": participant,
				"quotedFromMe":      quotedFromMe,
			},
		}
		return msg
	}
	msg.Message = map[string]any{"conversation": text}
	return msg
}

// optimisticOutgoingMediaMessage builds the placeholder wireMsg shown in
// the chat the moment the user hits send on an attachment. The TUI inserts
// it before kicking off the multipart upload; the real response from
// /messages/send-file replaces it by pendingID.
func optimisticOutgoingMediaMessage(chatID, kind, fileName, caption, pendingID string) wireMsg {
	payload := map[string]any{
		"fileName": fileName,
		"caption":  caption,
	}
	message := map[string]any{}
	switch kind {
	case "image":
		message["imageMessage"] = payload
	case "video":
		message["videoMessage"] = payload
	case "audio":
		message["audioMessage"] = payload
	default:
		message["documentMessage"] = payload
	}
	msg := wireMsg{
		Message:          message,
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sending",
		pending:          true,
	}
	msg.Key.ID = pendingID
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = true
	return msg
}

// saveDraft stores the current composer text under the active chat's ID so
// it can be restored if the user switches away without sending. An empty
// composer clears any previously saved draft for that chat.
func (x *m) saveDraft() {
	if x.active == "" {
		return
	}
	text := x.input + x.inputBuf
	if text == "" {
		delete(x.drafts, x.active)
		return
	}
	if x.drafts == nil {
		x.drafts = map[string]string{}
	}
	x.drafts[x.active] = text
}

// restoreDraft loads the saved composer text for the active chat (empty if
// none was saved), replacing whatever was previously shown.
func (x *m) restoreDraft() {
	x.input = x.drafts[x.active]
	x.inputBuf = ""
	x.inputFlushScheduled = false
}

// messageIndex returns the index of message id in chatID, or -1.
func (x *m) messageIndex(chatID, id string) int {
	if id == "" {
		return -1
	}
	for i, msg := range x.msgs[chatID] {
		if msg.Key.ID == id {
			return i
		}
	}
	return -1
}

// pendingPlaceholderIndex is messageIndex restricted to unconfirmed
// placeholders.
func (x *m) pendingPlaceholderIndex(chatID, id string) int {
	if i := x.messageIndex(chatID, id); i >= 0 && x.msgs[chatID][i].pending {
		return i
	}
	return -1
}

// removeMessage drops message id from chatID if present.
func (x *m) removeMessage(chatID, id string) {
	i := x.messageIndex(chatID, id)
	if i < 0 {
		return
	}
	msgs := x.msgs[chatID]
	x.msgs[chatID] = append(msgs[:i], msgs[i+1:]...)
}

// applySentMessage merges a successful send response. The placeholder and
// the real message normally share an ID; the placeholder is only swapped
// for the response while still unconfirmed, so a WhatsApp echo (and any
// receipt already applied to it) that arrived first is never overwritten.
// If the backend returned a different ID and WhatsApp's copy already
// arrived under it, the leftover placeholder is dropped instead.
func (x *m) applySentMessage(chatID, pendingID string, sent wireMsg) {
	pendIdx := x.messageIndex(chatID, pendingID)
	realIdx := x.messageIndex(chatID, sent.Key.ID)
	switch {
	case pendIdx >= 0 && realIdx >= 0 && pendIdx != realIdx:
		if x.msgs[chatID][pendIdx].pending {
			x.removeMessage(chatID, pendingID)
		}
	case pendIdx >= 0:
		if x.msgs[chatID][pendIdx].pending {
			x.msgs[chatID][pendIdx] = sent
		}
	case realIdx < 0:
		x.msgs[chatID] = append(x.msgs[chatID], sent)
	}
}

// restoreFailedSendText gives back the text of a send that failed. It only
// fills an empty composer (or, if the user has switched chats, an empty
// draft) so it never overwrites something new the user has started typing.
func (x *m) restoreFailedSendText(chatID, text string) {
	if chatID == "" || text == "" {
		return
	}
	if chatID == x.active {
		if x.input == "" && x.inputBuf == "" {
			x.input = text
		}
		return
	}
	if x.drafts[chatID] == "" {
		if x.drafts == nil {
			x.drafts = map[string]string{}
		}
		x.drafts[chatID] = text
	}
}

func (x *m) clearChatComposer() {
	x.input = ""
	x.inputBuf = ""
	x.inputFlushScheduled = false
	x.inputAllSelected = false
	x.clearPendingAttachment()
	x.replyTo = nil
	x.replyPickMode = false
	x.replyPickIndex = 0
	x.selectedMsgID = ""
	x.closeEmojiPicker()
	x.closeMentionPicker()
	x.composerMentions = nil
}
