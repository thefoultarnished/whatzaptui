package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func chatMessageWrapWidth(w int, msg string) int {
	// Keep message content comfortably inside the pane so inline timestamps and long links don't spill.
	contentW := max(8, w-2)
	seventyPct := (contentW * 7) / 10
	base := max(8, min(seventyPct, contentW-18))
	return max(8, base-countEmojiHeuristic(msg))
}

func wrapMessageLines(msgBody string, availableW int, fromMe bool, senderName string) []string {
	if fromMe {
		return strings.Split(wrapTextBalanced(msgBody, availableW), "\n")
	}
	prefixWidth := runeDisplayWidth(senderName + ": ")
	return strings.Split(wrapTextWithPrefix(msgBody, availableW, prefixWidth), "\n")
}

var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripAnsi(s string) string {
	return ansiEscapeRegex.ReplaceAllString(s, "")
}

func applyBgToAnsiString(s string, bg lipgloss.Color) string {
	if s == "" {
		return ""
	}
	// Parse ANSI sequences and construct a new string where every non-sequence part is wrapped with the background,
	// and any reset or background-clearing sequence is supplemented with the selection background sequence.
	// Since lipgloss.Color can be #RRGGBB, we can use lipgloss.NewStyle().Background(bg).Render("") to find the escape code,
	// or format it ourselves. In 24-bit color mode, background is "\x1b[48;2;R;G;Bm".
	bgStyle := lipgloss.NewStyle().Background(bg)
	bgSeq := bgStyle.Render("")
	// If bgSeq has a trailing reset \x1b[0m, strip it. Let's just find the start sequence.
	if idx := strings.Index(bgSeq, "m"); idx > 0 {
		bgSeq = bgSeq[:idx+1]
	} else {
		bgSeq = ""
	}

	if bgSeq == "" {
		return s
	}

	var sb strings.Builder
	// Start with the background sequence active
	sb.WriteString(bgSeq)

	matches := ansiEscapeRegex.FindAllStringIndex(s, -1)
	lastIdx := 0
	for _, match := range matches {
		// Write the text segment before this ANSI escape code
		sb.WriteString(s[lastIdx:match[0]])
		esc := s[match[0]:match[1]]
		sb.WriteString(esc)
		// If the escape code is a reset (\x1b[0m or contains a 0 code), we must re-enable our background color
		// because the reset clears both foreground and background colors.
		// Similarly, if it sets a background, it might override ours, but reset is the main one that clears all.
		if esc == "\x1b[0m" || esc == "\x1b[m" || strings.Contains(esc, ";0m") || strings.Contains(esc, "[0m") || strings.Contains(esc, "[0;") {
			sb.WriteString(bgSeq)
		}
		lastIdx = match[1]
	}
	sb.WriteString(s[lastIdx:])
	// End with a reset code
	sb.WriteString("\x1b[0m")
	return sb.String()
}

func splitAnsiStringAtWidth(s string, targetW int) (string, string) {
	if targetW <= 0 {
		return "", s
	}
	var sbPrefix strings.Builder
	var sbSuffix strings.Builder

	matches := ansiEscapeRegex.FindAllStringIndex(s, -1)
	currentW := 0
	lastIdx := 0

	// Keep track of the current active ANSI styles to prepend to the suffix so formatting is preserved
	var activeStyles []string

	for _, match := range matches {
		// Process text segment before this ANSI sequence
		textSeg := []rune(s[lastIdx:match[0]])
		for i := 0; i < len(textSeg); {
			rw, consumed := nextGlyphWidth(textSeg, i)
			glyph := string(textSeg[i : i+consumed])
			if currentW+rw <= targetW {
				sbPrefix.WriteString(glyph)
				currentW += rw
			} else {
				sbSuffix.WriteString(glyph)
			}
			i += consumed
		}

		esc := s[match[0]:match[1]]
		if esc == "\x1b[0m" || esc == "\x1b[m" {
			activeStyles = nil
		} else {
			activeStyles = append(activeStyles, esc)
		}

		if currentW <= targetW {
			sbPrefix.WriteString(esc)
		} else {
			sbSuffix.WriteString(esc)
		}
		lastIdx = match[1]
	}

	tailSeg := []rune(s[lastIdx:])
	for i := 0; i < len(tailSeg); {
		rw, consumed := nextGlyphWidth(tailSeg, i)
		glyph := string(tailSeg[i : i+consumed])
		if currentW+rw <= targetW {
			sbPrefix.WriteString(glyph)
			currentW += rw
		} else {
			sbSuffix.WriteString(glyph)
		}
		i += consumed
	}

	prefix := sbPrefix.String()
	suffix := sbSuffix.String()
	if suffix != "" && len(activeStyles) > 0 {
		suffix = strings.Join(activeStyles, "") + suffix
	}
	return prefix, suffix
}

