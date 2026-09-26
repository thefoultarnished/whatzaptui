package tui

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

var phoneMentionRegex = regexp.MustCompile(`(?:^|\s)@(\d{7,15})\b`)

// extractMentionQuery checks if text ends with a valid mention trigger:
// (@ at start of string or preceded by whitespace, followed immediately by 1+ alphanumerics, with no trailing space).
// Returns (query, atIndex, true) if matched, otherwise ("", -1, false).
func extractMentionQuery(text string) (string, int, bool) {
	if len(text) < 2 {
		return "", -1, false
	}
	idx := strings.LastIndex(text, "@")
	if idx == -1 {
		return "", -1, false
	}
	// Before '@': must be start of string or whitespace
	if idx > 0 {
		prev := text[idx-1]
		if prev != ' ' && prev != '\t' && prev != '\n' && prev != '\r' {
			return "", -1, false
		}
	}
	query := text[idx+1:]
	if len(query) == 0 {
		return "", -1, false
	}
	// Must be alphanumeric / underscores only
	for _, r := range query {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return "", -1, false
		}
	}
	return query, idx, true
}

// mentionCandidates gathers candidate contacts for autocomplete.
// In group chats, prefers group preview participants and active message participants.
// In 1:1 chats or as fallback, includes all known contacts.
func (x m) mentionCandidates() []groupParticipant {
	seen := make(map[string]bool)
	var out []groupParticipant

	isGroup := strings.HasSuffix(x.active, "@g.us")
	if isGroup {
		if gp, ok := x.groupPreviews[x.active]; ok && len(gp.participants) > 0 {
			for _, p := range gp.participants {
				jid := p.JID
				if jid == "" {
					jid = p.Phone + "@s.whatsapp.net"
				}
				if !seen[jid] {
					seen[jid] = true
					out = append(out, p)
				}
			}
		}
		// Also inspect participants from cached messages
		for _, msg := range x.msgs[x.active] {
			part := msg.Key.Participant
			if part == "" || msg.Key.FromMe {
				continue
			}
			if !seen[part] {
				seen[part] = true
				phone := num(part)
				name := x.nameFor(part)
				if name == "" || name == phone {
					if ct, ok := x.contacts[part]; ok {
						if ct.Notify != "" {
							name = ct.Notify
						} else if ct.Name != "" {
							name = ct.Name
						}
					}
				}
				if name == "" {
					name = phone
				}
				out = append(out, groupParticipant{
					JID:   part,
					Phone: phone,
					Name:  name,
				})
			}
		}
	}

	// Also add contacts
	for jid, ct := range x.contacts {
		if !seen[jid] {
			seen[jid] = true
			name := strings.TrimSpace(ct.Name)
			if name == "" {
				name = strings.TrimSpace(ct.Notify)
			}
			phone := num(jid)
			if name == "" {
				name = phone
			}
			out = append(out, groupParticipant{
				JID:   jid,
				Phone: phone,
				Name:  name,
			})
		}
	}

	return out
}

// filterMentionCandidates filters candidates matching query (case-insensitive prefix or contains).
// Caps result to max 6 candidates.
func (x m) filterMentionCandidates(query string) []groupParticipant {
	q := strings.ToLower(query)
	all := x.mentionCandidates()

	type scored struct {
		p     groupParticipant
		score int // 0 = name prefix, 1 = phone prefix, 2 = name contains
	}

	var matched []scored
	for _, p := range all {
		nameLower := strings.ToLower(p.Name)
		phoneLower := strings.ToLower(p.Phone)

		if strings.HasPrefix(nameLower, q) {
			matched = append(matched, scored{p: p, score: 0})
		} else if strings.HasPrefix(phoneLower, q) {
			matched = append(matched, scored{p: p, score: 1})
		} else if strings.Contains(nameLower, q) {
			matched = append(matched, scored{p: p, score: 2})
		}
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].score != matched[j].score {
			return matched[i].score < matched[j].score
		}
		return strings.ToLower(matched[i].p.Name) < strings.ToLower(matched[j].p.Name)
	})

	limit := min(6, len(matched))
	res := make([]groupParticipant, limit)
	for i := range limit {
		res[i] = matched[i].p
	}
	return res
}

