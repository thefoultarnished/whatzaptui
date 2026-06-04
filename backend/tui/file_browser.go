package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fileBrowserEntry struct {
	path          string
	name          string
	isDir         bool
	isPlaceholder bool
	size          int64
	modTime       int64
}

func defaultFileBrowserDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		downloads := filepath.Join(home, "Downloads")
		if info, err := os.Stat(downloads); err == nil && info.IsDir() {
			return downloads
		}
		return home
	}
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		return cwd
	}
	return "."
}

func fileTypeIcon(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return "◈"
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv":
		return "▶"
	case ".mp3", ".ogg", ".wav", ".flac", ".m4a", ".aac", ".opus":
		return "♪"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".csv":
		return "≡"
	case ".zip", ".rar", ".7z", ".tar", ".gz":
		return "⊕"
	default:
		return "·"
	}
}

func fileTypeColor(name string) lipgloss.Color {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return imageTag
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv":
		return videoTag
	case ".mp3", ".ogg", ".wav", ".flac", ".m4a", ".aac", ".opus":
		return audioTag
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".csv":
		return fileTag
	case ".zip", ".rar", ".7z", ".tar", ".gz":
		return anomalyTag
	default:
		return muted
	}
}

func fileTypeStyle(name string) lipgloss.Style {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".avif":
		return imageTagStyle
	case ".mp4", ".mkv", ".mov", ".avi", ".webm", ".flv":
		return videoTagStyle
	case ".mp3", ".ogg", ".wav", ".flac", ".m4a", ".aac", ".opus":
		return audioTagStyle
	default:
		return fileTagStyle
	}
}

