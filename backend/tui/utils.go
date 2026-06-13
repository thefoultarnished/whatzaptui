package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/rivo/uniseg"
	qrcode "github.com/skip2/go-qrcode"
)

type mediaSendCommand struct {
	kind    string
	path    string
	caption string
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 {
		return ""
	}
	if runeDisplayWidth(s) <= n {
		return s
	}
	if n <= 3 {
		return strings.Repeat(".", n)
	}
	limit := n - 3
	var b strings.Builder
	for _, ch := range s {
		next := string(ch)
		if runeDisplayWidth(b.String())+runeDisplayWidth(next) > limit {
			break
		}
		b.WriteString(next)
	}
	return b.String() + "..."
}

func truncateDisplayWidth(s string, width int) string {
	s = strings.TrimSpace(s)
	if width <= 0 {
		return ""
	}
	if runeDisplayWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	limit := width - 3
	var b strings.Builder
	for _, ch := range s {
		next := string(ch)
		if runeDisplayWidth(b.String())+runeDisplayWidth(next) > limit {
			break
		}
		b.WriteString(next)
	}
	return b.String() + "..."
}

func whatzapDataRoot() string {
	if override := strings.TrimSpace(os.Getenv("WHATZAP_DATA_DIR")); override != "" {
		return filepath.Clean(override)
	}
	if runtime.GOOS == "windows" {
		if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
			return filepath.Join(base, "WhatZAP")
		}
	}
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "WhatZAP")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".whatzap")
	}
	return "."
}

func resolveConfigPath() string {
	root := whatzapDataRoot()
	dir := filepath.Join(root, "tui")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "config.json")

	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	legacyPath := filepath.Join(home, ".whatzap_tui_config.json")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if data, err := os.ReadFile(legacyPath); err == nil {
		_ = os.WriteFile(path, data, 0o644)
	}
	return path
}

func saveConfig() {
	path := resolveConfigPath()
	data, err := json.Marshal(currentConfig)
	if err != nil {
		log.Printf("saveConfig: marshal: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("saveConfig: write %s: %v", path, err)
	}
}

func loadConfig() {
	currentConfig = Config{
		ThemeName:            "tokyonight",
		MouseEnabled:         true,
		SoundEnabled:         true,
		SoundProfile:         2,
		SendTypingIndicator:  true,
		FlashTaskbar:         true,
		NotificationsEnabled: true,
	}
	path := resolveConfigPath()
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &currentConfig); err != nil {
			log.Printf("loadConfig: parse %s: %v", path, err)
		}
	}
	currentConfig.SoundProfile = normalizeSoundProfile(currentConfig.SoundProfile)
	if currentConfig.PointerIcon != "" {
		receivedMsgIcon = currentConfig.PointerIcon
	}
}

func sanitizeOutgoingText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		if !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasVisibleText(s string) bool {
	return strings.TrimSpace(sanitizeOutgoingText(s)) != ""
}

// sanitizeIncomingText strips Unicode Cc-category control characters
// (ESC, BEL, DEL, C0/C1 controls — the bytes needed to start ANSI/OSC/CSI
// terminal escape sequences) from wire-derived text, while preserving all
// printable Unicode (emoji, ZWJ joiners, variation selectors, combining
// marks, RTL text). Newlines are preserved; \r\n and lone \r are normalized
// to \n.
func sanitizeIncomingText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeWireValue recursively sanitizes every string found in a decoded
// JSON value (map[string]any / []any), in place, via sanitizeIncomingText.
func sanitizeWireValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if s, ok := vv.(string); ok {
				t[k] = sanitizeIncomingText(s)
				continue
			}
			sanitizeWireValue(vv)
		}
	case []any:
		for i, vv := range t {
			if s, ok := vv.(string); ok {
				t[i] = sanitizeIncomingText(s)
				continue
			}
			sanitizeWireValue(vv)
		}
	}
}

func appendComposerText(base, extra string) string {
	if extra == "" {
		return base
	}
	extra = sanitizeOutgoingText(extra)
	if extra == "" {
		return base
	}
	return base + extra
}

func draftLineCount(s string, width int) int {
	if width <= 0 {
		return 1
	}
	if s == "" {
		return 1
	}
	lines := 0
	for _, segment := range strings.Split(s, "\n") {
		w := runeDisplayWidth(segment) + 1 // account for the leading pad/cursor column
		if w <= 0 {
			w = 1
		}
		lines += max(1, (w+width-1)/width)
	}
	return max(1, lines)
}

func graphemeCount(s string) int {
	g := uniseg.NewGraphemes(s)
	n := 0
	for g.Next() {
		n++
	}
	return n
}

func graphemeDeleteLast(s string) string {
	g := uniseg.NewGraphemes(s)
	var clusters []string
	for g.Next() {
		clusters = append(clusters, g.Str())
	}
	if len(clusters) == 0 {
		return s
	}
	return strings.Join(clusters[:len(clusters)-1], "")
}

