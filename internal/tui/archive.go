package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// archivedStats counts archived chats and their unread messages.
func (x m) archivedStats() (chats, unread int) {
	for _, c := range x.chats {
		if c.Archived {
			chats++
			unread += c.UnreadCount
		}
	}
	return chats, unread
}

// showArchiveFooter reports whether the fixed "Archived" / "Chats" line is
// drawn under the chat list. It shows in the archived view (so there is always
// a way back) and in the normal view once any chat is archived. Drawing and the
// list height both use it, so they cannot disagree.
func (x m) showArchiveFooter() bool {
	if x.sidebarTab != "chats" {
		return false
	}
	if x.archivedView {
		return true
	}
	n, _ := x.archivedStats()
	return n > 0
}

// archiveFooterHeight is the number of sidebar rows the footer takes.
func (x m) archiveFooterHeight() int {
	if x.showArchiveFooter() {
		return 1
	}
	return 0
}

// renderArchiveFooter draws the footer for a sidebar of width w. It is not a
// chat: it is not in the list, so it has no number and cannot be highlighted.
// Only alt+d uses it.
func (x m) renderArchiveFooter(w int) string {
	shortcut := lipgloss.NewStyle().Foreground(accent).Italic(true).Render("alt+d")
	var left string
	if x.archivedView {
		left = lipgloss.NewStyle().Foreground(muted).Render("‹ Chats")
	} else {
		left = lipgloss.NewStyle().Foreground(muted).Render("Archived")
		if _, unread := x.archivedStats(); unread > 0 {
			left += " " + lipgloss.NewStyle().Foreground(accent).Bold(true).Render(unreadBadgeText(unread))
		}
	}
	pad := w - 2 - lipgloss.Width(left) - lipgloss.Width(shortcut)
	if pad < 1 {
		// Too narrow for both: keep the label.
		return " " + left
	}
	return " " + left + strings.Repeat(" ", pad) + shortcut + " "
}

// archiveChat archives (archive=true) or unarchives the target chat. The
// change is sent to WhatsApp so it also shows on the phone; the chat moves
// between the normal and archived lists when the backend reports the new state.
func (x *m) archiveChat(includeGlobal, archive bool) tea.Cmd {
	idx := x.chatTarget(includeGlobal)
	if idx < 0 {
		if archive {
			return x.setTopBar("No chat selected to archive")
		}
		return x.setTopBar("No chat selected to unarchive")
	}
	if x.chats[idx].Archived == archive {
		if archive {
			return x.setTopBar("Already archived")
		}
		return x.setTopBar("Not archived")
	}
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
// archived ones (alt+d). alt+c also goes back to normal chats.
func (x *m) toggleArchivedView() {
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
}
