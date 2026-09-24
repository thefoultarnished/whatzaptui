package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestLoadingStages(t *testing.T) {
	fresh := m{status: "Starting backend..."}
	for _, s := range fresh.loadingStages() {
		if s.state != "pending" {
			t.Fatalf("fresh boot stage %q = %q, want pending", s.label, s.state)
		}
	}

	connecting := m{status: "Connecting..."}
	stages := connecting.loadingStages()
	if stages[0].state != "done" {
		t.Fatalf("backend stage = %q, want done", stages[0].state)
	}
	if stages[1].state != "active" {
		t.Fatalf("session stage = %q, want active", stages[1].state)
	}

	loaded := m{
		status:   "Connecting...",
		chats:    []chat{{ID: "a@s.whatsapp.net"}, {ID: "b@s.whatsapp.net"}},
		contacts: map[string]contact{"c@s.whatsapp.net": {ID: "c@s.whatsapp.net"}},
	}
	stages = loaded.loadingStages()
	if stages[2].state != "done" {
		t.Fatalf("chats stage = %+v, want done", stages[2])
	}
	if stages[3].state != "done" {
		t.Fatalf("contacts stage = %+v, want done", stages[3])
	}

	ready := m{status: "ready"}
	for _, s := range ready.loadingStages() {
		if s.label == "Handshake" && s.state != "done" {
			t.Fatalf("session stage at ready = %q, want done", s.state)
		}
	}
}

func TestLoadingScreenContainsBoltLogo(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := model.View()
	if strings.Contains(view, "WhatZap") {
		t.Errorf("loading screen should not show extra WhatZap text under the logo")
	}
	if !strings.ContainsFunc(view, func(r rune) bool { return r >= 0x2801 && r <= 0x28ff }) {
		t.Errorf("loading screen missing braille bolt characters")
	}
}

func TestSignedInSplashVisualElements(t *testing.T) {
	model := m{
		w:      120,
		h:      30,
		status: "Connecting...",
	}
	view := model.View()
	plainView := ansiStripRe.ReplaceAllString(view, "")
	// Stage section header remains; page edge bars are removed.
	if !strings.Contains(plainView, "CONNECTING TO WHATSAPP") || !strings.Contains(plainView, "v0.1.0") {
		t.Errorf("loading screen missing stage header title or version tag")
	}
	// Vertical branch stages keep only the live/done detail text, not stage-name labels.
	for _, item := range []string{"initiating...", "loading..."} {
		if !strings.Contains(view, item) {
			t.Errorf("loading screen missing pipeline stage %q", item)
		}
	}
	for _, removed := range []string{" Backend ", " Handshake ", " Chats ", " Contacts "} {
		if strings.Contains(plainView, removed) {
			t.Errorf("loading screen should not show stage-name label %q", removed)
		}
	}
	if !strings.Contains(plainView, "├─") || !strings.Contains(plainView, "└─") {
		t.Errorf("loading screen missing vertical branch stages")
	}
	// System badges were removed from the clean splash.
	for _, removed := range []string{"8787", "whatsmeow", "device:"} {
		if strings.Contains(plainView, removed) {
			t.Errorf("loading screen should not show system badge %q", removed)
		}
	}
	// Command action bar
	if !strings.Contains(view, "[ENTER]") || !strings.Contains(view, "[Q]") || !strings.Contains(view, "[R]") {
		t.Errorf("loading screen missing command action bar")
	}
	// Bottom hint
	if !strings.Contains(view, "Press any key to continue") {
		t.Errorf("loading screen missing continue hint")
	}
	// Page edge bars should be gone.
	for _, removed := range []string{"A private WhatsApp client for your terminal", "Terminal. Private. Yours.", "https://github.com/whatzap"} {
		if strings.Contains(plainView, removed) {
			t.Errorf("loading screen should not show page edge bar text %q", removed)
		}
	}
}
func TestSignedInSplashPageEdgeBarsAreRemoved(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := ansiStripRe.ReplaceAllString(model.View(), "")
	for _, removed := range []string{"A private WhatsApp client for your terminal", "Terminal. Private. Yours.", "https://github.com/whatzap"} {
		if strings.Contains(view, removed) {
			t.Fatalf("page edge bar text should be removed: %q", removed)
		}
	}
}

func TestNoPageEdgeFooterOnSignedInSplash(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := ansiStripRe.ReplaceAllString(model.View(), "")
	if strings.Contains(view, "github.com/whatzap") {
		t.Fatalf("signed-in splash should not render page edge footer:\n%s", view)
	}
}

