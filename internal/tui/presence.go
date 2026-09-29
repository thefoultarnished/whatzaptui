package tui

import (
	"encoding/json"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// presenceInfo is what we know about a contact's online status.
// lastSeen is 0 when the contact hides it.
type presenceInfo struct {
	online   bool
	lastSeen int64
}

// applyPresence stores a "presence" event payload.
func (x *m) applyPresence(payload []byte) bool {
	var p struct {
		ChatID   string `json:"chatId"`
		Online   bool   `json:"online"`
		LastSeen int64  `json:"lastSeen"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.ChatID == "" {
		return false
	}
	if x.presence == nil {
		x.presence = map[string]presenceInfo{}
	}
	x.presence[p.ChatID] = presenceInfo{online: p.Online, lastSeen: p.LastSeen}
	return true
}

// presenceLabel is the header text for a one-to-one chat: "" (show nothing)
// unless the contact is online, or offline with a visible last seen. A contact
// who hides their status never produces a label.
func (x m) presenceLabel(chatID string) (label string, online bool) {
	if strings.HasSuffix(chatID, "@g.us") {
		return "", false
	}
	p, ok := x.presence[chatID]
	if !ok {
		return "", false
	}
	if p.online {
		return "online", true
	}
	if p.lastSeen > 0 {
		return "offline", false
	}
	return "", false
}

func ignoreBody([]byte) tea.Msg { return nil }

// sendShowOnlineCmd tells the backend whether other people may see us online.
func (x m) sendShowOnlineCmd() tea.Cmd {
	if x.demoMode {
		return nil
	}
	return postJSON(x.reqCtx(), x.client, x.baseURL+"/presence", map[string]bool{"online": currentConfig.ShowOnline}, ignoreBody)
}

// subscribePresenceCmd asks for the active chat's online status. Groups have
// no presence, so only one-to-one chats are subscribed.
func (x m) subscribePresenceCmd() tea.Cmd {
	if x.demoMode || x.active == "" || strings.HasSuffix(x.active, "@g.us") {
		return nil
	}
	return postJSON(x.reqCtx(), x.client, x.baseURL+"/presence/subscribe", map[string]string{"chatId": x.active}, ignoreBody)
}

// presenceSyncCmd re-applies the setting and re-subscribes the open chat. Run
// it whenever the backend may have (re)connected.
func (x m) presenceSyncCmd() tea.Cmd {
	return tea.Batch(x.sendShowOnlineCmd(), x.subscribePresenceCmd())
}
