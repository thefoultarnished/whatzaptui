package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (x m) sidebarItems() []chat {
	if x.sidebarTab == "contacts" {
		if x.sidebarCache != nil && x.sidebarCache.contactsValid {
			return x.sidebarCache.contacts
		}
		out := make([]chat, 0, len(x.contacts))
		for _, ct := range x.contacts {
			if ct.ID == "" || strings.HasSuffix(ct.ID, "@g.us") || ct.ID == "status@broadcast" {
				continue
			}
			// Contacts tab should only show named contacts, not raw unresolved numbers.
			if strings.TrimSpace(ct.Notify) == "" && strings.TrimSpace(ct.Name) == "" {
				continue
			}
			// Stored-only filter: hide push-name-only strangers unless
			// the user opted into all contacts. Renamed and whitelisted
			// chats always show so they stay manageable.
			if !currentConfig.ShowAllContacts && !ct.Stored {
				n := num(ct.ID)
				if _, ok := x.names[n]; !ok {
					if _, ok := x.whitelist[n]; !ok {
						continue
					}
				}
			}
			out = append(out, chat{ID: ct.ID, Name: ct.Notify, Subject: ct.Name})
		}
		sort.Slice(out, func(i, j int) bool {
			classI, keyI := contactSortKey(x.name(out[i]))
			classJ, keyJ := contactSortKey(x.name(out[j]))
			if classI != classJ {
				return classI < classJ
			}
			if keyI == keyJ {
				return out[i].ID < out[j].ID
			}
			return keyI < keyJ
		})
		if x.sidebarCache != nil {
			x.sidebarCache.contacts = out
			x.sidebarCache.contactsValid = true
			return x.sidebarCache.contacts
		}
		return out
	}
	out := make([]chat, 0, len(x.chats))
	for _, ch := range x.chats {
		if ch.ID == "" || ch.ID == "status@broadcast" {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func contactSortKey(name string) (class int, key string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return 2, ""
	}
	first := rune(0)
	for _, r := range trimmed {
		first = r
		break
	}
	lower := strings.ToLower(trimmed)
	if (first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') {
		return 0, lower
	}
	return 1, lower
}

// selectedChatID returns the ID of the chat currently highlighted in the
// sidebar (x.sel into x.chats), or "" if there's no valid selection. Used to
// re-anchor x.sel to the same chat after x.chats is re-sorted.
func (x m) selectedChatID() string {
	if x.sidebarTab == "contacts" || x.sel < 0 || x.sel >= len(x.chats) {
		return ""
	}
	return x.chats[x.sel].ID
}

// resortChats re-sorts x.chats by ConversationTimestamp (most recent first)
// and, if selectedID is non-empty, moves x.sel so it keeps pointing at that
// chat — otherwise the sidebar highlight (and anything that targets it, like
// Alt+B/Alt+W) would silently jump to whatever chat ends up at the old index
// when an incoming message reorders the list.
func (x *m) resortChats(selectedID string) {
	sort.Slice(x.chats, func(i, j int) bool { return x.chats[i].ConversationTimestamp > x.chats[j].ConversationTimestamp })
	if selectedID == "" {
		return
	}
	for i, c := range x.chats {
		if c.ID == selectedID {
			x.sel = i
			return
		}
	}
}

func (x m) filtered() []chat {
	items := x.sidebarItems()
	if strings.TrimSpace(x.search) == "" {
		return items
	}
	q := strings.ToLower(strings.TrimSpace(x.search))
	out := make([]chat, 0, len(items))
	for _, c := range items {
		if strings.Contains(strings.ToLower(x.name(c)), q) || strings.Contains(strings.ToLower(c.ID), q) {
			out = append(out, c)
		}
	}
	return out
}
func (x *m) ensureSideVisible(viewRows int) {
	f := x.filtered()
	if len(f) == 0 {
		x.sel = 0
		x.sideScroll = 0
		return
	}
	if x.sel < 0 {
		x.sel = 0
	}
	if x.sel >= len(f) {
		x.sel = len(f) - 1
	}
	if viewRows <= 0 {
		return
	}
	maxStart := max(0, len(f)-viewRows)
	if x.sideScroll > maxStart {
		x.sideScroll = maxStart
	}
	if x.sideScroll < 0 {
		x.sideScroll = 0
	}
	if x.sel < x.sideScroll {
		x.sideScroll = x.sel
	}
	if x.sel >= x.sideScroll+viewRows {
		x.sideScroll = x.sel - viewRows + 1
	}
}

func (x m) sideViewRows() int {
	outerH := x.h - 2
	if outerH <= 0 {
		return 1
	}
	sideH := outerH - 5
	return max(1, sideH-3)
}

func (x m) sidePaneWidth() int {
	frameW := max(1, x.w-2)
	outerW := frameW
	contentW := outerW
	return min(28, max(24, contentW/3))
}

func (x m) currentSidebarMarqueeKey() string {
	f := x.filtered()
	if len(f) == 0 {
		return ""
	}
	if x.mode != "chat" || x.sidebarFocused {
		if x.sel >= 0 && x.sel < len(f) {
			return x.sidebarTab + ":" + f[x.sel].ID
		}
	} else if x.active != "" {
		return x.sidebarTab + ":" + x.active
	}
	return ""
}

func (x m) currentSidebarMarqueeLabel() (string, int) {
	f := x.filtered()
	if len(f) == 0 {
		return "", 0
	}
	var target chat
	var targetIdx int
	found := false
	if x.mode != "chat" || x.sidebarFocused {
		if x.sel >= 0 && x.sel < len(f) {
			target = f[x.sel]
			targetIdx = x.sel
			found = true
		}
	} else if x.active != "" {
		for i, c := range f {
			if num(c.ID) == num(x.active) {
				target = c
				targetIdx = i
				found = true
				break
			}
		}
	}
	if !found {
		return "", 0
	}
	sideW := x.sidePaneWidth()
	rowWidth := max(1, sideW-2)
	nameWidth := max(1, rowWidth-1)
	n := targetIdx + 1
	var numLabel string
	switch {
	case n >= 100:
		numLabel = fmt.Sprintf("%d ", n)
	case n >= 10:
		numLabel = fmt.Sprintf("%d. ", n)
	default:
		numLabel = fmt.Sprintf("0%d. ", n)
	}
	return numLabel + x.name(target), nameWidth
}

func (x *m) resetSidebarMarquee() {
	x.sidebarMarqueeOffset = 0
	x.sidebarMarqueePause = 8
	x.sidebarMarqueeDir = 1
	x.sidebarMarqueeTick = 0
}

func (x *m) advanceSidebarMarquee() {
	key := x.currentSidebarMarqueeKey()
	if key == "" {
		x.sidebarMarqueeKey = ""
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueePause = 0
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueeTick = 0
		return
	}
	if key != x.sidebarMarqueeKey {
		x.sidebarMarqueeKey = key
		x.resetSidebarMarquee()
		return
	}
	label, width := x.currentSidebarMarqueeLabel()
	if graphemeCount(label) <= width {
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueePause = 0
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueeTick = 0
		return
	}
	maxOffset := graphemeCount(label) - width
	if maxOffset <= 0 {
		x.sidebarMarqueeOffset = 0
		x.sidebarMarqueeTick = 0
		return
	}
	if x.sidebarMarqueePause > 0 {
		x.sidebarMarqueePause--
		return
	}
	x.sidebarMarqueeTick = (x.sidebarMarqueeTick + 1) % 3
	if x.sidebarMarqueeTick != 0 {
		return
	}
	if x.sidebarMarqueeDir <= 0 {
		if x.sidebarMarqueeOffset > 0 {
			x.sidebarMarqueeOffset--
			if x.sidebarMarqueeOffset == 0 {
				x.sidebarMarqueeDir = 1
				x.sidebarMarqueePause = 8
			}
			return
		}
		x.sidebarMarqueeDir = 1
		x.sidebarMarqueePause = 8
		return
	}
	if x.sidebarMarqueeOffset < maxOffset {
		x.sidebarMarqueeOffset++
		if x.sidebarMarqueeOffset == maxOffset {
			x.sidebarMarqueeDir = -1
			x.sidebarMarqueePause = 8
		}
		return
	}
	x.sidebarMarqueeDir = -1
	x.sidebarMarqueePause = 8
}

func (x m) openSelectedChat() (tea.Model, tea.Cmd) {
	f := x.filtered()
	if len(f) == 0 {
		return x, nil
	}
	if x.sel < 0 || x.sel >= len(f) {
		x.sel = 0
	}
	newID := f[x.sel].ID
	if newID != x.active {
		x.saveDraft()
		x.active = newID
		x.restoreDraft()
		x.stopAudio(true)
	}
	x.mode, x.scroll = "chat", 0
	if x.chatInputLocked() {
		x.clearChatComposer()
	}

	// Clear the unread dot right away.
	for i := range x.chats {
		if x.chats[i].ID == x.active {
			x.chats[i].UnreadCount = 0
			break
		}
	}

	if x.demoMode {
		if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
			return x, titleCmd
		}
		return x, nil
	}

	// Always mark read so the phone badge clears even when local count was stale,
	// but only dispatch to the network if the WhatsApp session is already ready.
	batch := []tea.Cmd{
		getMsgs(x.reqCtx(), x.client, x.baseURL, x.active, 120),
	}
	if x.status == "ready" {
		batch = append(batch, postJSON(x.reqCtx(), x.client, x.baseURL+"/messages/read", map[string]string{"chatId": x.active}, func([]byte) tea.Msg { return dataErr{} }))
	}
	if strings.HasSuffix(x.active, "@g.us") {
		if _, cached := x.groupPreviews[x.active]; !cached {
			batch = append(batch, fetchGroupPreview(x.reqCtx(), x.client, x.baseURL, x.active))
		}
	}
	if titleCmd := x.refreshWindowTitleCmd(); titleCmd != nil {
		batch = append(batch, titleCmd)
	}
	return x, tea.Batch(batch...)
}

// maybeLoadOlder fires a lazy-load fetch for older messages if the user has
// scrolled within `lazyLoadTriggerRows` rows of the top of the loaded window.
// Returns nil if no fetch is needed (already loading, exhausted, or far from top).
func (x *m) maybeLoadOlder() tea.Cmd {
	if x.demoMode || x.active == "" {
		return nil
	}
	chatID := x.active
	if x.loadingOlder[chatID] || x.noMoreOlder[chatID] {
		return nil
	}
	msgs := x.msgs[chatID]
	if len(msgs) == 0 {
		return nil
	}
	// scroll is rows-from-bottom. Total visible content rows ≈ len(msgs) (one row per msg
	// is a lower bound; multi-line bubbles take more). Trigger when scroll is within
	// `lazyLoadTriggerRows` of len(msgs).
	const lazyLoadTriggerRows = 10
	if x.scroll < len(msgs)-lazyLoadTriggerRows {
		return nil
	}
	oldest := msgs[0].MessageTimestamp
	if oldest <= 0 {
		return nil
	}
	if x.loadingOlder == nil {
		x.loadingOlder = map[string]bool{}
	}
	x.loadingOlder[chatID] = true
	return getMsgsBefore(x.reqCtx(), x.client, x.baseURL, chatID, 100, oldest)
}

func (x *m) triggerBackgroundDownloads() tea.Cmd {
	if x.status != "ready" || x.active == "" {
		return nil
	}
	items := x.msgs[x.active]
	needed := x.h + x.scroll
	var cmds []tea.Cmd
	if x.downloadingMedia == nil {
		x.downloadingMedia = make(map[string]bool)
	}
	if x.downloadedMedia == nil {
		x.downloadedMedia = make(map[string]string)
		x.mediaOrder = nil
	}
	totalLines := 0
	for i := len(items) - 1; i >= 0; i-- {
		msg := items[i]
		isImg := false
		if msg.Message != nil {
			if _, ok := msg.Message["imageMessage"]; ok {
				isImg = true
			}
		}
		if isImg {
			msgID := msg.Key.ID
			if _, downloaded := x.downloadedMedia[msgID]; !downloaded {
				if _, downloading := x.downloadingMedia[msgID]; !downloading {
					x.downloadingMedia[msgID] = true
					cmds = append(cmds, downloadMedia(x.reqCtx(), x.client, x.baseURL, x.active, msgID, true))
				}
			}
		}
		totalLines += msgRowHeight(msg, x.w)
		if totalLines >= needed {
			break
		}
	}
	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
