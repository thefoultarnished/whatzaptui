package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// toggleArchive archives the target chat, or unarchives it if it is already
// archived. The change is sent to WhatsApp so it also shows on the phone; the
// chat moves between the normal and archived lists when the backend reports
// the new state.
func (x *m) toggleArchive(includeGlobal bool) tea.Cmd {
	idx := x.chatTarget(includeGlobal)
	if idx < 0 {
		return x.setTopBar("No chat selected to archive")
	}
	archive := !x.chats[idx].Archived
	if x.demoMode {
		selectedID := x.selectedChatID()
		x.chats[idx].Archived = archive
		if archive {
			x.chats[idx].Pinned = false
		}
		x.resortChats(selectedID)
		x.ensureSideVisible(x.sideViewRows())
		x.invalidate()
		if archive {
			return x.setTopBar("Archived")
		}
		return x.setTopBar("Unarchived")
	}
	msg := "Archiving..."
	if !archive {
		msg = "Unarchiving..."
	}
	body := map[string]any{"chatId": x.chats[idx].ID, "archived": archive}
	return tea.Batch(
		x.setTopBar(msg),
		postJSON(x.reqCtx(), x.client, x.baseURL+"/chats/archive", body, ignoreBody),
	)
}

// toggleArchivedView switches the Chats tab between the normal chats and the
// archived ones. Use alt+c or run it again to go back.
func (x *m) toggleArchivedView() tea.Cmd {
	x.archivedView = !x.archivedView
	x.sidebarTab = "chats"
	x.sidebarFocused = true
	x.leftInputFocused = false
	if x.mode == "chat" || x.mode == "search" {
		x.mode = "nav"
	}
	x.search, x.searchInput = "", ""
	x.sel, x.sideScroll = 0, 0
	x.invalidate()
	x.ensureSideVisible(x.sideViewRows())
	if x.archivedView {
		return x.setTopBar("Archived chats (/archived or alt+c to go back)")
	}
	return x.setTopBar("Chats")
}
