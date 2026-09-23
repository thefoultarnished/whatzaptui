package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (x m) callBanner(cm callMsg) string {
	name := ""
	switch {
	case cm.GroupID != "":
		name = x.nameFor(cm.GroupID)
	case cm.CallerID != "":
		name = x.nameFor(cm.CallerID)
	}
	if strings.TrimSpace(name) == "" {
		if cm.GroupID != "" {
			name = num(cm.GroupID)
		} else if cm.CallerID != "" {
			name = num(cm.CallerID)
		} else {
			name = "unknown"
		}
	}
	callType := "call"
	if cm.Media != "" {
		callType = cm.Media + " call"
	}
	switch cm.Status {
	case "incoming":
		if cm.GroupID != "" {
			return "Incoming " + callType + " in " + name
		}
		return "Incoming " + callType + " from " + name
	case "ended":
		if cm.Reason != "" {
			return "Call ended: " + name + " (" + cm.Reason + ")"
		}
		return "Call ended: " + name
	default:
		return "Call update: " + name
	}
}

func messagePreviewForNotification(msg wireMsg) string {
	body := strings.TrimSpace(renderMessageBody(msg.Message))
	body = strings.Join(strings.Fields(body), " ")
	if body == "" {
		body = "(message)"
	}
	r := []rune(body)
	if len(r) > 90 {
		body = string(r[:90]) + "..."
	}
	return body
}

func setTerminalBg(color string) {
	if color == "" {
		fmt.Printf("\033]111\a") // reset to terminal default
	} else {
		fmt.Printf("\033]11;%s\a", color)
	}
}

func setTerminalBgCmd(color string) tea.Cmd {
	return func() tea.Msg {
		setTerminalBg(color)
		return nil
	}
}

func setTerminalTitleCmd(title string) tea.Cmd {
	clean := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(title), "\x1b", ""), "\a", "")
	if clean == "" {
		clean = "WhatZap"
	}
	legacy := func() tea.Msg {
		fmt.Printf("\033]0;%s\a", clean)
		fmt.Printf("\033]2;%s\a", clean)
		return nil
	}
	return tea.Batch(tea.SetWindowTitle(clean), legacy)
}

func (x *m) refreshWindowTitleCmd() tea.Cmd {
	title := "WhatZap"
	names := x.unreadTitleNames(3)
	if len(names) > 0 {
		extra := x.unreadNamedChatCount() - len(names)
		if extra > 0 {
			title = fmt.Sprintf("WhatZap (🟢 %s +%d)", strings.Join(names, ","), extra)
		} else {
			title = fmt.Sprintf("WhatZap (🟢 %s)", strings.Join(names, ","))
		}
	}
	if x.windowTitle == title {
		return nil
	}
	x.windowTitle = title
	return setTerminalTitleCmd(title)
}

func (x *m) unreadNamedChatCount() int {
	count := 0
	for _, ch := range x.chats {
		if ch.UnreadCount > 0 {
			count++
		}
	}
	return count
}

func firstNameForTitle(name string) string {
	fields := strings.Fields(strings.TrimSpace(name))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (x *m) unreadTitleNames(limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, ch := range x.chats {
		if ch.UnreadCount <= 0 {
			continue
		}
		n := firstNameForTitle(x.nameFor(ch.ID))
		if n == "" {
			n = firstNameForTitle(num(ch.ID))
		}
		if n == "" {
			continue
		}
		key := strings.ToLower(n)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, n)
		if len(out) == limit {
			break
		}
	}
	return out
}

func (x *m) shouldNotifyIncoming(wm wireMsg) bool {
	if wm.Key.FromMe || wm.Key.RemoteJID == "" {
		return false
	}
	// Keep alerts quiet while you're already looking at that chat.
	if x.mode == "chat" && !x.sidebarFocused && !x.leftInputFocused && x.active == wm.Key.RemoteJID {
		return false
	}
	now := time.Now()
	if !x.lastNotifyGlobal.IsZero() && now.Sub(x.lastNotifyGlobal) < 1200*time.Millisecond {
		return false
	}
	if last, ok := x.lastNotifyAt[wm.Key.RemoteJID]; ok && now.Sub(last) < 4*time.Second {
		return false
	}
	x.lastNotifyGlobal = now
	if x.lastNotifyAt == nil {
		x.lastNotifyAt = map[string]time.Time{}
	}
	x.lastNotifyAt[wm.Key.RemoteJID] = now
	return true
}
