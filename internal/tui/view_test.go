package tui

import (
	"strings"
	"testing"
)

func TestTopBorderJunctionAlignsWithHeaderDivider(t *testing.T) {
	currentConfig.Borderless = false

	for _, w := range []int{70, 80, 100, 120} {
		model := m{
			w:            w,
			h:            30,
			status:       "ready",
			mode:         "chat",
			active:       "12345@s.whatsapp.net",
			mainCache:    &renderCache{},
			sidebarCache: &sidebarCache{},
			chats: []chat{
				{ID: "12345@s.whatsapp.net", Name: "Alice"},
			},
			contacts:      map[string]contact{},
			whitelist:     map[string]string{},
			names:         map[string]string{},
			drafts:        map[string]string{},
			groupPreviews: map[string]groupPreview{},
		}

		view := model.View()
		lines := strings.Split(view, "\n")
		if len(lines) < 2 {
			t.Fatalf("width %d: view had %d lines, want >= 2", w, len(lines))
		}

		topRow := ansiStripRe.ReplaceAllString(lines[0], "")
		headRow := ansiStripRe.ReplaceAllString(lines[1], "")

		topRunes := []rune(topRow)
		headRunes := []rune(headRow)

		topJunctionCol := -1
		for i, r := range topRunes {
			if r == '┬' {
				topJunctionCol = i
				break
			}
		}
		if topJunctionCol == -1 {
			t.Fatalf("width %d: top border row missing '┬' junction: %q", w, topRow)
		}

		// In headRow, col 0 is the left frame border "│".
		// The divider between sidebar and chat is the second "│".
		if len(headRunes) == 0 || headRunes[0] != '│' {
			t.Fatalf("width %d: head row missing left frame border at col 0: %q", w, headRow)
		}
		dividerCol := -1
		for i := 1; i < len(headRunes); i++ {
			if headRunes[i] == '│' {
				dividerCol = i
				break
			}
		}
		if dividerCol == -1 {
			t.Fatalf("width %d: head row missing vertical divider '│': %q", w, headRow)
		}

		if topJunctionCol != dividerCol {
			t.Errorf("width %d: top row '┬' at col %d does not match header divider '│' at col %d",
				w, topJunctionCol, dividerCol)
		}
	}
}

func TestHeaderDividerUsesCrossJunction(t *testing.T) {
	currentConfig.Borderless = false

	for _, w := range []int{70, 80, 100, 120} {
		model := m{
			w:            w,
			h:            30,
			status:       "ready",
			mode:         "chat",
			active:       "12345@s.whatsapp.net",
			mainCache:    &renderCache{},
			sidebarCache: &sidebarCache{},
			chats: []chat{
				{ID: "12345@s.whatsapp.net", Name: "Alice"},
			},
			contacts:      map[string]contact{},
			whitelist:     map[string]string{},
			names:         map[string]string{},
			drafts:        map[string]string{},
			groupPreviews: map[string]groupPreview{},
		}

		view := model.View()
		lines := strings.Split(view, "\n")
		if len(lines) < 3 {
			t.Fatalf("width %d: view had %d lines, want >= 3", w, len(lines))
		}

		headRow := ansiStripRe.ReplaceAllString(lines[1], "")
		sepRow := ansiStripRe.ReplaceAllString(lines[2], "")

		headRunes := []rune(headRow)
		sepRunes := []rune(sepRow)

		// Divider in head row is the second '│'
		dividerCol := -1
		for i := 1; i < len(headRunes); i++ {
			if headRunes[i] == '│' {
				dividerCol = i
				break
			}
		}
		if dividerCol == -1 {
			t.Fatalf("width %d: head row missing vertical divider '│': %q", w, headRow)
		}

		// Cross junction in sepRow
		crossCol := -1
		for i, r := range sepRunes {
			if r == '┼' {
				crossCol = i
				break
			}
		}
		if crossCol == -1 {
			t.Fatalf("width %d: separator row missing '┼' cross junction: %q", w, sepRow)
		}

		if crossCol != dividerCol {
			t.Errorf("width %d: separator '┼' at col %d does not match header divider '│' at col %d",
				w, crossCol, dividerCol)
		}
	}
}

