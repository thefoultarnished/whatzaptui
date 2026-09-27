package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)
func TestTypingAnimationListContainsSquares(t *testing.T) {
	idx := typingAnimationIndex("squares")
	if idx < 0 || idx >= len(typingAnimationList) {
		t.Fatalf("squares style not found in typingAnimationList")
	}
	def := typingAnimationList[idx]
	if def.key != "squares" {
		t.Fatalf("key = %q, want squares", def.key)
	}
	if def.displayName != "Squares" {
		t.Fatalf("displayName = %q, want Squares", def.displayName)
	}
	icons := getTypingIcons("squares")
	if len(icons) != 6 {
		t.Fatalf("len(icons) = %d, want 6 frames", len(icons))
	}
	for i, frame := range icons {
		if w := runeDisplayWidth(frame); w != 8 {
			t.Fatalf("frame %d width = %d, want 8", i, w)
		}
	}

	// Verify the picker item fits within the 20-rune column limit
	items := buildTypingAnimationPickerItems()
	var squaresItem *pickerItem
	for i := range items {
		if items[i].key == "squares" {
			squaresItem = &items[i]
			break
		}
	}
	if squaresItem == nil {
		t.Fatal("squares item missing from buildTypingAnimationPickerItems()")
	}
	if w := runeDisplayWidth(squaresItem.label); w > 20 {
		t.Fatalf("squares label display width = %d, want <= 20 to preserve 2-column layout", w)
	}
}

func TestRenderSquaresIconFrames(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	for f := range 6 {
		rendered := renderSquaresIcon(f, "")
		if rendered == "" {
			t.Fatalf("renderSquaresIcon(%d) returned empty string", f)
		}
		// Strip ANSI codes and verify track width is exactly 8 characters
		plain := ansiStripRe.ReplaceAllString(rendered, "")
		if w := runeDisplayWidth(plain); w != 8 {
			t.Fatalf("frame %d display width = %d, want 8 (plain=%q)", f, w, plain)
		}
	}

	// Frame 0 should match progress.png: 2 dots on the left and 6 squares on the right: "··◼◼◼◼◼◼"
	frame0 := renderSquaresIcon(0, "")
	frame0Plain := ansiStripRe.ReplaceAllString(frame0, "")
	if frame0Plain != "··◼◼◼◼◼◼" {
		t.Fatalf("frame 0 plain = %q, want '··◼◼◼◼◼◼'", frame0Plain)
	}
	// The leading face moving left (slot 2) should contain the brightest electric blue (#58A6FF)
	if !strings.Contains(frame0, "88;166;255") { // #58A6FF in ANSI RGB (88, 166, 255)
		t.Fatalf("frame 0 missing brightest electric blue #58A6FF:\n%q", frame0)
	}
	// The trailing back (slot 7) should contain the darkest shadow navy (#101E3C)
	if !strings.Contains(frame0, "16;30;60") { // #101E3C in ANSI RGB (16, 30, 60)
		t.Fatalf("frame 0 missing darkest shadow navy #101E3C:\n%q", frame0)
	}

	// Frame 3 should have 6 squares on the left and 2 dots on the right: "◼◼◼◼◼◼··"
	frame3Plain := ansiStripRe.ReplaceAllString(renderSquaresIcon(3, ""), "")
	if frame3Plain != "◼◼◼◼◼◼··" {
		t.Fatalf("frame 3 plain = %q, want '◼◼◼◼◼◼··'", frame3Plain)
	}
}

func TestTypingAnimationPickerRenderWithSquares(t *testing.T) {
	p := picker{title: "Typing Style", items: buildTypingAnimationPickerItems()}
	p.Open("squares")
	if !p.open {
		t.Fatal("expected picker to be open")
	}

	// Active frame
	viewActive := p.RenderTypingAnimation(80, 24, 3)
	if !strings.Contains(viewActive, "Squares") {
		t.Fatalf("view should contain 'Squares':\n%s", viewActive)
	}

	// Inactive frame (when other item is selected)
	p.idx = 0 // dots selected, squares inactive
	viewInactive := p.RenderTypingAnimation(80, 24, 0)
	if !strings.Contains(viewInactive, "Squares") {
		t.Fatalf("inactive view should contain 'Squares':\n%s", viewInactive)
	}
}

func TestAssembleChatLinesTypingSquares(t *testing.T) {
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()
	currentConfig.TypingAnimationStyle = "squares"

	model := m{
		w:           80,
		h:           24,
		active:      "user@s.whatsapp.net",
		chats:       []chat{{ID: "user@s.whatsapp.net", Name: "Alice"}},
		typingChats: map[string]time.Time{"user@s.whatsapp.net": time.Now()},
	}
	out := model.assembleChatLines(80, 20, [][]string{}, []int64{}, []int{})
	plain := ansiStripRe.ReplaceAllString(out, "")
	if !strings.Contains(plain, "Alice is typing...") {
		t.Fatalf("assembleChatLines missing 'Alice is typing...':\n%s", plain)
	}
	if !strings.Contains(plain, "◼") {
		t.Fatalf("assembleChatLines missing squares glyph '◼':\n%s", plain)
	}
}
