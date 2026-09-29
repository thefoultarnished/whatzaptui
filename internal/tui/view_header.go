package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

func (x m) renderHeaderContainer(contentW, leftW int) string {
	totalUnread := 0
	for _, c := range x.chats {
		if !c.Archived {
			totalUnread += c.UnreadCount
		}
	}

	logo := " " + logoStyle.Render("WhatZap")
	statusPart := ""
	if totalUnread > 0 {
		statusPart = lipgloss.NewStyle().Foreground(v2Color(accent, amber)).Bold(true).Render(strconv.Itoa(totalUnread)+" unread") + " "
	} else if x.demoMode {
		statusPart = " " + lipgloss.NewStyle().Foreground(v2Color(muted, brand)).Render("demo") + " "
	}

	statusW := lipgloss.Width(statusPart)
	padW := leftW - statusW
	if padW < 0 {
		padW = 0
	}
	logoBlock := lipgloss.NewStyle().Width(padW).Render(logo)
	leftContent := logoBlock + statusPart
	leftStr := leftContent + lipgloss.NewStyle().Foreground(borderSubtle).Render("│")

	// V2: the two badges show the theme's identity pair, Brand + Emphasis.
	themeMood := lipgloss.NewStyle().
		Foreground(badgeInk).
		Background(v2Color(brand, anomalyTag)).
		Bold(true).
		Render(" ◉ MOOD ")
	themeName := lipgloss.NewStyle().
		Foreground(badgeInk).
		Background(v2Color(purple, accent)).
		Bold(true).
		Render(" " + strings.ToUpper(currentConfig.ThemeName) + " ")
	rightStr := themeMood + themeName + " "
	rightVW := lipgloss.Width(rightStr)
	centerW := max(0, contentW-(leftW+1)-rightVW)

	// Notices and sync progress are StatusInfo (legacy themes: Amber).
	infoStyle := lipgloss.NewStyle().Foreground(statusInfo).Bold(true)
	shineStyle := lipgloss.NewStyle().Foreground(fxShine).Bold(true)
	centerContent := " "
	if x.topBarMsg != "" && x.topBarShown > 0 {
		centerContent = " " + infoStyle.Render(graphemeSliceN(x.topBarMsg, x.topBarShown))
	} else if x.syncingContacts {
		centerContent = " " + renderShine(spinnerFrames[x.spinnerFrame]+" syncing contacts...", infoStyle, shineStyle, x.shineFrame)
	} else if x.syncingGroups {
		centerContent = " " + renderShine(spinnerFrames[x.spinnerFrame]+" syncing groups...", infoStyle, shineStyle, x.shineFrame)
	} else if x.syncingHistory {
		centerContent = " " + renderShine(spinnerFrames[x.spinnerFrame]+" syncing history...", infoStyle, shineStyle, x.shineFrame)
	} else if x.active != "" {
		displayName := x.nameFor(x.active)
		var avatarStr string
		avatarW := 0
		if centerW >= 24 {
			avatarStr = renderHeaderAvatar(displayName, x.active)
			avatarW = lipgloss.Width(avatarStr) + 2 // leading " " + avatar + trailing " "
		}
		// Use all available center width for the name; preview takes whatever's left.
		nameLimit := centerW - 1 - avatarW // 1 for the leading " "
		if nameLimit < 8 {
			nameLimit = 8
		}
		if avatarStr != "" {
			centerContent = " " + avatarStr + " " + accentStyle.Render(truncate(displayName, nameLimit))
		} else {
			centerContent = " " + accentStyle.Render(truncate(displayName, nameLimit))
		}
		if time.Now().Before(x.msgActivityUntil) && x.msgActivityType == "sent" {
			centerContent += accentStyle.Copy().Bold(false).Render("  " + spinnerFrames[x.spinnerFrame] + " message sent")
		}
		if label, online := x.presenceLabel(x.active); label != "" {
			dot, color := "○ ", muted
			if online {
				dot, color = "● ", v2Color(statusSuccess, brand)
			}
			text := "  " + dot + label
			if lipgloss.Width(centerContent)+lipgloss.Width(text) <= centerW {
				centerContent += lipgloss.NewStyle().Foreground(color).Render(text)
			}
		}
		if strings.HasSuffix(x.active, "@g.us") {
			if gp, ok := x.groupPreviews[x.active]; ok && len(gp.members) > 0 {
				preview := strings.Join(gp.members, ", ")
				rest := gp.total - len(gp.members)
				if rest > 0 {
					preview += fmt.Sprintf(" +%d", rest)
				}
				const sep = "  · "
				previewLimit := centerW - lipgloss.Width(centerContent) - len([]rune(sep))
				if previewLimit > 0 {
					centerContent += mutedStyle.Render(sep + truncate(preview, previewLimit))
				}
			}
		} else if phone := num(x.active); !currentConfig.HidePhoneNumber && displayName != phone {
			const sep = "  · "
			phoneText := "+" + phone
			previewLimit := centerW - lipgloss.Width(centerContent) - len([]rune(sep))
			if previewLimit > 0 {
				centerContent += mutedStyle.Render(sep + truncate(phoneText, previewLimit))
			}
		}
	}

	centerStr := lipgloss.NewStyle().Width(centerW).Render(centerContent)
	headLine := leftStr + centerStr + rightStr
	headBlock := lipgloss.NewStyle().Width(contentW).Render(headLine)
	// Use a 4-way cross junction "┼" where the vertical divider intersects
	// the horizontal header separator so lines connect in all four directions.
	borderRow := lipgloss.NewStyle().Foreground(borderSubtle).
		Render(strings.Repeat("─", leftW) + "┼" + strings.Repeat("─", max(0, contentW-leftW-1)))
	return lipgloss.JoinVertical(lipgloss.Left, headBlock, borderRow)
}

func renderHeaderAvatar(name, id string) string {
	initials := headerAvatarInitials(name, id)
	// V2: the avatar is "them" - Emphasis for 1:1, the group's own colour.
	fill := brand
	if themeV2 {
		fill = purple
		if strings.HasSuffix(id, "@g.us") {
			fill = senderColor(id)
		}
	}
	return lipgloss.NewStyle().
		Foreground(buttonInk).
		Background(fill).
		Bold(true).
		Render(" " + initials + " ")
}

func headerAvatarInitials(name, id string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	initials := make([]rune, 0, 2)
	for _, part := range parts {
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				initials = append(initials, unicode.ToUpper(r))
				break
			}
		}
		if len(initials) == 2 {
			break
		}
	}
	if len(initials) == 0 {
		n := num(id)
		rs := []rune(n)
		if len(rs) >= 2 {
			initials = append(initials, rs[len(rs)-2], rs[len(rs)-1])
		} else if len(rs) == 1 {
			initials = append(initials, rs[0])
		}
	}
	if len(initials) == 1 {
		initials = append(initials, ' ')
	}
	if len(initials) == 0 {
		return "??"
	}
	return string(initials[:2])
}
