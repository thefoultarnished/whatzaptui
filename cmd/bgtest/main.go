package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func main() {
	lipgloss.SetColorProfile(termenv.TrueColor)
	
	bg := lipgloss.Color("#3c2200")
	bgStyle := lipgloss.NewStyle().Background(bg)
	bgSeq := bgStyle.Render("")
	fmt.Fprintf(os.Stderr, "bgSeq repr: %q\n", bgSeq)

	if idx := strings.Index(bgSeq, "m"); idx > 0 {
		extracted := bgSeq[:idx+1]
		fmt.Fprintf(os.Stderr, "extracted: %q\n", extracted)
	} else {
		fmt.Fprintln(os.Stderr, "No 'm' found - bgSeq would be empty!")
	}

	rendered := bgStyle.Render("hello")
	fmt.Fprintf(os.Stderr, "rendered 'hello': %q\n", rendered)
}
