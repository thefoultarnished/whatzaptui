package tui

import tea "github.com/charmbracelet/bubbletea"

func flashTaskbarCmd() tea.Cmd {
	return func() tea.Msg {
		flashTaskbarWindow()
		return nil
	}
}