func TestSignedInSplashEdgeToEdgeDimensions(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 24 {
		t.Fatalf("view only has %d lines; expected at least 24 to fill h=24", len(lines))
	}
	for i := range lines {
		if i >= 24 {
			break
		}
		plain := ansiStripRe.ReplaceAllString(lines[i], "")
		if len([]rune(plain)) != 80 {
			t.Errorf("line %d plain width = %d, want 80", i, len([]rune(plain)))
		}
	}
}

func TestSignedInSplashActiveStageSpinnerAnimation(t *testing.T) {
	model0 := m{
		w:            80,
		h:            24,
		status:       "Connecting...",
		spinnerFrame: 0,
	}
	view0 := model0.View()
	expected0 := nodeFrames[0] + " initiating..."
	if !strings.Contains(view0, expected0) {
		t.Errorf("expected view to contain active stage spinner %q", expected0)
	}

	model1 := m{
		w:            80,
		h:            24,
		status:       "Connecting...",
		spinnerFrame: 1,
	}
	view1 := model1.View()
	expected1 := nodeFrames[1] + " initiating..."
	if !strings.Contains(view1, expected1) {
		t.Errorf("expected view to contain active stage spinner %q", expected1)
	}
}

func TestSignedInSplashKeyQuit(t *testing.T) {
	model := m{status: "Connecting..."}
	_, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatalf("expected quit command on 'q' during splash")
	}
}

func TestSignedInSplashKeyReconnect(t *testing.T) {
	model := m{status: "Connecting...", sessionReady: true}
	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	got := next.(m)
	if got.sessionReady {
		t.Fatalf("expected sessionReady = false after 'r' reconnect")
	}
	if cmd == nil {
		t.Fatalf("expected reconnect command on 'r' during splash")
	}
}

// The bolt logo is a static zap bolt (yellow -> orange -> red, 6 rows) at
// frame 0. It does NOT animate at frame 0; every call must produce the same
// output.
func TestRenderZapBolt(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	const wantRows, wantWidth = 6, 7
	got := renderZapBolt(0)
	lines := strings.Split(got, "\n")
	if len(lines) != wantRows {
		t.Fatalf("rows = %d, want %d", len(lines), wantRows)
	}
	for i, line := range lines {
		if n := lipgloss.Width(line); n != wantWidth {
			t.Errorf("row %d width = %d, want %d (line: %q)", i, n, wantWidth, line)
		}
	}
	if !strings.ContainsFunc(got, func(r rune) bool { return r >= 0x2801 && r <= 0x28ff }) {
		t.Errorf("bolt missing braille characters")
	}
	// Static: repeated calls must match byte-for-byte.
	if got != renderZapBolt(0) {
		t.Errorf("bolt output must be stable across calls")
	}
	// Gradient: every palette hex must appear.
	for _, hex := range []string{
		"#fef08a", "#fde047", "#facc15", "#eab308", "#f59e0b",
		"#fb923c", "#f97316", "#ea580c", "#ef4444", "#dc2626", "#b91c1c",
	} {
		prefix := strings.TrimSuffix(lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("#"), "#\x1b[0m")
		code := strings.TrimSuffix(prefix, "m")
		if !strings.Contains(got, code) {
			t.Errorf("bolt missing gradient colour %s", hex)
		}
	}
	// No leftover W-bubble artefacts.
	if strings.Contains(got, "▒") || strings.Contains(got, "▀█") {
		t.Errorf("bolt still contains W-bubble dither/tail glyphs")
	}
}

func TestRenderZapBoltAnimation(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	f0 := renderZapBolt(0)
	f1 := renderZapBolt(1)
	if f0 == f1 {
		t.Fatalf("expected frames 0 and 1 to differ for animated crackle")
	}
	f2 := renderZapBolt(2)
	if f1 != f2 {
		t.Fatalf("expected frames 1 and 2 to hold within ~300ms crackle interval")
	}
	f4 := renderZapBolt(4)
	if f1 == f4 {
		t.Fatalf("expected frame 4 to advance to next crackle state")
	}
	for f := range 11 {
		lines := strings.Split(renderZapBolt(f), "\n")
		if len(lines) != 6 {
			t.Fatalf("frame %d rows = %d, want 6", f, len(lines))
		}
		for i, line := range lines {
			if n := lipgloss.Width(line); n != 7 {
				t.Errorf("frame %d row %d width = %d, want 7", f, i, n)
			}
		}
	}
}