// updateMentionState updates autocomplete state based on current composer text.
func (x *m) updateMentionState() {
	if x.mentionDismissed {
		return
	}
	text := x.input + x.inputBuf
	query, _, ok := extractMentionQuery(text)
	if !ok {
		x.mentionPickerOpen = false
		x.mentionMatches = nil
		x.mentionSel = 0
		return
	}

	matches := x.filterMentionCandidates(query)
	if len(matches) == 0 {
		x.mentionPickerOpen = false
		x.mentionMatches = nil
		x.mentionSel = 0
		return
	}

	x.mentionPickerOpen = true
	x.mentionMatches = matches
	if x.mentionSel >= len(matches) {
		x.mentionSel = 0
	}
}

// applySelectedMention completes the mention currently highlighted in the picker.
func (x *m) applySelectedMention() {
	if !x.mentionPickerOpen || len(x.mentionMatches) == 0 || x.mentionSel >= len(x.mentionMatches) {
		return
	}
	selected := x.mentionMatches[x.mentionSel]

	text := x.input + x.inputBuf
	_, atIdx, ok := extractMentionQuery(text)
	if !ok || atIdx < 0 || atIdx >= len(text) {
		x.closeMentionPicker()
		return
	}

	// Use display name if present, otherwise phone
	displayName := strings.TrimSpace(selected.Name)
	if displayName == "" {
		displayName = selected.Phone
	}

	tagText := "@" + displayName
	replacement := tagText + " "
	newText := text[:atIdx] + replacement

	x.input = newText
	x.inputBuf = ""
	x.inputFlushScheduled = false
	x.inputAllSelected = false

	phone := selected.Phone
	if phone == "" {
		phone = num(selected.JID)
	}
	jid := selected.JID
	if jid == "" && phone != "" {
		jid = phone + "@s.whatsapp.net"
	}

	x.composerMentions = append(x.composerMentions, mentionTag{
		TagText: tagText,
		JID:     jid,
		Phone:   phone,
	})

	x.closeMentionPicker()
}

func (x *m) closeMentionPicker() {
	x.mentionPickerOpen = false
	x.mentionMatches = nil
	x.mentionSel = 0
}

// prepareOutgoingMentions translates tagged display names back to @phone in wire text
// and collects all unique mentioned JIDs.
func (x m) prepareOutgoingMentions(txt string) (string, []string) {
	wireText := txt
	var jids []string
	seen := make(map[string]bool)

	// Replace tracked composer mentions
	for _, m := range x.composerMentions {
		if strings.Contains(wireText, m.TagText) {
			if m.Phone != "" {
				wireText = strings.ReplaceAll(wireText, m.TagText, "@"+m.Phone)
			}
			if m.JID != "" && !seen[m.JID] {
				seen[m.JID] = true
				jids = append(jids, m.JID)
			}
		}
	}

	// Also detect any direct @<phone> mentions typed by hand
	matches := phoneMentionRegex.FindAllStringSubmatch(wireText, -1)
	for _, match := range matches {
		if len(match) > 1 {
			phone := match[1]
			jid := phone + "@s.whatsapp.net"
			if !seen[jid] {
				seen[jid] = true
				jids = append(jids, jid)
			}
		}
	}

	return wireText, jids
}

