package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

const (
	// pinMarkFallback is the plain pin mark, one cell wide in any font.
	pinMarkFallback = "▲"
	// pinMarkNerd is nf-cod-pinned (Nerd Fonts v3+, Codicons), also one cell wide.
	pinMarkNerd = ""
)

// pinMark is drawn in a pinned chat's row: the Nerd Font pin when Nerd Font
// icons are switched on (same switch as the media icons), else a plain triangle
// so terminals without a Nerd Font never show a blank box.
func pinMark() string {
	if currentConfig.MediaIconStyle == "nerd" || currentConfig.MediaViewStyle == "glyph" {
		return pinMarkNerd
	}
	return pinMarkFallback
}

// chatTarget is the chat /pin and /archive act on: the highlighted chat when
// the command comes from the command bar, otherwise the open chat. It returns
// the index into x.chats, or -1 when there is no such chat.
func (x m) chatTarget(includeGlobal bool) int {
	target := x.active
	if includeGlobal {
		if items := x.filtered(); x.sel >= 0 && x.sel < len(items) {
			target = items[x.sel].ID
		}
	}
	if target == "" {
		return -1
	}
	for i := range x.chats {
		if x.chats[i].ID == target {
			return i
		}
	}
	return -1
}

// togglePin pins the target chat, or unpins it if it is already pinned. The
// change is sent to WhatsApp so it also shows on the phone; the chat list
// reorders when the backend reports the new state.
func (x *m) togglePin(includeGlobal bool) tea.Cmd {
	idx := x.chatTarget(includeGlobal)
	if idx < 0 {
		return x.setTopBar("No chat selected to pin")
	}
	if x.chats[idx].Archived {
		return x.setTopBar("Archived chats can't be pinned, unarchive it first")
	}
	pin := !x.chats[idx].Pinned
	if x.demoMode {
		selectedID := x.selectedChatID()
		x.chats[idx].Pinned = pin
		x.resortChats(selectedID)
		x.invalidate()
		if pin {
			return x.setTopBar("Pinned")
		}
		return x.setTopBar("Unpinned")
	}
	msg := "Pinning..."
	if !pin {
		msg = "Unpinning..."
	}
	body := map[string]any{"chatId": x.chats[idx].ID, "pinned": pin}
	return tea.Batch(
		x.setTopBar(msg),
		postJSON(x.reqCtx(), x.client, x.baseURL+"/chats/pin", body, ignoreBody),
	)
}
