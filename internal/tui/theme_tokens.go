package tui

import (
	"hash/fnv"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Role tokens from docs/themes.md. Themes flagged V2 set them directly;
// every other theme gets them filled from its legacy fields with values
// equal to what the UI drew before, so unmigrated themes look unchanged.
// Layout changes that go beyond a colour swap are gated on themeV2.

const (
	qrLightFixed = "#FFFFFF"
	qrDarkFixed  = "#000000"
)

var (
	themeV2 bool

	bgSidebar, bgPanel, bgSelected, bgActive, bgReply lipgloss.Color
	textSecondary, textFaint, textOnFill              lipgloss.Color
	textLink, textMatch                               lipgloss.Color
	borderSubtle, borderFocus                         lipgloss.Color
	statusSuccess, statusInfo, fxShine                lipgloss.Color
	senderPalette                                     []lipgloss.Color
)

// normalizeTheme fills whichever token set a theme doesn't define. It is
// idempotent, so it is safe to call on an already-normalized theme.
func normalizeTheme(t Theme) Theme {
	if t.V2 {
		return normalizeV2(t)
	}
	return normalizeLegacy(t)
}

func setIfEmpty(dst *string, v string) {
	if *dst == "" {
		*dst = v
	}
}

func normalizeLegacy(t Theme) Theme {
	setIfEmpty(&t.BgApp, t.Background)
	setIfEmpty(&t.BgSidebar, t.Background)
	setIfEmpty(&t.BgPanel, t.SidebarActiveBg)
	setIfEmpty(&t.BgSelected, t.ShortcutActive)
	setIfEmpty(&t.BgActive, t.ShortcutActive)
	setIfEmpty(&t.BgReply, t.ReplyPreviewBg)
	setIfEmpty(&t.BgMessageSelected, t.MessageSelectedBg)
	setIfEmpty(&t.TextPrimary, t.Text)
	setIfEmpty(&t.TextSecondary, t.Text)
	setIfEmpty(&t.TextMuted, t.Muted)
	setIfEmpty(&t.TextFaint, t.Muted)
	setIfEmpty(&t.TextOnFill, t.ButtonInk)
	setIfEmpty(&t.TextLink, "#89b4fa") // link colour every legacy theme was drawn with
	setIfEmpty(&t.TextMatch, t.Accent)
	setIfEmpty(&t.BorderSubtle, t.Muted)
	setIfEmpty(&t.BorderFocus, t.Accent)
	setIfEmpty(&t.Action, t.Accent)
	setIfEmpty(&t.Emphasis, t.Purple)
	setIfEmpty(&t.StatusSuccess, "#22c55e") // legacy settings ON dot
	setIfEmpty(&t.StatusWarning, t.Amber)
	setIfEmpty(&t.StatusDanger, t.Red)
	setIfEmpty(&t.StatusInfo, t.Amber) // legacy sync / top-bar text
	setIfEmpty(&t.ChatSentText, t.SentText)
	setIfEmpty(&t.ChatReceivedText, t.ReceivedText)
	setIfEmpty(&t.ChatSentName, t.SentName)
	setIfEmpty(&t.ChatReceivedName, t.ReceivedName)
	setIfEmpty(&t.ChatQuotedSent, t.QuotedSentText)
	setIfEmpty(&t.ChatQuotedReceived, t.QuotedReceivedText)
	setIfEmpty(&t.TagImage, t.ImageTag)
	setIfEmpty(&t.TagVideo, t.VideoTag)
	setIfEmpty(&t.TagAudio, t.AudioTag)
	setIfEmpty(&t.TagFile, t.FileTag)
	setIfEmpty(&t.TagSticker, t.StickerTag)
	setIfEmpty(&t.TagContact, t.ContactTag)
	setIfEmpty(&t.TagPoll, t.PollTag)
	setIfEmpty(&t.TagLocation, t.LocationTag)
	setIfEmpty(&t.TagSystem, t.AnomalyTag)
	setIfEmpty(&t.FxShine, "#FFFFFF")
	return t
}

func normalizeV2(t Theme) Theme {
	// Derived tokens (docs/themes.md §4); an explicit value wins.
	setIfEmpty(&t.BgActive, blendHex(t.BgSelected, t.BgSidebar, 0.5))
	setIfEmpty(&t.BgMessageSelected, t.BgSelected)
	setIfEmpty(&t.BgReply, blendHex(t.Action, t.BgApp, 0.90))
	setIfEmpty(&t.TextOnFill, pickOnFill(t.Brand, t.BgApp, t.TextPrimary))
	setIfEmpty(&t.TextLink, t.Action)
	setIfEmpty(&t.TextMatch, t.StatusWarning)
	setIfEmpty(&t.BorderFocus, blendHex(t.Action, t.BgApp, 0.45))
	setIfEmpty(&t.StatusInfo, t.Action)
	setIfEmpty(&t.ChatSentName, t.Brand)
	setIfEmpty(&t.ChatReceivedName, t.Emphasis)
	setIfEmpty(&t.ChatQuotedSent, blendHex(t.ChatSentText, t.TextMuted, 0.5))
	setIfEmpty(&t.ChatQuotedReceived, blendHex(t.ChatReceivedText, t.TextMuted, 0.5))
	if relLuminance(t.BgApp) < 0.5 {
		setIfEmpty(&t.FxShine, blendHex(t.TextPrimary, "#FFFFFF", 0.6))
	} else {
		setIfEmpty(&t.FxShine, blendHex(t.TextPrimary, "#000000", 0.4))
	}

	// Legacy fields mirror the tokens so code that still reads them draws
	// the V2 palette.
	t.Background = t.BgApp
	t.Text = t.TextPrimary
	t.Muted = t.TextMuted
	t.Accent = t.Action
	t.Purple = t.Emphasis
	t.Amber = t.StatusWarning
	t.Red = t.StatusDanger
	t.ImageTag = t.TagImage
	t.VideoTag = t.TagVideo
	t.AudioTag = t.TagAudio
	t.FileTag = t.TagFile
	t.StickerTag = t.TagSticker
	t.ContactTag = t.TagContact
	t.PollTag = t.TagPoll
	t.LocationTag = t.TagLocation
	t.AnomalyTag = t.TagSystem
	t.SentText = t.ChatSentText
	t.ReceivedText = t.ChatReceivedText
	t.SentName = t.ChatSentName
	t.ReceivedName = t.ChatReceivedName
	t.QuotedSentText = t.ChatQuotedSent
	t.QuotedReceivedText = t.ChatQuotedReceived
	t.BadgeInk = t.TextOnFill
	t.ButtonInk = t.TextOnFill
	t.TagInk = t.TextOnFill
	t.Cursor = t.Action
	t.QRLight = qrLightFixed
	t.QRDark = qrDarkFixed
	t.ShortcutActive = t.BgSelected
	t.SidebarActiveBg = t.BgPanel
	t.SidebarActiveUnreadBg = t.BgActive
	t.SidebarWhitelistActiveBg = t.StatusSuccess
	t.SidebarBlacklistActiveBg = t.StatusDanger
	t.ReplyPreviewBg = t.BgReply
	t.MessageSelectedBg = t.BgMessageSelected
	t.MediaTokenBg = t.Action
	t.MediaTokenPulseBg = blendHex(t.Action, t.TextPrimary, 0.4)
	return t
}

// resolveTokens loads the role-token package colours from currentTheme,
// which rehashStyles has already normalized.
func resolveTokens() {
	t := currentTheme
	themeV2 = t.V2
	bgSidebar = lipgloss.Color(t.BgSidebar)
	bgPanel = lipgloss.Color(t.BgPanel)
	bgSelected = lipgloss.Color(t.BgSelected)
	bgActive = lipgloss.Color(t.BgActive)
	bgReply = lipgloss.Color(t.BgReply)
	textSecondary = lipgloss.Color(t.TextSecondary)
	textFaint = lipgloss.Color(t.TextFaint)
	textOnFill = lipgloss.Color(t.TextOnFill)
	textLink = lipgloss.Color(t.TextLink)
	textMatch = lipgloss.Color(t.TextMatch)
	borderSubtle = lipgloss.Color(t.BorderSubtle)
	borderFocus = lipgloss.Color(t.BorderFocus)
	statusSuccess = lipgloss.Color(t.StatusSuccess)
	statusInfo = lipgloss.Color(t.StatusInfo)
	fxShine = lipgloss.Color(t.FxShine)
	senderPalette = []lipgloss.Color{
		lipgloss.Color(t.Emphasis),
		lipgloss.Color(t.Action),
		lipgloss.Color(t.TagAudio),
		lipgloss.Color(t.TagContact),
		lipgloss.Color(t.TagImage),
		lipgloss.Color(t.TagSticker),
		lipgloss.Color(t.TagFile),
	}
}

// v2Color returns v2 for migrated themes and legacy otherwise, for spots
// where the V2 design picks a different role than the old UI did.
func v2Color(v2, legacy lipgloss.Color) lipgloss.Color {
	if themeV2 {
		return v2
	}
	return legacy
}

// selectionMarker is the "▌" cursor bar that starts every selected row in
// V2 themes, drawn in Action on the row's background.
func selectionMarker(bg lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(accent).Background(bg).Render("▌")
}

// senderColor gives each group member a stable colour from the palette.
func senderColor(id string) lipgloss.Color {
	if !themeV2 || len(senderPalette) == 0 {
		return receivedName
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(num(id)))
	return senderPalette[h.Sum32()%uint32(len(senderPalette))]
}

// withPanelOutline draws the focus outline around a floating panel in V2
// themes when there is room for it; legacy themes keep their borderless
// panels.
func withPanelOutline(box string, w, h int) string {
	if !themeV2 || currentConfig.HideMenuBorder || lipgloss.Width(box)+2 > w || lipgloss.Height(box)+2 > h {
		return box
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderFocus).
		BorderBackground(bgPanel).
		Render(box)
}

// paintLines gives every cell of a multi-line block the background bg,
// line by line, while segments that set their own background keep it.
func paintLines(s string, bg lipgloss.Color) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = applyBgToAnsiString(l, bg)
	}
	return strings.Join(lines, "\n")
}

// pickOnFill returns whichever of the two candidate inks reads better on fill.
func pickOnFill(fill, dark, light string) string {
	if contrastRatio(dark, fill) >= contrastRatio(light, fill) {
		return dark
	}
	return light
}

func hexChannels(hex string) (r, g, b float64, ok bool) {
	s := strings.TrimPrefix(hex, "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255, true
}

// relLuminance is the WCAG 2 relative luminance of a "#RRGGBB" colour.
func relLuminance(hex string) float64 {
	r, g, b, ok := hexChannels(hex)
	if !ok {
		return 0
	}
	lin := func(c float64) float64 {
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio is the WCAG 2 contrast ratio between two colours (1–21).
func contrastRatio(a, b string) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
