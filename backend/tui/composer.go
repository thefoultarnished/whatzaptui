package main

import (
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
	for _, m := range msgs {
		if _, ok := m.Message["reactionMessage"]; ok {
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

func optimisticOutgoingMessage(chatID, text, pendingID string, replyTo *wireMsg) wireMsg {
	msg := wireMsg{
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sent",
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
	default:
		message["documentMessage"] = payload
	}
	msg := wireMsg{
		Message:          message,
		MessageTimestamp: time.Now().Unix(),
		ReceiptStatus:    "sending",
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
}
