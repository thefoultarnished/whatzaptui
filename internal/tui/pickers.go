package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"whatzap/internal/tui/picker"
)

// The picker screens live in internal/tui/picker. This file holds what ties
// them to the app: the item lists, the theme/config side effects, and the
// Style snapshot they draw with.

func init() {
	picker.Width = runeDisplayWidth
	picker.Truncate = truncate
}

// pickerStyle snapshots the current theme for a picker render.
func pickerStyle() picker.Style {
	return picker.Style{
		V2:              themeV2,
		Text:            text,
		TextSecondary:   textSecondary,
		Muted:           muted,
		Accent:          accent,
		Brand:           brand,
		Purple:          purple,
		BorderSubtle:    borderSubtle,
		BorderFocus:     borderFocus,
		BgSelected:      bgSelected,
		BadgeInk:        badgeInk,
		StatusSuccess:   statusSuccess,
		PanelBg:         lipgloss.Color(currentTheme.SidebarActiveBg),
		ActivePanelBg:   lipgloss.Color(currentTheme.ShortcutActive),
		LightBackground: relLuminance(string(background)) >= 0.5,
		Outline:         withPanelOutline,
		Shine:           renderShine,
	}
}

// --- Theme ---

var themeList = []struct {
	name        string
	displayName string
	theme       Theme
}{
	{"halo", "Halo", Halo},
	{"cornflower", "Cornflower", Cornflower},
	{"whatsapp", "WhatsApp", WhatsApp},
	{"linen", "Linen", Linen},
	{"tokyonight", "Tokyo Night", TokyoNight},
	{"catppuccin", "Catppuccin", Catppuccin},
	{"monokai", "Monokai", Monokai},
	{"charcoal", "Charcoal", Charcoal},
	{"aurora", "Aurora", Aurora},
	{"sakura", "Sakura", Sakura},
	{"abyssal", "Abyssal", Abyssal},
	{"cyberpunk", "Cyberpunk", Cyberpunk},
	{"lilac", "Lilac", Lilac},
	{"ember", "Ember", Ember},
	{"glacier", "Glacier", Glacier},
	{"verdant", "Verdant", Verdant},
	{"dusk", "Dusk", Dusk},
	{"fossil", "Fossil", Fossil},
	{"nord", "Nord", Nord},
}

var themeGroupOrder = []struct {
	name        string
	displayName string
	theme       Theme
}{
	// Dark
	{"whatsapp", "WhatsApp", WhatsApp},
	{"tokyonight", "Tokyo Night", TokyoNight},
	{"catppuccin", "Catppuccin", Catppuccin},
	{"monokai", "Monokai", Monokai},
	{"charcoal", "Charcoal", Charcoal},
	{"aurora", "Aurora", Aurora},
	{"abyssal", "Abyssal", Abyssal},
	{"cyberpunk", "Cyberpunk", Cyberpunk},
	// Warm
	{"ember", "Ember", Ember},
	{"sakura", "Sakura", Sakura},
	{"dusk", "Dusk", Dusk},
	{"fossil", "Fossil", Fossil},
	// Cool
	{"glacier", "Glacier", Glacier},
	{"verdant", "Verdant", Verdant},
	{"halo", "Halo", Halo},
	{"cornflower", "Cornflower", Cornflower},
	{"linen", "Linen", Linen},
	{"lilac", "Lilac", Lilac},
	{"nord", "Nord", Nord},
}

var themeGroupDefs = []picker.Group{
	{Name: "Dark", Count: 8},
	{Name: "Warm", Count: 4},
	{Name: "Cool", Count: 7},
}

func applyThemeByName(name string) {
	for _, t := range themeList {
		if t.name == name {
			currentTheme = t.theme
			currentConfig.ThemeName = name
			rehashStyles()
			return
		}
	}
	currentTheme = themeList[0].theme
	currentConfig.ThemeName = themeList[0].name
	rehashStyles()
}

func newThemePicker() picker.Picker {
	items := make([]picker.Item, len(themeGroupOrder))
	for i, t := range themeGroupOrder {
		items[i] = picker.Item{
			Key:      t.name,
			Label:    t.displayName,
			Swatch:   lipgloss.Color(t.theme.AnomalyTag),
			Tint:     lipgloss.Color(t.theme.Accent),
			SwatchV2: lipgloss.Color(t.theme.Brand),
			TintV2:   lipgloss.Color(t.theme.Purple),
		}
	}
	p := picker.New("Select Theme", items)
	p.Groups = themeGroupDefs
	return p
}