func graphemeSliceN(s string, n int) string {
	g := uniseg.NewGraphemes(s)
	var b strings.Builder
	i := 0
	for g.Next() && i < n {
		b.WriteString(g.Str())
		i++
	}
	return b.String()
}

func graphemeSplitFirst(s string) (first, rest string) {
	g := uniseg.NewGraphemes(s)
	if !g.Next() {
		return "", ""
	}
	first = g.Str()
	var b strings.Builder
	for g.Next() {
		b.WriteString(g.Str())
	}
	return first, b.String()
}

func graphemeWindow(s string, start, n int) string {
	if n <= 0 {
		return ""
	}
	g := uniseg.NewGraphemes(s)
	var b strings.Builder
	i := 0
	written := 0
	for g.Next() {
		if i >= start {
			b.WriteString(g.Str())
			written++
			if written >= n {
				break
			}
		}
		i++
	}
	return b.String()
}

func countEmojiHeuristic(s string) int {
	count := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && size <= 1 {
			break
		}
		if (r >= 0x1F300 && r <= 0x1F5FF) ||
			(r >= 0x1F600 && r <= 0x1F64F) ||
			(r >= 0x1F680 && r <= 0x1F6FF) ||
			(r >= 0x1F700 && r <= 0x1F77F) ||
			(r >= 0x1F780 && r <= 0x1F7FF) ||
			(r >= 0x1F900 && r <= 0x1F9FF) ||
			(r >= 0x1FA70 && r <= 0x1FAFF) ||
			(r >= 0x2600 && r <= 0x26FF) ||
			(r >= 0x2700 && r <= 0x27BF) {
			count++
		}
		s = s[size:]
	}
	return count
}

func runeDisplayWidth(s string) int {
	extra := 0
	for _, r := range s {
		if r >= 0x1F300 {
			extra++
		}
	}
	return len([]rune(s)) + extra
}