var (
	urlRegex          = regexp.MustCompile(`https?://[^\s]+`)
	mentionTokenRegex = regexp.MustCompile(`(^|\s)(@[a-zA-Z0-9_]+)`)
)

func mentionPattern(knownTags []string) *regexp.Regexp {
	if len(knownTags) == 0 {
		return mentionTokenRegex
	}
	parts := make([]string, 0, len(knownTags)+1)
	for _, t := range knownTags {
		parts = append(parts, regexp.QuoteMeta(t))
	}
	parts = append(parts, `@[a-zA-Z0-9_]+`)
	return regexp.MustCompile(`(^|\s)(` + strings.Join(parts, "|") + `)`)
}

func renderSegmentWithMentions(s string, baseStyle lipgloss.Style, knownTags ...string) string {
	re := mentionTokenRegex
	if len(knownTags) > 0 {
		re = mentionPattern(knownTags)
	}
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return baseStyle.Render(s)
	}
	mentionStyle := baseStyle.Copy().
		Foreground(accent).
		Bold(true)
	var sb strings.Builder
	lastIdx := 0
	for _, match := range matches {
		prefixEnd := match[4]
		if prefixEnd > lastIdx {
			sb.WriteString(baseStyle.Render(s[lastIdx:prefixEnd]))
		}
		sb.WriteString(mentionStyle.Render(s[match[4]:match[5]]))
		lastIdx = match[5]
	}
	if lastIdx < len(s) {
		sb.WriteString(baseStyle.Render(s[lastIdx:]))
	}
	return sb.String()
}

func renderTextWithLinks(s string, baseStyle lipgloss.Style, knownTags []string, original ...string) string {
	matches := urlRegex.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return renderSegmentWithMentions(s, baseStyle, knownTags...)
	}
	var origURLs []string
	if len(original) > 0 && original[0] != "" {
		origURLs = urlRegex.FindAllString(original[0], -1)
	}
	linkStyle := baseStyle.Copy().
		Foreground(textLink).
		Underline(true)
	var sb strings.Builder
	lastIdx := 0
	for _, match := range matches {
		sb.WriteString(renderSegmentWithMentions(s[lastIdx:match[0]], baseStyle, knownTags...))
		matchText := s[match[0]:match[1]]
		targetURL := matchText
		prefix := strings.TrimRight(matchText, ".")
		for _, oURL := range origURLs {
			if strings.HasPrefix(oURL, prefix) {
				targetURL = oURL
				break
			}
		}
		sb.WriteString("\x1b]8;;" + targetURL + "\x1b\\" + linkStyle.Render(matchText) + "\x1b]8;;\x1b\\")
		lastIdx = match[1]
	}
	sb.WriteString(renderSegmentWithMentions(s[lastIdx:], baseStyle, knownTags...))
	return sb.String()
}

func renderStyledMessageText(
	ln string,
	bodyStyle lipgloss.Style,
	tokenStyle lipgloss.Style,
	isMediaMsg bool,
	original string,
	knownTags ...string,
) string {
	if isMediaMsg && strings.HasPrefix(ln, "[") {
		if end := strings.Index(ln, "]"); end > 0 {
			token := ln[:end+1]
			rest := ln[end+1:]
			return tokenStyle.Render(token) + renderTextWithLinks(rest, bodyStyle, knownTags, original)
		}
	}
	return renderTextWithLinks(ln, bodyStyle, knownTags, original)
}

func outgoingMessageIndent(paneW int, blockW int, fromMe bool) string {
	if !fromMe {
		return ""
	}
	paneW = max(1, paneW)
	blockW = max(1, min(blockW, paneW))
	return strings.Repeat(" ", max(0, paneW-blockW))
}

// renderUploadProgress draws a horizontal progress bar of the form
// "████████░░░░░░░░░ 47%". availableW caps the total width; the bar
// shrinks on narrow panes and the percent label is dropped if even
// that doesn't fit.
func renderUploadProgress(pct, availableW int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	// Reserve " 100%" worst-case (5 chars).
	barW := 16
	if availableW > 0 {
		// Total = 1 ('[') + barW + 1 (']') + 1 (space) + 4 ("100%") = barW + 7
		maxBarW := availableW - 7
		if maxBarW < 4 {
			// No room for a bar; fall back to "47%".
			return fmt.Sprintf("%d%%", pct)
		}
		if maxBarW < barW {
			barW = maxBarW
		}
	}
	filled := pct * barW / 100
	if filled > barW {
		filled = barW
	}
	empty := barW - filled
	filledStr := strings.Repeat("█", filled)
	emptyStr := strings.Repeat("░", empty)
	return "[" + filledStr + emptyStr + "] " + fmt.Sprintf("%d%%", pct)
}
