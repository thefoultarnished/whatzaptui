package main

import "fmt"

var pointerList = []struct {
	icon        string
	displayName string
}{
	{"✦", "Sparkle"},
	{"▸", "Triangle"},
	{"➤", "Arrow"},
	{"◉", "Bullseye"},
	{"●", "Circle"},
	{"◆", "Diamond"},
	{"■", "Square"},
	{"★", "Star"},
	{"►", "Play"},
	{"⬥", "Small Diamond"},
	{"⏵", "Right Triangle"},
	{"⊳", "Open Triangle"},
	{"⦿", "Target"},
	{"❯", "Chevron"},
	{"⁕", "Asterisk"},
	{"│", "Connected Line"},
	{"┃", "Thick Connected Line"},
	{"║", "Double Connected Line"},
	{" ", "None"},
}

func buildPointerPickerItems() []pickerItem {
	items := make([]pickerItem, len(pointerList))
	for i, p := range pointerList {
		items[i] = pickerItem{key: p.icon, label: fmt.Sprintf("%s  %s", p.icon, p.displayName)}
	}
	return items
}