// --- Help ---

var helpCommands = []struct {
	cmd  string
	desc string
}{
	// Interface
	{"/help", "Show commands"},
	{"/theme", "Change color theme"},
	{"/pointer", "Change message icon"},
	{"/settings", "Open settings panel"},
	{"/fonttest", "Show Nerd Font icon test"},
	{"/emoji", "Open emoji picker"},
	{"/mouseon", "Enable mouse"},
	{"/mouseoff", "Disable mouse"},
	// Contacts
	{"/pin", "Pin or unpin a chat (synced with phone)"},
	{"/archive", "Archive a chat (synced with phone)"},
	{"/unarchive", "Unarchive a chat (synced with phone)"},
	{"/rename", "Rename a contact"},
	{"/whitelist", "Allow a contact"},
	{"/whitelistall", "Allow all"},
	{"/blacklist", "Block a contact"},
	{"/blacklistall", "Block all"},
	{"/block", "Block on WhatsApp"},
	{"/synccontacts", "Sync contacts"},
	{"/syncgroups", "Sync groups"},
	{"/synchistory", "Sync chat history"},
	{"/allcontacts", "Toggle stored-only People"},
	{"Alt+B / Alt+W", "Toggle whitelist for selected contact"},
	{"Alt+D", "Archived chats, again to go back"},
	{"Alt+G", "Who reacted to the selected message"},
	// Sounds
	{"/soundon", "Enable sounds"},
	{"/soundoff", "Disable sounds"},
	{"/sound1", "Sound profile 1"},
	{"/sound2", "Sound profile 2"},
	{"/sound3", "Sound profile 3"},
	{"/sound4", "Sound profile 4"},
	{"/sound5", "Sound profile 5"},
	// Session
	{"/logout", "Log out"},
	{"/restart", "Restart app"},
	{"/exit", "Exit"},
}

var helpGroupDefs = []picker.Group{
	{Name: "Interface", Count: 8},
	{Name: "Contacts", Count: 16},
	{Name: "Sounds", Count: 7},
	{Name: "Session", Count: 3},
}

func newHelpPicker() picker.Picker {
	items := make([]picker.Item, len(helpCommands))
	for i, c := range helpCommands {
		items[i] = picker.Item{Key: c.cmd, Label: c.cmd + "  " + c.desc, Desc: c.desc}
	}
	p := picker.New("Commands", items)
	p.Groups = helpGroupDefs
	return p
}

// --- Pointer icon ---

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
	{"\U000F0335", "Lightbulb"},
	{"\U000F0171", "Microchip"},
	{"│", "Connected Line"},
	{"┃", "Thick Connected Line"},
	{"║", "Double Connected Line"},
	{" ", "None"},
}

func newPointerPicker() picker.Picker {
	items := make([]picker.Item, len(pointerList))
	for i, p := range pointerList {
		items[i] = picker.Item{Key: p.icon, Label: fmt.Sprintf("%s  %s", p.icon, p.displayName)}
	}
	return picker.New("Select Pointer Icon", items)
}

// --- Typing animation ---

var typingAnimationList = []struct {
	key         string
	displayName string
	icons       []string
}{
	{"dots", "Animated dots", []string{"●○○", "○●○", "○○●"}},
	{"braille", "Braille", []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}},
	{"sparkle", "Sparkle", []string{"✿", "✦", "◇", "☆", "⌘"}},
	{"bars", "Loading bars", []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█", "▇", "▆", "▅", "▄", "▃", "▂"}},
	{"pulse", "Pulse", []string{"○", "◉", "○"}},
	{"arrows", "Rotating arrows", []string{"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"}},
	{picker.SquaresKey, "Squares", []string{
		"··◼◼◼◼◼◼",
		"·◼◼◼◼◼◼·",
		"◼◼◼◼◼◼··",
		"◼◼◼◼◼◼··",
		"·◼◼◼◼◼◼·",
		"··◼◼◼◼◼◼",
	}},
}

