package main

import (
	"os"

	"whatzap/internal/backend"
	"whatzap/internal/tui"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "backend" {
		backend.Run()
		return
	}
	tui.Run()
}
