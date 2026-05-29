package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

func getBrand() lipgloss.Color        { return lipgloss.Color(currentTheme.Brand) }
func getAccent() lipgloss.Color       { return lipgloss.Color(currentTheme.Accent) }
func getPurple() lipgloss.Color       { return lipgloss.Color(currentTheme.Purple) }
func getAmber() lipgloss.Color        { return lipgloss.Color(currentTheme.Amber) }
func getRed() lipgloss.Color          { return lipgloss.Color(currentTheme.Red) }
func getMuted() lipgloss.Color        { return lipgloss.Color(currentTheme.Muted) }
func getText() lipgloss.Color         { return lipgloss.Color(currentTheme.Text) }
func getImageTag() lipgloss.Color     { return lipgloss.Color(currentTheme.ImageTag) }
func getVideoTag() lipgloss.Color     { return lipgloss.Color(currentTheme.VideoTag) }
func getAudioTag() lipgloss.Color     { return lipgloss.Color(currentTheme.AudioTag) }
func getFileTag() lipgloss.Color      { return lipgloss.Color(currentTheme.FileTag) }
func getStickerTag() lipgloss.Color   { return lipgloss.Color(currentTheme.StickerTag) }
func getContactTag() lipgloss.Color   { return lipgloss.Color(currentTheme.ContactTag) }
func getPollTag() lipgloss.Color      { return lipgloss.Color(currentTheme.PollTag) }
func getLocationTag() lipgloss.Color  { return lipgloss.Color(currentTheme.LocationTag) }
func getAnomalyTag() lipgloss.Color   { return lipgloss.Color(currentTheme.AnomalyTag) }
func getSentText() lipgloss.Color     { return lipgloss.Color(currentTheme.SentText) }
func getReceivedText() lipgloss.Color { return lipgloss.Color(currentTheme.ReceivedText) }
func getSentName() lipgloss.Color     { return lipgloss.Color(currentTheme.SentName) }
func getReceivedName() lipgloss.Color { return lipgloss.Color(currentTheme.ReceivedName) }
func getBadgeInk() lipgloss.Color     { return lipgloss.Color(currentTheme.BadgeInk) }
func getButtonInk() lipgloss.Color    { return lipgloss.Color(currentTheme.ButtonInk) }
func getTagInk() lipgloss.Color       { return lipgloss.Color(currentTheme.TagInk) }
func getCursor() lipgloss.Color       { return lipgloss.Color(currentTheme.Cursor) }
func getQRLight() lipgloss.Color      { return lipgloss.Color(currentTheme.QRLight) }
func getQRDark() lipgloss.Color       { return lipgloss.Color(currentTheme.QRDark) }
func getShortcutActive() lipgloss.Color {
	return lipgloss.Color(currentTheme.ShortcutActive)
}
func getSidebarActiveBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.SidebarActiveBg)
}
func getSidebarActiveUnreadBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.SidebarActiveUnreadBg)
}
func getReplyPreviewBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.ReplyPreviewBg)
}
func getMessageSelectedBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.MessageSelectedBg)
}
func getMediaTokenBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.MediaTokenBg)
}
func getMediaTokenPulseBg() lipgloss.Color {
	return lipgloss.Color(currentTheme.MediaTokenPulseBg)
}

var (
	brand, accent, purple, amber, red, muted, text                     lipgloss.Color
	imageTag, videoTag, audioTag, fileTag, stickerTag                  lipgloss.Color
	contactTag, pollTag, locationTag, anomalyTag                       lipgloss.Color
	sentText, receivedText, sentName, receivedName                     lipgloss.Color
	quotedSentText, quotedReceivedText                                 lipgloss.Color
	badgeInk, buttonInk, tagInk, cursorColor, qrLight, qrDark          lipgloss.Color
	shortcutActive, sidebarActiveBg, sidebarActiveUnreadBg             lipgloss.Color
	replyPreviewBg, messageSelectedBg, mediaTokenBg, mediaTokenPulseBg lipgloss.Color

	spinnerFrames  = []string{"✶", "✸", "✹", "✺", "✹", "✷"}
	systemCommands []string
	chatCommands   = []string{
		"/emoji",
		"/send",
	}
)

func init() {
	cmds := []string{
		"/synccontacts", "/syncgroups", "/whitelist", "/whitelistall", "/blacklist", "/blacklistall", "/block", "/rename", "/logout", "/restart", "/exit",
		"/theme", "/pointer", "/help",
	}
	for i, t := range themeList {
		cmds = append(cmds, fmt.Sprintf("/theme%d%s", i+1, t.name))
	}
	cmds = append(cmds,
		"/mouseon", "/mouseoff",
		"/sound1", "/sound2", "/sound3", "/sound4", "/sound5", "/soundon", "/soundoff",
	)
	systemCommands = cmds
}

var (
	baseBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(brand).
			Foreground(text).
			Padding(1, 2).
			Align(lipgloss.Center, lipgloss.Center)

	logoStyle   = lipgloss.NewStyle().Bold(true).Foreground(brand)
	accentStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(muted)
	amberStyle  = lipgloss.NewStyle().Foreground(amber).Bold(true)
	purpleStyle = lipgloss.NewStyle().Foreground(purple).Bold(true)
	redStyle    = lipgloss.NewStyle().Foreground(red).Bold(true)

	cmdBadgeStyle    = lipgloss.NewStyle().Foreground(badgeInk).Background(accent).Bold(true)
	ghostStyle       = lipgloss.NewStyle().Foreground(muted)
	cursorStyle      = lipgloss.NewStyle().Foreground(cursorColor).Background(cursorColor)
	inputCursorStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	inputCursorGlyph = "█"

	receivedMsgIcon = "✦"

	sidebarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(muted)

	msgPaneStyle = lipgloss.NewStyle().Padding(0, 1)
	dateSepStyle = lipgloss.NewStyle().Foreground(muted).Bold(true)

	bColor = qrDark
	wColor = qrLight
	bb     = lipgloss.NewStyle().Foreground(bColor).Background(bColor).Render("▀")
	bw     = lipgloss.NewStyle().Foreground(bColor).Background(wColor).Render("▀")
	wb     = lipgloss.NewStyle().Foreground(wColor).Background(bColor).Render("▀")
	ww     = lipgloss.NewStyle().Foreground(wColor).Background(wColor).Render("▀")

	imageTagStyle    = lipgloss.NewStyle().Foreground(tagInk).Background(imageTag).Bold(true)
	videoTagStyle    = lipgloss.NewStyle().Foreground(tagInk).Background(videoTag).Bold(true)
	audioTagStyle    = lipgloss.NewStyle().Foreground(tagInk).Background(audioTag).Bold(true)
	fileTagStyle     = lipgloss.NewStyle().Foreground(tagInk).Background(fileTag).Bold(true)
	stickerTagStyle  = lipgloss.NewStyle().Foreground(tagInk).Background(stickerTag).Bold(true)
	contactTagStyle  = lipgloss.NewStyle().Foreground(tagInk).Background(contactTag).Bold(true)
	pollTagStyle     = lipgloss.NewStyle().Foreground(tagInk).Background(pollTag).Bold(true)
	locationTagStyle = lipgloss.NewStyle().Foreground(tagInk).Background(locationTag).Bold(true)
	anomalyTagStyle  = lipgloss.NewStyle().Foreground(tagInk).Background(anomalyTag).Bold(true)
)