func wrapText(s string, width int) string {
	if width < 4 {
		return s
	}
	if strings.Contains(s, "\n") {
		paragraphs := strings.Split(s, "\n")
		for i, p := range paragraphs {
			paragraphs[i] = wrapText(p, width)
		}
		return strings.Join(paragraphs, "\n")
	}
	chunkWord := func(w string) []string {
		r := []rune(w)
		var out []string
		start := 0
		cur := 0
		for i := range r {
			cw := runeDisplayWidth(string(r[i : i+1]))
			if cur+cw > width && i > start {
				out = append(out, string(r[start:i]))
				start = i
				cur = 0
			}
			cur += cw
		}
		if start < len(r) {
			out = append(out, string(r[start:]))
		}
		return out
	}
	trimmed := strings.TrimLeft(s, " ")
	leading := strings.Repeat(" ", len(s)-len(trimmed))
	words := strings.Fields(trimmed)
	if len(words) == 0 {
		return s
	}
	lines := make([]string, 0, 4)
	cur := leading
	for _, w := range words {
		chunks := chunkWord(w)
		for _, chunk := range chunks {
			if cur == "" || cur == leading && runeDisplayWidth(cur)+runeDisplayWidth(chunk) <= width {
				cur += chunk
				continue
			}
			if runeDisplayWidth(cur)+1+runeDisplayWidth(chunk) <= width {
				cur += " " + chunk
			} else {
				lines = append(lines, cur)
				cur = chunk
			}
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// wrapTextBalanced wraps text so lines are roughly equal length, avoiding orphan words.
// It uses greedy wrapping first, then tries narrower widths until lines are balanced.
func wrapTextBalanced(s string, width int) string {
	if width < 4 {
		return s
	}
	greedy := wrapText(s, width)
	lines := strings.Split(greedy, "\n")
	if len(lines) <= 1 {
		return greedy
	}
	// Find the narrowest width that keeps the same number of lines.
	// Binary search between the longest word width and the greedy max width.
	targetLines := len(lines)
	longestWord := 0
	for _, w := range strings.Fields(s) {
		if ww := runeDisplayWidth(w); ww > longestWord {
			longestWord = ww
		}
	}
	lo, hi := longestWord, width
	best := width
	for lo <= hi {
		mid := (lo + hi) / 2
		attempt := strings.Split(wrapText(s, mid), "\n")
		if len(attempt) <= targetLines {
			best = mid
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	return wrapText(s, best)
}

func wrapTextWithPrefix(s string, width, prefixWidth int) string {
	if prefixWidth <= 0 {
		return wrapText(s, width)
	}
	if width < 4 {
		return s
	}
	if strings.Contains(s, "\n") {
		paragraphs := strings.Split(s, "\n")
		result := make([]string, len(paragraphs))
		result[0] = wrapTextWithPrefix(paragraphs[0], width, prefixWidth)
		for i, p := range paragraphs[1:] {
			result[i+1] = wrapText(p, width)
		}
		return strings.Join(result, "\n")
	}
	chunkWord := func(w string, lineWidth int) []string {
		r := []rune(w)
		var out []string
		start := 0
		cur := 0
		for i := range r {
			cw := runeDisplayWidth(string(r[i : i+1]))
			if cur+cw > lineWidth && i > start {
				out = append(out, string(r[start:i]))
				start = i
				cur = 0
			}
			cur += cw
		}
		if start < len(r) {
			out = append(out, string(r[start:]))
		}
		return out
	}

	firstWidth := max(4, width-prefixWidth)
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	lines := make([]string, 0, 4)
	cur := ""
	lineWidth := firstWidth
	for _, w := range words {
		chunks := chunkWord(w, lineWidth)
		for _, chunk := range chunks {
			if cur == "" {
				cur = chunk
				continue
			}
			if runeDisplayWidth(cur)+1+runeDisplayWidth(chunk) <= lineWidth {
				cur += " " + chunk
			} else {
				lines = append(lines, cur)
				cur = chunk
				lineWidth = width
			}
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func renderQR(payload string, maxW, maxH int) string {
	p := strings.TrimSpace(payload)
	if p == "" {
		return ""
	}
	qr, err := qrcode.New(p, qrcode.Low)
	if err != nil {
		return ""
	}
	bitmap := qr.Bitmap()
	if len(bitmap) == 0 || len(bitmap[0]) == 0 {
		return ""
	}
	rows := len(bitmap)
	cols := len(bitmap[0])
	downsample := 1
	if maxW > 0 && cols > maxW {
		downsample = (cols + maxW - 1) / maxW
	}
	if maxH > 0 {
		vScale := (rows + 2*maxH - 1) / (2 * maxH)
		if vScale > downsample {
			downsample = vScale
		}
	}
	upscale := 1
	if downsample == 1 {
		hScale := 1
		if maxW > 0 {
			hScale = max(1, maxW/cols)
		}
		vScale := 1
		if maxH > 0 {
			vScale = max(1, (2*maxH)/rows)
		}
		upscale = min(hScale, vScale)
		if upscale < 1 {
			upscale = 1
		}
		upscale = min(upscale, 1)
	}
	virtualRows := rows * upscale
	virtualCols := cols * upscale
	lines := make([]string, 0, virtualRows/2+1)
	for y := 0; y < virtualRows; y += 2 {
		var line strings.Builder
		for x := 0; x < virtualCols; x++ {
			srcX := x / upscale
			top := bitmap[min(rows-1, y/upscale)][srcX]
			bot := false
			if y+1 < virtualRows {
				bot = bitmap[min(rows-1, (y+1)/upscale)][srcX]
			}
			switch {
			case top && bot:
				line.WriteString(bb)
			case top && !bot:
				line.WriteString(bw)
			case !top && bot:
				line.WriteString(wb)
			default:
				line.WriteString(ww)
			}
		}
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n")
}

func formatRelativeTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	diff := time.Since(t)
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("03:04 PM")
	}
	y := now.AddDate(0, 0, -1)
	if t.Year() == y.Year() && t.YearDay() == y.YearDay() {
		return "Yesterday"
	}
	if diff < 7*24*time.Hour {
		return t.Format("Mon")
	}
	return t.Format("Jan 2")
}

func dateSeparatorLabel(ts int64) string {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "Today"
	}
	yesterday := now.AddDate(0, 0, -1)
	if t.Year() == yesterday.Year() && t.YearDay() == yesterday.YearDay() {
		return "Yesterday"
	}
	return t.Format("02 Jan 2006")
}

func formatExactTime(ts int64) string {
	if ts == 0 {
		return ""
	}
	t := time.Unix(ts, 0)
	diff := time.Since(t)
	if diff < 0 {
		diff = 0
	}
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	return t.Format("03:04 PM")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func num(jid string) string {
	p := strings.Split(jid, "@")
	return p[0]
}

func renderMessageBody(m map[string]any) string {
	if v, ok := m["conversation"].(string); ok {
		return v
	}
	if v, ok := m["extendedTextMessage"].(map[string]any); ok {
		if t, ok2 := v["text"].(string); ok2 {
			return t
		}
	}
	if v, ok := m["imageMessage"].(map[string]any); ok {
		return renderMediaSummary(mediaTagStyle("image").Render(mediaIconLabel("image")), v)
	}
	if v, ok := m["videoMessage"].(map[string]any); ok {
		return renderMediaSummary(mediaTagStyle("video").Render(mediaIconLabel("video")), v)
	}
	if v, ok := m["documentMessage"].(map[string]any); ok {
		return renderMediaSummary(mediaTagStyle("file").Render(mediaIconLabel("file")), v)
	}
	if v, ok := m["audioMessage"].(map[string]any); ok {
		if ptt, _ := v["ptt"].(bool); ptt {
			return mediaTagStyle("audio").Render(mediaIconLabel("voice"))
		}
		return mediaTagStyle("audio").Render(mediaIconLabel("audio"))
	}
	if _, ok := m["stickerMessage"]; ok {
		return mediaTagStyle("sticker").Render(mediaIconLabel("sticker"))
	}
	if v, ok := m["reactionMessage"].(map[string]any); ok {
		if emoji, _ := v["emoji"].(string); emoji != "" {
			return emoji
		}
		return "[reaction]"
	}
	if v, ok := m["pollCreationMessage"].(map[string]any); ok {
		name, _ := v["name"].(string)
		var opts []string
		if rawOpts, ok := v["options"].([]any); ok {
			for _, o := range rawOpts {
				if s, ok := o.(string); ok && s != "" {
					opts = append(opts, s)
				}
			}
		} else if rawOpts, ok := v["options"].([]string); ok {
			for _, s := range rawOpts {
				if s != "" {
					opts = append(opts, s)
				}
			}
		}
		cardWidth := len(name) + 6
		for _, o := range opts {
			if len(o)+8 > cardWidth {
				cardWidth = len(o) + 8
			}
		}
		if cardWidth < 30 {
			cardWidth = 30
		}
		if cardWidth > 38 {
			cardWidth = 38
		}
		accentCol := lipgloss.Color(currentTheme.Accent)
		mutedCol := lipgloss.Color(currentTheme.Muted)
		tagStyle := mediaTagStyle("poll")
		borderStyle := lipgloss.NewStyle().Foreground(mutedCol)
		accentBorderStyle := lipgloss.NewStyle().Foreground(accentCol)
		var sb strings.Builder
		badge := tagStyle.Render(" " + mediaIconLabel("poll") + " ")
		topLen := cardWidth - lipgloss.Width(badge) - 4
		if topLen < 2 {
			topLen = 2
		}
		sb.WriteString(borderStyle.Render(" ╭─") + badge + borderStyle.Render(strings.Repeat("─", topLen)+"╮") + "\n")
		qText := " " + name
		if len([]rune(qText)) > cardWidth-4 {
			qText = string([]rune(qText)[:cardWidth-7]) + "..."
		} else {
			qText = qText + strings.Repeat(" ", cardWidth-4-len([]rune(qText)))
		}
		sb.WriteString(borderStyle.Render(" │ ") + lipgloss.NewStyle().Bold(true).Foreground(accentCol).Render(qText) + borderStyle.Render("│") + "\n")
		sb.WriteString(borderStyle.Render(" ├"+strings.Repeat("─", cardWidth-3)+"┤") + "\n")
		for _, opt := range opts {
			oText := opt
			if len([]rune(oText)) > cardWidth-8 {
				oText = string([]rune(oText)[:cardWidth-11]) + "..."
			} else {
				oText = oText + strings.Repeat(" ", cardWidth-8-len([]rune(oText)))
			}
			bullet := accentBorderStyle.Render("◯")
			sb.WriteString(borderStyle.Render(" │  ") + bullet + " " + oText + borderStyle.Render(" │") + "\n")
		}
		sb.WriteString(borderStyle.Render(" ╰" + strings.Repeat("─", cardWidth-3) + "╯"))
		return sb.String()
	}
	if v, ok := m["pollUpdateMessage"].(map[string]any); ok {
		names, hasNames := v["selectedOptionNames"]
		if hasNames {
			switch opts := names.(type) {
			case []string:
				if len(opts) == 0 {
					return anomalyTagStyle.Render("[removed vote]")
				}
				return mediaTagStyle("poll").Render(mediaIconLabel("poll")) + " voted \"" + strings.Join(opts, ", ") + "\" on poll"
			case []any:
				strs := make([]string, 0, len(opts))
				for _, o := range opts {
					if s, ok := o.(string); ok {
						strs = append(strs, s)
					}
				}
				if len(strs) == 0 {
					return anomalyTagStyle.Render("[removed vote]")
				}
				return mediaTagStyle("poll").Render(mediaIconLabel("poll")) + " voted \"" + strings.Join(strs, ", ") + "\" on poll"
			}
		}
		return mediaTagStyle("poll").Render(mediaIconLabel("poll")) + " voted on poll"
	}
	if v, ok := m["protocolMessage"].(map[string]any); ok {
		if label := renderProtocolMessage(v); label != "" {
			return label
		}
	}
	if v, ok := m["unknown"].(map[string]any); ok {
		if label := unknownMessageLabel(v); label != "" {
			if label == "pollUpdateMessage" {
				return mediaTagStyle("poll").Render(mediaIconLabel("poll")) + " voted on poll"
			}
			return renderUnknownTag(label)
		}
		return mediaTagStyle("anomaly").Render(mediaIconLabel("anomaly"))
	}
	return ""
}

// renderMediaSummary renders a media message as a multi-line block:
// line 1 is the kind tag (icon or "[kind]"), line 2 is the filename
// (if present), line 3 is the caption (if present). Stickers, voice
// notes, and other media without filename or caption render as a
// single line. The rendering code applies muted styling to the
// filename line and body styling to the caption line.
func renderMediaSummary(tag string, payload map[string]any) string {
	name, _ := payload["fileName"].(string)
	caption, _ := payload["caption"].(string)

	lines := []string{tag}
	if name != "" {
		lines = append(lines, name)
	}
	if caption != "" {
		lines = append(lines, caption)
	}
	return strings.Join(lines, "\n")
}

func renderProtocolMessage(v map[string]any) string {
	typ, _ := v["type"].(string)
	switch strings.TrimSpace(typ) {
	case "REVOKE":
		return anomalyTagStyle.Render("[message deleted]")
	case "MESSAGE_EDIT":
		if edited, _ := v["editedText"].(string); strings.TrimSpace(edited) != "" {
			return anomalyTagStyle.Render("[message edited]") + " " + edited
		}
		return anomalyTagStyle.Render("[message edited]")
	case "EPHEMERAL_SETTING":
		if seconds, ok := protocolUint(v["ephemeralExpiration"]); ok {
			return anomalyTagStyle.Render("[disappearing messages]") + " " + formatEphemeralDuration(seconds)
		}
		return anomalyTagStyle.Render("[disappearing messages changed]")
	default:
		if typ == "" {
			return ""
		}
		return anomalyTagStyle.Render("[system: " + strings.ToLower(strings.ReplaceAll(typ, "_", " ")) + "]")
	}
}

func protocolUint(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case uint32:
		return uint64(n), true
	case int:
		if n >= 0 {
			return uint64(n), true
		}
	case float64:
		if n >= 0 {
			return uint64(n), true
		}
	}
	return 0, false
}

func formatEphemeralDuration(seconds uint64) string {
	switch seconds {
	case 0:
		return "off"
	case 24 * 60 * 60:
		return "24h"
	case 7 * 24 * 60 * 60:
		return "7d"
	case 90 * 24 * 60 * 60:
		return "90d"
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func renderUnknownTag(label string) string {
	known := label == "image" || label == "video" || label == "audio" ||
		label == "voice" || label == "file" || label == "sticker" ||
		label == "contact" || label == "poll" || label == "location" ||
		label == "anomaly"
	if known {
		return mediaTagStyle(label).Render(mediaIconLabel(label))
	}
	if label == "" {
		return mediaTagStyle("anomaly").Render(mediaIconLabel("anomaly"))
	}
	return mediaTagStyle("anomaly").Render("[" + label + "]")
}

// mediaIconLabel returns the tag text for a given media kind. With
// MediaIconStyle == "nerd" it returns a Nerd Font glyph followed by the
// kind name (e.g. "<image-glyph> image") — the icon is too small to read on
// its own at terminal cell sizes, so the kind name carries the meaning and
// the glyph is a visual marker. In the default text mode it returns the
// plain bracketed text (e.g. "[image]").
func mediaIconLabel(kind string) string {
	if currentConfig.MediaIconStyle == "nerd" || currentConfig.MediaViewStyle == "glyph" || (currentConfig.MediaViewStyle == "pixel" && kind != "image") {
		if g := nerdIconFor(kind); g != "" {
			return g + " " + kind
		}
	}
	return "[" + kind + "]"
}

// mediaTagColor returns the theme color associated with a media kind.
// "voice" shares the audio color since it is conceptually audio.
func mediaTagColor(kind string) lipgloss.Color {
	switch kind {
	case "image":
		return imageTag
	case "video":
		return videoTag
	case "audio", "voice":
		return audioTag
	case "file":
		return fileTag
	case "sticker":
		return stickerTag
	case "contact":
		return contactTag
	case "poll":
		return pollTag
	case "location":
		return locationTag
	case "anomaly":
		return anomalyTag
	}
	return muted
}

// mediaTagStyle returns the style used to render a media-kind tag in chat
// message bodies. In the default (text) mode it is the "pill" style used
// since before the icon feature: a saturated background with dark text. In
// Nerd mode it switches to the file-browser style: a saturated foreground
// with no background, so the icon glyph is actually visible at terminal cell
// sizes (the pill style would render the icon as a dark blob on a colored
// background, which reads as a smudge).
func mediaTagStyle(kind string) lipgloss.Style {
	if currentConfig.MediaIconStyle == "nerd" {
		return lipgloss.NewStyle().Foreground(mediaTagColor(kind)).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(tagInk).Background(mediaTagColor(kind)).Bold(true)
}

// nerdIconFor returns a Nerd Font (Material Design) glyph for the given media
// kind, or "" if no glyph is defined for that kind. Codepoints are the
// Nerd Fonts v3.0+ Material Design set, which lives in the SMP Private Use
// Area (U+F0000+). Render these with a Nerd Font installed; otherwise your
// terminal will show tofu (e.g. "[]") for each glyph. See
// https://www.nerdfonts.com/cheat-sheet and the i_md.sh file in
// https://github.com/ryanoasis/nerd-fonts.
func nerdIconFor(kind string) string {
	switch kind {
	case "image":
		return "\U000F02E9" // nf-md-image
	case "video":
		return "\U000F022F" // nf-md-film
	case "file":
		return "\U000F0219" // nf-md-file-document
	case "audio":
		return "\U000F0387" // nf-md-music_note
	case "voice":
		return "\U000F036C" // nf-md-microphone
	case "sticker":
		return "\U000F0785" // nf-md-sticker-emoji
	case "contact":
		return "\U000F0004" // nf-md-account
	case "location":
		return "\U000F034E" // nf-md-map-marker
	case "poll":
		return "\U000F041F" // nf-md-poll
	case "anomaly":
		return "\U000F0028" // nf-md-alert-circle
	}
	return ""
}

func unknownMessageLabel(v map[string]any) string {
	for _, key := range []string{"effectiveFields", "rawFields"} {
		if label := firstUnknownField(v[key]); label != "" {
			return prettyUnknownLabel(label)
		}
	}
	return ""
}

func prettyUnknownLabel(label string) string {
	switch label {
	case "imageMessage", "eventCoverImage", "pollCreationOptionImageMessage":
		return "image"
	case "videoMessage", "ptvMessage":
		return "video"
	case "audioMessage":
		return "audio"
	case "documentMessage", "documentWithCaptionMessage":
		return "file"
	case "stickerMessage", "lottieStickerMessage", "stickerPackMessage", "statusStickerInteractionMessage":
		return "sticker"
	case "contactMessage":
		return "contact"
	case "pollCreationMessage", "pollCreationMessageV2", "pollCreationMessageV3", "pollCreationMessageV4", "pollCreationMessageV5":
		return "poll"
	case "locationMessage", "liveLocationMessage":
		return "location"
	default:
		return label
	}
}

func firstUnknownField(v any) string {
	switch vals := v.(type) {
	case []string:
		for _, s := range vals {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	case []any:
		for _, item := range vals {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func isMediaWire(msg wireMsg) bool {
	if msg.MediaProto != "" {
		return true
	}
	m := msg.Message
	if m == nil {
		return false
	}
	if _, ok := m["conversation"]; ok && len(m) == 1 {
		return false
	}
	if _, ok := m["extendedTextMessage"]; ok && len(m) == 1 {
		return false
	}
	if _, ok := m["imageMessage"]; ok {
		return true
	}
	if _, ok := m["videoMessage"]; ok {
		return true
	}
	if _, ok := m["documentMessage"]; ok {
		return true
	}
	if _, ok := m["audioMessage"]; ok {
		return true
	}
	if _, ok := m["stickerMessage"]; ok {
		return true
	}
	if _, ok := m["documentWithCaptionMessage"]; ok {
		return true
	}
	if _, ok := m["viewOnceMessage"]; ok {
		return true
	}
	if _, ok := m["viewOnceMessageV2"]; ok {
		return true
	}
	if _, ok := m["viewOnceMessageV2Extension"]; ok {
		return true
	}
	if _, ok := m["ephemeralMessage"]; ok {
		return true
	}
	if _, ok := m["locationMessage"]; ok {
		return true
	}
	if _, ok := m["liveLocationMessage"]; ok {
		return true
	}
	if _, ok := m["contactMessage"]; ok {
		return true
	}
	if _, ok := m["contactsArrayMessage"]; ok {
		return true
	}
	if _, ok := m["pollCreationMessage"]; ok {
		return true
	}
	if _, ok := m["ptvMessage"]; ok {
		return true
	}
	for k := range m {
		if k != "conversation" && k != "extendedTextMessage" && k != "reactionMessage" {
			return true
		}
	}
	return false
}

func commandBestMatch(input string) string {
	in := strings.ToLower(strings.TrimSpace(input))
	if !strings.HasPrefix(in, "/") {
		return ""
	}
	if theme := themeCommandBestMatch(in); theme != "" {
		return theme
	}
	for _, c := range systemCommands {
		if strings.HasPrefix(c, in) && c != in {
			return c
		}
	}
	return ""
}

func chatCommandBestMatch(input string) string {
	in := strings.ToLower(strings.TrimSpace(input))
	if !strings.HasPrefix(in, "/") {
		return ""
	}
	for _, c := range chatCommands {
		if strings.HasPrefix(c, in) && c != in {
			return c
		}
	}
	return ""
}

func isMediaSendCommand(cmd string) bool {
	switch strings.ToLower(strings.TrimSpace(cmd)) {
	case "/send", "/sendimage", "/sendvideo", "/sendfile":
		return true
	default:
		return false
	}
}

func mediaSendCommandPrefix(input string) (string, string, bool) {
	in := strings.TrimSpace(input)
	for _, cmd := range chatCommands {
		if in == cmd {
			return cmd, "", true
		}
		if strings.HasPrefix(in, cmd+" ") {
			return cmd, strings.TrimSpace(strings.TrimPrefix(in, cmd)), true
		}
	}
	for _, cmd := range []string{"/sendimage", "/sendvideo", "/sendfile"} {
		if in == cmd {
			return cmd, "", true
		}
		if strings.HasPrefix(in, cmd+" ") {
			return cmd, strings.TrimSpace(strings.TrimPrefix(in, cmd)), true
		}
	}
	return "", "", false
}

func completeChatInputStep(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}

	if best := chatCommandBestMatch(trimmed); best != "" {
		if isMediaSendCommand(best) {
			return best + " ", true
		}
		return best, true
	}

	cmd, rest, ok := mediaSendCommandPrefix(trimmed)
	if !ok {
		return "", false
	}
	if rest == "" && !strings.HasSuffix(input, " ") {
		return cmd + " ", true
	}
	if rest == "" {
		return "", false
	}

	_, caption, parsed := splitMediaPathAndCaption(rest)
	if parsed && caption == "" && !strings.HasSuffix(input, " ") {
		return input + " ", true
	}
	return "", false
}

func chatInputGhost(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}

	if best := chatCommandBestMatch(trimmed); best != "" && strings.HasPrefix(best, strings.ToLower(trimmed)) {
		return best[len(trimmed):]
	}

	_, rest, ok := mediaSendCommandPrefix(trimmed)
	if !ok {
		return ""
	}
	if rest == "" {
		if strings.HasSuffix(input, " ") {
			return "<file path>"
		}
		return " <file path>"
	}

	_, caption, parsed := splitMediaPathAndCaption(rest)
	if parsed && caption == "" {
		if strings.HasSuffix(input, " ") {
			return "[caption]"
		}
		return " [caption]"
	}
	return ""
}

func themeCommandBestMatch(in string) string {
	switch {
	case strings.HasPrefix("/theme", in) && in != "/theme":
		return "/theme"
	case in == "/theme":
		if len(themeList) == 0 {
			return ""
		}
		return "/theme1" + themeList[0].name
	}
	for i, t := range themeList {
		prefix := fmt.Sprintf("/theme%d", i+1)
		full := prefix + t.name
		if strings.HasPrefix(in, prefix) && in != full {
			return full
		}
	}
	return ""
}

func parseMediaSendCommand(input string) (mediaSendCommand, string, bool) {
	type spec struct {
		cmd   string
		kind  string
		usage string
	}
	specs := []spec{
		{cmd: "/send", kind: "", usage: "usage: /send <path> [caption]"},
		// Old send aliases.
		{cmd: "/sendimage", kind: "image", usage: "usage: /sendimage <path> [caption]"},
		{cmd: "/sendvideo", kind: "video", usage: "usage: /sendvideo <path> [caption]"},
		{cmd: "/sendfile", kind: "document", usage: "usage: /sendfile <path> [caption]"},
	}
	in := strings.TrimSpace(input)
	for _, s := range specs {
		if in != s.cmd && !strings.HasPrefix(in, s.cmd+" ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(in, s.cmd))
		if rest == "" {
			return mediaSendCommand{}, s.usage, true
		}
		path, caption, ok := splitMediaPathAndCaption(rest)
		if !ok {
			return mediaSendCommand{}, s.usage + `; quote paths with spaces if needed`, true
		}
		return mediaSendCommand{kind: s.kind, path: path, caption: caption}, "", true
	}
	return mediaSendCommand{}, "", false
}

func splitMediaPathAndCaption(s string) (string, string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if s[0] == '"' || s[0] == '\'' {
		quote := s[0]
		end := strings.IndexByte(s[1:], quote)
		if end < 0 {
			return "", "", false
		}
		path := s[1 : 1+end]
		caption := strings.TrimSpace(s[2+end:])
		if path == "" {
			return "", "", false
		}
		return path, caption, true
	}
	if stat, err := os.Stat(s); err == nil && !stat.IsDir() {
		return s, "", true
	}
	parts := strings.Fields(s)
	for i := len(parts); i >= 1; i-- {
		path := strings.Join(parts[:i], " ")
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return path, strings.Join(parts[i:], " "), true
		}
	}
	return "", "", false
}

func detectMediaSendKind(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("file not found")
	}
	if info.IsDir() {
		return "", fmt.Errorf("path must be a file")
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return "image", nil
	case ".mp4", ".mov", ".avi", ".mkv", ".webm", ".3gp", ".m4v":
		return "video", nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file")
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read file")
	}
	mimeType := http.DetectContentType(buf[:n])
	if strings.HasPrefix(mimeType, "image/") {
		return "image", nil
	}
	if strings.HasPrefix(mimeType, "video/") {
		return "video", nil
	}
	return "document", nil
}

// blendHex linearly interpolates between two "#RRGGBB" colors by t (0.0–1.0).
func blendHex(a, b string, t float64) string {
	parse := func(h string) (int, int, int) {
		if len(h) == 7 && h[0] == '#' {
			var r, g, bb int
			fmt.Sscanf(h, "#%02x%02x%02x", &r, &g, &bb)
			return r, g, bb
		}
		return 0, 0, 0
	}
	r1, g1, b1 := parse(a)
	r2, g2, b2 := parse(b)
	mix := func(c1, c2 int) int {
		v := float64(c1) + t*(float64(c2)-float64(c1))
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return int(v)
	}
	return fmt.Sprintf("#%02x%02x%02x", mix(r1, r2), mix(g1, g2), mix(b1, b2))
}

func dateSeparatorLine(label string, w int) string {
	if label == "" {
		return ""
	}
	styled := dateSepStyle.Render(label)
	return lipgloss.NewStyle().Width(w - 2).Align(lipgloss.Center).Render(styled)
}

// renderSnippet renders a search snippet that may contain <b>...</b> highlight
// tags. Bold segments are rendered with the accent colour. The visible text is
// truncated to maxW runes before rendering so the layout stays intact.
func renderSnippet(snippet string, maxW int) string {
	// Split on tag boundaries while preserving whether each segment is bold.
	type seg struct {
		text string
		bold bool
	}
	var segs []seg
	rest := snippet
	for rest != "" {
		open := strings.Index(rest, "<b>")
		if open == -1 {
			segs = append(segs, seg{rest, false})
			break
		}
		if open > 0 {
			segs = append(segs, seg{rest[:open], false})
		}
		rest = rest[open+3:]
		close := strings.Index(rest, "</b>")
		if close == -1 {
			segs = append(segs, seg{rest, true})
			break
		}
		segs = append(segs, seg{rest[:close], true})
		rest = rest[close+4:]
	}

	// Truncate total visible width to maxW runes.
	remaining := maxW
	var out strings.Builder
	for _, s := range segs {
		runes := []rune(s.text)
		if len(runes) > remaining {
			runes = runes[:remaining]
		}
		if len(runes) == 0 {
			continue
		}
		t := string(runes)
		if s.bold {
			out.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Accent)).Render(t))
		} else {
			out.WriteString(t)
		}
		remaining -= len(runes)
		if remaining <= 0 {
			break
		}
	}
	return out.String()
}

// stripSnippetTags returns the visible text of a snippet with <b>...</b> tags
// removed and truncated to maxW runes. Used for the reverse-video selected row
// where bold styling would conflict with the highlight colour.
func stripSnippetTags(snippet string, maxW int) string {
	plain := strings.ReplaceAll(snippet, "<b>", "")
	plain = strings.ReplaceAll(plain, "</b>", "")
	runes := []rune(plain)
	if len(runes) > maxW {
		return string(runes[:maxW-1]) + "…"
	}
	return plain
}

func renderPixelArt(localPath string, maxW int) string {
	f, err := os.Open(localPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return ""
	}
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width == 0 || height == 0 {
		return ""
	}
	w := maxW
	if w > 36 {
		w = 36
	}
	if w < 10 {
		w = 10
	}
	h := (w * height) / width
	h = (h / 2) * 2
	if h < 2 {
		h = 2
	}
	var sb strings.Builder
	for cy := 0; cy < h/2; cy++ {
		for cx := 0; cx < w; cx++ {
			srcX := (cx * width) / w
			srcYTop := (cy * 2 * height) / h
			srcYBot := ((cy*2 + 1) * height) / h
			cTop := img.At(bounds.Min.X+srcX, bounds.Min.Y+srcYTop)
			cBot := img.At(bounds.Min.X+srcX, bounds.Min.Y+srcYBot)
			r1, g1, b1, _ := cTop.RGBA()
			r2, g2, b2, _ := cBot.RGBA()
			r1, g1, b1 = r1/257, g1/257, b1/257
			r2, g2, b2 = r2/257, g2/257, b2/257
			sb.WriteString(fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀\x1b[0m", r1, g1, b1, r2, g2, b2))
		}
		if cy < h/2-1 {
			sb.WriteRune('\n')
		}
	}
	return sb.String()
}