func TestHorizontalDividersConnectToOuterFrame(t *testing.T) {
	currentConfig.Borderless = false
	model := m{
		w:            80,
		h:            25,
		status:       "ready",
		mode:         "chat",
		active:       "12345@s.whatsapp.net",
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
		chats: []chat{
			{ID: "12345@s.whatsapp.net", Name: "Alice"},
		},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
	}
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 7 {
		t.Fatalf("view had %d lines, want >= 7", len(lines))
	}

	// Line 2: Header divider (should be ├ ... ┤)
	l2 := []rune(ansiStripRe.ReplaceAllString(lines[2], ""))
	if l2[0] != '├' {
		t.Errorf("line 2 (header divider) left border = %q, want '├'", string(l2[0]))
	}
	if l2[len(l2)-1] != '┤' {
		t.Errorf("line 2 (header divider) right border = %q, want '┤'", string(l2[len(l2)-1]))
	}

	// Line 4: Tabs divider (should be ├ ... ┤ ... │)
	l4 := []rune(ansiStripRe.ReplaceAllString(lines[4], ""))
	if l4[0] != '├' {
		t.Errorf("line 4 (tabs divider) left border = %q, want '├'", string(l4[0]))
	}
	// The tabs divider meets the sidebar vertical divider with '┤'
	dividerCol := strings.Index(string(l4[1:]), "┤") + 1
	if dividerCol == 0 {
		t.Errorf("line 4 missing '┤' at vertical divider junction: %q", string(l4))
	}

	// Line 6: Search divider (should be ├ ... ┤ ... │)
	l6 := []rune(ansiStripRe.ReplaceAllString(lines[6], ""))
	if l6[0] != '├' {
		t.Errorf("line 6 (search divider) left border = %q, want '├'", string(l6[0]))
	}
	searchDividerCol := strings.Index(string(l6[1:]), "┤") + 1
	if searchDividerCol == 0 {
		t.Errorf("line 6 missing '┤' at vertical divider junction: %q", string(l6))
	}
}

func TestCommandBoxTopBorderUsesCrossJunction(t *testing.T) {
	currentConfig.Borderless = false
	model := m{
		w:            80,
		h:            20,
		status:       "ready",
		mode:         "chat",
		active:       "12345@s.whatsapp.net",
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
		chats: []chat{
			{ID: "12345@s.whatsapp.net", Name: "Alice"},
		},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
	}
	view := model.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 4 {
		t.Fatalf("view had %d lines, want >= 4", len(lines))
	}

	// Line len-3 is the horizontal divider between sidebar/chat and cmdBox/composer
	cmdDividerLine := ansiStripRe.ReplaceAllString(lines[len(lines)-3], "")
	runes := []rune(cmdDividerLine)

	if runes[0] != '├' {
		t.Errorf("command box divider left border = %q, want '├'", string(runes[0]))
	}
	if runes[len(runes)-1] != '┤' {
		t.Errorf("command box divider right border = %q, want '┤'", string(runes[len(runes)-1]))
	}
	if !strings.Contains(cmdDividerLine, "┼") {
		t.Errorf("command box divider missing '┼' cross junction: %q", cmdDividerLine)
	}
}

