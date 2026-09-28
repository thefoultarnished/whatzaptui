package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (x m) renderStartupView(frameW int) string {
	outerW := x.w
	if outerW == 0 {
		outerW = frameW
	}
	outerH := x.h
	innerW, innerH := outerW, outerH
	statusBody := x.status
	logo := renderZapBolt(x.shineFrame)
	title := logoStyle.Render("WhatZap")
	subtitle := mutedStyle.Render("Private WhatsApp in your terminal")
	hint := mutedStyle.Render("Keep this window open  •  graphics: " + x.gfxName())
	statusMsgTemplate := func(body, progress string) string {
		header := lipgloss.JoinVertical(lipgloss.Center, logo, "", title, subtitle)
		content := header + "\n\n" + body
		if progress != "" {
			content += "\n" + progress
		}
		return content + "\n\n" + hint
	}
	if x.status == "qr" {
		if x.qrRaw != "" {
			qrMaxW := min(max(12, innerW-6), 48)
			qrMaxH := min(max(8, innerH-6), 24)
			qrBody := renderQR(x.qrRaw, qrMaxW, qrMaxH)
			if qrBody == "" {
				qrBody = x.qrRaw
			}
			qrBoxed := renderViewfinder(qrBody, 6, 3, brand, qrDark)
			qrW := lipgloss.Width(qrBoxed)

			var body string
			if innerW >= linkPanelW+qrW+4 {
				spacerW := max(2, min(8, innerW-(linkPanelW+qrW)-2))
				panel := x.renderLinkPanel(lipgloss.Height(qrBoxed))
				row := lipgloss.JoinHorizontal(lipgloss.Center, panel, strings.Repeat(" ", spacerW), qrBoxed)
				body = lipgloss.PlaceHorizontal(innerW, lipgloss.Center, row)
			} else {
				body = lipgloss.JoinVertical(
					lipgloss.Center,
					lipgloss.PlaceHorizontal(innerW, lipgloss.Center, qrBoxed),
					"",
					lipgloss.PlaceHorizontal(innerW, lipgloss.Center, x.renderLinkPanel(0)),
				)
			}
			return renderStatusBox(body, innerW, innerH, outerW, outerH)
		}
		statusBody = "Generating QR..."
		hint = mutedStyle.Render("Preparing login QR")
	} else if strings.HasPrefix(strings.ToLower(x.status), "logged out") {
		statusBody = accentStyle.Copy().Bold(false).Render(x.status)
		hint = mutedStyle.Render("Restart and scan a QR code to sign in again")
	} else if strings.HasPrefix(x.status, "Error:") {
		errMsg := strings.TrimPrefix(x.status, "Error:")
		errMsg = strings.TrimSpace(errMsg)
		statusBody = lipgloss.NewStyle().Foreground(red).Bold(true).Render("Error: " + errMsg)
		hint = mutedStyle.Render("Press ctrl+c to exit, then re-run whatzap")
	} else {
		return x.renderSignedInSplash(innerW, innerH, outerW, outerH)
	}
	msg := statusMsgTemplate(statusBody, "")
	return renderStatusBox(msg, innerW, innerH, outerW, outerH)
}

