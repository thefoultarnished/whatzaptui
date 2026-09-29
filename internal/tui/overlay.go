package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlayCenter draws box over base, centered in a w×h area, and leaves the
// rest of base untouched so it still shows around the box. Both are cut by
// screen cells, so colored text and wide characters (emoji) are not split
// mid-character and the styling of the cut lines carries on past the box.
func overlayCenter(base, box string, w, h int) string {
	if w <= 0 || h <= 0 || box == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < h {
		baseLines = append(baseLines, "")
	}
	boxLines := strings.Split(box, "\n")

	boxW := 0
	for _, l := range boxLines {
		boxW = max(boxW, ansi.StringWidth(l))
	}
	boxW = min(boxW, w)
	top := max(0, (h-len(boxLines))/2)
	left := max(0, (w-boxW)/2)

	const reset = "\x1b[0m"
	for i, bl := range boxLines {
		y := top + i
		if y >= len(baseLines) {
			break
		}
		line := baseLines[y]
		if pad := w - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		bl = ansi.Truncate(bl, boxW, "")
		if pad := boxW - ansi.StringWidth(bl); pad > 0 {
			bl += strings.Repeat(" ", pad)
		}
		// A wide character (emoji) cut by the box edge is dropped on one side and
		// kept whole on the other: pad with a space so every row stays w cells.
		leftPart := ansi.Truncate(line, left, "")
		if d := left - ansi.StringWidth(leftPart); d > 0 {
			leftPart += strings.Repeat(" ", d)
		}
		rightStart := left + boxW
		want := w - rightStart
		rightPart := ansi.TruncateLeft(line, rightStart, "")
		if got := ansi.StringWidth(rightPart); got > want {
			rightPart = strings.Repeat(" ", got-want) + ansi.TruncateLeft(line, rightStart+(got-want), "")
		} else if got < want {
			rightPart = strings.Repeat(" ", want-got) + rightPart
		}
		baseLines[y] = leftPart + reset + bl + reset + rightPart
	}
	return strings.Join(baseLines, "\n")
}
