package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	linkPanelW = 52
	qrTTL      = 60 * time.Second
)

// spreadLine puts left and right at opposite ends of a width-wide line.
func spreadLine(left, right string, width int) string {
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

// renderLinkPanel is the left column of the QR login screen, laid out to be
// exactly height rows tall (when it fits) so it lines up with the QR box.
func (x m) renderLinkPanel(height int) string {
	sub := lerpColor(muted, text, 0.55)
	rail := lerpColor(muted, brand, 0.35)
	subSt := lipgloss.NewStyle().Foreground(sub)
	textSt := lipgloss.NewStyle().Foreground(text)
	brandSt := lipgloss.NewStyle().Foreground(brand)
	railSt := lipgloss.NewStyle().Foreground(rail)

	// Header: the shared bolt and wordmark logo (see logo.go) beside the
	// tagline, so this stays in sync with the splash screen's logo.
	mark := lipgloss.JoinVertical(lipgloss.Left,
		renderPixelWordmark(),
		"",
		subSt.Render("Private WhatsApp, right in your terminal"),
	)
	header := lipgloss.JoinHorizontal(lipgloss.Center, renderZapBolt(x.shineFrame), "   ", mark)

	headline := []string{
		brandSt.Render("") + " " + textSt.Bold(true).Render("Here's how to link your phone"),
		subSt.Render("Don't worry, your data stays on your device."),
	}

	// Steps as a timeline; the last one is what the user is doing right now,
	// so it pulses.
	type step struct{ title, desc string }
	steps := []step{
		{"Open WhatsApp", "on your phone"},
		{"Open Linked devices", "⋮ Menu on Android · Settings on iOS"},
		{"Tap Link a device", "Confirm with your PIN or biometrics"},
		{"Point your camera at the QR", "Hold steady until it links"},
	}
	pulse := float64(x.shineFrame%16) / 8
	if pulse > 1 {
		pulse = 2 - pulse
	}
	var stepLines []string
	for i, s := range steps {
		chipBg := brand
		titleSt := textSt.Bold(true)
		title := titleSt.Render(s.title)
		last := i == len(steps)-1
		if last {
			chipBg = lerpColor(brand, "#FFFFFF", 0.35*pulse)
			title = brandSt.Bold(true).Render(s.title)
		}
		chip := lipgloss.NewStyle().Foreground(qrDark).Background(chipBg).Bold(true).Render(fmt.Sprintf(" %d ", i+1))
		railGlyph := " │ "
		if last {
			railGlyph = "   "
		}
		stepLines = append(stepLines,
			chip+"  "+title,
			railSt.Render(railGlyph)+"  "+subSt.Render(s.desc),
		)
		if !last {
			stepLines = append(stepLines, railSt.Render(railGlyph))
		}
	}

	// Countdown: a slim bar that drains in half-cell steps and warms from
	// brand to amber to red, above a live status line.
	received := x.qrReceivedAt
	if received.IsZero() {
		received = time.Now()
	}
	left := max(0, qrTTL-time.Since(received))
	frac := float64(left) / float64(qrTTL)
	fill := brand
	switch {
	case frac < 0.2:
		fill = lerpColor(red, amber, frac/0.2)
	case frac < 0.5:
		fill = lerpColor(amber, brand, (frac-0.2)/0.3)
	}
	fillSt := lipgloss.NewStyle().Foreground(fill)
	trackSt := lipgloss.NewStyle().Foreground(lerpColor(muted, qrDark, 0.3))
	filled := int(frac*float64(linkPanelW) + 0.5)
	var bar strings.Builder
	for i := range linkPanelW {
		if i < filled {
			bar.WriteString(fillSt.Render("━"))
		} else {
			bar.WriteString(trackSt.Render("━"))
		}
	}
	spinner := brandSt.Render(nodeFrames[x.spinnerFrame%len(nodeFrames)])
	waiting := renderShine("Waiting for scan", brandSt, lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true), x.shineFrame)
	secs := int((left + time.Second - 1) / time.Second)
	countdown := subSt.Render("new code in ") + textSt.Bold(true).Render(fmt.Sprintf("%ds", secs))
	if secs == 0 {
		countdown = subSt.Render("refreshing…")
	}
	timer := []string{bar.String(), spreadLine(spinner+" "+waiting, countdown, linkPanelW)}

	footer := []string{
		railSt.Render(strings.Repeat("─", linkPanelW)),
		spreadLine(
			brandSt.Render("\U000F033E")+" "+subSt.Render("Encrypted · v0.9"),
			brandSt.Bold(true).Render("ctrl+c")+" "+subSt.Italic(true).Render("quit"),
			linkPanelW,
		),
	}

	// Spread the sections so the header sits on the QR box's top edge and
	// the footer on its bottom edge.
	blocks := [][]string{strings.Split(header, "\n"), headline, stepLines, timer, footer}
	fixed := 0
	for _, b := range blocks {
		fixed += len(b)
	}
	gaps := len(blocks) - 1
	extra := max(gaps, height-fixed)
	var out []string
	for i, b := range blocks {
		if i > 0 {
			n := extra / gaps
			if i-1 < extra%gaps {
				n++
			}
			for range n {
				out = append(out, "")
			}
		}
		out = append(out, b...)
	}
	pad := lipgloss.NewStyle().Width(linkPanelW)
	for i, l := range out {
		out[i] = pad.Render(l)
	}
	return strings.Join(out, "\n")
}
