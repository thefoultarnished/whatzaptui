package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"whatzap/internal/tui/picker"
)

func (x m) renderWelcomePane(w, h int) string {
	keyStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(v2Color(textSecondary, text))
	secStyle := lipgloss.NewStyle().Foreground(purple).Bold(true)
	divColor := lipgloss.NewStyle().Foreground(borderSubtle)

	// content column width
	col := 38
	if col > w-4 {
		col = w - 4
	}
	if col < 20 {
		col = 20
	}

	// header centered
	title := lipgloss.NewStyle().Foreground(text).Bold(true).Render("WhatZap")
	tagline := mutedStyle.Render("Terminal WhatsApp client")
	header := lipgloss.JoinVertical(lipgloss.Center, title, tagline)
	header = lipgloss.PlaceHorizontal(col, lipgloss.Center, header)

	div := divColor.Render(strings.Repeat("─", col))

	kw := 15
	shortcut := func(key, desc string) string {
		rk := keyStyle.Render(key)
		pad := kw - len([]rune(key))
		if pad < 1 {
			pad = 1
		}
		return rk + strings.Repeat(" ", pad) + descStyle.Render(desc)
	}

	section := func(title string, items []string) string {
		h := secStyle.Render(title)
		return h + "\n" + strings.Join(items, "\n")
	}

	nav := section("Navigation", []string{
		shortcut("↑ / ↓", "Browse chats"),
		shortcut("Enter", "Open chat"),
		shortcut("Esc", "Close / go back"),
		shortcut("Tab", "Toggle sidebar"),
	})

	chat := section("In Chat", []string{
		shortcut("Alt+↑ / ↓", "Switch chats"),
		shortcut("↑ / ↓", "Scroll messages"),
		shortcut("Alt+R", "Pick msg to reply"),
		shortcut("Alt+E", "Emoji picker"),
		shortcut("Alt+F", "File picker"),
		shortcut("Tab / Esc", "Exit chat"),
	})

	quick := section("Quick Actions", []string{
		shortcut("Alt+S", "Search"),
		shortcut("Alt+C", "Chats tab"),
		shortcut("Alt+P", "Contacts tab"),
		shortcut("Ctrl+K", "Command palette"),
		shortcut("Alt+M", "Toggle mouse"),
	})

	hint := lipgloss.PlaceHorizontal(col, lipgloss.Center,
		mutedStyle.Render("Select a chat to start messaging"))

	body := lipgloss.JoinVertical(lipgloss.Left,
		"", header, "", div, "",
		nav, "", chat, "", quick,
		"", div, hint,
	)

	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, body)
}

func (x m) renderMain(w, h int) string {
	if x.active == "" {
		return x.renderWelcomePane(w, h)
	}
	msgBlocks, msgTimestamps, msgIDs := x.chatMessageBlocks(w, h)
	return x.assembleChatLines(w, h, msgBlocks, msgTimestamps, msgIDs)
}

