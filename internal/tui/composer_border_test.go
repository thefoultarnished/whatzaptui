package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRenderComposerTopBorderWidthInvariant(t *testing.T) {
	model := m{
		mode:   "chat",
		active: "12345@s.whatsapp.net",
	}

	for w := 1; w <= 150; w++ {
		border := model.renderComposerTopBorder(w, true, false)
		plain := stripGraphicsSeqs(border)
		gotW := runeDisplayWidth(plain)
		if gotW != w {
			t.Fatalf("at width %d, runeDisplayWidth(%q) = %d, want %d", w, plain, gotW, w)
		}
	}
}

func TestRenderComposerTopBorderResponsiveTiers(t *testing.T) {
	model := m{
		mode:   "chat",
		active: "12345@s.whatsapp.net",
	}

	// Narrow (< 24): solid rule
	narrow := stripGraphicsSeqs(model.renderComposerTopBorder(20, true, false))
	if strings.Contains(narrow, "alt") {
		t.Fatalf("narrow width should not contain shortcuts: %q", narrow)
	}
	if runeDisplayWidth(narrow) != 20 {
		t.Fatalf("narrow width = %d, want 20", runeDisplayWidth(narrow))
	}

	// Medium (~55): contains send emoji [alt+e] and attach file [alt+f]
	med := stripGraphicsSeqs(model.renderComposerTopBorder(55, true, false))
	if !strings.Contains(med, "send emoji [alt+e]") || !strings.Contains(med, "attach file [alt+f]") {
		t.Fatalf("medium width should contain 'send emoji [alt+e]' and 'attach file [alt+f]': %q", med)
	}
	if !strings.Contains(med, "·") {
		t.Fatalf("medium width should contain dot separator '·': %q", med)
	}

	// Wide (105): contains all 4 shortcuts in label [key] order
	wide := stripGraphicsSeqs(model.renderComposerTopBorder(105, true, false))
	if !strings.HasPrefix(wide, "─────── ") {
		t.Fatalf("wide width should start with '─────── ': %q", wide)
	}
	if !strings.Contains(wide, "send emoji [alt+e]") || !strings.Contains(wide, "attach file [alt+f]") ||
		!strings.Contains(wide, "reply [alt+r]") || !strings.Contains(wide, "edit message [alt+a]") {
		t.Fatalf("wide width should contain all shortcuts in label [key] order: %q", wide)
	}
}

func TestRenderComposerTopBorderModes(t *testing.T) {
	// Status mode (blacklisted)
	modelLocked := m{mode: "chat", active: "12345@s.whatsapp.net"}
	lockedBorder := stripGraphicsSeqs(modelLocked.renderComposerTopBorder(60, true, true))
	if !strings.Contains(lockedBorder, "blacklisted") || !strings.Contains(lockedBorder, "[/whitelist]") {
		t.Fatalf("locked border should contain blacklisted and [/whitelist]: %q", lockedBorder)
	}

	// Reply pick mode
	modelReply := m{mode: "chat", active: "12345@s.whatsapp.net", replyPickMode: true}
	replyBorder := stripGraphicsSeqs(modelReply.renderComposerTopBorder(60, true, false))
	if !strings.Contains(replyBorder, "quote reply") {
		t.Fatalf("reply border should contain quote reply: %q", replyBorder)
	}

	// Edit pick mode
	modelEdit := m{mode: "chat", active: "12345@s.whatsapp.net", editPickMode: true}
	editBorder := stripGraphicsSeqs(modelEdit.renderComposerTopBorder(60, true, false))
	if !strings.Contains(editBorder, "edit message") {
		t.Fatalf("edit border should contain edit message: %q", editBorder)
	}

	// Attachment mode
	modelAtt := m{mode: "chat", active: "12345@s.whatsapp.net", pendingAttachmentPath: "photo.png"}
	attBorder := stripGraphicsSeqs(modelAtt.renderComposerTopBorder(60, true, false))
	if !strings.Contains(attBorder, "attachment") {
		t.Fatalf("attachment border should contain attachment: %q", attBorder)
	}
}

func TestRenderComposerTopBorderUnfocusedOrNoActiveChat(t *testing.T) {
	// When chat is not open (active == "")
	modelNoActive := m{mode: "chat", active: ""}
	borderNoActive := stripGraphicsSeqs(modelNoActive.renderComposerTopBorder(60, true, false))
	if strings.Contains(borderNoActive, "alt") || strings.Contains(borderNoActive, "attach") {
		t.Fatalf("expected plain line without shortcuts when active is empty, got %q", borderNoActive)
	}
	if borderNoActive != strings.Repeat("─", 60) {
		t.Fatalf("expected plain line of 60 dashes, got %q", borderNoActive)
	}

	// When unfocused (rightFocused == false) even if active != ""
	modelUnfocused := m{mode: "nav", active: "12345@s.whatsapp.net"}
	borderUnfocused := stripGraphicsSeqs(modelUnfocused.renderComposerTopBorder(60, false, false))
	if strings.Contains(borderUnfocused, "alt") || strings.Contains(borderUnfocused, "attach") {
		t.Fatalf("expected plain line without shortcuts when unfocused, got %q", borderUnfocused)
	}
	if borderUnfocused != strings.Repeat("─", 60) {
		t.Fatalf("expected plain line of 60 dashes, got %q", borderUnfocused)
	}
}

func TestRenderComposerTopBorderItalics(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	model := m{
		mode:   "chat",
		active: "12345@s.whatsapp.net",
	}
	raw := model.renderComposerTopBorder(105, true, false)

	// Action label should have italic ANSI code (3m)
	if !strings.Contains(raw, "\x1b[3m") && !strings.Contains(raw, ";3m") {
		t.Fatalf("expected italic escape code for action labels in: %q", raw)
	}

	// Find the bracket and key portion "[alt+e]" and verify it does NOT contain italic
	idx := strings.Index(raw, "[alt+e]")
	if idx == -1 {
		t.Fatalf("expected '[alt+e]' in output: %q", raw)
	}
	// The escape sequence immediately preceding "[alt+e]" should be bold (\x1b[1m) without italic (3m)
	lastEsc := raw[:idx]
	escIdx := strings.LastIndex(lastEsc, "\x1b[")
	if escIdx == -1 {
		t.Fatalf("expected escape sequence before '[alt+e]' in %q", raw)
	}
	seq := lastEsc[escIdx:]
	if strings.Contains(seq, "3m") {
		t.Fatalf("key [alt+e] has italic sequence: %q", seq)
	}
	if !strings.Contains(seq, "1m") {
		t.Fatalf("key [alt+e] should be bold (1m): %q", seq)
	}
}