func renderViewfinder(content string, cornerLen, armHeight int, frameColor, bgColor lipgloss.Color) string {
	bSt := lipgloss.NewStyle().Foreground(frameColor).Bold(true).Background(bgColor)
	bgSt := lipgloss.NewStyle().Background(bgColor)

	lines := strings.Split(content, "\n")
	contentH := len(lines)
	contentW := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > contentW {
			contentW = w
		}
	}

	// Brackets are one QR module thick (▀ = half a cell tall, █ = one cell
	// wide) and every gap is one module: the lower half of the top row, one
	// pad column per side, and at the bottom the blank lower half the QR's
	// odd module count always leaves in its last row. The top vertical arm
	// gets one row fewer because its corner cell already spans a full row.
	const hPad = 1
	const vPad = 0

	totalW := contentW + 2*hPad + 2
	innerW := totalW - 2
	totalH := contentH + 2*vPad

	midSpace := max(0, totalW-2*cornerLen)
	topRow := bSt.Render("█"+strings.Repeat("▀", cornerLen-1)) + bgSt.Render(strings.Repeat(" ", midSpace)) + bSt.Render(strings.Repeat("▀", cornerLen-1)+"█")
	botRow := bSt.Render(strings.Repeat("▀", cornerLen)) + bgSt.Render(strings.Repeat(" ", midSpace)) + bSt.Render(strings.Repeat("▀", cornerLen))

	out := []string{topRow}

	for y := 0; y < totalH; y++ {
		var leftBorder, rightBorder string
		if y < armHeight-1 || y >= totalH-armHeight {
			leftBorder = bSt.Render("█")
			rightBorder = bSt.Render("█")
		} else {
			leftBorder = bgSt.Render(" ")
			rightBorder = bgSt.Render(" ")
		}

		rowContent := ""
		contentIdx := y - vPad
		if contentIdx >= 0 && contentIdx < contentH {
			rowContent = lines[contentIdx]
		}

		rowW := lipgloss.Width(rowContent)
		padL := max(0, (innerW-rowW)/2)
		padR := max(0, innerW-rowW-padL)

		rowStr := leftBorder + bgSt.Render(strings.Repeat(" ", padL)) + rowContent + bgSt.Render(strings.Repeat(" ", padR)) + rightBorder
		out = append(out, rowStr)
	}

	out = append(out, botRow)
	return strings.Join(out, "\n")
}

func (x m) loadingPulse() string {
	steps := []string{
		"●○○",
		"○●○",
		"○○●",
	}
	idx := x.spinnerFrame % len(steps)
	return accentStyle.Copy().Bold(false).Render(steps[idx])
}

// bootStage is one row of the startup checklist. state is one of
// "done", "active", "pending".
type bootStage struct {
	label string
	state string
}

// bootStageHold is how long each startup step stays on screen before the
// next one can light up. The spinner itself keeps ticking fast.
const bootStageHold = time.Second

// stageTimeAllow returns how many startup stages time has unlocked so far
// (0..4). Zero bootAt (tests) means no gating.
func (x m) stageTimeAllow() int {
	if x.bootAt.IsZero() {
		return 1 << 30
	}
	hold := splashStageHoldDuration()
	if hold <= 0 {
		return 1 << 30
	}
	n := int(time.Since(x.bootAt) / hold)
	if n < 0 {
		n = 0
	}
	if n > 4 {
		n = 4
	}
	return n
}

var bootLevel = map[string]int{"pending": 0, "active": 1, "done": 2}

func bootState(level int) string {
	switch level {
	case 2:
		return "done"
	case 1:
		return "active"
	default:
		return "pending"
	}
}

// pluralSuffix returns "" for 1, "s" otherwise.
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// fitText cuts s to w runes with an ellipsis if it is longer.
func fitText(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:max(1, w)])
	}
	return string(r[:w-1]) + "…"
}

// loadingStages derives the startup checklist from already-available state:
// backend liveness from status, session from status, chats/contacts from
// the prefetched lists (populated before `ready`). Pure for testability.
func (x m) loadingStages() []bootStage {
	backendDone := x.status != "" && x.status != "Starting backend..." && x.status != "Starting demo..."
	sessionDone := x.status == "ready" || x.sessionReady
	chatsDone := len(x.chats) > 0 || x.chatsLoaded
	contactsDone := len(x.contacts) > 0 || x.contactsLoaded

	sessionState := "pending"
	if sessionDone {
		sessionState = "done"
	} else if backendDone {
		sessionState = "active"
	}
	chats := bootStage{label: "Chats", state: "pending"}
	if chatsDone {
		chats.state = "done"
	} else if backendDone {
		chats.state = "active"
	}
	contacts := bootStage{label: "Contacts", state: "pending"}
	if contactsDone {
		contacts.state = "done"
	} else if backendDone {
		contacts.state = "active"
	}
	backend := bootStage{label: "Backend", state: "pending"}
	if backendDone {
		backend.state = "done"
	}
	stages := []bootStage{backend, {label: "Authentication", state: sessionState}, chats, contacts}
	// Hold each stage ~1s so fast loads stay readable. Stage i can show
	// at most level (allow - i + 1): pending, then active, then done.
	allow := x.stageTimeAllow()
	for i := range stages {
		maxLvl := allow - i + 1
		if maxLvl < 0 {
			maxLvl = 0
		}
		if maxLvl > 2 {
			maxLvl = 2
		}
		if bootLevel[stages[i].state] > maxLvl {
			stages[i].state = bootState(maxLvl)
		}
	}
	return stages
}