// resolvePhoneToName resolves a phone number or LID to a display name.
// Precedence:
// 1. Custom names (names, whitelist, address book contact Name/Notify, group participant name)
// 2. WhatsApp profile name (selfName for self account)
// 3. Phone number
// 4. Raw LID/JID
func (x m) resolvePhoneToName(phone string) string {
	if phone == "" {
		return ""
	}
	cleanPhone := num(phone)

	targetPhone := cleanPhone
	isSelf := false

	// Map LID to Phone number if known
	if x.selfLID != "" && cleanPhone == x.selfLID {
		isSelf = true
		if x.selfPhone != "" {
			targetPhone = x.selfPhone
		}
	} else if cleanPhone == x.selfPhone {
		isSelf = true
	} else if x.lidMap != nil {
		if mappedPN, ok := x.lidMap[cleanPhone]; ok && mappedPN != "" {
			targetPhone = num(mappedPN)
			if targetPhone == x.selfPhone {
				isSelf = true
			}
		}
	}

	// Step 1: Custom names (address book / contacts / whitelist / nicknames)
	// Check custom nickname
	if n, ok := x.names[targetPhone]; ok && strings.TrimSpace(n) != "" {
		return strings.TrimSpace(n)
	}

	// Check whitelist name
	if n, ok := x.whitelist[targetPhone]; ok && strings.TrimSpace(n) != "" {
		return strings.TrimSpace(n)
	}

	// Check group participants (active chat)
	if gp, ok := x.groupPreviews[x.active]; ok {
		for _, p := range gp.participants {
			if (p.Phone != "" && p.Phone == targetPhone) || (p.JID != "" && num(p.JID) == targetPhone) {
				if strings.TrimSpace(p.Name) != "" && p.Name != p.Phone && p.Name != targetPhone {
					return strings.TrimSpace(p.Name)
				}
			}
		}
	}

	// Check contacts by number
	if ct, ok := x.contactsByNumber[targetPhone]; ok {
		if strings.TrimSpace(ct.Name) != "" {
			return strings.TrimSpace(ct.Name)
		}
		if strings.TrimSpace(ct.Notify) != "" {
			return strings.TrimSpace(ct.Notify)
		}
	}

	// Check contacts by JID
	jid := targetPhone + "@s.whatsapp.net"
	if ct, ok := x.contacts[jid]; ok {
		if strings.TrimSpace(ct.Name) != "" {
			return strings.TrimSpace(ct.Name)
		}
		if strings.TrimSpace(ct.Notify) != "" {
			return strings.TrimSpace(ct.Notify)
		}
	}

	// Check general name resolution
	if n := x.nameFor(jid); n != "" && n != targetPhone && n != num(targetPhone) {
		return n
	}

	// Step 2: WhatsApp profile / push name
	if isSelf && strings.TrimSpace(x.selfName) != "" {
		return strings.TrimSpace(x.selfName)
	}

	// Step 3: Phone number (if resolved from LID, or if input was a phone number)
	if targetPhone != "" && targetPhone != cleanPhone {
		return targetPhone
	}

	// Step 4: LID / JID
	return ""
}

// formatMessageMentions converts all @<phone> in message body to @<Name> when a contact name is known.
// Returns the display-formatted message text and a slice of mention tags (e.g. "@Alice Smith") for styling.
func (x m) formatMessageMentions(text string) (string, []string) {
	if !strings.Contains(text, "@") {
		return text, nil
	}

	matches := phoneMentionRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}

	var sb strings.Builder
	var tags []string
	lastIdx := 0

	for _, match := range matches {
		phone := text[match[2]:match[3]]
		name := x.resolvePhoneToName(phone)
		if name != "" {
			atIdx := match[2] - 1
			sb.WriteString(text[lastIdx:atIdx])
			tag := "@" + name
			sb.WriteString(tag)
			tags = append(tags, tag)
			lastIdx = match[3]
		}
	}

	if lastIdx == 0 {
		return text, nil
	}

	if lastIdx < len(text) {
		sb.WriteString(text[lastIdx:])
	}

	return sb.String(), tags
}

// renderMentionPopup draws a compact 5-6 row floating box directly above the composer.
func (x m) renderMentionPopup(w int) string {
	if !x.mentionPickerOpen || len(x.mentionMatches) == 0 {
		return ""
	}

	var rows []string
	for i, item := range x.mentionMatches {
		selected := i == x.mentionSel
		name := item.Name
		if name == "" {
			name = item.Phone
		}
		phone := item.Phone
		if phone == "" {
			phone = num(item.JID)
		}

		line := " @" + name
		if phone != "" && phone != name {
			line += "  (" + phone + ")"
		}

		if selected {
			row := " ▌" + lipgloss.NewStyle().
				Foreground(accent).
				Bold(true).
				Render(line)
			if themeV2 {
				pad := max(0, w-lipgloss.Width(row))
				row += strings.Repeat(" ", pad)
				rows = append(rows, applyBgToAnsiString(row, bgSelected))
			} else {
				rowStyle := lipgloss.NewStyle().
					Background(sidebarActiveBg).
					Width(w)
				rows = append(rows, rowStyle.Render(row))
			}
		} else {
			row := "   " + lipgloss.NewStyle().
				Foreground(text).
				Render("@"+name)
			if phone != "" && phone != name {
				row += lipgloss.NewStyle().
					Foreground(muted).
					Render("  (" + phone + ")")
			}
			if themeV2 {
				pad := max(0, w-lipgloss.Width(row))
				row += strings.Repeat(" ", pad)
				rows = append(rows, applyBgToAnsiString(row, bgPanel))
			} else {
				rowStyle := lipgloss.NewStyle().
					Foreground(text).
					Width(w)
				rows = append(rows, rowStyle.Render(row))
			}
		}
	}

	return strings.Join(rows, "\n")
}