func TestLoadingStagesHoldOneSecondEach(t *testing.T) {
	loaded := func(bootAt time.Time) m {
		return m{
			status:       "Connecting...",
			sessionReady: true,
			chats:        []chat{{ID: "a@s.whatsapp.net"}},
			contacts:     map[string]contact{"c@s.whatsapp.net": {ID: "c@s.whatsapp.net"}},
			bootAt:       bootAt,
		}
	}
	// Just booted: nothing may show done yet.
	for _, s := range loaded(time.Now()).loadingStages() {
		if s.state == "done" {
			t.Fatalf("fresh boot stage %q = done, want held back", s.label)
		}
	}
	// 2.5s in: first two stages done, third held at active, fourth pending.
	stages := loaded(time.Now().Add(-2500 * time.Millisecond)).loadingStages()
	want := []string{"done", "done", "active", "pending"}
	for i, s := range stages {
		if s.state != want[i] {
			t.Fatalf("stage %q = %q, want %q", s.label, s.state, want[i])
		}
	}
	// Long up: everything done.
	for _, s := range loaded(time.Now().Add(-10 * time.Second)).loadingStages() {
		if s.state != "done" {
			t.Fatalf("old boot stage %q = %q, want done", s.label, s.state)
		}
	}
}

func TestSplashStageTexts(t *testing.T) {
	fresh := m{w: 120, h: 30, status: "Connecting...", bootAt: time.Now()}
	if out := fresh.View(); !strings.Contains(out, "starting...") {
		t.Errorf("fresh splash missing backend working text")
	}
	done := m{
		w:            120,
		h:            30,
		status:       "Connecting...",
		sessionReady: true,
		chats:        []chat{{ID: "a@s.whatsapp.net"}, {ID: "b@s.whatsapp.net"}},
		contacts:     map[string]contact{"c@s.whatsapp.net": {ID: "c@s.whatsapp.net"}},
		bootAt:       time.Now().Add(-10 * time.Second),
	}
	out := done.View()
	for _, want := range []string{"backend", "handshake", "chats", "contacts", "running", "done", "2 loaded", "1 synced"} {
		if !strings.Contains(out, want) {
			t.Errorf("finished splash missing %q", want)
		}
	}
}

func TestSplashStageSectionIsCompactAndCentered(t *testing.T) {
	model := m{w: 120, h: 30, status: "Connecting..."}
	out := ansiStripRe.ReplaceAllString(model.View(), "")
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "├─") && strings.Contains(l, "backend") {
			trimmed := strings.TrimSpace(l)
			if got := len([]rune(trimmed)); got > 60 {
				t.Fatalf("stage row width = %d, want compact <= 60: %q", got, trimmed)
			}
			leftPad := len([]rune(l)) - len([]rune(strings.TrimLeft(l, " ")))
			rightPad := len([]rune(l)) - len([]rune(strings.TrimRight(l, " ")))
			if diff := leftPad - rightPad; diff < -20 || diff > 20 {
				t.Fatalf("stage row should stay near center, leftPad=%d rightPad=%d line=%q", leftPad, rightPad, l)
			}
			return
		}
	}
	t.Fatalf("missing compact centered stage row in view:\n%s", out)
}

func TestSplashVerticalBranchStages(t *testing.T) {
	model := m{w: 120, h: 30, status: "Connecting..."}
	out := ansiStripRe.ReplaceAllString(model.View(), "")
	lines := strings.Split(out, "\n")
	var branchLines []string
	for _, l := range lines {
		if strings.Contains(l, "├─") || strings.Contains(l, "└─") {
			branchLines = append(branchLines, l)
		}
	}
	if len(branchLines) != 4 {
		t.Fatalf("expected 4 vertical branch stage rows, got %d:\n%s", len(branchLines), out)
	}
	if !strings.Contains(branchLines[0], "backend") {
		t.Fatalf("first branch should show backend status, got %q", branchLines[0])
	}
	if !strings.Contains(branchLines[1], "handshake") || !strings.Contains(branchLines[1], "initiating...") {
		t.Fatalf("second branch should show active handshake status, got %q", branchLines[1])
	}
	if !strings.Contains(branchLines[3], "└─") {
		t.Fatalf("last branch should use terminator, got %q", branchLines[3])
	}
}