// renderBootStages renders the startup checklist as a connected pipeline.
func (x m) renderBootStages() string {
	stages := x.loadingStages()
	var rows []string
	for i, s := range stages {
		isLast := i == len(stages)-1
		branch := "├─ "
		if isLast {
			branch = "└─ "
		}
		branchStr := lipgloss.NewStyle().Foreground(v2Color(textFaint, muted)).Render(branch)

		var icon string
		switch s.state {
		case "done":
			icon = lipgloss.NewStyle().Foreground(v2Color(statusSuccess, accent)).Bold(true).Render("✓")
		case "active":
			icon = lipgloss.NewStyle().Foreground(v2Color(statusInfo, brand)).Bold(true).Render(spinnerFrames[x.spinnerFrame])
		default:
			icon = lipgloss.NewStyle().Foreground(v2Color(textFaint, muted)).Render("·")
		}
		rows = append(rows, branchStr+icon)
	}
	return strings.Join(rows, "\n")
}

// splashPalette holds every colour the signed-in splash draws. Legacy
// themes keep the splash's original fixed palette; V2 themes map each
// slot to a role token (docs/themes.md §5 Splash).
type splashPalette struct {
	tilde, name, ver, sep, pipe, footer, link          lipgloss.Color
	border, dot1, dot2, dot3, title                    lipgloss.Color
	railDone, railActive, railDim                      lipgloss.Color
	lblDone, lblActive, lblDim                         lipgloss.Color
	badgeDoneBg, badgeDoneFg, badgeActiveBg            lipgloss.Color
	badgeActiveFg, badgeDimBg, badgeDimFg, leader      lipgloss.Color
	keyEnterBg, keyEnterFg, keyBg, keyFg, keyLbl, hint lipgloss.Color
	keyEnterLbl                                        lipgloss.Color
}

func currentSplashPalette() splashPalette {
	if !themeV2 {
		return splashPalette{
			tilde: "#38bdf8", name: "#25D366", ver: "#64748b", sep: "#1e293b",
			pipe: "#475569", footer: "#64748b", link: "#475569",
			border: "#1e3a5f", dot1: "#f472b6", dot2: "#c084fc", dot3: "#38bdf8", title: "#38bdf8",
			railDone: "#10b981", railActive: "#f59e0b", railDim: "#334155",
			lblDone: "#f8fafc", lblActive: "#fef08a", lblDim: "#64748b",
			badgeDoneBg: "#064e3b", badgeDoneFg: "#34d399", badgeActiveBg: "#78350f",
			badgeActiveFg: "#fbbf24", badgeDimBg: "#1e293b", badgeDimFg: "#64748b", leader: "#1e293b",
			keyEnterBg: "#065f46", keyEnterFg: "#34d399", keyBg: "#1e293b", keyFg: "#38bdf8",
			keyLbl: "#94a3b8", hint: "#475569", keyEnterLbl: "#f1f5f9",
		}
	}
	tint := func(c lipgloss.Color) lipgloss.Color {
		return lipgloss.Color(blendHex(string(c), string(background), 0.75))
	}
	return splashPalette{
		tilde: accent, name: brand, ver: muted, sep: borderSubtle,
		pipe: borderSubtle, footer: muted, link: textFaint,
		border: borderSubtle, dot1: brand, dot2: purple, dot3: accent, title: accent,
		railDone: statusSuccess, railActive: statusInfo, railDim: textFaint,
		lblDone: text, lblActive: statusInfo, lblDim: muted,
		badgeDoneBg: tint(statusSuccess), badgeDoneFg: statusSuccess, badgeActiveBg: tint(statusInfo),
		badgeActiveFg: statusInfo, badgeDimBg: bgPanel, badgeDimFg: muted, leader: textFaint,
		keyEnterBg: tint(statusSuccess), keyEnterFg: statusSuccess, keyBg: bgPanel, keyFg: accent,
		keyLbl: textSecondary, hint: muted, keyEnterLbl: text,
	}
}