// chatMessageBlocks renders the newest messages of the active chat (enough to
// fill h rows plus the scroll offset), oldest first. Each block is one
// message's screen lines; timestamps and ids line up with the blocks.
func (x m) chatMessageBlocks(w, h int) ([][]string, []int64, []string) {
	items := x.msgs[x.active]
	reactionsFor := x.collectReactions(items)
	pollVotesFor := x.collectPollVotes(items)
	needed := h + x.scroll
	msgBlocks := [][]string{}
	msgTimestamps := []int64{}
	msgIDs := []string{}
	for i := len(items) - 1; i >= 0; i-- {
		msg := items[i]
		msgBody := renderMessageBody(msg.Message)
		if poll, ok := msg.Message["pollCreationMessage"].(map[string]any); ok {
			msgBody = renderPollCard(poll, pollVotesFor[msg.Key.ID])
		}
		if _, ok := msg.Message["pollUpdateMessage"].(map[string]any); ok {
			msgBody = x.pollVoteLine(msg)
		}
		if isForwarded(msg.Message) {
			msgBody = forwardedLabel + "\n" + msgBody
		}
		if msgBody == "" {
			msgBody = "[media]"
		}
		var msgMentionTags []string
		msgBody, msgMentionTags = x.formatMessageMentions(msgBody)
		msgBody += x.audioProgressLine(msg)

		timeStr := formatExactTime(msg.MessageTimestamp)
		if edited, _ := msg.Message["edited"].(bool); edited {
			timeStr += " (edited)"
		}
		receiptText := ""
		if msg.Key.FromMe {
			switch msg.ReceiptStatus {
			case "delivered":
				receiptText = "  ✓✓ "
			case "read", "played":
				receiptText = "  ✓✓ "
			default:
				receiptText = "  ✓ "
			}
		}
		senderName := "Me"
		if !msg.Key.FromMe {
			senderName = truncate(x.senderNameForMsg(msg), 40)
		}

		isFlashing := !msg.Key.FromMe && msg.Key.ID != "" && x.flashUntil[msg.Key.ID].After(time.Now())

		timeColor := muted
		if isFlashing {
			timeColor = accent
		}

		if _, ok := msg.Message["reactionMessage"]; ok {
			continue
		}

		isGroup := strings.HasSuffix(x.active, "@g.us")
		isMediaMsg := isMediaWire(msg)
		availableW := chatMessageWrapWidth(w, msgBody)
		// In 2-line mode, non-media outgoing lines need room for the right icon.
		// Reduce availableW so text wraps before the icon column; without this,
		// long lines fill the full pane and indent collapses to 0 (left-aligned).
		if msg.Key.FromMe && !isMediaMsg && currentConfig.TimestampNewLine {
			availableW = max(8, availableW-runeDisplayWidth(receivedMsgIcon+" "))
		}

		wrappedSender := senderName
		if !isGroup {
			wrappedSender = receivedMsgIcon
		}
		isImageMsg := false
		isPollCreation := false
		var imgPayload map[string]any
		if msg.Message != nil {
			if p, ok := msg.Message["imageMessage"].(map[string]any); ok {
				isImageMsg = true
				imgPayload = p
			}
			if _, ok := msg.Message["pollCreationMessage"]; ok {
				isPollCreation = true
			}
		}
		numPixelLines := 0
		var wrapped []string
		if inlineMediaArt() && isImageMsg {
			localPath := x.downloadedMedia[msg.Key.ID]
			if localPath != "" {
				var artLines []string
				if currentConfig.MediaViewStyle == "full" {
					artLines, _ = x.fullImageLines(msg, availableW)
				}
				if artLines == nil {
					pixelArt := renderPixelArt(localPath, availableW)
					if pixelArt != "" {
						artLines = strings.Split(pixelArt, "\n")
					} else {
						artLines = []string{"[image]"}
					}
				}
				numPixelLines = len(artLines)
				wrapped = artLines
			} else {
				wrapped = []string{"[downloading preview...]"}
			}
			if caption, _ := imgPayload["caption"].(string); caption != "" {
				wrappedCaption := wrapMessageLines(caption, availableW, msg.Key.FromMe, wrappedSender)
				wrapped = append(wrapped, wrappedCaption...)
			}
		} else if isPollCreation {
			var prefixWidth int
			if !msg.Key.FromMe {
				if isGroup {
					prefixWidth = runeDisplayWidth(senderName + ": ")
				} else {
					prefixWidth = 0
				}
			}
			rawLines := strings.Split(msgBody, "\n")
			wrapped = make([]string, len(rawLines))
			wrapped[0] = rawLines[0]
			for i := 1; i < len(rawLines); i++ {
				if !msg.Key.FromMe {
					wrapped[i] = strings.Repeat(" ", prefixWidth) + rawLines[i]
				} else {
					wrapped[i] = rawLines[i]
				}
			}
		} else {
			wrapped = wrapMessageLines(msgBody, availableW, msg.Key.FromMe, wrappedSender)
		}

		bodyColor := sentText
		if !msg.Key.FromMe {
			bodyColor = receivedText
		}

		// For multi-line outgoing text messages, try re-wrapping at a slightly
		// narrower width so the first (widest) line doesn't reach the right edge
		// while the last line's text sits well to the left of the timestamp.
		if msg.Key.FromMe && !isMediaMsg && len(wrapped) > 1 {
			tsApprox := runeDisplayWidth("  " + timeStr + receiptText + " ")
			narrowW := max(8, availableW-tsApprox/2)
			if narrowW < availableW {
				if attempt := wrapMessageLines(msgBody, narrowW, msg.Key.FromMe, wrappedSender); len(attempt) == len(wrapped) {
					wrapped = attempt
				}
			}
		}
		mediaTokenBG := mediaTokenBg
		if x.pulseOn {
			mediaTokenBG = mediaTokenPulseBg
		}
		if isFlashing {
			bodyColor = accent
		}

		isClickedSelected := x.selectedMsgID != "" && msg.Key.ID == x.selectedMsgID
		selectColor := lipgloss.Color("")
		if isClickedSelected && (x.replyPickMode || x.editPickMode) {
			if x.pulseOn {
				selectColor = accent
			} else {
				selectColor = lipgloss.Color(blendHex(string(accent), string(muted), 0.45))
			}
		} else if isClickedSelected {
			selectColor = v2Color(accent, brand)
		}
		replySelected := isClickedSelected && (x.replyPickMode || x.editPickMode)
		applySelectedBG := func(st lipgloss.Style) lipgloss.Style {
			if selectColor != "" {
				st = st.Foreground(selectColor)
				if replySelected {
					st = st.Bold(true)
				}
			}
			return st
		}
		applyBodyBG := func(st lipgloss.Style) lipgloss.Style {
			if selectColor != "" {
				st = st.Foreground(selectColor)
				if replySelected {
					st = st.Bold(true)
				}
			}
			return st
		}

		timeStyled := mutedStyle.Copy().Foreground(timeColor).Render("  " + timeStr)
		progressActive := false
		if msg.Key.FromMe && isMediaMsg {
			if _, ok := x.uploadProgress[msg.Key.ID]; ok {
				progressActive = true
				// During upload, replace the timestamp slot with a live
				// progress bar. Same gutter width as the timestamp+receipt
				// it stands in for, so the right edge stays anchored.
				progressW := runeDisplayWidth(timeStr+receiptText) + 2
				if progressW < 4 {
					progressW = 16
				}
				timeStyled = mutedStyle.Copy().Foreground(timeColor).Render(renderUploadProgress(x.uploadProgress[msg.Key.ID], progressW))
			}
		}
		if receiptText != "" && !progressActive {
			receiptColor := muted
			if msg.ReceiptStatus == "read" || msg.ReceiptStatus == "played" {
				receiptColor = accent
			}
			timeStyled += mutedStyle.Copy().Foreground(receiptColor).Bold(true).Render(receiptText)
		}
		block := []string{}
		hasQuoteLine := false

		reactionLinePlain := ""
		reactionLine := ""
		reactionMerged := false
		if rxns, ok := reactionsFor[msg.Key.ID]; ok && len(rxns) > 0 {
			// The first maxNamedReactions known reactors are shown by name;
			// everyone else is grouped into per-emoji counts.
			var contactRxns []reactionRender
			otherEmojis := map[string]int{}
			for _, rxn := range rxns {
				if rxn.isContact && len(contactRxns) < maxNamedReactions {
					contactRxns = append(contactRxns, rxn)
				} else {
					otherEmojis[rxn.emoji]++
				}
			}

			reactionPlainParts := []string{}
			reactionStyledParts := []string{}
			firstReactionColor := receivedName
			for i, rxn := range contactRxns {
				reactionColor := receivedName
				if rxn.isMe {
					reactionColor = sentName
				}
				if i == 0 {
					firstReactionColor = reactionColor
				}
				// 1-to-1 chats: emoji alone is enough; the other party is
				// implicit. Groups keep the reactor's name so multiple
				// reactions from different people stay distinguishable.
				label := rxn.emoji
				if isGroup {
					label = rxn.emoji + " " + rxn.name
				}
				reactionPlainParts = append(reactionPlainParts, label)
				reactionStyledParts = append(reactionStyledParts,
					applySelectedBG(lipgloss.NewStyle().Foreground(reactionColor).Bold(true)).Render(label))
			}

			// Build summary for non-contact reactions.
			if len(otherEmojis) > 0 {
				sortedEmojis := make([]string, 0, len(otherEmojis))
				for e := range otherEmojis {
					sortedEmojis = append(sortedEmojis, e)
				}
				sort.Slice(sortedEmojis, func(i, j int) bool {
					if otherEmojis[sortedEmojis[i]] != otherEmojis[sortedEmojis[j]] {
						return otherEmojis[sortedEmojis[i]] > otherEmojis[sortedEmojis[j]]
					}
					return sortedEmojis[i] < sortedEmojis[j]
				})
				summaryPlainParts := []string{}
				summaryStyledParts := []string{}
				for _, emoji := range sortedEmojis {
					count := otherEmojis[emoji]
					summaryPlainParts = append(summaryPlainParts, fmt.Sprintf("%s %d", emoji, count))
					summaryStyledParts = append(summaryStyledParts,
						applySelectedBG(mutedStyle).Render(fmt.Sprintf("%s %d", emoji, count)))
				}
				if len(contactRxns) > 0 {
					reactionPlainParts = append(reactionPlainParts, "│ "+strings.Join(summaryPlainParts, " "))
					reactionStyledParts = append(reactionStyledParts,
						applySelectedBG(mutedStyle).Render("│ ")+strings.Join(summaryStyledParts, applySelectedBG(mutedStyle).Render(" ")))
				} else {
					reactionPlainParts = append(reactionPlainParts, strings.Join(summaryPlainParts, " "))
					reactionStyledParts = append(reactionStyledParts, strings.Join(summaryStyledParts, applySelectedBG(mutedStyle).Render(" ")))
					firstReactionColor = muted
				}
			}

			reactionLinePlain = "  ╰─ " + strings.Join(reactionPlainParts, ", ")
			reactionPrefix := applySelectedBG(lipgloss.NewStyle().Foreground(firstReactionColor)).Render("  ╰─ ")
			if len(contactRxns) > 0 {
				reactionLine = reactionPrefix + strings.Join(reactionStyledParts, applySelectedBG(mutedStyle).Render(", "))
			} else {
				reactionLine = reactionPrefix + strings.Join(reactionStyledParts, applySelectedBG(mutedStyle).Render(" "))
			}
		}

		quoteStyled := ""
		quoteStyledRight := ""
		quotePlainRight := ""
		if ext, ok := msg.Message["extendedTextMessage"].(map[string]any); ok {
			if qText, _ := ext["quotedText"].(string); qText != "" {
				hasQuoteLine = true
				qParticipant, _ := ext["quotedParticipant"].(string)
				qFromMe, _ := ext["quotedFromMe"].(bool)
				qSender := "Me"
				if !qFromMe && strings.TrimSpace(qParticipant) != "" {
					qSender = x.nameFor(qParticipant)
				}
				if !qFromMe && strings.TrimSpace(qSender) == "" {
					qSender = num(qParticipant)
				}
				origQuote := stripAnsi(strings.ReplaceAll(qText, "\n", " "))
				qText = origQuote
				var qMentionTags []string
				qText, qMentionTags = x.formatMessageMentions(qText)
				if len([]rune(qText)) > 50 {
					qText = string([]rune(qText)[:50]) + "..."
				}
				qSenderColor := receivedName
				if isGroup {
					qSenderColor = senderColor(qParticipant)
				}
				// V2: quoted text steps back to the ChatQuoted… tints.
				qTextColor := v2Color(quotedReceivedText, receivedText)
				if qFromMe {
					qSenderColor = sentName
					qTextColor = v2Color(quotedSentText, sentText)
				}
				quotePrefix := "  ╭─ "
				quoteSuffix := " ─╮ "
				quoteStyled = applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor)).Render(quotePrefix) +
					applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor).Bold(true)).Render(qSender+": ") +
					renderTextWithLinks(qText, applySelectedBG(lipgloss.NewStyle().Foreground(qTextColor)), qMentionTags, origQuote)
				quoteStyledRight = applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor).Bold(true)).Render(qSender+": ") +
					renderTextWithLinks(qText, applySelectedBG(lipgloss.NewStyle().Foreground(qTextColor)), qMentionTags, origQuote) +
					applySelectedBG(lipgloss.NewStyle().Foreground(qSenderColor)).Render(quoteSuffix)
				quotePlainRight = qSender + ": " + qText + quoteSuffix
			}
		}
		outgoingBlockW := 0
		outgoingBodyW := 0
		// In 2-line mode the icon appears at a fixed right column on every body
		// line and the timestamp line. Reserve its width in both suffix and body.
		outgoingIconW := 0
		if currentConfig.TimestampNewLine {
			outgoingIconW = runeDisplayWidth(receivedMsgIcon + " ")
		}
		timeSuffixPlain := "  " + timeStr + receiptText + " "

		timeSuffixW := runeDisplayWidth(timeSuffixPlain)
		if msg.Key.FromMe {
			for _, ln := range wrapped {
				extra := outgoingIconW
				spacing := 1
				if currentConfig.TimestampNewLine {
					spacing = 2
				}
				outgoingBodyW = max(outgoingBodyW, lipgloss.Width(stripGraphicsSeqs(ln))+spacing+extra)
			}
			// Block must fit the widest text line OR the timestamp, whichever is wider.
			lastLineW := runeDisplayWidth(wrapped[len(wrapped)-1] + " ")
			if !currentConfig.TimestampNewLine && lastLineW+timeSuffixW <= max(1, w-2) {
				// Timestamp fits on the last line (and 2-line mode is off).
				outgoingBlockW = max(outgoingBodyW, lastLineW+timeSuffixW)
			} else if currentConfig.TimestampNewLine {
				// Timestamp goes on its own line. Reserve outgoingIconW extra
				// columns so the timestamp line's pad never needs to go
				// negative to align its receipt tick with the last character
				// of the message text (which sits behind the right-side icon).
				outgoingBlockW = max(outgoingBodyW, timeSuffixW+outgoingIconW)
			} else {
				// Timestamp goes on its own line (too wide for the last line).
				outgoingBlockW = max(outgoingBodyW, timeSuffixW)
			}
			if reactionLinePlain != "" {
				if currentConfig.TimestampNewLine {
					// Reaction shares the timestamp line (reaction on the left,
					// timestamp on the right), so reserve room for both plus a
					// 1-space gap between them.
					outgoingBlockW = max(outgoingBlockW, runeDisplayWidth(reactionLinePlain)+1+timeSuffixW+outgoingIconW)
				} else {
					outgoingBlockW = max(outgoingBlockW, runeDisplayWidth(reactionLinePlain))
				}
			}
			outgoingBlockW = min(outgoingBlockW, max(1, w-2))
		}
		indent := outgoingMessageIndent(max(1, w-2), outgoingBlockW, msg.Key.FromMe)
		// For multi-line outgoing media, the icon and filename lines should
		// be right-aligned within the last text line's width (not the full
		// bubble width) so their right edges match the caption/name's right
		// edge, with the timestamp extending further right. The last line
		// itself (caption or name) right-aligns within the full width.
		// Use the caption width WITHOUT the trailing space (which outgoing
		// rendering appends to every line) so the content right edges line
		// up after the trailing space is stripped.
		var mediaName string
		if msg.Key.FromMe && isMediaMsg && len(wrapped) > 1 {
			if msg.Message != nil {
				for _, key := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage"} {
					if v, ok := msg.Message[key].(map[string]any); ok {
						mediaName, _ = v["fileName"].(string)
						break
					}
				}
			}
		}
		if quoteStyled != "" {
			if msg.Key.FromMe {
				qPlainW := runeDisplayWidth(quotePlainRight)
				targetW := outgoingBlockW
				if currentConfig.TimestampNewLine {
					targetW -= 3
				}
				qIndent := max(0, len(indent)+targetW-qPlainW)
				block = append(block, strings.Repeat(" ", qIndent)+quoteStyledRight)
			} else {
				block = append(block, quoteStyled)
			}
		}
		lastBodyPlainW := 0
		for i, ln := range wrapped {
			// Media layout: line 1 is the kind tag (pre-styled in saturated
			// color), line 2 is the filename (muted), line 3 is the caption
			// (body color, with timestamp appended). Render the filename
			// muted and the caption in body color; for 2-line media where
			// only one of name/caption is present, apply the matching style.
			lineFg := bodyColor
			if isMediaMsg && i == 1 && mediaName != "" {
				lineFg = muted
			}
			bodyStyle := applyBodyBG(lipgloss.NewStyle().Foreground(lineFg).Bold(isFlashing))
			tokenStyle := applyBodyBG(lipgloss.NewStyle().
				Foreground(tagInk).
				Background(mediaTokenBG).
				Bold(true))
			lineParts := []string{}
			if !msg.Key.FromMe && i == 0 && isGroup {
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(senderColor(x.senderIDForMsg(msg))).
					Bold(true)).
					Render(senderName+": "))
			} else if !msg.Key.FromMe && i == 0 && !isGroup {
				isLast := i == len(wrapped)-1
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(receivedName)).
					Render(incomingLeftIcon(0, isLast)+" "))
			} else if !msg.Key.FromMe && i > 0 && !isGroup {
				isLast := i == len(wrapped)-1
				var iconPad string
				switch receivedMsgIcon {
				case "│", "┃", "║":
					iconPad = incomingLeftIcon(i, isLast) + " "
				default:
					iconPad = strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon+" "))
				}
				lineParts = append(lineParts, applyBodyBG(lipgloss.NewStyle().
					Foreground(receivedName)).
					Render(iconPad))
			}
			if inlineMediaArt() && isImageMsg && i < numPixelLines {
				lineParts = append(lineParts, ln)
			} else {
				lineParts = append(lineParts, renderStyledMessageText(ln, bodyStyle, tokenStyle, isMediaMsg, msgBody, msgMentionTags...))
			}
			if msg.Key.FromMe && i == len(wrapped)-1 {
				// Float timestamp to the right of the last line if it fits and
				// 2-line mode is off; otherwise it goes on its own line below.
				lastW := runeDisplayWidth(stripGraphicsSeqs(ln) + " ")
				if !currentConfig.TimestampNewLine && lastW+timeSuffixW <= outgoingBlockW {
					pad := outgoingBlockW - lastW - timeSuffixW
					lineParts = append(lineParts, bodyStyle.Render(strings.Repeat(" ", pad+1)))
					lineParts = append(lineParts, timeStyled)
				}
			} else if !msg.Key.FromMe && i == len(wrapped)-1 && !currentConfig.TimestampNewLine {
				lineParts = append(lineParts, timeStyled)
			}
			var bodyContent string
			if msg.Key.FromMe {
				if currentConfig.TimestampNewLine {
					contentW := lipgloss.Width(stripGraphicsSeqs(strings.Join(lineParts, "")))
					fillW := max(0, outgoingBlockW-outgoingIconW-2-contentW)
					lineParts = append([]string{strings.Repeat(" ", fillW)}, lineParts...)
					lineParts = append(lineParts, "  ", lipgloss.NewStyle().Foreground(muted).Render(outgoingRightIcon(i, i == len(wrapped)-1)), " ")
				} else {
					lineParts = append(lineParts, " ")
				}
				bodyContent = indent + strings.Join(lineParts, "")
			} else {
				bodyContent = strings.Join(lineParts, "")
			}
			lastBodyPlainW = runeDisplayWidth(stripGraphicsSeqs(ln))
			if !msg.Key.FromMe && i == 0 && isGroup {
				lastBodyPlainW += runeDisplayWidth(senderName + ": ")
			} else if !msg.Key.FromMe && !isGroup {
				lastBodyPlainW += runeDisplayWidth(receivedMsgIcon + " ")
			}
			block = append(block, bodyContent)
		}
		if msg.Key.FromMe {
			lastW := runeDisplayWidth(wrapped[len(wrapped)-1] + " ")
			if currentConfig.TimestampNewLine || lastW+timeSuffixW > outgoingBlockW {
				if currentConfig.TimestampNewLine {
					// Body lines reserve outgoingIconW columns on the right for
					// the right-side icon, so the message text's last character
					// sits outgoingIconW+2 columns before the line's right edge.
					// Pad the timestamp line so its last visible glyph (the
					// second receipt tick, before receiptText's own trailing
					// space) lands in that same column.
					pad := max(0, outgoingBlockW-outgoingIconW-timeSuffixW)
					if reactionLine != "" {
						// Put the reaction on the left of the same line, with
						// the timestamp staying right-aligned as usual.
						reactionW := runeDisplayWidth(reactionLinePlain)
						fillW := max(0, pad-reactionW)
						timeLine := indent + reactionLine + strings.Repeat(" ", fillW) + timeStyled
						block = append(block, timeLine)
						reactionMerged = true
					} else {
						timeLine := indent + strings.Repeat(" ", pad) + timeStyled
						block = append(block, timeLine)
					}
				} else {
					pad := max(0, outgoingBlockW-timeSuffixW)
					timeLine := indent + strings.Repeat(" ", pad) + timeStyled + " "
					block = append(block, timeLine)
				}
			}
		} else if currentConfig.TimestampNewLine {
			var prefix string
			var prefixWidth int
			if isGroup {
				prefixWidth = runeDisplayWidth(senderName + ": ")
				prefix = strings.Repeat(" ", prefixWidth)
			} else {
				prefixWidth = runeDisplayWidth(receivedMsgIcon + " ")
				icon := strings.Repeat(" ", prefixWidth)
				prefix = applyBodyBG(lipgloss.NewStyle().Foreground(receivedName)).Render(icon)
			}
			// timeStyled carries a "  " lead-in meant for the same-line layout;
			// drop it here so the timestamp starts directly under the message
			// text instead of leaving an extra gap after the icon/prefix.
			timeOnly := mutedStyle.Foreground(timeColor).Render(timeStr)
			if reactionLine != "" {
				// Reaction shares the timestamp line, right-aligned to the
				// message bubble's right edge while the timestamp stays left.
				reactionW := runeDisplayWidth(reactionLinePlain)
				timeW := prefixWidth + runeDisplayWidth(timeStr)
				targetW := max(lastBodyPlainW, timeW+1+reactionW)
				fillW := max(1, targetW-timeW-reactionW)
				timeLine := prefix + timeOnly + strings.Repeat(" ", fillW) + reactionLine
				block = append(block, timeLine)
				reactionMerged = true
			} else {
				timeLine := prefix + timeOnly
				block = append(block, timeLine)
			}
		}
		if reactionLine != "" && !reactionMerged {
			reactionOffset := max(0, lastBodyPlainW-runeDisplayWidth(reactionLinePlain))
			if msg.Key.FromMe {
				reactionLine = indent + strings.Repeat(" ", reactionOffset) + reactionLine
			} else {
				reactionLine = strings.Repeat(" ", reactionOffset) + reactionLine
			}
			block = append(block, reactionLine)
		}

		timeLine := len(wrapped)
		if hasQuoteLine {
			timeLine++
		}
		// No shifting or prefix marker prepending for replySelected
		if replySelected && len(block) > 0 {
			minStart := 9999
			maxEnd := 0
			plainLines := make([]string, len(block))
			starts := make([]int, len(block))
			ends := make([]int, len(block))
			for idx, ln := range block {
				plain := stripAnsi(ln)
				plainLines[idx] = plain
			}
			for idx := range block {
				if idx == timeLine {
					continue
				}
				plain := plainLines[idx]
				trimmed := strings.TrimLeft(plain, " ")
				leading := len(plain) - len(trimmed)

				pointerW := 0
				if !msg.Key.FromMe {
					bodyLineIdx := idx
					if hasQuoteLine {
						bodyLineIdx = idx - 1
					}
					// If the index is for a quote line (idx == 0 and hasQuoteLine), it has no pointer.
					if bodyLineIdx >= 0 {
						if isGroup {
							if bodyLineIdx == 0 {
								pointerW = runeDisplayWidth(senderName + ": ")
							}
						} else {
							// For single chat, first line has receivedMsgIcon + " ", subsequent lines have corresponding padding/icon
							if bodyLineIdx == 0 {
								pointerW = runeDisplayWidth(receivedMsgIcon + " ")
							} else {
								// Check receivedMsgIcon type to get correct padding width
								switch receivedMsgIcon {
								case "│", "┃", "║":
									pointerW = runeDisplayWidth(receivedMsgIcon + " ")
								default:
									pointerW = runeDisplayWidth(receivedMsgIcon + " ")
								}
							}
						}
					}
				}

				contentStart := leading + pointerW
				contentEnd := leading + runeDisplayWidth(trimmed)

				starts[idx] = contentStart
				ends[idx] = contentEnd
				if starts[idx] < minStart {
					minStart = starts[idx]
				}
				if ends[idx] > maxEnd {
					maxEnd = ends[idx]
				}
			}

			for idx, ln := range block {
				if idx == timeLine {
					continue
				}
				start := starts[idx]
				end := ends[idx]
				rightPadding := ""
				if end < maxEnd {
					rightPadding = strings.Repeat(" ", maxEnd-end)
				}

				// Reconstruct the line preserving the prefix (up to start) unhighlighted,
				// and highlighting the content from start to maxEnd.
				// Let's extract the prefix and content using ANSI-safe splitting at `start`.
				prefixPart, contentPart := splitAnsiStringAtWidth(ln, start)

				// Apply background style to contentPart + rightPadding.
				block[idx] = prefixPart + applyBgToAnsiString(contentPart+rightPadding, messageSelectedBg)
			}
		}
		msgBlocks = append(msgBlocks, block)
		msgTimestamps = append(msgTimestamps, msg.MessageTimestamp)
		msgIDs = append(msgIDs, msg.Key.ID)

		total := 0
		for _, b := range msgBlocks {
			total += len(b)
		}
		if total >= needed {
			break
		}
	}
	for i, j := 0, len(msgBlocks)-1; i < j; i, j = i+1, j-1 {
		msgBlocks[i], msgBlocks[j] = msgBlocks[j], msgBlocks[i]
		msgTimestamps[i], msgTimestamps[j] = msgTimestamps[j], msgTimestamps[i]
		msgIDs[i], msgIDs[j] = msgIDs[j], msgIDs[i]
	}
	return msgBlocks, msgTimestamps, msgIDs
}

