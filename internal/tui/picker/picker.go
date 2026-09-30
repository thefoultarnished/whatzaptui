// Package picker holds the TUI's modal list pickers (theme, help, settings,
// typing style, pointer icon and the settings sub-menus): selection state,
// key handling and drawing. Colours arrive through Style on every render so
// this package never touches the TUI's theme globals.
package picker

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Width and Truncate measure and shorten labels. The TUI replaces them with
// its own emoji-aware helpers so pickers lay out exactly like the rest of
// the screen.
var (
	Width    = lipgloss.Width
	Truncate = func(s string, n int) string { return s }
)

// Item is one entry. Key and Label apply to every picker; the other fields
// are only read by the picker layout that needs them.
type Item struct {
	Key   string
	Label string

	Desc string // help: command description

	Selector bool   // settings: opens a sub-picker instead of toggling
	Dim      bool   // filter list: drawn muted (for example a chat that is not allowed)
	Value    string // settings: "ON"/"OFF" or the selector's current choice

	Name  string   // typing style: display name
	Icons []string // typing style: animation frames

	Swatch, Tint     lipgloss.Color // theme: preview colours (legacy themes)
	SwatchV2, TintV2 lipgloss.Color // theme: preview colours (V2 themes)
}

// Group is a titled run of Count consecutive items (theme and help pickers).
type Group struct {
	Name  string
	Count int
}

type Picker struct {
	IsOpen    bool
	SingleCol bool
	// Panel draws the list as a raised panel like the theme and help pickers,
	// and lets a long list use two columns however wide its labels are.
	Panel bool
	// Query is what has been typed to narrow a filter list.
	Query    string
	Idx      int
	Title    string
	Items    []Item
	Groups   []Group
	original string
}

func New(title string, items []Item) Picker {
	return Picker{Title: title, Items: items}
}

func (p *Picker) isSingleCol() bool {
	if p.Panel {
		return p.SingleCol || len(p.Items) <= 4
	}
	if p.SingleCol || len(p.Items) <= 4 {
		return true
	}
	for _, item := range p.Items {
		if Width(item.Label) > 20 {
			return true
		}
	}
	return false
}

func (p *Picker) Open(currentKey string) {
	p.IsOpen = true
	p.original = currentKey
	p.Idx = 0
	for i, item := range p.Items {
		if item.Key == currentKey {
			p.Idx = i
			break
		}
	}
}

// Close returns the selected key if confirmed, or the original key if cancelled.
func (p *Picker) Close(confirm bool) string {
	p.IsOpen = false
	if confirm {
		return p.Items[p.Idx].Key
	}
	return p.original
}

func (p *Picker) SelectedKey() string {
	return p.Items[p.Idx].Key
}

func (p *Picker) rows() int {
	return (len(p.Items) + 1) / 2
}

func (p *Picker) colRow() (col, row int) {
	rows := p.rows()
	if p.Idx < rows {
		return 0, p.Idx
	}
	return 1, p.Idx - rows
}

func (p *Picker) fromColRow(col, row int) int {
	if col == 0 {
		return row
	}
	return p.rows() + row
}

func (p *Picker) rightColLen() int {
	return len(p.Items) - p.rows()
}

// Handle processes a key event. Returns ("confirm", true), ("cancel", true),
// or ("", false) if the picker just moved.
func (p *Picker) Handle(k tea.KeyMsg) (action string, done bool) {
	if p.Panel && !p.isSingleCol() {
		// Two-column panels move exactly like the theme picker.
		if len(p.Groups) == 0 {
			p.Groups = []Group{{Count: len(p.Items)}}
		}
		return p.handleGrouped(k, themeCols)
	}
	if p.isSingleCol() {
		switch k.Type {
		case tea.KeyUp, tea.KeyLeft:
			p.Idx--
			if p.Idx < 0 {
				p.Idx = len(p.Items) - 1
			}
		case tea.KeyDown, tea.KeyRight:
			p.Idx++
			if p.Idx >= len(p.Items) {
				p.Idx = 0
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
		p.Idx = p.fromColRow(col, row)
	case tea.KeyDown:
		row++
		maxRow := rows
		if col == 1 {
			maxRow = rightLen
		}
		if row >= maxRow {
			row = 0
		}
		p.Idx = p.fromColRow(col, row)
	case tea.KeyLeft, tea.KeyRight:
		if col == 0 {
			if row >= rightLen {
				row = rightLen - 1
			}
			p.Idx = p.fromColRow(1, row)
		} else {
			p.Idx = p.fromColRow(0, row)
		}
	case tea.KeyEnter:
		return "confirm", true
	case tea.KeyEsc:
		return "cancel", true
	}
	return "", false
}

// activeCell draws the selected item; V2 themes colour the ▶ marker with
// Action and keep the label in TextPrimary.
func activeCell(s Style, st lipgloss.Style, label string) string {
	if s.V2 {
		return lipgloss.NewStyle().Foreground(s.Accent).Bold(true).Render("▶ ") + st.Render(label)
	}
	return st.Render(fmt.Sprintf("▶ %s", label))
}

func (p *Picker) RenderBox(s Style, w, h int) string {
	if p.Panel {
		return p.renderPanelBox(s, w, h)
	}
	titleStyle := lipgloss.NewStyle().Foreground(s.pick(s.Text, s.Accent)).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(s.Muted)
	activeStyle := lipgloss.NewStyle().Foreground(s.pick(s.Text, s.Brand)).Bold(true)
	inactiveStyle := lipgloss.NewStyle().Foreground(s.pick(s.TextSecondary, s.Text))
	divStyle := lipgloss.NewStyle().Foreground(s.BorderSubtle)
	keyStyle := lipgloss.NewStyle().Foreground(s.Accent).Bold(true)

	if p.isSingleCol() {
		maxLabelW := 0
		for _, item := range p.Items {
			lw := Width(item.Label)
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
		lines = append(lines, titleStyle.Render("  "+p.Title))
		lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))

		for i, item := range p.Items {
			var cell string
			if i == p.Idx {
				cell = activeCell(s, activeStyle, item.Label)
			} else {
				cell = inactiveStyle.Render(fmt.Sprintf("  %s", item.Label))
			}
			lines = append(lines, "  "+cell)
		}

		lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))
		lines = append(lines, hint)

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(s.pick(s.BorderFocus, s.Accent)).
			Width(pickerW).
			Render(strings.Join(lines, "\n"))

		return box
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
	lines = append(lines, titleStyle.Render("  "+p.Title))
	lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))

	for r := range rows {
		leftIdx := r
		var leftCell, rightCell string

		item := p.Items[leftIdx]
		if leftIdx == p.Idx {
			leftCell = activeCell(s, activeStyle, item.Label)
		} else {
			leftCell = inactiveStyle.Render(fmt.Sprintf("  %s", item.Label))
		}
		leftCell = lipgloss.NewStyle().Width(colW).Render(leftCell)

		if r < rightLen {
			rightIdx := p.fromColRow(1, r)
			item2 := p.Items[rightIdx]
			if rightIdx == p.Idx {
				rightCell = activeCell(s, activeStyle, item2.Label)
			} else {
				rightCell = inactiveStyle.Render(fmt.Sprintf("  %s", item2.Label))
			}
		}

		lines = append(lines, fmt.Sprintf("  %s  %s", leftCell, rightCell))
	}

	lines = append(lines, divStyle.Render("  "+strings.Repeat("─", pickerW-4)))
	lines = append(lines, hint)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(s.pick(s.BorderFocus, s.Accent)).
		Width(pickerW).
		Render(strings.Join(lines, "\n"))

	return box
}
