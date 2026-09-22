package main

import (
	"testing"
	"strings"
	tea "github.com/charmbracelet/bubbletea"
)

func TestPointerPickerIncludesLightbulbAndMicrochip(t *testing.T) {
	items := buildPointerPickerItems()

	foundLightbulb := false
	foundMicrochip := false

	for _, item := range items {
		if item.key == "\U000F0335" {
			foundLightbulb = true
			if item.label != "\U000F0335  Lightbulb" {
				t.Errorf("Lightbulb item label = %q, want %q", item.label, "\U000F0335  Lightbulb")
			}
		}
		if item.key == "\U000F0171" {
			foundMicrochip = true
			if item.label != "\U000F0171  Microchip" {
				t.Errorf("Microchip item label = %q, want %q", item.label, "\U000F0171  Microchip")
			}
		}
	}

	if !foundLightbulb {
		t.Errorf("Lightbulb pointer icon (\\U000F0335) not found in pointer picker items")
	}
	if !foundMicrochip {
		t.Errorf("Microchip pointer icon (\\U000F0171) not found in pointer picker items")
	}
}

func TestMediaViewPickerClosesAndSwitchesToSettings(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	origCfg := currentConfig
	defer func() { currentConfig = origCfg }()
	currentConfig.MediaViewStyle = "full"
	x := m{status: "ready"}
	x.mediaViewPicker = picker{title: "Media View", items: buildMediaViewPickerItems()}
	x.mediaViewPicker.Open(currentConfig.MediaViewStyle)
	if !x.mediaViewPicker.open {
		t.Fatal("expected mediaViewPicker to be open")
	}

	// 1. Arrow down
	resModel, _ := x.key(tea.KeyMsg{Type: tea.KeyDown})
	resM := resModel.(m)
	if !resM.mediaViewPicker.open {
		t.Fatal("mediaViewPicker should remain open while navigating")
	}

	// 2. Press Enter to confirm
	resModel2, _ := resM.key(tea.KeyMsg{Type: tea.KeyEnter})
	resM2 := resModel2.(m)
	if resM2.mediaViewPicker.open {
		t.Fatal("mediaViewPicker should be closed after Enter confirm")
	}
	if !resM2.settingsPicker.open {
		t.Fatal("settingsPicker should be open after confirming sub-picker")
	}

	// 3. Test Esc cancel restores original
	currentConfig.MediaViewStyle = "text"
	y := m{status: "ready"}
	y.mediaViewPicker = picker{title: "Media View", items: buildMediaViewPickerItems()}
	y.mediaViewPicker.Open(currentConfig.MediaViewStyle)
	// Arrow to another option
	resY, _ := y.key(tea.KeyMsg{Type: tea.KeyDown})
	resYM := resY.(m)
	// Cancel with Esc
	resY2, _ := resYM.key(tea.KeyMsg{Type: tea.KeyEsc})
	resYM2 := resY2.(m)
	if resYM2.mediaViewPicker.open {
		t.Fatal("mediaViewPicker should be closed after Esc")
	}
	if currentConfig.MediaViewStyle != "text" {
		t.Fatalf("MediaViewStyle = %q, want restored original 'text'", currentConfig.MediaViewStyle)
	}
}

func TestMediaIconPickerRenderAndNavigation(t *testing.T) {
	p := picker{title: "Media Icons", items: buildMediaIconPickerItems()}
	p.Open("text")
	if !p.isSingleCol() {
		t.Fatal("Media Icons picker should be single column to prevent label wrapping")
	}

	rendered := p.Render(80, 20)
	if !strings.Contains(rendered, "Media Icons") {
		t.Fatalf("missing title in rendered picker: %q", rendered)
	}
	if !strings.Contains(rendered, "Text") || !strings.Contains(rendered, "Nerd") {
		t.Fatalf("missing Text or Nerd option in rendered picker: %q", rendered)
	}

	// Navigation: KeyDown should move from item 0 ("text") to item 1 ("nerd")
	action, done := p.Handle(tea.KeyMsg{Type: tea.KeyDown})
	if done || action != "" {
		t.Fatalf("KeyDown should just navigate, got action=%q, done=%v", action, done)
	}
	if p.SelectedKey() != "nerd" {
		t.Fatalf("SelectedKey after KeyDown = %q, want 'nerd'", p.SelectedKey())
	}

	// Navigation: KeyUp should move back to item 0 ("text")
	action, done = p.Handle(tea.KeyMsg{Type: tea.KeyUp})
	if done || action != "" {
		t.Fatalf("KeyUp should just navigate, got action=%q, done=%v", action, done)
	}
	if p.SelectedKey() != "text" {
		t.Fatalf("SelectedKey after KeyUp = %q, want 'text'", p.SelectedKey())
	}
}