const maxNamedReactions = 2

// reactorShortName keeps only the first word of a name to save space, with ".."
// marking that more of the name was cut ("Nav Ray" -> "Nav..").
func reactorShortName(full string) string {
	words := strings.Fields(full)
	if len(words) == 0 {
		return ""
	}
	if len(words) == 1 {
		return truncate(words[0], 10)
	}
	return truncate(words[0], 10) + ".."
}

type reactionRender struct {
	emoji     string
	name      string
	full      string // full display name, for the who-reacted list
	key       string // who reacted: one live reaction per person
	isMe      bool
	isContact bool
}

// collectReactions gathers the reactions on each message. A person has one
// live reaction per message: a newer one replaces their older one, and an empty
// one (WhatsApp's "remove reaction") takes it away. items must be oldest first.
func (x m) collectReactions(items []wireMsg) map[string][]reactionRender {
	reactionsFor := map[string][]reactionRender{}
	for _, msg := range items {
		rxn, ok := msg.Message["reactionMessage"].(map[string]any)
		if !ok {
			continue
		}
		targetID, _ := rxn["targetMsgID"].(string)
		emoji, _ := rxn["emoji"].(string)
		if targetID == "" {
			continue
		}
		sid := x.senderIDForMsg(msg)
		key := "me"
		if !msg.Key.FromMe {
			key = num(sid)
			if key == "" {
				key = msg.Key.ID
			}
		}
		kept := make([]reactionRender, 0, len(reactionsFor[targetID])+1)
		for _, r := range reactionsFor[targetID] {
			if r.key != key {
				kept = append(kept, r)
			}
		}
		reactionsFor[targetID] = kept
		if emoji == "" {
			continue
		}
		rSender := "Me"
		rFull := "You"
		rIsMe := msg.Key.FromMe
		fullName := x.senderNameForMsg(msg)
		senderNum := num(sid)
		isKnown := rIsMe || strings.TrimSpace(fullName) != "" || x.names[senderNum] != "" || x.whitelist[senderNum] != ""
		if !msg.Key.FromMe {
			rSender = reactorShortName(fullName)
			rFull = strings.TrimSpace(fullName)
			if rFull == "" {
				rFull = senderNum
			}
		}
		reactionsFor[targetID] = append(reactionsFor[targetID], reactionRender{
			emoji:     emoji,
			name:      rSender,
			full:      rFull,
			key:       key,
			isMe:      rIsMe,
			isContact: isKnown,
		})
	}
	return reactionsFor
}