func newTypingAnimationPicker() picker.Picker {
	items := make([]picker.Item, len(typingAnimationList))
	for i, a := range typingAnimationList {
		items[i] = picker.Item{
			Key:   a.key,
			Label: fmt.Sprintf("%s  %s", a.icons[0], a.displayName),
			Name:  a.displayName,
			Icons: a.icons,
		}
	}
	return picker.New("Typing Style", items)
}

func getTypingIcons(style string) []string {
	return typingAnimationList[typingAnimationIndex(style)].icons
}

// typingAnimationIndex returns the typingAnimationList index for style,
// falling back to Sparkle when style is unset or unknown.
func typingAnimationIndex(style string) int {
	for i, a := range typingAnimationList {
		if a.key == style {
			return i
		}
	}
	return 2
}

// userlistIconPrefix returns the static icon glyph (plus trailing space) used
// in place of the row number when UserlistIconStyle names a sparkle icon
// (e.g. "sparkle-2"). Returns "" for "numbers" (or unset), which keeps the
// existing numbering.
func userlistIconPrefix(style string) string {
	idx, ok := strings.CutPrefix(style, "sparkle-")
	if !ok {
		return ""
	}
	i, err := strconv.Atoi(idx)
	if err != nil {
		return ""
	}
	for _, a := range typingAnimationList {
		if a.key == "sparkle" && i >= 0 && i < len(a.icons) {
			return a.icons[i] + " "
		}
	}
	return ""
}

// --- Settings ---

var settingsDefs = []struct {
	name       string
	isSelector bool
	get        func() bool
	set        func(bool)
	getStr     func() string
}{
	{"Send typing status", false, func() bool { return currentConfig.SendTypingIndicator }, func(v bool) { currentConfig.SendTypingIndicator = v }, nil},
	{"Show online status", false, func() bool { return currentConfig.ShowOnline }, func(v bool) { currentConfig.ShowOnline = v }, nil},
	{"Message sounds", false, func() bool { return currentConfig.SoundEnabled }, func(v bool) { currentConfig.SoundEnabled = v }, nil},
	{"Mouse support", false, func() bool { return currentConfig.MouseEnabled }, func(v bool) { currentConfig.MouseEnabled = v }, nil},
	{"Flash taskbar", false, func() bool { return currentConfig.FlashTaskbar }, func(v bool) { currentConfig.FlashTaskbar = v }, nil},
	{"Tab Alerts", false, func() bool { return currentConfig.NotificationsEnabled }, func(v bool) { currentConfig.NotificationsEnabled = v }, nil},
	{"Time on new line", false, func() bool { return currentConfig.TimestampNewLine }, func(v bool) { currentConfig.TimestampNewLine = v }, nil},
	{"Hide borders", false, func() bool { return currentConfig.Borderless }, func(v bool) { currentConfig.Borderless = v }, nil},
	{"Menu borders", false, func() bool { return !currentConfig.HideMenuBorder }, func(v bool) { currentConfig.HideMenuBorder = !v }, nil},
	{"Show phone number", false, func() bool { return !currentConfig.HidePhoneNumber }, func(v bool) { currentConfig.HidePhoneNumber = !v }, nil},
	{"Typing style", true, nil, nil, func() string {
		return typingAnimationList[typingAnimationIndex(currentConfig.TypingAnimationStyle)].displayName
	}},
	{"Media icon style", true, nil, nil, func() string {
		if currentConfig.MediaIconStyle == "nerd" {
			return "Nerd"
		}
		return "Text"
	}},
	{"Media preview", true, nil, nil, func() string {
		for _, e := range mediaViewList {
			if e.key == currentConfig.MediaViewStyle {
				return strings.ToUpper(e.key[:1]) + e.key[1:]
			}
		}
		return "Text"
	}},
	{"Chat list icons", true, nil, nil, func() string {
		if icon := strings.TrimSpace(userlistIconPrefix(currentConfig.UserlistIconStyle)); icon != "" {
			return icon
		}
		return "Numbers"
	}},
	{"Startup speed", true, nil, nil, func() string {
		switch currentConfig.SplashStageSpeed {
		case "off":
			return "Off"
		case "fast":
			return "Fast"
		case "slow":
			return "Slow"
		case "extremely_slow":
			return "Extra slow"
		default:
			return "Normal"
		}
	}},
	{"Theme", true, nil, nil, func() string {
		for _, t := range themeList {
			if t.name == currentConfig.ThemeName {
				return t.displayName
			}
		}
		if len(themeList) > 0 {
			return themeList[0].displayName
		}
		return ""
	}},
}

