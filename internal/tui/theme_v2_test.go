package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Tests for the V2 role-token theme system (docs/themes.md). The rule
// tests run against every V2 theme; today that is Tokyo Night only.

func v2Themes() []struct {
	name  string
	theme Theme
} {
	var out []struct {
		name  string
		theme Theme
	}
	for _, t := range themeList {
		if t.theme.V2 {
			out = append(out, struct {
				name  string
				theme Theme
			}{t.name, normalizeTheme(t.theme)})
		}
	}
	return out
}

// hsl returns HSL hue (degrees) and saturation (0–1) of a "#RRGGBB" colour.
func hsl(hex string) (hue, sat float64) {
	r, g, b, ok := hexChannels(hex)
	if !ok {
		return 0, 0
	}
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (mx + mn) / 2
	d := mx - mn
	if d == 0 {
		return 0, 0
	}
	if l > 0.5 {
		sat = d / (2 - mx - mn)
	} else {
		sat = d / (mx + mn)
	}
	switch mx {
	case r:
		hue = math.Mod((g-b)/d, 6)
	case g:
		hue = (b-r)/d + 2
	default:
		hue = (r-g)/d + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	return hue, sat
}

func hueDiff(a, b string) float64 {
	ha, _ := hsl(a)
	hb, _ := hsl(b)
	d := math.Abs(ha - hb)
	return math.Min(d, 360-d)
}

// confusable follows docs/themes.md §9: hue < 20° apart and luminance
// ratio < 1.4. Neutral colours (saturation < 0.15) compare by luminance.
func confusable(a, b string) bool {
	_, sa := hsl(a)
	_, sb := hsl(b)
	lum := contrastRatio(a, b)
	if sa < 0.15 || sb < 0.15 {
		return lum < 1.4
	}
	return hueDiff(a, b) < 20 && lum < 1.4
}

func inHueRange(h, from, to float64) bool {
	if from <= to {
		return h >= from && h <= to
	}
	return h >= from || h <= to // wraps past 360
}

func TestV2ThemesExist(t *testing.T) {
	if len(v2Themes()) == 0 {
		t.Fatal("expected at least one V2 theme (Tokyo Night)")
	}
}

func TestThemeContrast(t *testing.T) {
	for _, tt := range v2Themes() {
		th := tt.theme
		check := func(fgName, fg, bgName, bg string, min float64) {
			t.Helper()
			if got := contrastRatio(fg, bg); got < min {
				t.Errorf("%s: %s on %s = %.2f, need ≥ %.1f (§8.1)", tt.name, fgName, bgName, got, min)
			}
		}
		surfaces := []struct{ name, hex string }{
			{"BgApp", th.BgApp}, {"BgSidebar", th.BgSidebar}, {"BgPanel", th.BgPanel},
			{"BgSelected", th.BgSelected}, {"BgActive", th.BgActive},
		}
		body := map[string]string{"TextPrimary": th.TextPrimary, "ChatSentText": th.ChatSentText, "ChatReceivedText": th.ChatReceivedText}
		for n, c := range body {
			for _, s := range surfaces {
				min := 7.0
				if s.name == "BgSelected" || s.name == "BgActive" {
					min = 4.5
				}
				check(n, c, s.name, s.hex, min)
			}
		}
		for _, s := range surfaces {
			min := 7.0
			if s.name != "BgApp" && s.name != "BgSidebar" {
				min = 4.5
			}
			check("TextSecondary", th.TextSecondary, s.name, s.hex, min)
		}
		voices := map[string]string{
			"Brand": th.Brand, "Action": th.Action, "Emphasis": th.Emphasis,
			"StatusSuccess": th.StatusSuccess, "StatusWarning": th.StatusWarning, "StatusDanger": th.StatusDanger,
			"ChatSentName": th.ChatSentName, "ChatReceivedName": th.ChatReceivedName,
		}
		setTestTheme(t, th)
		for i, c := range senderPalette {
			voices[fmt.Sprintf("ChatSenderPalette[%d]", i)] = string(c)
		}
		for n, c := range voices {
			for _, s := range surfaces {
				min := 4.5
				switch s.name {
				case "BgSelected":
					if n != "Action" && n != "StatusDanger" {
						min = 3.0
					}
				case "BgActive":
					min = 3.0
				}
				check(n, c, s.name, s.hex, min)
			}
		}
		for _, s := range []struct {
			name, hex string
			min       float64
		}{{"BgApp", th.BgApp, 4.5}, {"BgSidebar", th.BgSidebar, 4.5}, {"BgPanel", th.BgPanel, 4.0}, {"BgSelected", th.BgSelected, 3.0}, {"BgActive", th.BgActive, 3.0}} {
			check("TextMuted", th.TextMuted, s.name, s.hex, s.min)
		}
		if got := contrastRatio(th.TextFaint, th.BgApp); got < 2.0 || got > 3.0 {
			t.Errorf("%s: TextFaint on BgApp = %.2f, want 2.0–3.0 (§8.1)", tt.name, got)
		}
		check("ChatQuotedSent", th.ChatQuotedSent, "BgApp", th.BgApp, 4.5)
		check("ChatQuotedReceived", th.ChatQuotedReceived, "BgApp", th.BgApp, 4.5)
		check("ChatQuotedSent", th.ChatQuotedSent, "BgReply", th.BgReply, 4.5)
		check("ChatQuotedReceived", th.ChatQuotedReceived, "BgReply", th.BgReply, 4.5)
		check("TextPrimary", th.TextPrimary, "BgReply", th.BgReply, 4.5)

		fills := map[string]string{
			"Brand": th.Brand, "Action": th.Action, "Emphasis": th.Emphasis, "StatusDanger": th.StatusDanger,
			"TagImage": th.TagImage, "TagVideo": th.TagVideo, "TagAudio": th.TagAudio, "TagFile": th.TagFile,
			"TagSticker": th.TagSticker, "TagContact": th.TagContact, "TagPoll": th.TagPoll,
			"TagLocation": th.TagLocation, "TagSystem": th.TagSystem,
		}
		for n, f := range fills {
			check("TextOnFill", th.TextOnFill, n, f, 4.5)
		}
		for _, st := range []string{th.StatusSuccess, th.StatusInfo} {
			check("stage text", st, "stage badge", blendHex(st, th.BgApp, 0.75), 4.5)
		}
	}
}

func TestThemeLadder(t *testing.T) {
	for _, tt := range v2Themes() {
		th := tt.theme
		between := func(name, a, b string, lo, hi float64) {
			t.Helper()
			if got := contrastRatio(a, b); got < lo || got > hi {
				t.Errorf("%s: %s = %.2f, want %.2f–%.2f (§8.2)", tt.name, name, got, lo, hi)
			}
		}
		between("BgSidebar vs BgApp", th.BgSidebar, th.BgApp, 1.04, 1.20)
		between("BgPanel vs BgApp", th.BgPanel, th.BgApp, 1.08, 1.35)
		between("BgSelected vs BgApp", th.BgSelected, th.BgApp, 1.35, 21)
		between("BgSelected vs BgSidebar", th.BgSelected, th.BgSidebar, 1.35, 21)
		between("BgSelected vs BgPanel", th.BgSelected, th.BgPanel, 1.20, 21)
		between("BgSelected vs BgActive", th.BgSelected, th.BgActive, 1.15, 21)
		between("BgActive vs BgSidebar", th.BgActive, th.BgSidebar, 1.12, 21)
		between("BorderSubtle vs BgApp", th.BorderSubtle, th.BgApp, 1.5, 2.5)
		between("BorderFocus vs BgApp", th.BorderFocus, th.BgApp, 3.0, 21)

		order := []struct{ name, hex string }{
			{"TextPrimary", th.TextPrimary}, {"TextSecondary", th.TextSecondary},
			{"TextMuted", th.TextMuted}, {"TextFaint", th.TextFaint}, {"BorderSubtle", th.BorderSubtle},
		}
		for i := 1; i < len(order); i++ {
			if contrastRatio(order[i-1].hex, th.BgApp) <= contrastRatio(order[i].hex, th.BgApp) {
				t.Errorf("%s: %s must out-contrast %s on BgApp (§8.3)", tt.name, order[i-1].name, order[i].name)
			}
		}

		// 256-colour terminals: App, Panel and Selected must stay distinct
		// after quantizing (BgSidebar may merge with BgApp; borders cover it).
		q := func(hex string) string { return fmt.Sprint(termenv.ANSI256.Convert(termenv.RGBColor(hex))) }
		app, panel, sel := q(th.BgApp), q(th.BgPanel), q(th.BgSelected)
		if app == panel || app == sel || panel == sel {
			t.Errorf("%s: 256-colour ladder collapses: app=%s panel=%s selected=%s (§12)", tt.name, app, panel, sel)
		}
	}
}

func TestThemeSeparation(t *testing.T) {
	for _, tt := range v2Themes() {
		th := tt.theme
		if d := hueDiff(th.Brand, th.Emphasis); d < 90 {
			t.Errorf("%s: identity pair Brand/Emphasis only %.0f° apart, need ≥ 90° (§9.1)", tt.name, d)
		}
		for n, c := range map[string]string{"Brand": th.Brand, "Emphasis": th.Emphasis} {
			if confusable(c, th.StatusDanger) {
				t.Errorf("%s: %s is confusable with StatusDanger (§9.1)", tt.name, n)
			}
		}
		core := []struct{ name, hex string }{
			{"Brand", th.Brand}, {"Action", th.Action}, {"Emphasis", th.Emphasis},
			{"StatusWarning", th.StatusWarning}, {"StatusDanger", th.StatusDanger},
		}
		for i := range core {
			for j := i + 1; j < len(core); j++ {
				if confusable(core[i].hex, core[j].hex) {
					t.Errorf("%s: %s and %s are confusable (§9.2)", tt.name, core[i].name, core[j].name)
				}
			}
		}
		tags := []struct{ name, hex string }{
			{"TagImage", th.TagImage}, {"TagVideo", th.TagVideo}, {"TagAudio", th.TagAudio},
			{"TagFile", th.TagFile}, {"TagSticker", th.TagSticker}, {"TagContact", th.TagContact},
			{"TagPoll", th.TagPoll}, {"TagLocation", th.TagLocation},
		}
		for i := range tags {
			if confusable(tags[i].hex, th.StatusDanger) {
				t.Errorf("%s: %s is confusable with StatusDanger (§9.2)", tt.name, tags[i].name)
			}
			for j := i + 1; j < len(tags); j++ {
				if confusable(tags[i].hex, tags[j].hex) {
					t.Errorf("%s: %s and %s are confusable (§9.2)", tt.name, tags[i].name, tags[j].name)
				}
			}
		}
		setTestTheme(t, th)
		for i, a := range senderPalette {
			if string(a) == th.Brand {
				t.Errorf("%s: ChatSenderPalette[%d] equals Brand (§9.2)", tt.name, i)
			}
			if confusable(string(a), th.StatusDanger) {
				t.Errorf("%s: ChatSenderPalette[%d] confusable with StatusDanger (§9.2)", tt.name, i)
			}
			for j := i + 1; j < len(senderPalette); j++ {
				if confusable(string(a), string(senderPalette[j])) {
					t.Errorf("%s: ChatSenderPalette[%d] and [%d] are confusable (§9.2)", tt.name, i, j)
				}
			}
		}
		if d := hueDiff(th.ChatSentText, th.ChatReceivedText); d < 60 {
			t.Errorf("%s: sent/received text only %.0f° apart, need ≥ 60° (§9.2)", tt.name, d)
		}
	}
}

func TestThemeMeaning(t *testing.T) {
	for _, tt := range v2Themes() {
		th := tt.theme
		rng := func(name, hex string, from, to, minSat, maxSat float64) {
			t.Helper()
			h, s := hsl(hex)
			if from != to && !inHueRange(h, from, to) {
				t.Errorf("%s: %s hue %.0f° outside %.0f°–%.0f° (§9.3)", tt.name, name, h, from, to)
			}
			if s < minSat || s > maxSat {
				t.Errorf("%s: %s saturation %.2f outside %.2f–%.2f (§9.3)", tt.name, name, s, minSat, maxSat)
			}
		}
		rng("StatusDanger", th.StatusDanger, 335, 25, 0.45, 1)
		rng("StatusSuccess", th.StatusSuccess, 70, 170, 0.30, 1)
		rng("StatusWarning", th.StatusWarning, 25, 60, 0.40, 1)
		rng("TagSystem", th.TagSystem, 0, 0, 0, 0.30)
		rng("ChatSentText", th.ChatSentText, 0, 0, 0, 0.70)
		rng("ChatReceivedText", th.ChatReceivedText, 0, 0, 0, 0.70)
	}
}

// Unmigrated themes must render exactly as before: normalizing leaves
// their legacy fields alone and fills the tokens with the old values.
func TestNormalizeLegacyThemesUnchanged(t *testing.T) {
	for _, tt := range themeList {
		if tt.theme.V2 {
			continue
		}
		got := normalizeTheme(tt.theme)
		if got.Accent != tt.theme.Accent || got.Muted != tt.theme.Muted || got.SidebarActiveBg != tt.theme.SidebarActiveBg ||
			got.Background != tt.theme.Background || got.AnomalyTag != tt.theme.AnomalyTag || got.Cursor != tt.theme.Cursor {
			t.Errorf("%s: normalize changed a legacy field", tt.name)
		}
		want := map[string][2]string{
			"BorderSubtle":  {got.BorderSubtle, tt.theme.Muted},
			"BgPanel":       {got.BgPanel, tt.theme.SidebarActiveBg},
			"BgSelected":    {got.BgSelected, tt.theme.ShortcutActive},
			"Action":        {got.Action, tt.theme.Accent},
			"Emphasis":      {got.Emphasis, tt.theme.Purple},
			"StatusInfo":    {got.StatusInfo, tt.theme.Amber},
			"StatusSuccess": {got.StatusSuccess, "#22c55e"},
			"TextLink":      {got.TextLink, "#89b4fa"},
			"TextMatch":     {got.TextMatch, tt.theme.Accent},
			"FxShine":       {got.FxShine, "#FFFFFF"},
		}
		for k, v := range want {
			if v[0] != v[1] {
				t.Errorf("%s: legacy %s = %q, want %q (must match the old UI)", tt.name, k, v[0], v[1])
			}
		}
		if normalizeTheme(got) != got {
			t.Errorf("%s: normalizeTheme is not idempotent", tt.name)
		}
	}
}

func TestTokyoNightBackfillsLegacyFields(t *testing.T) {
	th := TokyoNight
	if !th.V2 {
		t.Fatal("Tokyo Night should be a V2 theme")
	}
	pairs := map[string][2]string{
		"Background": {th.Background, th.BgApp}, "Text": {th.Text, th.TextPrimary},
		"Muted": {th.Muted, th.TextMuted}, "Accent": {th.Accent, th.Action},
		"Purple": {th.Purple, th.Emphasis}, "Red": {th.Red, th.StatusDanger},
		"AnomalyTag": {th.AnomalyTag, th.TagSystem}, "SidebarActiveBg": {th.SidebarActiveBg, th.BgPanel},
		"ShortcutActive": {th.ShortcutActive, th.BgSelected}, "ButtonInk": {th.ButtonInk, th.TextOnFill},
		"QRLight": {th.QRLight, qrLightFixed}, "QRDark": {th.QRDark, qrDarkFixed},
	}
	for k, v := range pairs {
		if v[0] != v[1] {
			t.Errorf("TokyoNight.%s = %q, want %q", k, v[0], v[1])
		}
	}
	if normalizeTheme(th) != th {
		t.Error("normalizeTheme is not idempotent for Tokyo Night")
	}
}

func v2SidebarModel(whitelisted bool, active bool) m {
	wl := map[string]string{}
	if whitelisted {
		wl["15551230001"] = "Allowed"
	}
	model := m{
		mode:           "nav",
		sidebarFocused: true,
		sel:            0,
		whitelist:      wl,
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
	}
	if active {
		model.mode = "chat"
		model.sidebarFocused = false
		model.sel = 1
		model.active = "15551230001@s.whatsapp.net"
	}
	return model
}

// colourParam probes lipgloss for the SGR parameter it emits for hex
// ("38;2;R;G;B" or "48;2;R;G;B"), so tests match its exact rounding and
// work whether or not the renderer merged it with other attributes.
func colourParam(hex string, bg bool) string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
	lead := "38;2;"
	if bg {
		st = lipgloss.NewStyle().Background(lipgloss.Color(hex))
		lead = "48;2;"
	}
	probe := st.Render("x")
	i := strings.Index(probe, lead)
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(probe[i:], 'm')
	if j < 0 {
		return ""
	}
	return probe[i : i+j]
}

