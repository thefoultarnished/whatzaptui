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

// qrExpired reports whether the displayed QR code is past its usable life
// (qrTTL plus a small grace for in-flight refreshes). A new "qr" event
// resets x.qrReceivedAt to time.Now(), so this flips back to false on its
// own when WhatsApp delivers a fresh code.
func (x m) qrExpired() bool {
	if x.qrReceivedAt.IsZero() {
		return false
	}
	return time.Since(x.qrReceivedAt) >= qrTTL+5*time.Second
}

// renderLinkPanel is the left column of the QR login screen, laid out to be
// exactly height rows tall (when it fits) so it lines up with the QR box.
func (x m) renderLinkPanel(height int) string {
	sub := v2Color(textSecondary, lerpColor(muted, text, 0.55))
	rail := v2Color(borderSubtle, lerpColor(muted, brand, 0.35))
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
	headerLines := append(strings.Split(header, "\n"),
		railSt.Render(strings.Repeat("─", linkPanelW)),
		"",
		lipgloss.PlaceHorizontal(linkPanelW, lipgloss.Center, brandSt.Bold(true).Italic(true).Render("Ready to link your device?")),
	)

	headline := []string{
		brandSt.Render("\U000F02FD") + " " + textSt.Bold(true).Render("Scan the QR with Your Phone"),
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
	// buildSteps repeats the connector line between steps stretch times, so
	// leftover height can expand the timeline itself instead of piling up
	// as blank gaps elsewhere.
	buildSteps := func(stretch int) []string {
		var lines []string
		for i, s := range steps {
			title := textSt.Bold(true).Render(s.title)
			last := i == len(steps)-1
			chip := lipgloss.NewStyle().Foreground(v2Color(textOnFill, qrDark)).Background(brand).Bold(true).Render(fmt.Sprintf(" %d ", i+1))
			railGlyph := " │ "
			if last {
				railGlyph = "   "
			}
			lines = append(lines,
				chip+"  "+title,
				railSt.Render(railGlyph)+"  "+subSt.Render(s.desc),
			)
			if !last {
				for range stretch {
					lines = append(lines, railSt.Render(railGlyph))
				}
			}
		}
		return lines
	}
	stepLines := buildSteps(1)

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
	trackSt := lipgloss.NewStyle().Foreground(v2Color(textFaint, lerpColor(muted, qrDark, 0.3)))
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
	waiting := renderShine("Waiting for scan", brandSt, lipgloss.NewStyle().Foreground(fxShine).Bold(true), x.shineFrame)
	secs := int((left + time.Second - 1) / time.Second)
	countdown := subSt.Render("new code in ") + textSt.Bold(true).Render(fmt.Sprintf("%ds", secs))
	if secs == 0 {
		countdown = subSt.Render("refreshing…")
	}
	timer := []string{bar.String(), spreadLine(spinner+" "+waiting, countdown, linkPanelW)}
	if x.qrExpired() {
		amberSt := lipgloss.NewStyle().Foreground(amber).Bold(true)
		timer = []string{bar.String(), spreadLine(amberSt.Render("⚠ QR expired"), amberSt.Render("[R] Restart session"), linkPanelW)}
	}

	footer := []string{
		railSt.Render(strings.Repeat("─", linkPanelW)),
		spreadLine(
			brandSt.Render("\U000F033E")+" "+subSt.Render("Encrypted · v0.9"),
			brandSt.Bold(true).Render("ctrl+c")+" "+subSt.Italic(true).Render("quit"),
			linkPanelW,
		),
	}

	// Spread the sections so the header sits on the QR box's top edge and
	// the footer on its bottom edge. Gaps between sections are capped so
	// leftover height on a tall terminal doesn't pile up as dead space
	// between unrelated blocks - past the cap, extra room instead stretches
	// the step timeline's own connector lines, which reads as an
	// intentional, evenly-paced list rather than empty gaps.
	const maxGap = 3
	const maxStretch = 6
	blocks := [][]string{headerLines, headline, stepLines, timer, footer}
	gaps := len(blocks) - 1
	sum := func() int {
		n := 0
		for _, b := range blocks {
			n += len(b)
		}
		return n
	}
	extra := max(gaps, height-sum())
	// Grow the timeline's connector lines to soak up leftover height instead
	// of it piling into a handful of blank gaps - but only up to maxStretch,
	// so a very tall terminal doesn't turn the timeline itself into a wall
	// of blank connector lines. Past that, the final loop below falls back
	// to plain even gaps; since it never clamps, the total always still
	// adds up to exactly height.
	for stretch := 2; stretch <= maxStretch && extra/gaps > maxGap; stretch++ {
		blocks[2] = buildSteps(stretch)
		extra = max(gaps, height-sum())
	}
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
