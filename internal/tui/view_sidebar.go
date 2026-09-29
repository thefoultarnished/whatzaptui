package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (x m) renderSide(w, h int) string {
	f := x.filtered()
	chatsW := (w - 1) / 2
	peopleW := (w - 1) - chatsW

	chatsInactiveStyle := lipgloss.NewStyle().
		Width(chatsW).
		Align(lipgloss.Center).
		Foreground(muted)
	chatsActiveStyle := lipgloss.NewStyle().
		Width(chatsW).
		Align(lipgloss.Center).
		Foreground(buttonInk).
		Background(accent).
		Bold(true)

	peopleInactiveStyle := lipgloss.NewStyle().
		Width(peopleW).
		Align(lipgloss.Center).
		Foreground(muted)
	peopleActiveStyle := lipgloss.NewStyle().
		Width(peopleW).
		Align(lipgloss.Center).
		Foreground(buttonInk).
		Background(accent).
		Bold(true)

	labelStyle := lipgloss.NewStyle().Bold(true)
	shortcutStyle := lipgloss.NewStyle().Foreground(accent).Italic(true)
	activeShortcutStyle := lipgloss.NewStyle().Foreground(shadeColor(buttonInk, 0.8)).Italic(true)
	if themeV2 {
		// Calm tabs: the active tab uses the shared selection surface
		// instead of a full Action fill.
		chatsActiveStyle = chatsActiveStyle.Background(bgSelected).Foreground(text)
		peopleActiveStyle = peopleActiveStyle.Background(bgSelected).Foreground(text)
		activeShortcutStyle = lipgloss.NewStyle().Foreground(textSecondary).Background(bgSelected).Italic(true)
	}

	var chatsTab, contactsTab string
	if x.sidebarTab == "chats" && x.archivedView {
		// No room for the shortcut hint beside the longer label.
		chatsTab = chatsActiveStyle.Render("Archived")
		contactsTab = peopleInactiveStyle.Render(labelStyle.Render("People") + " " + shortcutStyle.Render("alt+p"))
	} else if x.sidebarTab == "chats" {
		chatsTab = chatsActiveStyle.Render("Chats " + activeShortcutStyle.Render("alt+c"))
		contactsTab = peopleInactiveStyle.Render(labelStyle.Render("People") + " " + shortcutStyle.Render("alt+p"))
	} else {
		chatsTab = chatsInactiveStyle.Render(labelStyle.Render("Chats") + " " + shortcutStyle.Render("alt+c"))
		contactsTab = peopleActiveStyle.Render("People " + activeShortcutStyle.Render("alt+p"))
	}
	tabsLine := chatsTab + contactsTab
	underlineColor := borderSubtle
	if x.mode == "search" {
		underlineColor = v2Color(borderFocus, accent)
	}

	sideStyle := lipgloss.NewStyle().
		Width(w).
		Padding(0, 0, 0, 0).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(borderSubtle)

	tabsRow := sideStyle.Height(1).Render(tabsLine)
	searchRow := sideStyle.Height(1).Render(x.renderSearchBox())

	// Divider rows span the full width (instead of the padded content area)
	// so the "─" line reaches all the way to the left edge of the sidebar.
	tabsDivider := lipgloss.NewStyle().Foreground(borderSubtle).Render(strings.Repeat("─", w) + "┤")
	searchDivider := lipgloss.NewStyle().Foreground(underlineColor).Render(strings.Repeat("─", w) + "┤")

	footerH := x.archiveFooterHeight()
	viewRows := max(1, h-4-footerH)
	maxStart := max(0, len(f)-viewRows)
	start := x.sideScroll
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	end := min(len(f), start+viewRows)
	listLines := x.renderUserList(f, start, end, w)
	if len(f) == 0 && strings.TrimSpace(x.search) != "" {
		listLines = append(listLines, mutedStyle.Render("  no results"))
	}
	for len(listLines) < viewRows {
		listLines = append(listLines, "")
	}
	listBlock := sideStyle.Height(viewRows).Render(strings.Join(listLines, "\n"))

	parts := []string{tabsRow, tabsDivider, searchRow, searchDivider, listBlock}
	if footerH > 0 {
		parts = append(parts, sideStyle.Height(1).Render(x.renderArchiveFooter(w)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (x m) renderSearchBox() string {
	searchFocused := x.mode == "search"
	searchValue := x.search
	if searchFocused {
		searchValue = x.searchInput
	}
	searchIconText := " ⌕ "
	searchIcon := mutedStyle.Render(searchIconText)
	searchLine := searchIcon
	placeholder := "search [Alt+S]"
	if x.sidebarTab == "contacts" {
		placeholder = "type to search users"
	}
	if searchFocused && x.sidebarTab == "contacts" && searchValue == "" {
		searchLine += inputCursorStyle.Render(inputCursorGlyph)
		searchLine += mutedStyle.Render(placeholder)
		return searchLine
	}
	if searchValue == "" && !searchFocused {
		searchLine += mutedStyle.Render(placeholder)
	} else if searchValue != "" {
		searchLine += lipgloss.NewStyle().Foreground(text).Render(searchValue)
	}
	if searchFocused {
		searchLine += inputCursorStyle.Render(inputCursorGlyph)
	}
	return searchLine
}

func (x m) renderUserList(f []chat, start, end, w int) []string {
	lines := []string{}
	for i := start; i < end; i++ {
		c := f[i]
		isActive := x.active != "" && num(x.active) == num(c.ID)
		hasUnread := c.UnreadCount > 0 && !(isActive && x.mode == "chat")
		isSel := i == x.sel
		navActive := x.mode != "chat" || x.sidebarFocused
		highlighted := isSel && navActive
		_, whitelisted := x.whitelist[num(c.ID)]

		rowBase := lipgloss.NewStyle()
		bg := lipgloss.Color("")
		fg := lipgloss.Color("")
		if themeV2 {
			// One calm selection style; whitelist state moves to the
			// coloured number/icon prefix instead of a red/green row fill.
			if highlighted {
				bg, fg = bgSelected, text
				rowBase = rowBase.Background(bg).Foreground(fg).Bold(isSel && navActive)
			} else if isActive {
				bg, fg = bgActive, text
				rowBase = rowBase.Background(bg).Foreground(fg)
			}
		} else if highlighted {
			bg = sidebarWhitelistActiveBg
			fg = buttonInk
			if !whitelisted {
				bg = sidebarBlacklistActiveBg
				fg = buttonInk
			}
			rowBase = rowBase.Background(bg).Foreground(fg).Bold(isSel && navActive)
		} else if isActive {
			bg = sidebarWhitelistActiveBg
			fg = buttonInk
			if !whitelisted {
				bg = sidebarBlacklistActiveBg
				fg = buttonInk
			}
			rowBase = rowBase.Background(bg).Foreground(fg)
		}

		rowWidth := max(1, w)
		nameWidth := max(1, rowWidth-2)

		n := i + 1
		var numLabel string
		if icon := userlistIconPrefix(currentConfig.UserlistIconStyle); icon != "" {
			numLabel = icon
		} else {
			switch {
			case n >= 100:
				numLabel = fmt.Sprintf("%d ", n)
			case n >= 10:
				numLabel = fmt.Sprintf("%d. ", n)
			default:
				numLabel = fmt.Sprintf("0%d. ", n)
			}
		}
		chatName := x.name(c)
		nameText := numLabel + chatName
		isMarqueeRow := highlighted || (isActive && x.mode == "chat" && !x.sidebarFocused)

		_, typing := x.typingChats[c.ID]
		adjustW := 0
		badge := ""
		if typing {
			adjustW = 2
		} else if hasUnread {
			badge = unreadBadgeText(c.UnreadCount)
			adjustW = len(badge)
		}
		// A pinned row keeps one more cell for the space after the pin mark.
		if c.Pinned && !typing {
			adjustW++
		}

		if isMarqueeRow && graphemeCount(nameText) > nameWidth-adjustW {
			offset := x.sidebarMarqueeOffset
			maxOffset := graphemeCount(nameText) - (nameWidth - adjustW)
			if offset < 0 {
				offset = 0
			}
			if offset > maxOffset {
				offset = maxOffset
			}
			nameText = graphemeWindow(nameText, offset, nameWidth-adjustW)
		} else {
			nameText = truncate(nameText, nameWidth-adjustW)
		}
		var content string
		switch {
		case themeV2:
			content = v2SidebarRowContent(nameText, numLabel, nameWidth-adjustW, sidebarRowState{
				unread:      hasUnread,
				selected:    highlighted,
				active:      isActive,
				bold:        isSel && navActive,
				whitelisted: whitelisted,
			}, bg, x.shineFrame+i*4)
		case hasUnread:
			// Shine plays for unread rows in every state (highlighted/active too).
			// The wave travels only across the name, so it bounces at the last
			// character instead of drifting through the trailing padding.
			var shineBase, shineHigh lipgloss.Style
			if highlighted || isActive {
				shineBase = lipgloss.NewStyle().Foreground(fg).Background(bg)
				shineHigh = lipgloss.NewStyle().Foreground(accent).Background(bg).Bold(true)
			} else {
				shineBase = lipgloss.NewStyle().Foreground(muted)
				shineHigh = lipgloss.NewStyle().Foreground(accent).Bold(true)
			}
			if highlighted {
				shineBase = shineBase.Bold(isSel && navActive)
			}
			// Clamp the label to the row so the wave's end matches the visible name.
			label := nameText
			if runeDisplayWidth(label) > nameWidth-adjustW {
				label = padRight(label, nameWidth-adjustW)
			}
			content = renderShine(label, shineBase, shineHigh, x.shineFrame+i*4)
			if pad := (nameWidth - adjustW) - runeDisplayWidth(label); pad > 0 {
				padStyle := lipgloss.NewStyle()
				if highlighted || isActive {
					padStyle = padStyle.Background(bg)
				}
				content += padStyle.Render(strings.Repeat(" ", pad))
			}
		case highlighted || isActive:
			contentStyle := lipgloss.NewStyle().Foreground(fg).Background(bg)
			if highlighted {
				contentStyle = contentStyle.Bold(isSel && navActive)
			}
			content = contentStyle.Render(padRight(nameText, nameWidth-adjustW))
		default:
			content = padRight(nameText, nameWidth-adjustW)
		}

		unreadStyle := lipgloss.NewStyle()
		if highlighted || isActive {
			unreadStyle = unreadStyle.Background(bg)
		}
		if typing {
			frames := []string{".  ", ".. ", "...", ".. "}
			dots := frames[(x.shineFrame/3)%len(frames)]
			content += unreadStyle.Foreground(accent).Bold(true).Render(dots)
		} else {
			// Pin mark plus one space after it; typing dots take this spot instead.
			tail := unreadStyle.Render(" ")
			if c.Pinned {
				tail = unreadStyle.Foreground(muted).Render(pinMark()) + unreadStyle.Render(" ")
			}
			if hasUnread {
				content += unreadStyle.Foreground(accent).Render(badge) + tail
			} else {
				content += tail
			}
		}
		leftPad := " "
		if themeV2 && highlighted {
			leftPad = selectionMarker(bg)
		} else if highlighted || isActive {
			leftPad = lipgloss.NewStyle().Background(bg).Render(" ")
		}
		line := rowBase.Width(rowWidth).Render(leftPad + content)
		lines = append(lines, line)
	}
	return lines
}

// unreadBadgeText is the unread count drawn at the end of a chat row. It is
// 1 to 3 cells wide ("7", "42", "99+") so the name column can reserve room.
func unreadBadgeText(n int) string {
	if n > 99 {
		return "99+"
	}
	return strconv.Itoa(n)
}

type sidebarRowState struct {
	unread, selected, active, bold, whitelisted bool
}

// v2SidebarRowContent renders a chat row's label for V2 themes. The
// number/icon prefix doubles as the whitelist marker (StatusSuccess when
// allowed, TextMuted otherwise); read chats step back to TextSecondary and
// unread ones are TextPrimary bold with the shine sweep.
func v2SidebarRowContent(nameText, prefix string, width int, st sidebarRowState, bg lipgloss.Color, frame int) string {
	base := lipgloss.NewStyle()
	if st.selected || st.active {
		base = base.Background(bg)
	}
	label := padRight(nameText, width)
	head, rest := "", label
	if prefix != "" && strings.HasPrefix(label, prefix) {
		head, rest = prefix, label[len(prefix):]
	}
	var sb strings.Builder
	if head != "" {
		markFg := muted
		if st.whitelisted {
			markFg = statusSuccess
		}
		sb.WriteString(base.Foreground(markFg).Render(head))
	}
	nameFg := textSecondary
	if st.unread || st.selected || st.active {
		nameFg = text
	}
	nameSt := base.Foreground(nameFg).Bold(st.unread || (st.selected && st.bold))
	if !st.unread {
		sb.WriteString(nameSt.Render(rest))
		return sb.String()
	}
	trimmed := strings.TrimRight(rest, " ")
	sb.WriteString(renderShine(trimmed, nameSt, base.Foreground(fxShine).Bold(true), frame))
	if pad := len(rest) - len(trimmed); pad > 0 {
		sb.WriteString(base.Render(strings.Repeat(" ", pad)))
	}
	return sb.String()
}