func fgANSI(hex string) string { return colourParam(hex, false) }
func bgANSI(hex string) string { return colourParam(hex, true) }

func TestV2SidebarSelectedRowUsesSelectionStyle(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	for _, wl := range []bool{true, false} {
		lines := v2SidebarModel(wl, false).renderUserList(items, 0, 1, 30)
		row := lines[0]
		if got := lipgloss.Width(row); got != 30 {
			t.Fatalf("row width = %d, want 30", got)
		}
		if !strings.Contains(row, bgANSI(TokyoNight.BgSelected)) {
			t.Errorf("whitelisted=%v: selected row missing BgSelected in %q", wl, row)
		}
		for _, loud := range []string{TokyoNight.StatusDanger, TokyoNight.StatusSuccess, TokyoNight.Brand} {
			if strings.Contains(row, bgANSI(loud)) {
				t.Errorf("whitelisted=%v: selected row must not use a loud %s fill", wl, loud)
			}
		}
		if !strings.Contains(stripAnsi(row), "▌") {
			t.Errorf("whitelisted=%v: selected row missing ▌ marker", wl)
		}
		marker := TokyoNight.TextMuted
		if wl {
			marker = TokyoNight.StatusSuccess
		}
		if !strings.Contains(row, fgANSI(marker)) {
			t.Errorf("whitelisted=%v: prefix marker should be %s in %q", wl, marker, row)
		}
	}
}