// layoutChatLines lays message blocks out into the rows the pane shows:
// date separators between days, the scroll window, and the date pinned over
// the top row. lineIDs[i] is the message ID drawn on row i ("" for date rows
// and padding). Drawing and mouse hit-testing both use this, so a click
// always lands on the message that is actually drawn there.
func (x m) layoutChatLines(w, h int, msgBlocks [][]string, msgTimestamps []int64, msgIDs []string) (lines, lineIDs []string) {
	all := []string{}
	allIDs := []string{}
	allDates := []string{}
	lastDay := ""
	for idx, b := range msgBlocks {
		dayLabel := dateSeparatorLabel(msgTimestamps[idx])
		if dayLabel != lastDay {
			lastDay = dayLabel
			all = append(all, dateSeparatorLine(dayLabel, w))
			allIDs = append(allIDs, "")
			allDates = append(allDates, "")
		}
		id := ""
		if idx < len(msgIDs) {
			id = msgIDs[idx]
		}
		for _, ln := range b {
			all = append(all, ln)
			allIDs = append(allIDs, id)
			allDates = append(allDates, dayLabel)
		}
	}
	messageH := h
	if _, typing := x.typingChats[x.active]; typing {
		messageH = h - 1
	}

	start := len(all) - messageH - x.scroll
	if start < 0 {
		start = 0
	}
	end := start + messageH
	if end > len(all) {
		end = len(all)
	}
	if end < start {
		end = start
	}
	lines = make([]string, end-start)
	copy(lines, all[start:end])
	lineIDs = make([]string, end-start)
	copy(lineIDs, allIDs[start:end])
	if len(lines) > 0 && start < len(allDates) {
		pinLabel := ""
		for i := start; i >= 0 && pinLabel == ""; i-- {
			if i < len(allDates) && allDates[i] != "" {
				pinLabel = allDates[i]
			}
		}
		if pinLabel != "" {
			firstIsSep := start < len(allDates) && allDates[start] == ""
			if !firstIsSep {
				lines[0] = dateSeparatorLine(pinLabel, w)
				lineIDs[0] = ""
			}
		}
	}
	for len(lines) < messageH {
		lines = append(lines, "")
		lineIDs = append(lineIDs, "")
	}
	return lines, lineIDs
}

