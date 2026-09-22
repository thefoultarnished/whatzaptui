package main

import (
	"strings"
	"testing"

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
	if stages[2].state != "done" || stages[2].detail != "2 chats" {
		t.Fatalf("chats stage = %+v, want done/2 chats", stages[2])
	}
	if stages[3].state != "done" || stages[3].detail != "1 contact" {
		t.Fatalf("contacts stage = %+v, want done/1 contact", stages[3])
	}

	ready := m{status: "ready"}
	for _, s := range ready.loadingStages() {
		if s.label == "Session" && s.state != "done" {
			t.Fatalf("session stage at ready = %q, want done", s.state)
		}
	}
}

func TestRenderPiLogo(t *testing.T) {
	logo := renderPiLogo()
	lines := strings.Split(logo, "\n")
	if len(lines) != 5 {
		t.Fatalf("renderPiLogo lines count = %d, want 5", len(lines))
	}
	for i, line := range lines {
		plain := ansiStripRe.ReplaceAllString(line, "")
		if len([]rune(plain)) != 12 {
			t.Errorf("line %d plain rune length = %d, want 12 (line: %q)", i, len([]rune(plain)), plain)
		}
	}
	if !strings.Contains(logo, "█") || !strings.Contains(logo, "▒") {
		t.Errorf("renderPiLogo missing block or dither characters")
	}
}

func TestLoadingScreenContainsPiLogo(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := model.View()
	if !strings.Contains(view, "WhatZap") {
		t.Errorf("loading screen missing title 'WhatZap'")
	}
	if !strings.Contains(view, "█") {
		t.Errorf("loading screen missing Pi logo full block '█'")
	}
	if !strings.Contains(view, "▒") {
		t.Errorf("loading screen missing Pi logo dither block '▒'")
	}
}

func TestSignedInSplashVisualElements(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := model.View()
	// Window card header
	if !strings.Contains(view, "CONNECTING TO WHATSAPP") || !strings.Contains(view, "v0.1.0") {
		t.Errorf("loading screen missing header title or version tag")
	}
	// Checklist items
	for _, item := range []string{"Backend", "Session", "Chats", "Contacts", "Encryption"} {
		if !strings.Contains(view, item) {
			t.Errorf("loading screen missing checklist item %q", item)
		}
	}
	// System badges
	if !strings.Contains(view, "8787") || !strings.Contains(view, "whatsmeow") || !strings.Contains(view, "device:") {
		t.Errorf("loading screen missing system info badges")
	}
	// Command action bar
	if !strings.Contains(view, "[ENTER]") || !strings.Contains(view, "[Q]") || !strings.Contains(view, "[R]") {
		t.Errorf("loading screen missing command action bar")
	}
	// Bottom hint
	if !strings.Contains(view, "Press any key to continue") {
		t.Errorf("loading screen missing continue hint")
	}
}

func TestSignedInSplashHeaderBorderAlignment(t *testing.T) {
	model := m{
		w:      80,
		h:      24,
		status: "Connecting...",
	}
	view := model.View()
	lines := strings.Split(view, "\n")
	var topBorderLine, botBorderLine, dividerLine string
	for _, l := range lines {
		plain := ansiStripRe.ReplaceAllString(l, "")
		if strings.Contains(plain, "CONNECTING TO WHATSAPP") && strings.Contains(plain, "╭") {
			topBorderLine = plain
		}
		if strings.Contains(plain, "╰") && strings.Contains(plain, "───") {
			botBorderLine = plain
		}
		if strings.Contains(plain, "├") && strings.Contains(plain, "───") {
			dividerLine = plain
		}
	}
	if topBorderLine == "" || botBorderLine == "" || dividerLine == "" {
		t.Fatalf("could not find card borders in view:\n%s", view)
	}
	topW := len([]rune(topBorderLine))
	botW := len([]rune(botBorderLine))
	divW := len([]rune(dividerLine))
	if topW != botW || topW != divW {
		t.Fatalf("header border width mismatch: top=%d, bottom=%d, divider=%d", topW, botW, divW)
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
func TestChatBubbleLogoColorAnimation(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	f0 := renderChatBubbleLogo(false, 0)
	f3 := renderChatBubbleLogo(false, 3)
	if f0 == f3 {
		t.Fatalf("expected logo colors to shift across frames")
	}
	// Plain text / structure must stay completely stationary
	plain0 := ansiStripRe.ReplaceAllString(f0, "")
	plain3 := ansiStripRe.ReplaceAllString(f3, "")
	if plain0 != plain3 {
		t.Fatalf("plain text structure must not move when colors animate:\nf0:\n%s\nf3:\n%s", plain0, plain3)
	}
	// Must contain block characters
	if !strings.Contains(f0, "█") || !strings.Contains(f0, "▒") {
		t.Errorf("logo missing block characters")
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
