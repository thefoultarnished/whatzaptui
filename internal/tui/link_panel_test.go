package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestRenderLinkPanelMatchesHeightAndWidth(t *testing.T) {
	x := m{qrReceivedAt: time.Now()}
	for _, h := range []int{31, 40} {
		out := x.renderLinkPanel(h)
		if got := lipgloss.Height(out); got != h {
			t.Fatalf("height=%d: panel is %d rows, want %d", h, got, h)
		}
		for i, l := range strings.Split(out, "\n") {
			if w := lipgloss.Width(l); w != linkPanelW {
				t.Fatalf("height=%d line %d: width %d, want %d: %q", h, i, w, linkPanelW, stripAnsi(l))
			}
		}
	}
}

// A tall terminal gives renderLinkPanel a large height. The layout grows the
// step timeline's own connector lines to absorb that leftover instead of
// piling it into a handful of blank gaps, so the exact row count has to
// match on every height, not just a couple of samples - a prior version of
// this logic silently undershot on specific heights (e.g. 43 rows short by
// 4) because it recomputed the stretch only once instead of looping to a
// fixed point.
func TestRenderLinkPanelMatchesHeightAcrossRange(t *testing.T) {
	x := m{qrReceivedAt: time.Now()}
	floor := lipgloss.Height(x.renderLinkPanel(0))
	for h := floor; h <= 150; h++ {
		out := x.renderLinkPanel(h)
		if got := lipgloss.Height(out); got != h {
			t.Fatalf("height=%d: panel is %d rows, want %d", h, got, h)
		}
	}
}

// Within a realistic terminal height, gaps between sections should stay
// capped (leftover height goes into the step timeline's own spacing
// instead) - remainder from the floor division can add one extra blank line
// to the earliest gaps, so the cap is a soft "maxGap+1" rather than an exact
// ceiling. A pathologically tall height is allowed to fall back to plain
// even gaps - see TestRenderLinkPanelMatchesHeightAcrossRange, which checks
// the row count still comes out exact even then.
func TestRenderLinkPanelCapsGapsOnTallTerminals(t *testing.T) {
	x := m{qrReceivedAt: time.Now()}
	out := stripAnsi(x.renderLinkPanel(45))
	lines := strings.Split(out, "\n")
	blank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 4 {
				t.Fatalf("found a gap of more than 4 blank lines on a tall panel (h=45): %v", lines)
			}
			continue
		}
		blank = 0
	}
}

func TestRenderLinkPanelKeepsMinimumGapsWhenShort(t *testing.T) {
	out := (m{}).renderLinkPanel(0)
	if lipgloss.Height(out) < 20 {
		t.Fatalf("short panel dropped content: %d rows", lipgloss.Height(out))
	}
}

func TestRenderLinkPanelLastStepNamesTheQR(t *testing.T) {
	out := stripAnsi((m{qrReceivedAt: time.Now()}).renderLinkPanel(31))
	if !strings.Contains(out, "Point your camera at the QR") {
		t.Fatalf("last step should tell the user to aim at the QR: %q", out)
	}
	if strings.Contains(out, "▶") {
		t.Fatalf("panel should not draw an arrow: %q", out)
	}
}

func TestRenderLinkPanelCountdown(t *testing.T) {
	fresh := stripAnsi((m{qrReceivedAt: time.Now().Add(-15 * time.Second)}).renderLinkPanel(0))
	if !strings.Contains(fresh, "new code in 45s") {
		t.Fatalf("expected 45s remaining: %q", fresh)
	}
	expired := stripAnsi((m{qrReceivedAt: time.Now().Add(-62 * time.Second)}).renderLinkPanel(0))
	if !strings.Contains(expired, "refreshing…") {
		t.Fatalf("expected refreshing state: %q", expired)
	}
}

func TestRenderLinkPanelExpiredQRShowsRestart(t *testing.T) {
	out := stripAnsi((m{qrReceivedAt: time.Now().Add(-70 * time.Second)}).renderLinkPanel(31))
	if !strings.Contains(out, "QR expired") {
		t.Fatalf("expected expired QR prompt: %q", out)
	}
	if !strings.Contains(out, "[R] Restart session") {
		t.Fatalf("expected restart hint: %q", out)
	}
	if strings.Contains(out, "refreshing…") {
		t.Fatalf("expired QR must not show refreshing state: %q", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w != linkPanelW {
			t.Fatalf("line %d: width %d, want %d: %q", i, w, linkPanelW, l)
		}
	}
}