func TestBottomBorderJunctionAlignsWithHeaderDivider(t *testing.T) {
	currentConfig.Borderless = false

	for _, w := range []int{70, 80, 100, 120} {
		model := m{
			w:            w,
			h:            30,
			status:       "ready",
			mode:         "chat",
			active:       "12345@s.whatsapp.net",
			mainCache:    &renderCache{},
			sidebarCache: &sidebarCache{},
			chats: []chat{
				{ID: "12345@s.whatsapp.net", Name: "Alice"},
			},
			contacts:      map[string]contact{},
			whitelist:     map[string]string{},
			names:         map[string]string{},
			drafts:        map[string]string{},
			groupPreviews: map[string]groupPreview{},
		}

		view := model.View()
		lines := strings.Split(view, "\n")
		if len(lines) < 3 {
			t.Fatalf("width %d: view had %d lines, want >= 3", w, len(lines))
		}

		topRow := ansiStripRe.ReplaceAllString(lines[0], "")
		botRow := ansiStripRe.ReplaceAllString(lines[len(lines)-1], "")

		topRunes := []rune(topRow)
		botRunes := []rune(botRow)

		if botRunes[0] != '╰' {
			t.Errorf("width %d: bot row missing '╰' corner: %q", w, botRow)
		}
		if botRunes[len(botRunes)-1] != '╯' {
			t.Errorf("width %d: bot row missing '╯' corner: %q", w, botRow)
		}

		topCol := -1
		for i, r := range topRunes {
			if r == '┬' {
				topCol = i
				break
			}
		}

		botCol := -1
		for i, r := range botRunes {
			if r == '┴' {
				botCol = i
				break
			}
		}
		if botCol == -1 {
			t.Fatalf("width %d: bot row missing '┴' junction: %q", w, botRow)
		}

		if topCol != botCol {
			t.Errorf("width %d: bot row '┴' at col %d does not match top row '┬' at col %d",
				w, botCol, topCol)
		}
	}
}

func TestInputDividerColorUniformOnFocus(t *testing.T) {
	currentConfig.Borderless = false

	// Test 1: leftInputFocused = true
	mLeft := m{
		w:                80,
		h:                20,
		status:           "ready",
		mode:             "chat",
		active:           "12345@s.whatsapp.net",
		leftInputFocused: true,
		mainCache:        &renderCache{},
		sidebarCache:     &sidebarCache{},
		chats: []chat{
			{ID: "12345@s.whatsapp.net", Name: "Alice"},
		},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
	}
	viewLeft := mLeft.View()
	linesLeft := strings.Split(viewLeft, "\n")
	divLeft := linesLeft[len(linesLeft)-3]
	plainLeft := ansiStripRe.ReplaceAllString(divLeft, "")
	rLeft := []rune(plainLeft)
	if !strings.Contains(plainLeft, "┼") || rLeft[0] != '├' || rLeft[len(rLeft)-1] != '┤' {
		t.Errorf("left focused divider missing proper junctions: %q", plainLeft)
	}
	// Test 2: rightFocused = true
	mRight := m{
		w:                80,
		h:                20,
		status:           "ready",
		mode:             "chat",
		active:           "12345@s.whatsapp.net",
		sidebarFocused:   false,
		leftInputFocused: false,
		mainCache:        &renderCache{},
		sidebarCache:     &sidebarCache{},
		chats: []chat{
			{ID: "12345@s.whatsapp.net", Name: "Alice"},
		},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{"12345": "Alice"},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
	}
	viewRight := mRight.View()
	linesRight := strings.Split(viewRight, "\n")
	divRight := linesRight[len(linesRight)-3]
	plainRight := ansiStripRe.ReplaceAllString(divRight, "")
	rRight := []rune(plainRight)
	if !strings.Contains(plainRight, "┼") || rRight[0] != '├' || rRight[len(rRight)-1] != '┤' {
		t.Errorf("right focused divider missing proper junctions: %q", plainRight)
	}
}