func (x m) assembleChatLines(w, h int, msgBlocks [][]string, msgTimestamps []int64, msgIDs []string) string {
	lines, _ := x.layoutChatLines(w, h, msgBlocks, msgTimestamps, msgIDs)
	if _, typing := x.typingChats[x.active]; typing {
		name := x.nameFor(x.active)
		typingText := name + " is typing..."
		if currentConfig.TypingAnimationStyle == picker.SquaresKey {
			icon := picker.SquaresIcon(x.shineFrame, lipgloss.Color(""), relLuminance(string(background)) >= 0.5)
			var textWithShine string
			if themeV2 {
				baseSt := lipgloss.NewStyle().Foreground(purple)
				shineSt := lipgloss.NewStyle().Foreground(fxShine).Bold(true)
				textWithShine = renderShine(typingText, baseSt, shineSt, x.shineFrame)
			} else {
				baseSt := lipgloss.NewStyle().Foreground(anomalyTag)
				shineSt := lipgloss.NewStyle().Foreground(accent).Bold(true)
				textWithShine = renderShine(typingText, shineSt, baseSt, x.shineFrame)
			}
			lines = append(lines, icon+" "+textWithShine)
		} else {
			typingIcons := getTypingIcons(currentConfig.TypingAnimationStyle)
			icon := typingIcons[x.shineFrame%len(typingIcons)]
			text := icon + " " + typingText
			if themeV2 {
				// Typing is "them": Emphasis with the shared shine sweep.
				baseSt := lipgloss.NewStyle().Foreground(purple)
				shineSt := lipgloss.NewStyle().Foreground(fxShine).Bold(true)
				lines = append(lines, renderShine(text, baseSt, shineSt, x.shineFrame))
			} else {
				baseSt := lipgloss.NewStyle().Foreground(anomalyTag)
				shineSt := lipgloss.NewStyle().Foreground(accent).Bold(true)
				lines = append(lines, renderShine(text, shineSt, baseSt, x.shineFrame))
			}
		}
	}
	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

func (x m) name(c chat) string {
	if n, ok := x.names[num(c.ID)]; ok && strings.TrimSpace(n) != "" {
		return n
	}
	if strings.HasSuffix(c.ID, "@g.us") {
		if c.Name != "" {
			return c.Name
		}
		if c.Subject != "" {
			return c.Subject
		}
		return num(c.ID)
	}
	if n, ok := x.whitelist[num(c.ID)]; ok && strings.TrimSpace(n) != "" {
		return n
	}
	if c.Name != "" {
		return c.Name
	}
	if c.Subject != "" {
		return c.Subject
	}
	if ct, ok := x.contacts[c.ID]; ok {
		if ct.Notify != "" {
			return ct.Notify
		}
		if ct.Name != "" {
			return ct.Name
		}
	}
	n := num(c.ID)
	if ct, ok := x.contactsByNumber[n]; ok {
		if strings.TrimSpace(ct.Notify) != "" {
			return ct.Notify
		}
		if strings.TrimSpace(ct.Name) != "" {
			return ct.Name
		}
	}
	return num(c.ID)
}

func (x m) nameFor(id string) string {
	for _, c := range x.chats {
		if c.ID == id {
			return x.name(c)
		}
	}
	return x.name(chat{ID: id})
}

func (x m) senderIDForMsg(msg wireMsg) string {
	if msg.Key.Participant != "" {
		return msg.Key.Participant
	}
	return msg.Key.RemoteJID
}

func (x m) senderNameForMsg(msg wireMsg) string {
	sid := x.senderIDForMsg(msg)
	name := x.nameFor(sid)
	if name == num(sid) && msg.PushName != "" {
		return msg.PushName
	}
	return name
}

// msgIDAtLine returns the ID of the message drawn on row lineIdx of the
// message pane, or "" for date rows, padding, and the typing indicator.
func (x m) msgIDAtLine(lineIdx, w, h int) string {
	if x.active == "" {
		return ""
	}
	msgBlocks, msgTimestamps, msgIDs := x.chatMessageBlocks(w, h)
	_, lineIDs := x.layoutChatLines(w, h, msgBlocks, msgTimestamps, msgIDs)
	if lineIdx < 0 || lineIdx >= len(lineIDs) {
		return ""
	}
	return lineIDs[lineIdx]
}