func TestRenderBootStagesPipelineTree(t *testing.T) {
	model := m{status: "Connecting..."}
	tree := model.renderBootStages()
	lines := strings.Split(tree, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 pipeline stages, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "├─") {
		t.Errorf("first line should start with ├─, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[3], "└─") {
		t.Errorf("last line should start with └─, got %q", lines[3])
	}
	plain := ansiStripRe.ReplaceAllString(tree, "")
	for _, banned := range []string{"backend", "handshake", "chats", "contacts", "ready", "waiting", "syncing", "connecting", "starting"} {
		if strings.Contains(strings.ToLower(plain), banned) {
			t.Errorf("boot stages should not show stage/detail text %q, got %q", banned, plain)
		}
	}
}

func TestSplashHoldHasNoWait(t *testing.T) {
	if splashHoldDuration != 0 {
		t.Fatalf("splashHoldDuration = %v, want 0 (no loading-screen wait)", splashHoldDuration)
	}
}

func TestSplashHoldTransitionsOnTimer(t *testing.T) {
	model := m{
		status:       "Connecting...",
		sessionReady: true,
	}
	next, _ := model.Update(splashDoneMsg{})
	got := next.(m)
	if got.status != "ready" {
		t.Fatalf("expected status 'ready' after splashDoneMsg, got %q", got.status)
	}
}

func TestSplashHoldSkipsOnKeypress(t *testing.T) {
	model := m{
		status:       "Connecting...",
		sessionReady: true,
	}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(m)
	if got.status != "ready" {
		t.Fatalf("expected status 'ready' after keypress, got %q", got.status)
	}
}

// The pixel wordmark is Cyber neon: indigo "What", cyan "Zap", with the
// accent bridging the two. Structure must stay identical to the old
// single-colour-per-word render.
func TestRenderPixelWordmarkThemeDriven(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	prefix := func(hex lipgloss.Color) string {
		return strings.TrimSuffix(lipgloss.NewStyle().Foreground(hex).Render("#"), "#\x1b[0m")
	}

	testThemes := []struct {
		name  string
		theme Theme
	}{
		{"TokyoNight", TokyoNight},
		{"Monokai", Monokai},
		{"Nord", Nord},
		{"Ember", Ember},
	}

	for _, tt := range testThemes {
		t.Run(tt.name, func(t *testing.T) {
			setTestTheme(t, tt.theme)

			mark := renderPixelWordmark()
			plain := ansiStripRe.ReplaceAllString(mark, "")
			got := strings.Split(plain, "\n")
			want := []string{
				"▄ ▄ ▄ █▄▄▄ ▄▄▄▄ ▄█▄ ▄▄▄▄ ▄▄▄▄ ▄▄▄▄",
				"█ █ █ █  █ ▄▄▄█  █   ▄▄█ ▄▄▄█ █  █",
				"█▄█▄█ █  █ █▄▄█  █▄ █▄▄▄ █▄▄█ █▄▄█",
				"                              ▀",
			}
			if len(got) != len(want) {
				t.Fatalf("wordmark rows = %d, want %d", len(got), len(want))
			}
			for i := range want {
				if strings.TrimRight(got[i], " ") != want[i] {
					t.Errorf("row %d = %q, want %q", i, strings.TrimRight(got[i], " "), want[i])
				}
				if n := len([]rune(got[i])); n != len([]rune(want[0])) {
					t.Errorf("row %d width = %d runes, want %d (columns must stay aligned)", i, n, len([]rune(want[0])))
				}
			}

			// "What" must carry the theme's Text color
			textColor := lipgloss.Color(tt.theme.Text)
			if !strings.Contains(mark, prefix(textColor)) {
				t.Errorf("%s: wordmark missing theme Text colour %s", tt.name, tt.theme.Text)
			}

			// "Zap" starts at Brand and ends at Accent
			brandColor := lipgloss.Color(tt.theme.Brand)
			accentColor := lipgloss.Color(tt.theme.Accent)
			if !strings.Contains(mark, prefix(brandColor)) {
				t.Errorf("%s: wordmark missing theme Brand colour %s", tt.name, tt.theme.Brand)
			}
			if !strings.Contains(mark, prefix(accentColor)) {
				t.Errorf("%s: wordmark missing theme Accent colour %s", tt.name, tt.theme.Accent)
			}
		})
	}

	// Must no longer contain the old hardcoded Cyber neon colours
	setTestTheme(t, Ember)
	emberMark := renderPixelWordmark()
	for _, oldHex := range []string{"#a5b4fc", "#818cf8", "#6366f1", "#00f5d4"} {
		if strings.Contains(emberMark, prefix(lipgloss.Color(oldHex))) {
			t.Errorf("wordmark still contains hardcoded color %s in Ember theme", oldHex)
		}
	}
}
