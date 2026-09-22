package main

import (
	"testing"
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