func TestV2SidebarActiveRowUsesBgActive(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}
	row := v2SidebarModel(true, true).renderUserList(items, 0, 1, 30)[0]
	if !strings.Contains(row, bgANSI(TokyoNight.BgActive)) {
		t.Errorf("open chat row missing BgActive in %q", row)
	}
	if strings.Contains(stripAnsi(row), "▌") {
		t.Error("open (non-cursor) row must not carry the selection marker")
	}
}

func TestV2SidebarReadRowUsesTextSecondary(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)
	model := v2SidebarModel(true, false)
	model.sel = 5 // no row highlighted
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}
	row := model.renderUserList(items, 0, 1, 30)[0]
	if !strings.Contains(row, fgANSI(TokyoNight.TextSecondary)) {
		t.Errorf("read chat name should use TextSecondary in %q", row)
	}
	if strings.Contains(row, "\x1b[48;2;") {
		t.Errorf("unselected row should have no background, got %q", row)
	}
}

func TestV2HeaderBadgesShowIdentityPair(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)
	head := m{}.renderHeaderContainer(100, 28)
	if !strings.Contains(head, bgANSI(TokyoNight.Brand)) {
		t.Error("MOOD badge should be filled with Brand")
	}
	if !strings.Contains(head, bgANSI(TokyoNight.Emphasis)) {
		t.Error("theme-name badge should be filled with Emphasis")
	}
}

