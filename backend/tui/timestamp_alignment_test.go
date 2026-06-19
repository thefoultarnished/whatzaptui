package main

import (
	"strings"
	"testing"
)

// lastNonSpaceCol returns the column index (rune offset) of the last
// non-space character in s, or -1 if s is empty/all spaces.
func lastNonSpaceCol(s string) int {
	idx := -1
	for i, r := range []rune(s) {
		if r != ' ' {
			idx = i
		}
	}
	return idx
}

func TestTimestampNewLineAlignsLastCharWithReceiptTick(t *testing.T) {
	setTestTheme(t, TokyoNight)

	saved := currentConfig.TimestampNewLine
	t.Cleanup(func() { currentConfig.TimestampNewLine = saved })
	currentConfig.TimestampNewLine = true

	msg := wireMsg{
		Message: map[string]any{"conversation": "Hi"},
	}
	msg.Key.ID = "m1"
	msg.Key.RemoteJID = "15551230001@s.whatsapp.net"
	msg.Key.FromMe = true
	msg.MessageTimestamp = 1710000000
	msg.ReceiptStatus = "read"

	model := m{
		active: msg.Key.RemoteJID,
		msgs:   map[string][]wireMsg{msg.Key.RemoteJID: {msg}},
	}

	rendered := model.renderMain(60, 6)
	visible := ansiStripRe.ReplaceAllString(rendered, "")
	lines := strings.Split(visible, "\n")

	var bodyLine, timeLine string
	for _, ln := range lines {
		if strings.Contains(ln, "Hi") {
			bodyLine = ln
		}
		if strings.Contains(ln, "✓✓") {
			timeLine = ln
		}
	}
	if bodyLine == "" || timeLine == "" {
		t.Fatalf("could not find both message and timestamp lines in:\n%s", visible)
	}

	// The last character of the message text ("i" in "Hi") must align with
	// the last receipt tick ("✓✓"), not with the trailing right-side icon.
	bodyRunes := []rune(bodyLine)
	textEnd := -1
	for i, r := range bodyRunes {
		if r == 'i' && i > 0 && bodyRunes[i-1] == 'H' {
			textEnd = i
			break
		}
	}
	if textEnd == -1 {
		t.Fatalf("could not locate message text in body line: %q", bodyLine)
	}
	tickEnd := lastNonSpaceCol(timeLine)
	if textEnd != tickEnd {
		t.Fatalf("message text ends at col %d, receipt tick ends at col %d; want equal\nbody: %q\ntime: %q", textEnd, tickEnd, bodyLine, timeLine)
	}
}

func TestOutgoingReactionSharesTimestampLineInTimestampNewLineMode(t *testing.T) {
	setTestTheme(t, Monokai)

	saved := currentConfig.TimestampNewLine
	t.Cleanup(func() { currentConfig.TimestampNewLine = saved })
	currentConfig.TimestampNewLine = true

	msg := wireMsg{
		Message:          map[string]any{"conversation": "common day in India"},
		MessageTimestamp: 1710000000,
	}
	msg.Key.ID = "m1"
	msg.Key.RemoteJID = "15551230001@s.whatsapp.net"
	msg.Key.FromMe = true
	msg.ReceiptStatus = "read"

	rxn := wireMsg{
		Message: map[string]any{
			"reactionMessage": map[string]any{
				"targetMsgID": "m1",
				"emoji":       "soccer",
			},
		},
		MessageTimestamp: 1710000001,
	}
	rxn.Key.ID = "r1"
	rxn.Key.RemoteJID = msg.Key.RemoteJID
	rxn.Key.Participant = "15551230001@s.whatsapp.net"

	model := m{
		active: msg.Key.RemoteJID,
		msgs:   map[string][]wireMsg{msg.Key.RemoteJID: {msg, rxn}},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Alice"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Alice"},
		},
	}

	rendered := model.renderMain(100, 8)
	visible := ansiStripRe.ReplaceAllString(rendered, "")
	lines := strings.Split(visible, "\n")

	var combinedLine string
	reactionLines := 0
	timeLines := 0
	for _, ln := range lines {
		hasReaction := strings.Contains(ln, "╰─")
		hasTime := strings.Contains(ln, "✓✓")
		if hasReaction {
			reactionLines++
		}
		if hasTime {
			timeLines++
		}
		if hasReaction && hasTime {
			combinedLine = ln
		}
	}
	if reactionLines != 1 || timeLines != 1 {
		t.Fatalf("expected exactly one reaction line and one timestamp line, got %d reaction, %d timestamp:\n%s", reactionLines, timeLines, visible)
	}
	if combinedLine == "" {
		t.Fatalf("reaction and timestamp should share a line in 2-line mode:\n%s", visible)
	}
	if strings.Index(combinedLine, "╰─") > strings.Index(combinedLine, "✓✓") {
		t.Fatalf("reaction should be left of timestamp: %q", combinedLine)
	}
}

func TestIncomingReactionSharesTimestampLineInTimestampNewLineMode(t *testing.T) {
	setTestTheme(t, Monokai)

	saved := currentConfig.TimestampNewLine
	t.Cleanup(func() { currentConfig.TimestampNewLine = saved })
	currentConfig.TimestampNewLine = true

	msg := wireMsg{
		Message:          map[string]any{"conversation": "common day in India"},
		MessageTimestamp: 1710000000,
	}
	msg.Key.ID = "m1"
	msg.Key.RemoteJID = "15551230001@s.whatsapp.net"
	msg.Key.FromMe = false

	rxn := wireMsg{
		Message: map[string]any{
			"reactionMessage": map[string]any{
				"targetMsgID": "m1",
				"emoji":       "soccer",
			},
		},
		MessageTimestamp: 1710000001,
	}
	rxn.Key.ID = "r1"
	rxn.Key.RemoteJID = msg.Key.RemoteJID
	rxn.Key.Participant = "self"
	rxn.Key.FromMe = true

	model := m{
		active: msg.Key.RemoteJID,
		msgs:   map[string][]wireMsg{msg.Key.RemoteJID: {msg, rxn}},
	}

	rendered := model.renderMain(100, 8)
	visible := ansiStripRe.ReplaceAllString(rendered, "")
	lines := strings.Split(visible, "\n")

	timeStr := formatExactTime(msg.MessageTimestamp)

	var combinedLine string
	reactionLines := 0
	timeLines := 0
	for _, ln := range lines {
		hasReaction := strings.Contains(ln, "╰─")
		hasTime := strings.Contains(ln, timeStr)
		if hasReaction {
			reactionLines++
		}
		if hasTime {
			timeLines++
		}
		if hasReaction && hasTime {
			combinedLine = ln
		}
	}
	if reactionLines != 1 || timeLines != 1 {
		t.Fatalf("expected exactly one reaction line and one timestamp line, got %d reaction, %d timestamp:\n%s", reactionLines, timeLines, visible)
	}
	if combinedLine == "" {
		t.Fatalf("reaction and timestamp should share a line in 2-line mode:\n%s", visible)
	}
	if strings.Index(combinedLine, timeStr) > strings.Index(combinedLine, "╰─") {
		t.Fatalf("timestamp should be left of reaction: %q", combinedLine)
	}
}
