package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	// Pipeline tree connectors
	if !strings.Contains(view, "├─") || !strings.Contains(view, "└─") {
		t.Errorf("loading screen missing pipeline tree connectors (├─ / └─)")
	}
	// Stages
	for _, stage := range []string{"Backend", "Session", "Chats", "Contacts"} {
		if !strings.Contains(view, stage) {
			t.Errorf("loading screen missing stage %q", stage)
		}
	}
	// Progress indicator
	if !strings.Contains(view, "%") {
		t.Errorf("loading screen missing progress percentage")
	}
	// System badges
	if !strings.Contains(view, "8787") || !strings.Contains(view, "whatsmeow") {
		t.Errorf("loading screen missing system info badges")
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