func formatFileSize(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func (e fileBrowserEntry) label() string {
	switch {
	case e.isPlaceholder:
		return e.name
	case e.isDir:
		return "[" + e.name + "]"
	default:
		return e.name
	}
}

func readFileBrowserEntries(dir string, sortRecent bool) ([]fileBrowserEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	out := make([]fileBrowserEntry, 0, len(entries)+1)
	parent := filepath.Dir(dir)
	if parent != "" && parent != dir {
		out = append(out, fileBrowserEntry{
			path:  parent,
			name:  "..",
			isDir: true,
		})
	}
	for _, entry := range entries {
		fe := fileBrowserEntry{
			path:  filepath.Join(dir, entry.Name()),
			name:  entry.Name(),
			isDir: entry.IsDir(),
		}
		if info, err := entry.Info(); err == nil {
			fe.size = info.Size()
			fe.modTime = info.ModTime().Unix()
		}
		out = append(out, fe)
	}

	// Separate ".." placeholder from the sortable entries
	var dotdot []fileBrowserEntry
	var sortable []fileBrowserEntry
	for _, e := range out {
		if e.name == ".." {
			dotdot = append(dotdot, e)
		} else {
			sortable = append(sortable, e)
		}
	}

	if sortRecent {
		sort.Slice(sortable, func(i, j int) bool {
			if sortable[i].isDir != sortable[j].isDir {
				return sortable[i].isDir
			}
			return sortable[i].modTime > sortable[j].modTime
		})
	} else {
		sort.Slice(sortable, func(i, j int) bool {
			if sortable[i].isDir != sortable[j].isDir {
				return sortable[i].isDir
			}
			return strings.ToLower(sortable[i].name) < strings.ToLower(sortable[j].name)
		})
	}

	result := append(dotdot, sortable...)
	if len(result) == 0 {
		result = append(result, fileBrowserEntry{name: "(empty folder)", isPlaceholder: true})
	}
	return result, nil
}

func applyFileBrowserFilter(entries []fileBrowserEntry, filter string) []fileBrowserEntry {
	if filter == "" {
		return entries
	}
	f := strings.ToLower(filter)
	out := make([]fileBrowserEntry, 0, len(entries))
	for _, e := range entries {
		if e.name == ".." || strings.Contains(strings.ToLower(e.name), f) {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		out = append(out, fileBrowserEntry{name: "(no matches)", isPlaceholder: true})
	}
	return out
}

func (x *m) rebuildFileBrowserFiltered() {
	x.fileBrowserFiltered = applyFileBrowserFilter(x.fileBrowserEntries, x.fileBrowserFilter)
}

func (x *m) loadFileBrowserDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := readFileBrowserEntries(abs, x.fileBrowserSortRecent)
	if err != nil {
		return err
	}
	x.fileBrowserDir = abs
	x.fileBrowserEntries = entries
	x.fileBrowserFilter = ""
	x.rebuildFileBrowserFiltered()
	x.fileBrowserIndex = 0
	x.fileBrowserScroll = 0
	if x.mainCache != nil {
		x.mainCache.result = ""
	}
	return nil
}

func (x *m) openFileBrowser() tea.Cmd {
	startDir := x.fileBrowserDir
	if strings.TrimSpace(startDir) == "" {
		startDir = defaultFileBrowserDir()
	}
	x.fileBrowserSortRecent = true
	if err := x.loadFileBrowserDir(startDir); err != nil {
		return x.setTopBar(fmt.Sprintf("File browser: %v", err))
	}
	x.fileBrowserOpen = true
	return nil
}

func (x *m) closeFileBrowser() {
	x.fileBrowserOpen = false
	x.fileBrowserFilter = ""
	if x.mainCache != nil {
		x.mainCache.result = ""
	}
}

func (x *m) ensureFileBrowserVisible(viewRows int) {
	list := x.fileBrowserFiltered
	if len(list) == 0 {
		x.fileBrowserIndex = 0
		x.fileBrowserScroll = 0
		return
	}
	if x.fileBrowserIndex < 0 {
		x.fileBrowserIndex = 0
	}
	if x.fileBrowserIndex >= len(list) {
		x.fileBrowserIndex = len(list) - 1
	}
	if viewRows <= 0 {
		return
	}
	maxStart := max(0, len(list)-viewRows)
	if x.fileBrowserScroll > maxStart {
		x.fileBrowserScroll = maxStart
	}
	if x.fileBrowserScroll < 0 {
		x.fileBrowserScroll = 0
	}
	if x.fileBrowserIndex < x.fileBrowserScroll {
		x.fileBrowserScroll = x.fileBrowserIndex
	}
	if x.fileBrowserIndex >= x.fileBrowserScroll+viewRows {
		x.fileBrowserScroll = x.fileBrowserIndex - viewRows + 1
	}
}

func (x m) fileBrowserVisibleRows(h int) int {
	return max(1, h-8) // extra row for filter bar
}

func (x m) selectedFileBrowserEntry() (fileBrowserEntry, bool) {
	list := x.fileBrowserFiltered
	if len(list) == 0 || x.fileBrowserIndex < 0 || x.fileBrowserIndex >= len(list) {
		return fileBrowserEntry{}, false
	}
	return list[x.fileBrowserIndex], true
}

func quoteSendPath(path string) string {
	return strings.ReplaceAll(path, `"`, `\"`)
}

func (x *m) setPendingAttachment(path string) {
	x.pendingAttachmentPath = path
	x.pendingAttachmentName = filepath.Base(path)
	x.pendingAttachmentKind = ""
	if kind, err := detectMediaSendKind(path); err == nil {
		x.pendingAttachmentKind = kind
	}
}

func (x *m) clearPendingAttachment() {
	x.pendingAttachmentPath = ""
	x.pendingAttachmentKind = ""
	x.pendingAttachmentName = ""
}

func (x m) pendingAttachmentLabel() string {
	if x.pendingAttachmentPath == "" {
		return ""
	}
	if currentConfig.MediaIconStyle == "nerd" {
		kind := x.pendingAttachmentKind
		if kind == "document" {
			kind = "file"
		}
		if g := nerdIconFor(kind); g != "" {
			return mediaTagStyle(kind).Render(g) + "  " + x.pendingAttachmentName
		}
		return x.pendingAttachmentName
	}
	switch x.pendingAttachmentKind {
	case "image":
		return "[image attached] " + x.pendingAttachmentName
	case "video":
		return "[video attached] " + x.pendingAttachmentName
	case "document":
		return "[file attached] " + x.pendingAttachmentName
	default:
		return "[attached] " + x.pendingAttachmentName
	}
}

func (x m) renderFileBrowser(w, h int) string {
	panelBg := lipgloss.Color(currentTheme.SidebarActiveBg)
	bg := func(s lipgloss.Style) lipgloss.Style { return s.Background(panelBg) }

	titleSt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	hintSt := bg(lipgloss.NewStyle().Foreground(muted))
	keySt := bg(lipgloss.NewStyle().Foreground(accent).Bold(true))
	divSt := bg(lipgloss.NewStyle().Foreground(muted))
	dirSt := bg(lipgloss.NewStyle().Foreground(brand).Bold(true))
	fileSt := bg(lipgloss.NewStyle().Foreground(text))

	activePanelBg := lipgloss.Color(currentTheme.ShortcutActive)

	fill := bg(lipgloss.NewStyle().Width(w - 2))
	ln := func(s string) string { return fill.Render(s) }

	divLine := ln(divSt.Render(strings.Repeat("─", w-2)))

	sortLabel := "name"
	if x.fileBrowserSortRecent {
		sortLabel = "recent"
	}
	titleRunes := len([]rune(" File Browser"))
	sortTagW := len([]rune(" sort:" + sortLabel + " "))
	gapW := max(0, w-2-titleRunes-sortTagW)
	titleRow := ln(
		titleSt.Render(" File Browser") +
			bg(lipgloss.NewStyle().Width(gapW)).Render("") +
			hintSt.Render(" sort:"+sortLabel+" "),
	)

	dirRow := ln(hintSt.Render(" " + truncateDisplayWidth(x.fileBrowserDir, max(10, w-4))))

	// filter or path-input bar
	var filterRow string
	if x.fileBrowserPathMode {
		prefix := keySt.Render(" path: ")
		placeholder := ""
		if x.fileBrowserPathBuf == "" {
			placeholder = hintSt.Render("paste or type path, Enter to go, Esc to cancel")
		}
		cursor := lipgloss.NewStyle().Background(panelBg).Foreground(accent).Render("█")
		buf := bg(lipgloss.NewStyle().Foreground(accent)).Render(x.fileBrowserPathBuf) + placeholder + cursor
		filterRow = ln(prefix + buf)
	} else {
		filterPrefix := hintSt.Render(" filter: ")
		filterText := x.fileBrowserFilter
		if filterText == "" {
			filterText = hintSt.Render("type to search...")
		} else {
			filterText = lipgloss.NewStyle().Background(panelBg).Foreground(accent).Render(filterText)
		}
		filterRow = ln(filterPrefix + filterText)
	}

	lines := []string{titleRow, dirRow, divLine, filterRow, divLine}

	rows := x.fileBrowserVisibleRows(h)
	list := x.fileBrowserFiltered

	xCopy := x
	xCopy.ensureFileBrowserVisible(rows)
	start := xCopy.fileBrowserScroll
	end := min(len(list), start+rows)

	sp := bg(lipgloss.NewStyle()) // bg-colored space helper
	spc := func(s string) string { return sp.Render(s) }

	const maxName = 80
	rowW := w - 2

	renderFileRow := func(
		isActive bool,
		isDir, isPlaceholder bool,
		entry fileBrowserEntry,
		cursor string,
	) string {
		if isDir || isPlaceholder {
			label := truncateDisplayWidth(entry.label(), maxName)
			content := cursor + label
			if isActive {
				abg := lipgloss.NewStyle().Background(activePanelBg)
				return abg.Foreground(accent).Bold(true).Width(rowW).Render(content)
			}
			if isPlaceholder {
				return ln(spc("  ") + hintSt.Render(label))
			}
			return ln(spc("  ") + dirSt.Render(label))
		}

		icon := fileTypeIcon(entry.name)
		ext := strings.ToLower(filepath.Ext(entry.name))
		if ext == "" {
			ext = "·"
		}
		size := formatFileSize(entry.size)

		if isActive {
			abg := lipgloss.NewStyle().Background(activePanelBg)
			asp := abg.Render
			iconSt := abg.Foreground(fileTypeColor(entry.name)).Bold(true)
			nameSt := abg.Foreground(accent).Bold(true).Underline(true)
			extSt := abg.Foreground(muted)
			sizeSt := abg.Foreground(accent)

			prefix := asp(" ") + iconSt.Render(icon) + asp(" ")
			name := truncateDisplayWidth(entry.name, maxName)
			left := prefix + nameSt.Render(name) + asp("  ") + extSt.Render(ext)
			leftW := lipgloss.Width(left)
			sizeW := lipgloss.Width(size)
			pad := max(1, rowW-leftW-sizeW-1)
			return lipgloss.NewStyle().Background(activePanelBg).Width(rowW).Render(
				left + asp(strings.Repeat(" ", pad)) + sizeSt.Render(size),
			)
		}

		iconSt := bg(lipgloss.NewStyle().Foreground(fileTypeColor(entry.name)).Bold(true))
		extSt := bg(lipgloss.NewStyle().Foreground(muted))
		sizeSt := bg(lipgloss.NewStyle().Foreground(muted))

		prefix := spc(" ") + iconSt.Render(icon) + spc(" ")
		name := truncateDisplayWidth(entry.name, maxName)
		left := prefix + fileSt.Render(name) + spc("  ") + extSt.Render(ext)
		leftW := lipgloss.Width(left)
		sizeW := lipgloss.Width(size)
		pad := max(1, rowW-leftW-sizeW-1)
		return ln(left + spc(strings.Repeat(" ", pad)) + sizeSt.Render(size))
	}

	for i := start; i < end; i++ {
		entry := list[i]
		isActive := i == xCopy.fileBrowserIndex
		cursor := "  "
		if isActive {
			cursor = "▶ "
		}
		lines = append(lines, renderFileRow(isActive, entry.isDir, entry.isPlaceholder, entry, cursor))
	}

	// pad remaining rows
	for len(lines) < max(1, h-3) {
		lines = append(lines, ln(""))
	}

	status := fmt.Sprintf(" %d/%d", min(len(list), xCopy.fileBrowserIndex+1), len(list))
	lines = append(lines, ln(divLine))
	hint := ln(
		keySt.Render("↑↓") + hintSt.Render(" scroll  ") +
			keySt.Render("Enter") + hintSt.Render(" select  ") +
			keySt.Render("BS") + hintSt.Render(" up  ") +
			keySt.Render("Tab") + hintSt.Render(" sort  ") +
			keySt.Render("Alt+F") + hintSt.Render(" path  ") +
			keySt.Render("Esc") + hintSt.Render(" close") +
			hintSt.Render(status),
	)
	lines = append(lines, hint)

	box := lipgloss.NewStyle().
		Background(panelBg).
		Padding(0, 1).
		Width(w).
		Render(strings.Join(lines, "\n"))

	return lipgloss.NewStyle().Width(w).Height(max(1, h)).Render(box)
}
