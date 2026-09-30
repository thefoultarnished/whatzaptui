package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"whatzap/internal/tui/picker"
)

// forwardedMsg is sent when the backend has delivered a forwarded message.
type forwardedMsg struct{ to string }

// forwardText returns the text of a text message, or false when it has none.
func forwardText(msg wireMsg) (string, bool) {
	if t, ok := msg.Message["conversation"].(string); ok {
		return t, strings.TrimSpace(t) != ""
	}
	if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
		if t, ok := ext["text"].(string); ok {
			return t, strings.TrimSpace(t) != ""
		}
	}
	return "", false
}

// forwardMediaKinds are the media kinds that can be forwarded.
var forwardMediaKinds = []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage"}

// canForward reports whether a message can be forwarded: text, or an image,
// video, file or audio.
func canForward(msg wireMsg) bool {
	if _, ok := forwardText(msg); ok {
		return true
	}
	for _, kind := range forwardMediaKinds {
		if _, ok := msg.Message[kind].(map[string]any); ok {
			return true
		}
	}
	return false
}

// isForwarded reports whether a message carries WhatsApp's "Forwarded" mark.
func isForwarded(msg map[string]any) bool {
	for _, kind := range append([]string{"extendedTextMessage"}, forwardMediaKinds...) {
		entry, ok := msg[kind].(map[string]any)
		if !ok {
			continue
		}
		if f, _ := entry["forwarded"].(bool); f {
			return true
		}
	}
	return false
}

// forwardedLabel is drawn above the text of a forwarded message.
const forwardedLabel = "↪ Forwarded"

// openForwardPicker starts forwarding the selected message (Alt+T): it opens a
// list of chats to send a copy to.
func (x *m) openForwardPicker() tea.Cmd {
	if x.selectedMsgID == "" {
		return x.setTopBar("Select a message first (Alt+R or click), then Alt+T")
	}
	var src *wireMsg
	for i := range x.msgs[x.active] {
		if x.msgs[x.active][i].Key.ID == x.selectedMsgID {
			src = &x.msgs[x.active][i]
			break
		}
	}
	if src == nil {
		return x.setTopBar("Select a message first (Alt+R or click), then Alt+T")
	}
	if !canForward(*src) {
		return x.setTopBar("Only text, image, video, file and audio messages can be forwarded")
	}
	var items []picker.Item
	for _, c := range x.chats {
		if c.ID == "" || c.ID == "status@broadcast" || c.Archived || num(c.ID) == num(x.active) {
			continue
		}
		it := picker.Item{Key: c.ID, Label: x.name(c)}
		if !x.isAllowed(num(c.ID)) {
			it.Dim = true
			it.Desc = "not allowed"
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return x.setTopBar("No other chat to forward to")
	}
	x.replyPickMode = false
	x.editPickMode = false
	x.forwardMsgID = src.Key.ID
	x.forwardFromChat = x.active
	x.forwardPicker = picker.New("Forward to", items)
	x.forwardPicker.Open("")
	x.invalidate()
	return nil
}

// closeForwardPicker closes the list and clears the message selection.
func (x *m) closeForwardPicker() {
	x.forwardPicker.Close(false)
	x.selectedMsgID = ""
	x.invalidate()
}

// finishForwardPicker acts on Enter: it forwards to the highlighted chat, or
// explains why it cannot.
func (x *m) finishForwardPicker() tea.Cmd {
	items := x.forwardPicker.VisibleItems()
	idx := x.forwardPicker.Idx
	msgID, from := x.forwardMsgID, x.forwardFromChat
	x.closeForwardPicker()
	if idx < 0 || idx >= len(items) {
		return nil
	}
	target := items[idx]
	if target.Dim {
		return x.setTopBar(target.Label + " is not allowed yet: whitelist it first (Alt+W)")
	}
	if x.demoMode {
		return x.setTopBar("Forwarded to " + target.Label)
	}
	body := map[string]string{"fromChatId": from, "messageId": msgID, "toChatId": target.Key}
	name := target.Label
	return tea.Batch(
		x.setTopBar("Forwarding to "+name+"..."),
		postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/forward", body, func([]byte) tea.Msg { return forwardedMsg{to: name} }),
	)
}