func TestLegacyHeaderBadgesUnchanged(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, Catppuccin)
	head := m{}.renderHeaderContainer(100, 28)
	if !strings.Contains(head, bgANSI(Catppuccin.AnomalyTag)) || !strings.Contains(head, bgANSI(Catppuccin.Accent)) {
		t.Error("legacy header badges should keep AnomalyTag + Accent fills")
	}
}

func TestSettingsOffDotColour(t *testing.T) {
	enableTrueColor(t)
	red := fgANSI("#ef4444")
	render := func() string {
		var p picker
		p.items = buildSettingsPickerItems()
		p.idx = -1
		return p.RenderSettings(120, 60)
	}
	setTestTheme(t, TokyoNight)
	if strings.Contains(render(), red) {
		t.Error("V2: OFF toggles must not be drawn in the old error red")
	}
	setTestTheme(t, Catppuccin)
	if !strings.Contains(render(), red) {
		t.Error("legacy: OFF toggles should keep their red dot")
	}
}

func TestSplashPaletteLegacyUnchangedV2Themed(t *testing.T) {
	setTestTheme(t, Catppuccin)
	legacy := currentSplashPalette()
	if legacy.name != "#25D366" || legacy.border != "#1e3a5f" || legacy.railDone != "#10b981" {
		t.Errorf("legacy splash palette changed: %+v", legacy)
	}
	setTestTheme(t, TokyoNight)
	v2 := currentSplashPalette()
	if string(v2.name) != TokyoNight.Brand || string(v2.border) != TokyoNight.BorderSubtle ||
		string(v2.railDone) != TokyoNight.StatusSuccess || string(v2.railActive) != TokyoNight.StatusInfo {
		t.Errorf("V2 splash palette should use role tokens: %+v", v2)
	}
}

