package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (x *m) setTopBar(msg string) tea.Cmd {
	x.topBarVer++
	x.topBarMsg = msg
	x.topBarShown = 0
	ver := x.topBarVer
	d := 2200 * time.Millisecond
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "fail") || strings.Contains(lower, "err") || strings.Contains(lower, "invalid") || strings.Contains(lower, "not") {
		d = 10000 * time.Millisecond
	}
	return tea.Batch(nextTopBarTypeTick(ver), clearTopBarAfter(ver, d))
}
func nextTopBarTypeTick(ver int) tea.Cmd {
	return tea.Tick(28*time.Millisecond, func(time.Time) tea.Msg { return topBarTypeMsg{ver: ver} })
}
func nextCursorBlink() tea.Cmd {
	return tea.Tick(530*time.Millisecond, func(time.Time) tea.Msg { return cursorBlinkMsg{} })
}
func nextSpinnerTick() tea.Cmd {
	return tea.Tick(102*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
func clearTopBarAfter(ver int, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return topBarClearMsg{ver: ver} })
}
func setTopBarAfter(msg string, d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return topBarSetMsg{msg: msg} })
}
func padRight(s string, n int) string {
	w := runeDisplayWidth(s)
	if w == n {
		return s
	}
	if w > n {
		var b strings.Builder
		used := 0
		rs := []rune(s)
		for i := 0; i < len(rs); {
			rw, consumed := nextGlyphWidth(rs, i)
			if used+rw > n {
				break
			}
			b.WriteString(string(rs[i : i+consumed]))
			used += rw
			i += consumed
		}
		return b.String()
	}
	return s + strings.Repeat(" ", n-w)
}

// incomingLeftIcon returns the left-side icon glyph for a given line of a
// received message. Pipe-like icons use half-height box-drawing chars so each
// message forms a self-contained capsule: ╷ on the first line (open at top),
// │ on middle lines, ╵ on the last body line (open at bottom). Single-line
// messages use ╷ only - adjacent ╷ chars don't visually connect because each
// leaves the cell's top half empty, creating a gap between messages.
func incomingLeftIcon(lineIdx int, isLastLine bool) string {
	switch receivedMsgIcon {
	case "│":
		if lineIdx == 0 {
			return "╷"
		}
		if isLastLine {
			return "╵"
		}
		return "│"
	case "┃":
		if lineIdx == 0 {
			return "╻"
		}
		if isLastLine {
			return "╹"
		}
		return "┃"
	case "║":
		if lineIdx == 0 {
			return "╷"
		}
		if isLastLine {
			return "╵"
		}
		return "║"
	}
	if lineIdx == 0 {
		return receivedMsgIcon
	}
	return strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon))
}

// outgoingRightIcon returns the right-side icon glyph for a given line of an
// outgoing message in 2-line mode. Pipe-like icons use half-height box-drawing
// chars (╷ top, ╵ bottom) so each message forms a self-contained capsule and
// adjacent messages' bars don't appear connected. Other icons are used as-is.
func outgoingRightIcon(lineIdx int, isTimestamp bool) string {
	switch receivedMsgIcon {
	case "│":
		if isTimestamp {
			return "╵"
		}
		return "│"
	case "┃":
		if isTimestamp {
			return "╹"
		}
		return "┃"
	case "║":
		if isTimestamp {
			return "╵"
		}
		return "║"
	}
	if isTimestamp {
		return receivedMsgIcon
	}
	return strings.Repeat(" ", runeDisplayWidth(receivedMsgIcon))
}

func renderShine(text string, baseStyle, shineStyle lipgloss.Style, frame int) string {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return ""
	}
	cycle := 2 * (n - 1)
	if cycle < 1 {
		cycle = 1
	}
	f := frame % cycle
	center := f
	if f >= n {
		center = cycle - f
	}
	baseFg, _ := baseStyle.GetForeground().(lipgloss.Color)
	shineFg, _ := shineStyle.GetForeground().(lipgloss.Color)
	mid1 := lerpColor(baseFg, shineFg, 0.15)
	mid2 := lerpColor(baseFg, shineFg, 0.35)
	mid3 := lerpColor(baseFg, shineFg, 0.70)
	// Inherit the base background so the wave doesn't punch holes in a
	// highlighted row's background as it travels.
	bg := baseStyle.GetBackground()
	m3 := lipgloss.NewStyle().Foreground(mid3).Bold(true).Background(bg)
	m2 := lipgloss.NewStyle().Foreground(mid2).Background(bg)
	m1 := lipgloss.NewStyle().Foreground(mid1).Background(bg)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		dist := i - center
		if dist < 0 {
			dist = -dist
		}
		ch := string(runes[i])
		switch {
		case dist == 0:
			sb.WriteString(shineStyle.Render(ch))
		case dist == 1:
			sb.WriteString(m3.Render(ch))
		case dist == 2:
			sb.WriteString(m2.Render(ch))
		case dist == 3:
			sb.WriteString(m1.Render(ch))
		default:
			sb.WriteString(baseStyle.Render(ch))
		}
	}
	return sb.String()
}

func hexToRGB(c lipgloss.Color) (r, g, b uint8, ok bool) {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	rv, e1 := strconv.ParseUint(s[1:3], 16, 8)
	gv, e2 := strconv.ParseUint(s[3:5], 16, 8)
	bv, e3 := strconv.ParseUint(s[5:7], 16, 8)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false
	}
	return uint8(rv), uint8(gv), uint8(bv), true
}

func lerpColor(a, b lipgloss.Color, t float64) lipgloss.Color {
	ar, ag, ab, aok := hexToRGB(a)
	br, bg, bb, bok := hexToRGB(b)
	if !aok || !bok {
		if t < 0.5 {
			return a
		}
		return b
	}
	r := uint8(float64(ar) + t*(float64(br)-float64(ar)))
	g := uint8(float64(ag) + t*(float64(bg)-float64(ag)))
	bv := uint8(float64(ab) + t*(float64(bb)-float64(ab)))
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", r, g, bv))
}
