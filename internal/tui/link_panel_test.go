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
	expired := stripAnsi((m{qrReceivedAt: time.Now().Add(-2 * time.Minute)}).renderLinkPanel(0))
	if !strings.Contains(expired, "refreshing…") {
		t.Fatalf("expected refreshing state: %q", expired)
	}
}