func TestRenderStartupViewStages(t *testing.T) {
	for _, borderless := range []bool{false, true} {
		currentConfig.Borderless = borderless
		for _, status := range []string{"Connecting...", "Error: auth failed", "Logged out", "qr"} {
			model := m{
				w:      80,
				h:      24,
				status: status,
				qrRaw:  "fake-qr-code-data",
			}
			out := model.renderStartupView(80)
			if strings.TrimSpace(out) == "" {
				t.Fatalf("borderless=%v, status=%s: empty startup view", borderless, status)
			}
			if status == "qr" {
				if !strings.Contains(out, "Link a Device") {
					t.Fatalf("borderless=%v, status=%s: missing QR link hint: %q", borderless, status, out)
				}
			} else {
				if status == "Connecting..." {
					if strings.Contains(out, "WhatZap") {
						t.Fatalf("borderless=%v, status=%s: should not show extra WhatZap text under loading logo: %q", borderless, status, out)
					}
				} else if !strings.Contains(out, "WhatZap") {
					t.Fatalf("borderless=%v, status=%s: missing WhatZap logo/title: %q", borderless, status, out)
				}
			}
		}
	}
}

func TestRenderStatusBoxHasNoOuterBorder(t *testing.T) {
	for _, borderless := range []bool{false, true} {
		currentConfig.Borderless = borderless
		out := renderStatusBox("hi", 78, 22, 80, 24)
		if !strings.Contains(out, "hi") {
			t.Fatalf("borderless=%v: status box missing body", borderless)
		}
		for _, r := range []string{"╭", "╰", "╯", "╮"} {
			if strings.Contains(out, r) {
				t.Fatalf("borderless=%v: status box should not draw outer border, found %q", borderless, r)
			}
		}
	}
}

func TestWelcomePaneSectionHeadersAlignWithItems(t *testing.T) {
	model := m{w: 80, h: 30, spinnerFrame: 1}
	out := ansiStripRe.ReplaceAllString(model.renderWelcomePane(60, 25), "")
	lines := strings.Split(out, "\n")
	headerIdx := map[string]int{}
	for i, l := range lines {
		for _, h := range []string{"Navigation", "In Chat", "Quick Actions"} {
			if strings.TrimSpace(l) == h {
				headerIdx[h] = i
			}
		}
	}
	if len(headerIdx) != 3 {
		t.Fatalf("missing section headers, got %q", out)
	}
	for h, i := range headerIdx {
		headerLine := lines[i]
		headerIndent := len(headerLine) - len(strings.TrimLeft(headerLine, " "))
		itemLine := lines[i+1]
		itemIndent := len(itemLine) - len(strings.TrimLeft(itemLine, " "))
		if headerIndent != itemIndent {
			t.Errorf("%q header indent %d != item indent %d", h, headerIndent, itemIndent)
		}
	}
}

func TestRenderOuterAppFrameModes(t *testing.T) {
	inner := "header\nbody-content"
	// 1. Borderless mode
	currentConfig.Borderless = true
	frame := renderOuterAppFrame(inner, 80, 24, 25)
	if strings.Contains(frame, "╭") || strings.Contains(frame, "╰") {
		t.Fatalf("borderless frame should not contain border corners: %q", frame)
	}

	// 2. Bordered mode
	currentConfig.Borderless = false
	frameBordered := renderOuterAppFrame(inner, 80, 24, 25)
	if !strings.Contains(frameBordered, "╭") || !strings.Contains(frameBordered, "╰") {
		t.Fatalf("bordered frame missing border corners: %q", frameBordered)
	}
	if !strings.Contains(frameBordered, "┬") || !strings.Contains(frameBordered, "┴") {
		t.Fatalf("bordered frame missing divider junctions: %q", frameBordered)
	}
}

func TestRenderRightMainPickerPrecedence(t *testing.T) {
	model := m{
		w: 80,
		h: 24,
		confirmDialog: confirmDialog{
			open:    true,
			title:   "Log out?",
			message: "Are you sure?",
		},
		mainCache: &renderCache{},
	}
	out := model.renderRightMain(50, 20)
	if !strings.Contains(out, "Log out?") {
		t.Fatalf("renderRightMain should render confirmDialog when open: %q", out)
	}
}