func (x m) renderSignedInSplash(innerW, innerH, outerW, outerH int) string {
	stages := x.loadingStages()
	sp := currentSplashPalette()

	doneCount := 0
	activeIdx := -1
	for i, s := range stages {
		if s.state == "done" {
			doneCount++
		} else if s.state == "active" && activeIdx == -1 {
			activeIdx = i
		}
	}
	percent := (doneCount * 100) / len(stages)
	if activeIdx != -1 && percent < 95 {
		percent += 12
	}
	if percent > 100 {
		percent = 100
	}

	// 1. Top and Bottom Screen Bars & Height Budget
	barW := innerW
	topTilde := lipgloss.NewStyle().Foreground(sp.tilde).Render("~")
	topName := lipgloss.NewStyle().Bold(true).Foreground(sp.name).Render("WhatZap")
	topVer := lipgloss.NewStyle().Foreground(sp.ver).Render("v0.1.0")
	leftTopPlain := "~ WhatZap v0.1.0"
	leftTopStyled := topTilde + " " + topName + " " + topVer

	rightTopText := "A private WhatsApp client for your terminal"
	rightTopStyled := lipgloss.NewStyle().Foreground(sp.footer).Render(rightTopText)

	var topBarBlock string
	sepCol := sp.sep
	sepLine := lipgloss.NewStyle().Foreground(sepCol).Render(strings.Repeat("─", barW))

	if barW >= len(leftTopPlain)+len(rightTopText)+4 {
		spTop := barW - len(leftTopPlain) - len(rightTopText) - 2
		topTextLine := " " + leftTopStyled + strings.Repeat(" ", spTop) + rightTopStyled + " "
		if outerH >= 28 {
			topBarBlock = topTextLine + "\n" + sepLine
		} else {
			topBarBlock = topTextLine
		}
	} else if barW >= len(leftTopPlain)+4 {
		topTextLine := " " + leftTopStyled
		if outerH >= 28 {
			topBarBlock = topTextLine + "\n" + sepLine
		} else {
			topBarBlock = topTextLine
		}
	}

	botName := lipgloss.NewStyle().Bold(true).Foreground(sp.name).Render("WhatZap")
	botPipe := lipgloss.NewStyle().Foreground(sp.pipe).Render("│")
	botTag := lipgloss.NewStyle().Foreground(sp.footer).Render("Terminal. Private. Yours.")
	leftBotPlain := "WhatZap │ Terminal. Private. Yours."
	leftBotStyled := botName + "  " + botPipe + "  " + botTag

	rightBotText := "https://github.com/whatzap"
	rightBotStyled := lipgloss.NewStyle().Foreground(sp.link).Render(rightBotText)

	var botBarBlock string
	if barW >= len(leftBotPlain)+len(rightBotText)+4 {
		spBot := barW - len(leftBotPlain) - len(rightBotText) - 2
		botTextLine := " " + leftBotStyled + strings.Repeat(" ", spBot) + rightBotStyled + " "
		if outerH >= 28 {
			botBarBlock = sepLine + "\n" + botTextLine
		} else {
			botBarBlock = botTextLine
		}
	} else if barW >= len(leftBotPlain)+4 {
		botTextLine := " " + leftBotStyled
		if outerH >= 28 {
			botBarBlock = sepLine + "\n" + botTextLine
		} else {
			botBarBlock = botTextLine
		}
	}

	// Keep the splash edge-to-edge clean: no top or bottom page bars.
	topBarBlock = ""
	botBarBlock = ""

	centerH := innerH
	if outerH >= 22 {
		if topBarBlock != "" {
			centerH -= strings.Count(topBarBlock, "\n") + 1
		}
		if botBarBlock != "" {
			centerH -= strings.Count(botBarBlock, "\n") + 1
		}
	} else {
		topBarBlock = ""
		botBarBlock = ""
	}
	centerH = max(1, centerH)

	// 2. Logo, Title, Subtitle
	logo := renderZapBolt(x.shineFrame)
	var title string
	if centerH >= 18 {
		title = renderPixelWordmark()
	} else {
		titleWhat := lipgloss.NewStyle().Bold(true).Foreground(text).Render("What")
		titleZap := lipgloss.NewStyle().Bold(true).Foreground(brand).Render("Zap")
		title = titleWhat + titleZap
	}
	// 2. Stage section dimensions. Keep this compact now that stages
	// are vertical, then center the whole section under the title.
	cardW := min(58, max(50, innerW-8))
	if innerW < 50 {
		cardW = max(24, innerW-2)
	}
	innerBoxW := cardW
	borderCol := sp.border
	borderSt := lipgloss.NewStyle().Foreground(borderCol)

	cardRow := func(content string) string {
		visW := lipgloss.Width(content)
		pad := max(0, innerBoxW-visW)
		return content + strings.Repeat(" ", pad)
	}

	// 3. Telemetry card header and footer
	dotPink := lipgloss.NewStyle().Foreground(sp.dot1).Render("●")
	dotPurple := lipgloss.NewStyle().Foreground(sp.dot2).Render("●")
	dotCyan := lipgloss.NewStyle().Foreground(sp.dot3).Render("●")
	dots := dotPink + " " + dotPurple + " " + dotCyan
	headerTitle := lipgloss.NewStyle().Bold(true).Foreground(sp.title).Render("CONNECTING TO WHATSAPP")
	verTag := lipgloss.NewStyle().Foreground(sp.ver).Render("v0.1.0")
	fixedW := 45
	availDashes := max(2, cardW-fixedW)
	leftDashes := availDashes / 2
	rightDashes := availDashes - leftDashes
	topBorder := borderSt.Render("╭──") + " " + dots + " " +
		borderSt.Render(strings.Repeat("─", leftDashes)+" ") +
		headerTitle + " " +
		borderSt.Render(strings.Repeat("─", rightDashes)+" ") +
		verTag + " " +
		borderSt.Render("──╮")
	botBorder := borderSt.Render("╰" + strings.Repeat("─", cardW-2) + "╯")

	// 4. Vertical branched telemetry rows
	railDone := lipgloss.NewStyle().Foreground(sp.railDone)
	railActive := lipgloss.NewStyle().Foreground(sp.railActive)
	railDim := lipgloss.NewStyle().Foreground(sp.railDim)

	lblDone := lipgloss.NewStyle().Bold(true).Foreground(sp.lblDone)
	lblActive := lipgloss.NewStyle().Bold(true).Foreground(sp.lblActive)
	lblDim := lipgloss.NewStyle().Foreground(sp.lblDim)

	badgeDone := lipgloss.NewStyle().Background(sp.badgeDoneBg).Foreground(sp.badgeDoneFg).Bold(true)
	badgeActive := lipgloss.NewStyle().Background(sp.badgeActiveBg).Foreground(sp.badgeActiveFg).Bold(true)
	badgeDim := lipgloss.NewStyle().Background(sp.badgeDimBg).Foreground(sp.badgeDimFg)

	leaderStyle := lipgloss.NewStyle().Foreground(sp.leader)

	gated := x.loadingStages()
	labels := []string{"backend", "authentication", "chats", "contacts"}
	chatDoneTxt := fmt.Sprintf("%d loaded", len(x.chats))
	contactDoneTxt := fmt.Sprintf("%d synced", len(x.contacts))
	activeStatuses := []string{"starting...", "authenticating...", "loading...", "loading..."}
	doneStatuses := []string{"running", "authenticated", chatDoneTxt, contactDoneTxt}

	stageInnerW := cardW - 2
	var stageRows []string
	for i, s := range gated {
		branch := "├──●  "
		if i == len(gated)-1 {
			if s.state == "done" || s.state == "active" {
				branch = "└──●  "
			} else {
				branch = "└──○  "
			}
		} else if s.state == "waiting" {
			branch = "├──○  "
		}

		lblText := fmt.Sprintf("%-14s", labels[i])

		var branchStr, labelStr, badgeStr string
		switch s.state {
		case "done":
			branchStr = railDone.Render(branch)
			labelStr = lblDone.Render(lblText)
			badgeStr = badgeDone.Render(" ✓ " + doneStatuses[i] + " ")
		case "active":
			branchStr = railActive.Render(branch)
			labelStr = lblActive.Render(lblText)
			status := activeStatuses[i]
			brailleSpin := nodeFrames[x.spinnerFrame%len(nodeFrames)]
			badgeStr = badgeActive.Render(" " + brailleSpin + " " + status + " ")
		default:
			branchStr = railDim.Render(branch)
			labelStr = lblDim.Render(lblText)
			badgeStr = badgeDim.Render(" · waiting ")
		}

		contentLeft := "  " + branchStr + labelStr + " "
		contentRight := " " + badgeStr + "  "
		usedW := lipgloss.Width(contentLeft) + lipgloss.Width(contentRight)

		leaderW := max(2, stageInnerW-usedW)
		leaderStr := leaderStyle.Render(strings.Repeat("·", leaderW))

		middle := contentLeft + leaderStr + contentRight
		pad := max(0, stageInnerW-lipgloss.Width(middle))

		fullRow := borderSt.Render("│") + middle + strings.Repeat(" ", pad) + borderSt.Render("│")
		stageRows = append(stageRows, cardRow(fullRow))
	}

	blankRow := cardRow(borderSt.Render("│") + strings.Repeat(" ", cardW-2) + borderSt.Render("│"))
	var cardRows []string
	cardRows = append(cardRows,
		topBorder,
		blankRow,
	)
	cardRows = append(cardRows, stageRows...)
	cardRows = append(cardRows,
		blankRow,
		botBorder,
	)
	cardBox := strings.Join(cardRows, "\n")

	// 8. Command Action Bar - Physical Keycap Styling
	keycapEnter := lipgloss.NewStyle().Background(sp.keyEnterBg).Foreground(sp.keyEnterFg).Bold(true).Render(" [ENTER] ")
	lblEnter := lipgloss.NewStyle().Foreground(sp.keyEnterLbl).Render(" Open client")
	btnEnter := keycapEnter + lblEnter

	keycapQ := lipgloss.NewStyle().Background(sp.keyBg).Foreground(sp.keyFg).Bold(true).Render(" [Q] ")
	lblQ := lipgloss.NewStyle().Foreground(sp.keyLbl).Render(" Quit")
	btnQ := keycapQ + lblQ

	keycapR := lipgloss.NewStyle().Background(sp.keyBg).Foreground(sp.keyFg).Bold(true).Render(" [R] ")
	lblR := lipgloss.NewStyle().Foreground(sp.keyLbl).Render(" Reconnect")
	btnR := keycapR + lblR
	sep := borderSt.Render("│")

	cmdContent := btnEnter + "   " + sep + "   " + btnQ + "   " + sep + "   " + btnR
	cmdBox := cmdContent

	// 9. Hint at Bottom
	hint := lipgloss.NewStyle().Foreground(sp.hint).Render("Press any key to continue")

	// 11. Assemble Center Content
	var allSections []string
	if centerH >= 26 {
		allSections = []string{
			logo,
			"",
			title,
			"",
			cardBox,
			cmdBox,
			"",
			hint,
		}
	} else if centerH >= 20 {
		allSections = []string{
			logo,
			title,
			cardBox,
			cmdContent,
			hint,
		}
	} else {
		allSections = []string{
			title,
			cardBox,
			hint,
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Center, allSections...)
	centerContent := lipgloss.Place(innerW, centerH, lipgloss.Center, lipgloss.Center, body)

	var fullRows []string
	if topBarBlock != "" {
		fullRows = append(fullRows, topBarBlock)
	}
	fullRows = append(fullRows, centerContent)
	if botBarBlock != "" {
		fullRows = append(fullRows, botBarBlock)
	}

	fullBody := strings.Join(fullRows, "\n")
	return renderStatusBox(fullBody, innerW, innerH, outerW, outerH)
}