func TestSenderColorStableAndLegacyFallback(t *testing.T) {
	setTestTheme(t, TokyoNight)
	a := senderColor("15551230001@s.whatsapp.net")
	if a != senderColor("15551230001@s.whatsapp.net") {
		t.Error("sender colour must be stable for the same JID")
	}
	found := false
	for _, c := range senderPalette {
		if c == a {
			found = true
		}
	}
	if !found {
		t.Errorf("sender colour %s not from the palette", a)
	}
	setTestTheme(t, Catppuccin)
	if got := senderColor("15551230001@s.whatsapp.net"); got != receivedName {
		t.Errorf("legacy sender colour = %s, want ReceivedName %s", got, receivedName)
	}
}

func TestWithPanelOutline(t *testing.T) {
	box := lipgloss.NewStyle().Width(10).Height(3).Render("x")
	setTestTheme(t, Catppuccin)
	if withPanelOutline(box, 40, 20) != box {
		t.Error("legacy themes keep borderless panels")
	}
	setTestTheme(t, TokyoNight)
	out := withPanelOutline(box, 40, 20)
	if lipgloss.Width(out) != lipgloss.Width(box)+2 || !strings.Contains(out, "╭") {
		t.Error("V2 panels should get a rounded focus outline")
	}
	if withPanelOutline(box, lipgloss.Width(box)+1, 20) != box {
		t.Error("outline must be skipped when it would not fit")
	}
}
