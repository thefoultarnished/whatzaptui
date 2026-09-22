package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type pickerItem struct {
	key   string
	label string
}

type picker struct {
	open      bool
	singleCol bool
	idx       int
	original  string
	title     string
	items     []pickerItem
}

func (p *picker) isSingleCol() bool {
	if p.singleCol || len(p.items) <= 4 {
		return true
	}
	for _, item := range p.items {
		if runeDisplayWidth(item.label) > 20 {
			return true
		}
	}
	return false
}
func (p *picker) Open(currentKey string) {
	p.open = true
	p.original = currentKey
	p.idx = 0
	for i, item := range p.items {
		if item.key == currentKey {
			p.idx = i
			break
		}
	}
}

// Close returns the selected key if confirmed, or the original key if cancelled.
func (p *picker) Close(confirm bool) string {
	p.open = false
	if confirm {
		return p.items[p.idx].key
	}
	return p.original
}

func (p *picker) SelectedKey() string {
	return p.items[p.idx].key
}

func (p *picker) rows() int {
	return (len(p.items) + 1) / 2
}

func (p *picker) colRow() (col, row int) {
	rows := p.rows()
	if p.idx < rows {
		return 0, p.idx
	}
	return 1, p.idx - rows
}

func (p *picker) fromColRow(col, row int) int {
	if col == 0 {
		return row
	}
	return p.rows() + row
}

func (p *picker) rightColLen() int {
	return len(p.items) - p.rows()
}

// Handle processes a key event. Returns ("confirm", true), ("cancel", true),
// or ("", false) if the picker just moved.
func (p *picker) Handle(k tea.KeyMsg) (action string, done bool) {
	if p.isSingleCol() {
		switch k.Type {
		case tea.KeyUp, tea.KeyLeft:
			p.idx--
			if p.idx < 0 {
				p.idx = len(p.items) - 1
			}
		case tea.KeyDown, tea.KeyRight:
			p.idx++
			if p.idx >= len(p.items) {
				p.idx = 0
			}
		case tea.KeyEnter:
			return "confirm", true
		case tea.KeyEsc:
			return "cancel", true
		}
		return "", false
	}

	col, row := p.colRow()
	rows := p.rows()
	rightLen := p.rightColLen()

	switch k.Type {
	case tea.KeyUp:
		row--
		if row < 0 {
			if col == 0 {
				row = rows - 1
			} else {
				row = rightLen - 1
			}
		}
		p.idx = p.fromColRow(col, row)
	case tea.KeyDown:
		row++
		maxRow := rows
		if col == 1 {
			maxRow = rightLen
		}
		if row >= maxRow {
			row = 0
		}
		p.idx = p.fromColRow(col, row)
	case tea.KeyLeft, tea.KeyRight:
		if col == 0 {
			if row >= rightLen {
				row = rightLen - 1
			}
			p.idx = p.fromColRow(1, row)
		} else {
			p.idx = p.fromColRow(0, row)
		}
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}
	return "", false
}

func (p *picker) Render(w, h int) string {
	titleStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(muted)
	activeStyle := lipgloss.NewStyle().Foreground(brand).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(text)
	divStyle := lipgloss.NewStyle().Foreground(muted)
	keyStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)

	if p.isSingleCol() {
		maxLabelW := 0
		for _, item := range p.items {
			lw := runeDisplayWidth(item.label)
			if lw > maxLabelW {
				maxLabelW = lw
			}
		}
		pickerW := min(max(54, maxLabelW+8), max(40, w-4))

		hint := hintStyle.Render("  ") +
			keyStyle.Render("↑↓") + hintStyle.Render(" navigate  ") +
			keyStyle.Render("Enter") + hintStyle.Render(" confirm  ") +
			keyStyle.Render("Esc") + hintStyle.Render(" cancel")

		lines := []string{}
		lines = append(lines, titleStyle.Render("  "+p.title))
		lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))

		for i, item := range p.items {
			var cell string
			if i == p.idx {
				cell = activeStyle.Render(fmt.Sprintf("▶ %s", item.label))
			} else {
				cell = inactiveStyle.Render(fmt.Sprintf("  %s", item.label))
			}
			lines = append(lines, "  "+cell)
		}

		lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))
		lines = append(lines, hint)

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Width(pickerW).
			Render(strings.Join(lines, "\n"))

		return lipgloss.NewStyle().
			Width(w).
			Height(max(1, h)).
			Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
	}

	pickerW := min(54, max(44, w/2))
	colW := (pickerW - 6) / 2

	hint := hintStyle.Render("  ") +
		keyStyle.Render("↑↓←→") + hintStyle.Render(" navigate  ") +
		keyStyle.Render("Enter") + hintStyle.Render(" confirm  ") +
		keyStyle.Render("Esc") + hintStyle.Render(" cancel")

	rows := p.rows()
	rightLen := p.rightColLen()

	lines := []string{}
	lines = append(lines, titleStyle.Render("  "+p.title))
	lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))

	for r := range rows {
		leftIdx := r
		var leftCell, rightCell string

		item := p.items[leftIdx]
		if leftIdx == p.idx {
			leftCell = activeStyle.Render(fmt.Sprintf("▶ %s", item.label))
		} else {
			leftCell = inactiveStyle.Render(fmt.Sprintf("  %s", item.label))
		}
		leftCell = lipgloss.NewStyle().Width(colW).Render(leftCell)

		if r < rightLen {
			rightIdx := p.fromColRow(1, r)
			item2 := p.items[rightIdx]
			if rightIdx == p.idx {
				rightCell = activeStyle.Render(fmt.Sprintf("▶ %s", item2.label))
			} else {
				rightCell = inactiveStyle.Render(fmt.Sprintf("  %s", item2.label))
			}
		}

		lines = append(lines, fmt.Sprintf("  %s  %s", leftCell, rightCell))
	}

	lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))
	lines = append(lines, hint)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().
		Width(w).
		Height(max(1, h)).
		Render(lipgloss.Place(w, max(1, h), lipgloss.Center, lipgloss.Center, box))
}
