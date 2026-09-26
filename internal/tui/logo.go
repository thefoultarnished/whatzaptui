package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderZapBolt draws the WhatZap bolt as braille pixel art with a
// yellow-to-red gradient. frame == 0 renders a static bolt; any other value
// picks a per-cell flicker state from that frame, used to animate it.
//
// This is the app's one bolt logo: the splash screen, the QR login panel,
// and the plain status screens all call this function, so a change here
// shows up everywhere it's used.
// boltPalette returns the bolt gradient: the fixed yellow→red palette for
// legacy themes, or the same number of steps along Brand → Action →
// Emphasis for V2 themes. Going through Action keeps the ramp saturated;
// a straight blend between two near-complementary colours turns grey.
func boltPalette(legacy []lipgloss.Color) []lipgloss.Color {
	if !themeV2 {
		return legacy
	}
	out := make([]lipgloss.Color, len(legacy))
	for i := range out {
		out[i] = identityRamp(float64(i) / float64(max(1, len(out)-1)))
	}
	return out
}

// identityRamp maps t∈[0,1] onto Brand → Action → Emphasis.
func identityRamp(t float64) lipgloss.Color {
	if t <= 0.5 {
		return lerpColor(brand, accent, t*2)
	}
	return lerpColor(accent, purple, t*2-1)
}

func renderZapBolt(frame int) string {
	palette := []lipgloss.Color{
		"#fef08a", // spark yellow
		"#fde047", // bright yellow
		"#facc15", // gold yellow
		"#eab308", // golden amber
		"#f59e0b", // warm amber
		"#fb923c", // electric orange
		"#f97316", // deep orange
		"#ea580c", // fiery orange-red
		"#ef4444", // bright red
		"#dc2626", // crimson red
		"#b91c1c", // deep ruby red
	}
	palette = boltPalette(palette)
	pixels := []string{
		"        #####",
		"   ##  ######",
		"   #   ##### ",
		"     ######  ",
		"     #####   ",
		"    #####    ",
		"  #######  # ",
		"   #####  #  ",
		"  #####  #   ",
		" ############",
		" ########### ",
		"###########  ",
		"      ####   ",
		"     ####    ",
		"  #  ####    ",
		"   #####     ",
		"    ###      ",
		"   ###       ",
		"   ##        ",
		"   ##        ",
		"  #          ",
		"  #          ",
	}

	f := max(0, frame)
	dotMap := [4][2]rune{
		{0x01, 0x08},
		{0x02, 0x10},
		{0x04, 0x20},
		{0x40, 0x80},
	}

	numBrailleRows := (len(pixels) + 3) / 4
	const numBrailleCols = 7
	var lines []string

	for br := range numBrailleRows {
		rBase := br * 4
		var sb strings.Builder
		prevIdx := -1

		for bc := range numBrailleCols {
			cBase := bc * 2
			var mask rune
			var activeSum, activeCount int

			for rOff := range 4 {
				r := rBase + rOff
				if r >= len(pixels) {
					continue
				}
				row := pixels[r]
				for cOff := range 2 {
					c := cBase + cOff
					if c < len(row) && row[c] == '#' {
						mask |= dotMap[rOff][cOff]
						activeSum += r
						activeCount++
					}
				}
			}

			if mask == 0 {
				sb.WriteRune(' ')
			} else {
				div := activeCount * (len(pixels) - 1)
				baseIdx := min(len(palette)-1, (activeSum*(len(palette)-1)+div/2)/div)
				idx := baseIdx
				if f > 0 {
					tick := uint32((f-1)/3 + 1)
					h := uint32(br+1)*31337 ^ uint32(bc+1)*1103515245 ^ (tick * 1337)
					h = (h ^ (h >> 13)) * 1274126177
					h = h ^ (h >> 16)

					bands := []int{0, 1, 2, 4, 5, 6, 8, 9, 10}
					bIdx := int(h % uint32(len(bands)))
					idx = bands[bIdx]
					if prevIdx >= 0 && idx == prevIdx {
						bIdx = (bIdx + 1) % len(bands)
						idx = bands[bIdx]
					}
					prevIdx = idx
				}
				st := lipgloss.NewStyle().Foreground(palette[idx])
				sb.WriteString(st.Render(string(0x2800 + mask)))
			}
		}
		lines = append(lines, sb.String())
	}
	return strings.Join(lines, "\n")
}

// renderPixelWordmark draws the "WhatZap" wordmark in a blocky pixel font,
// using the active theme's text color for "What" and a brand-to-accent gradient
// across the letters of "Zap".
//
// This is the app's one wordmark: every screen that shows the WhatZap name
// as a logo (not as plain text) calls this function, so a change here shows
// up everywhere it's used.
func renderPixelWordmark() string {
	wGrid := []string{
		"▄ ▄ ▄",
		"█ █ █",
		"█▄█▄█",
		"     ",
	}
	hGrid := []string{
		"█▄▄▄",
		"█  █",
		"█  █",
		"    ",
	}
	a1Grid := []string{
		"▄▄▄▄",
		"▄▄▄█",
		"█▄▄█",
		"    ",
	}
	tGrid := []string{
		"▄█▄",
		" █ ",
		" █▄",
		"   ",
	}
	zGrid := []string{
		"▄▄▄▄",
		" ▄▄█",
		"█▄▄▄",
		"    ",
	}
	a2Grid := []string{
		"▄▄▄▄",
		"▄▄▄█",
		"█▄▄█",
		"    ",
	}
	pGrid := []string{
		"▄▄▄▄",
		"█  █",
		"█▄▄█",
		"▀   ",
	}

	whatSt := lipgloss.NewStyle().Foreground(text)
	zSt := lipgloss.NewStyle().Foreground(lerpColor(brand, accent, 0.0))
	aSt := lipgloss.NewStyle().Foreground(lerpColor(brand, accent, 0.5))
	pSt := lipgloss.NewStyle().Foreground(lerpColor(brand, accent, 1.0))
	if themeV2 {
		// "Zap" runs Brand → Action → Emphasis, ending on the identity pair.
		zSt = lipgloss.NewStyle().Foreground(identityRamp(0))
		aSt = lipgloss.NewStyle().Foreground(identityRamp(0.5))
		pSt = lipgloss.NewStyle().Foreground(identityRamp(1))
	}
	var rows []string
	for r := range 4 {
		segs := []string{
			whatSt.Render(wGrid[r]),
			whatSt.Render(hGrid[r]),
			whatSt.Render(a1Grid[r]),
			whatSt.Render(tGrid[r]),
			zSt.Render(zGrid[r]),
			aSt.Render(a2Grid[r]),
			pSt.Render(pGrid[r]),
		}
		rows = append(rows, strings.Join(segs, " "))
	}
	return strings.Join(rows, "\n")
}