// settingsItem renders settingsDefs[i] with its current value.
func settingsItem(i int) picker.Item {
	s := settingsDefs[i]
	state := "OFF"
	switch {
	case s.isSelector:
		state = s.getStr()
	case s.get():
		state = "ON"
	}
	return picker.Item{Key: s.name, Label: s.name + "  " + state, Selector: s.isSelector, Value: state}
}

func newSettingsPicker() picker.Picker {
	items := make([]picker.Item, len(settingsDefs))
	for i := range settingsDefs {
		items[i] = settingsItem(i)
	}
	return picker.New("Settings", items)
}

// toggleSetting flips the selected ON/OFF setting, saves the config and
// returns a "Name: ON" status line ("" when nothing is selected).
func toggleSetting(p *picker.Picker) string {
	if p.Idx < 0 || p.Idx >= len(settingsDefs) {
		return ""
	}
	s := settingsDefs[p.Idx]
	s.set(!s.get())
	saveConfig()
	p.Items[p.Idx] = settingsItem(p.Idx)
	return s.name + ": " + p.Items[p.Idx].Value
}

// reopenSettings rebuilds the settings picker after a sub-picker closes,
// restoring the selection that was active when the sub-picker was opened
// instead of jumping back to the first item.
func (x *m) reopenSettings() {
	x.settingsPicker = newSettingsPicker()
	x.settingsPicker.Open("")
	if x.settingsReturnIdx >= 0 && x.settingsReturnIdx < len(settingsDefs) {
		x.settingsPicker.Idx = x.settingsReturnIdx
	}
	x.invalidate()
}

// --- Settings sub-menus ---

var mediaIconList = []struct {
	key, label string
}{
	{"text", "Text   •  [image]  [video]  [file]  [audio]"},
	{"nerd", "Nerd   •  " + nerdIconFor("image") + " image  " + nerdIconFor("video") + " video  " + nerdIconFor("file") + " file  " + nerdIconFor("audio") + " audio"},
}

var mediaViewList = []struct {
	key, label string
}{
	{"text", "Text   •  [image] tag"},
	{"glyph", "Glyph  •  " + nerdIconFor("image") + " image tag"},
	{"pixel", "Pixel  •  inline terminal art"},
	{"full", "Full   •  terminal graphics (Kitty/iTerm)"},
}

var splashSpeedList = []struct {
	key, label string
}{
	{"off", "Off             •  0s delay (finishes as tasks complete)"},
	{"fast", "Fast            •  2s total (500ms per stage)"},
	{"normal", "Normal          •  4s total (1.0s per stage)"},
	{"slow", "Slow            •  6s total (1.5s per stage)"},
	{"extremely_slow", "Extremely Slow  •  8s total (2.0s per stage)"},
}

func keyLabelItems(list []struct{ key, label string }) []picker.Item {
	items := make([]picker.Item, len(list))
	for i, e := range list {
		items[i] = picker.Item{Key: e.key, Label: e.label}
	}
	return items
}

func newMediaIconPicker() picker.Picker {
	return picker.New("Media Icon Style", keyLabelItems(mediaIconList))
}

func newMediaViewPicker() picker.Picker {
	return picker.New("Media Preview", keyLabelItems(mediaViewList))
}

func newSplashSpeedPicker() picker.Picker {
	return picker.New("Startup Speed", keyLabelItems(splashSpeedList))
}

func newUserlistIconPicker() picker.Picker {
	items := []picker.Item{{Key: "numbers", Label: "Numbers"}}
	for _, a := range typingAnimationList {
		if a.key == "sparkle" {
			for i, icon := range a.icons {
				items = append(items, picker.Item{Key: fmt.Sprintf("sparkle-%d", i), Label: icon})
			}
		}
	}
	return picker.New("Chat List Icons", items)
}

func splashStageHoldDuration() time.Duration {
	switch currentConfig.SplashStageSpeed {
	case "off":
		return 0
	case "fast":
		return 500 * time.Millisecond
	case "slow":
		return 1500 * time.Millisecond
	case "extremely_slow":
		return 2000 * time.Millisecond
	default:
		return time.Second
	}
}
