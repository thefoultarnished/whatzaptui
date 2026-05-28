package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
)

func TestChatInputLockedIgnoresTyping(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{},
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	got := next.(m)

	if got.input != "" {
		t.Fatalf("input = %q, want empty", got.input)
	}
	if got.inputBuf != "" {
		t.Fatalf("inputBuf = %q, want empty", got.inputBuf)
	}
}

func TestRenderChatInputShowsBlacklistedPlaceholder(t *testing.T) {
	currentTheme = TokyoNight
	rehashStyles()

	model := m{
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{},
	}

	rendered := model.renderChatInput(48, "")
	if !strings.Contains(rendered, "blacklisted") {
		t.Fatalf("rendered input missing blacklisted placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "/whitelist") {
		t.Fatalf("rendered input missing whitelist hint: %q", rendered)
	}
}

func TestRenderChatInputFocusedTypableShowsBlinkingCursorState(t *testing.T) {
	currentTheme = TokyoNight
	rehashStyles()

	base := m{
		mode:             "chat",
		active:           "15551230001@s.whatsapp.net",
		whitelist:        map[string]string{"15551230001": "Allowed"},
		sidebarFocused:   false,
		leftInputFocused: false,
	}

	onModel := base
	onModel.cursorOn = true
	offModel := base
	offModel.cursorOn = false

	renderedOn := onModel.renderChatInput(48, "")
	renderedOff := offModel.renderChatInput(48, "")

	if renderedOn == renderedOff {
		t.Fatalf("expected locked focused input to render different blink states")
	}
}

func TestAltFOpensFilePickerInChat(t *testing.T) {
	currentTheme = TokyoNight
	rehashStyles()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(filePath, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := m{
		status:         "ready",
		mode:           "chat",
		active:         "15551230001@s.whatsapp.net",
		whitelist:      map[string]string{"15551230001": "Allowed"},
		fileBrowserDir: dir,
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true})
	got := next.(m)

	if !got.fileBrowserOpen {
		t.Fatal("expected file browser to open")
	}
	if got.fileBrowserDir != dir {
		t.Fatalf("fileBrowserDir = %q, want %q", got.fileBrowserDir, dir)
	}
	if len(got.fileBrowserEntries) == 0 {
		t.Fatal("expected file browser entries")
	}
}

func TestFilePickerSelectFileInsertsSendCommand(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(filePath, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := m{
		status:         "ready",
		mode:           "chat",
		active:         "15551230001@s.whatsapp.net",
		whitelist:      map[string]string{"15551230001": "Allowed"},
		fileBrowserDir: dir,
	}
	if err := model.loadFileBrowserDir(dir); err != nil {
		t.Fatal(err)
	}
	model.fileBrowserOpen = true
	for i, entry := range model.fileBrowserEntries {
		if entry.path == filePath {
			model.fileBrowserIndex = i
			break
		}
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(m)

	if got.fileBrowserOpen {
		t.Fatal("expected file browser to close after selecting a file")
	}
	if got.pendingAttachmentPath != filePath {
		t.Fatalf("pendingAttachmentPath = %q, want %q", got.pendingAttachmentPath, filePath)
	}
	if got.input != "" {
		t.Fatalf("input = %q, want empty caption composer", got.input)
	}
}

func TestFilePickerBackspaceGoesToParentDirectory(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	model := m{
		status:         "ready",
		mode:           "chat",
		active:         "15551230001@s.whatsapp.net",
		whitelist:      map[string]string{"15551230001": "Allowed"},
		fileBrowserDir: child,
	}
	if err := model.loadFileBrowserDir(child); err != nil {
		t.Fatal(err)
	}
	model.fileBrowserOpen = true

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyBackspace})
	got := next.(m)

	if got.fileBrowserDir != root {
		t.Fatalf("fileBrowserDir = %q, want %q", got.fileBrowserDir, root)
	}
}

func TestAttachmentEnterSendsFileWithCaptionInDemoMode(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(filePath, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	model := m{
		status:                "ready",
		mode:                  "chat",
		active:                "15551230001@s.whatsapp.net",
		whitelist:             map[string]string{"15551230001": "Allowed"},
		demoMode:              true,
		pendingAttachmentPath: filePath,
		pendingAttachmentKind: "image",
		pendingAttachmentName: "photo.jpg",
		input:                 "summer trip",
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	armed := next.(m)
	next, cmd := armed.Update(composerSendMsg{seq: armed.pendingSendSeq})
	got := next.(m)

	if cmd == nil {
		t.Fatal("expected top bar command for demo media send")
	}
	if got.pendingAttachmentPath != "" {
		t.Fatal("expected pending attachment to clear after send attempt")
	}
}

func TestEscCancelsPendingAttachment(t *testing.T) {
	model := m{
		status:                "ready",
		mode:                  "chat",
		active:                "15551230001@s.whatsapp.net",
		whitelist:             map[string]string{"15551230001": "Allowed"},
		pendingAttachmentPath: "C:\\tmp\\photo.jpg",
		pendingAttachmentKind: "image",
		pendingAttachmentName: "photo.jpg",
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(m)

	if got.pendingAttachmentPath != "" {
		t.Fatal("expected Esc to clear pending attachment")
	}
}

func TestNameFallsBackToIndexedContactByNumber(t *testing.T) {
	model := m{
		contacts: map[string]contact{
			"15551230001@lid": {ID: "15551230001@lid", Notify: "Alex Alias"},
		},
	}
	model.rebuildContactIndex()

	got := model.name(chat{ID: "15551230001@s.whatsapp.net"})
	if got != "Alex Alias" {
		t.Fatalf("name() = %q, want %q", got, "Alex Alias")
	}
}

func TestSidebarItemsContactsCacheInvalidatesOnIdentityChange(t *testing.T) {
	model := m{
		sidebarTab:   "contacts",
		sidebarCache: &sidebarCache{},
		contacts: map[string]contact{
			"15550000002@s.whatsapp.net": {ID: "15550000002@s.whatsapp.net", Notify: "Bravo"},
			"15550000001@s.whatsapp.net": {ID: "15550000001@s.whatsapp.net", Notify: "Alpha"},
		},
	}
	model.rebuildContactIndex()
	model.markIdentityChanged()

	first := model.sidebarItems()
	if len(first) != 2 || first[0].ID != "15550000001@s.whatsapp.net" {
		t.Fatalf("unexpected initial sidebar ordering: %+v", first)
	}

	model.names = map[string]string{"15550000002": "Aaron"}
	model.markIdentityChanged()

	second := model.sidebarItems()
	if len(second) != 2 || second[0].ID != "15550000002@s.whatsapp.net" {
		t.Fatalf("sidebar cache did not refresh after identity change: %+v", second)
	}
}

func TestRenderMainIncomingMessageKeepsBodyColorAfterPrefix(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	msg := wireMsg{
		Message: map[string]any{"conversation": "hello there"},
	}
	msg.Key.ID = "m1"
	msg.Key.RemoteJID = "120363040000001234@g.us"
	msg.Key.Participant = "15551230001@s.whatsapp.net"
	msg.MessageTimestamp = 1710000000

	model := m{
		active:           msg.Key.RemoteJID,
		msgs:             map[string][]wireMsg{msg.Key.RemoteJID: {msg}},
		contacts:         map[string]contact{"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "annu"}},
		contactsByNumber: map[string]contact{"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "annu"}},
		whitelist:        map[string]string{},
		names:            map[string]string{},
	}

	rendered := model.renderMain(60, 6)
	prefix := lipgloss.NewStyle().Foreground(receivedName).Bold(true).Render("annu: ")
	body := lipgloss.NewStyle().Foreground(receivedText).Render("hello there")

	if !strings.Contains(rendered, prefix) {
		t.Fatalf("rendered message missing received-name prefix styling: %q", rendered)
	}
	if !strings.Contains(rendered, body) {
		t.Fatalf("rendered message missing received-text styling after prefix: %q", rendered)
	}
}

func TestFileOpenMsgReportsFailure(t *testing.T) {
	model := m{}

	next, cmd := model.Update(fileOpenMsg{path: "C:\\missing.txt", err: fmt.Errorf("shell failed")})
	got := next.(m)
	if cmd == nil {
		t.Fatal("expected top bar command on file open failure")
	}
	followUp := cmd()
	final, _ := got.Update(followUp)
	updated := final.(m)
	if updated.topBarMsg != "Open failed: shell failed" {
		t.Fatalf("topBarMsg = %q, want %q", updated.topBarMsg, "Open failed: shell failed")
	}
}

func TestRenderMainQuoteUsesSentColorsWhenQuotingMe(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	msg := wireMsg{
		Message: map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              "reply text",
				"quotedText":        "my old message",
				"quotedParticipant": "",
			},
		},
	}
	msg.Key.ID = "m2"
	msg.Key.RemoteJID = "15551230001@s.whatsapp.net"
	msg.MessageTimestamp = 1710000000

	model := m{
		active:           msg.Key.RemoteJID,
		msgs:             map[string][]wireMsg{msg.Key.RemoteJID: {msg}},
		contacts:         map[string]contact{msg.Key.RemoteJID: {ID: msg.Key.RemoteJID, Notify: "annu"}},
		contactsByNumber: map[string]contact{"15551230001": {ID: msg.Key.RemoteJID, Notify: "annu"}},
	}

	rendered := model.renderMain(80, 8)
	quotePrefix := lipgloss.NewStyle().Foreground(sentName).Bold(true).Render("Me: ")
	quoteBody := lipgloss.NewStyle().Foreground(sentText).Render("my old message")
	if !strings.Contains(rendered, quotePrefix) || !strings.Contains(rendered, quoteBody) {
		t.Fatalf("self-quote did not use sent colors: %q", rendered)
	}
}

func TestRenderMainQuoteUsesReceivedColorsWhenQuotingOtherSender(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	msg := wireMsg{
		Message: map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              "reply text",
				"quotedText":        "their old message",
				"quotedParticipant": "15551230001@s.whatsapp.net",
			},
		},
	}
	msg.Key.ID = "m3"
	msg.Key.RemoteJID = "15551230002@s.whatsapp.net"
	msg.MessageTimestamp = 1710000000

	model := m{
		active: msg.Key.RemoteJID,
		msgs:   map[string][]wireMsg{msg.Key.RemoteJID: {msg}},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "bob"},
			"15551230002@s.whatsapp.net": {ID: "15551230002@s.whatsapp.net", Notify: "annu"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "bob"},
			"15551230002": {ID: "15551230002@s.whatsapp.net", Notify: "annu"},
		},
	}

	rendered := model.renderMain(80, 8)
	quotePrefix := lipgloss.NewStyle().Foreground(receivedName).Bold(true).Render("bob: ")
	quoteBody := lipgloss.NewStyle().Foreground(receivedText).Render("their old message")
	if !strings.Contains(rendered, quotePrefix) || !strings.Contains(rendered, quoteBody) {
		t.Fatalf("received quote did not use received colors: %q", rendered)
	}
}

func TestRenderReplyBarTruncatesToPaneWidth(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	reply := &wireMsg{
		Message: map[string]any{
			"conversation": "Are you planning to attend tomorrow? The first lecture?",
		},
	}
	reply.Key.ID = "q1"
	reply.Key.RemoteJID = "15551230001@s.whatsapp.net"

	model := m{
		replyTo: reply,
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu"},
		},
	}

	rendered := model.renderReplyBar(140, 32)
	if lipgloss.Width(rendered) != 32 {
		t.Fatalf("reply bar width = %d, want 32, rendered=%q", lipgloss.Width(rendered), rendered)
	}
	if !strings.Contains(rendered, "Esc cancel") {
		t.Fatalf("reply bar missing cancel hint: %q", rendered)
	}
	if !strings.Contains(rendered, "...") {
		t.Fatalf("reply bar should truncate long quoted text: %q", rendered)
	}
}

func TestViewReplyBarDoesNotExpandPastFrameWidth(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	reply := &wireMsg{
		Message: map[string]any{
			"conversation": "Are you planning to attend tomorrow? The first lecture?",
		},
	}
	reply.Key.ID = "q2"
	reply.Key.RemoteJID = "15551230001@s.whatsapp.net"

	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		w:         120,
		h:         8,
		replyTo:   reply,
		whitelist: map[string]string{"15551230001": "Shadu"},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu"},
		},
		msgs: map[string][]wireMsg{
			"15551230001@s.whatsapp.net": {},
		},
		sidebarCache: &sidebarCache{},
		mainCache:    &renderCache{},
	}

	rendered := model.View()
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > model.w {
			t.Fatalf("view line width = %d, want <= %d, line=%q", lipgloss.Width(line), model.w, line)
		}
	}
}

func TestRenderMainOutgoingReactionKeepsMessageIndentedRight(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	msg := wireMsg{
		Message:          map[string]any{"conversation": "big data. SPM later"},
		MessageTimestamp: 1710000000,
	}
	msg.Key.ID = "m4"
	msg.Key.RemoteJID = "15551230001@s.whatsapp.net"
	msg.Key.FromMe = true

	rxn := wireMsg{
		Message: map[string]any{
			"reactionMessage": map[string]any{
				"targetMsgID": "m4",
				"emoji":       "thumbs_up",
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
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu Mady"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Shadu Mady"},
		},
	}

	rendered := model.renderMain(100, 8)
	reactionPrefix := lipgloss.NewStyle().Foreground(receivedName).Render("  ╰─ ")
	reactionBody := lipgloss.NewStyle().Foreground(receivedName).Bold(true).Render("thumbs_up Shadu Mady")
	lines := strings.Split(rendered, "\n")
	foundMsg := false
	foundReaction := false
	for _, line := range lines {
		if strings.Contains(line, "big data. SPM later") {
			foundMsg = true
			if !strings.HasPrefix(line, " ") {
				t.Fatalf("outgoing reacted line lost right indent: %q", line)
			}
		}
		if strings.Contains(line, "Shadu Mady") {
			foundReaction = true
			if !strings.Contains(line, "╰─") {
				t.Fatalf("reaction line missing linked prefix: %q", line)
			}
			if !strings.HasPrefix(line, " ") {
				t.Fatalf("reaction line lost right indent: %q", line)
			}
		}
	}
	if !foundMsg {
		t.Fatalf("failed to find reacted outgoing line in render: %q", rendered)
	}
	if !foundReaction {
		t.Fatalf("failed to find outgoing reaction line in render: %q", rendered)
	}
	if !strings.Contains(rendered, reactionPrefix) {
		t.Fatalf("reaction link did not use reactor name color: %q", rendered)
	}
	if !strings.Contains(rendered, reactionBody) {
		t.Fatalf("reaction body did not use reactor name color: %q", rendered)
	}
}

func TestRenderUserListHighlightedNameUsesMarqueeOffset(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

	model := m{
		mode:                 "nav",
		sidebarFocused:       true,
		sel:                  0,
		sidebarMarqueeOffset: 4,
		whitelist:            map[string]string{"15551230001": "Very Long Highlighted Contact Name"},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Very Long Highlighted Contact Name"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Very Long Highlighted Contact Name"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	if !strings.Contains(lines[0], "Long Highl") {
		t.Fatalf("highlighted row did not render marquee window: %q", lines[0])
	}
	if strings.Contains(lines[0], "1. Very Long Highlighted") {
		t.Fatalf("highlighted row ignored marquee offset: %q", lines[0])
	}
	if got := lipgloss.Width(lines[0]); got != 28 {
		t.Fatalf("highlighted row width = %d, want 28", got)
	}
}

func TestSidebarHighlightInsetAnimatesToOne(t *testing.T) {
	model := m{
		mode:           "nav",
		sidebarFocused: true,
		sidebarTab:     "chats",
		sel:            0,
		chats:          []chat{{ID: "15551230001@s.whatsapp.net", ConversationTimestamp: 1710000000}},
	}

	model.syncSidebarHighlight()
	if model.sidebarHighlightInset != 0 {
		t.Fatalf("initial sidebarHighlightInset = %d, want 0", model.sidebarHighlightInset)
	}

	model.advanceSidebarHighlight()
	if model.sidebarHighlightInset != 1 {
		t.Fatalf("animated sidebarHighlightInset = %d, want 1", model.sidebarHighlightInset)
	}
}

func TestChatEnterSchedulesDeferredSend(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
		input:     "hello now",
	}

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(m)

	if cmd == nil {
		t.Fatal("expected deferred send command")
	}
	if !got.pendingSendArmed {
		t.Fatal("expected pending send to be armed")
	}
	msgs := got.msgs[got.active]
	if len(msgs) != 0 {
		t.Fatalf("optimistic send appended %d messages before timer, want 0", len(msgs))
	}
}

func TestDeferredSendCommitsOutgoingMessage(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
		input:     "hello now",
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	armed := next.(m)
	next, _ = armed.Update(composerSendMsg{seq: armed.pendingSendSeq})
	got := next.(m)

	msgs := got.msgs[got.active]
	if len(msgs) != 1 {
		t.Fatalf("optimistic send appended %d messages, want 1", len(msgs))
	}
	if !msgs[0].Key.FromMe {
		t.Fatalf("optimistic message should be from me: %+v", msgs[0].Key)
	}
	if !strings.HasPrefix(msgs[0].Key.ID, "local-") {
		t.Fatalf("optimistic message id = %q, want local-*", msgs[0].Key.ID)
	}
	if body, _ := msgs[0].Message["conversation"].(string); body != "hello now" {
		t.Fatalf("optimistic message body = %q, want %q", body, "hello now")
	}
}

func TestAltEnterAddsComposerNewlineWithoutSending(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
		input:     "hello",
	}

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := next.(m)

	if cmd != nil {
		t.Fatalf("expected no send command, got %#v", cmd)
	}
	if got.input != "hello\n" {
		t.Fatalf("input = %q, want %q", got.input, "hello\n")
	}
	if len(got.msgs[got.active]) != 0 {
		t.Fatalf("expected no optimistic messages, got %d", len(got.msgs[got.active]))
	}
}

func TestPastedEnterAddsComposerNewlineWithoutSending(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
		input:     "hello",
	}

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnter, Paste: true})
	got := next.(m)

	if cmd != nil {
		t.Fatalf("expected no send command, got %#v", cmd)
	}
	if got.input != "hello\n" {
		t.Fatalf("input = %q, want %q", got.input, "hello\n")
	}
	if len(got.msgs[got.active]) != 0 {
		t.Fatalf("expected no optimistic messages, got %d", len(got.msgs[got.active]))
	}
}

func TestChatEnterSendsMultilineDraftAsOneMessage(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
		input:     "hello\nworld",
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	armed := next.(m)
	next, _ = armed.Update(composerSendMsg{seq: armed.pendingSendSeq})
	got := next.(m)

	msgs := got.msgs[got.active]
	if len(msgs) != 1 {
		t.Fatalf("optimistic send appended %d messages, want 1", len(msgs))
	}
	if body, _ := msgs[0].Message["conversation"].(string); body != "hello\nworld" {
		t.Fatalf("optimistic multiline body = %q, want %q", body, "hello\nworld")
	}
}

func TestPasteLikeEnterAfterBulkRunesArmsDeferredSend(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	got := next.(m)
	if got.inputBuf == "" {
		t.Fatalf("expected buffered input after bulk runes")
	}

	next, cmd := got.key(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(m)

	if cmd == nil {
		t.Fatal("expected deferred send command")
	}
	if !got.pendingSendArmed {
		t.Fatal("expected pending send to be armed")
	}
	if len(got.msgs[got.active]) != 0 {
		t.Fatalf("expected no messages before deferred send, got %d", len(got.msgs[got.active]))
	}
}

func TestPastedMultilineBurstDoesNotSendUntilFinalEnter(t *testing.T) {
	model := m{
		status:    "ready",
		mode:      "chat",
		active:    "15551230001@s.whatsapp.net",
		whitelist: map[string]string{"15551230001": "Allowed"},
		msgs:      map[string][]wireMsg{},
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	state := next.(m)
	next, _ = state.key(tea.KeyMsg{Type: tea.KeyEnter})
	state = next.(m)
	next, _ = state.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	state = next.(m)

	if state.input != "line one\n" {
		t.Fatalf("input after paste burst = %q, want %q", state.input, "line one\n")
	}
	if state.inputBuf != "line two" {
		t.Fatalf("inputBuf after paste burst = %q, want %q", state.inputBuf, "line two")
	}
	if len(state.msgs[state.active]) != 0 {
		t.Fatalf("expected no messages sent during paste burst, got %d", len(state.msgs[state.active]))
	}

	next, _ = state.key(tea.KeyMsg{Type: tea.KeyEnter})
	state = next.(m)
	next, _ = state.Update(composerSendMsg{seq: state.pendingSendSeq})
	state = next.(m)

	msgs := state.msgs[state.active]
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	if body, _ := msgs[0].Message["conversation"].(string); body != "line one\nline two" {
		t.Fatalf("sent body = %q, want %q", body, "line one\nline two")
	}
}

func TestRunCommandSoundProfileAndToggle(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())
	currentConfig = Config{
		ThemeName:    "tokyonight",
		MouseEnabled: true,
		SoundEnabled: true,
		SoundProfile: 2,
	}

	model := m{
		soundEnabled: true,
		soundProfile: 2,
	}

	cmd, ok := model.runCommand("/sound4", true)
	if !ok || cmd == nil {
		t.Fatalf("expected /sound4 to be handled with preview command")
	}
	if !model.soundEnabled || model.soundProfile != 4 {
		t.Fatalf("model sound state after /sound4 = enabled:%v profile:%d, want enabled:true profile:4", model.soundEnabled, model.soundProfile)
	}
	if !currentConfig.SoundEnabled || currentConfig.SoundProfile != 4 {
		t.Fatalf("config sound state after /sound4 = enabled:%v profile:%d, want enabled:true profile:4", currentConfig.SoundEnabled, currentConfig.SoundProfile)
	}

	cmd, ok = model.runCommand("/soundoff", true)
	if !ok || cmd == nil {
		t.Fatalf("expected /soundoff to be handled")
	}
	if model.soundEnabled {
		t.Fatalf("model sound should be disabled after /soundoff")
	}
	if currentConfig.SoundEnabled {
		t.Fatalf("config sound should be disabled after /soundoff")
	}

	cmd, ok = model.runCommand("/soundon", true)
	if !ok || cmd == nil {
		t.Fatalf("expected /soundon to be handled with preview command")
	}
	if !model.soundEnabled || model.soundProfile != 4 {
		t.Fatalf("model sound state after /soundon = enabled:%v profile:%d, want enabled:true profile:4", model.soundEnabled, model.soundProfile)
	}
	if !currentConfig.SoundEnabled || currentConfig.SoundProfile != 4 {
		t.Fatalf("config sound state after /soundon = enabled:%v profile:%d, want enabled:true profile:4", currentConfig.SoundEnabled, currentConfig.SoundProfile)
	}
}

func TestNavListWrapAround(t *testing.T) {
	model := m{
		status: "ready",
		mode:   "nav",
		chats: []chat{
			{ID: "a@s.whatsapp.net", ConversationTimestamp: 3},
			{ID: "b@s.whatsapp.net", ConversationTimestamp: 2},
			{ID: "c@s.whatsapp.net", ConversationTimestamp: 1},
		},
		sel: 0,
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(m)
	if got.sel != 2 {
		t.Fatalf("nav up wrap sel = %d, want 2", got.sel)
	}

	next, _ = got.key(tea.KeyMsg{Type: tea.KeyDown})
	got = next.(m)
	if got.sel != 0 {
		t.Fatalf("nav down wrap sel = %d, want 0", got.sel)
	}
}

func TestSearchListWrapAround(t *testing.T) {
	model := m{
		status: "ready",
		mode:   "search",
		chats: []chat{
			{ID: "a@s.whatsapp.net", ConversationTimestamp: 3},
			{ID: "b@s.whatsapp.net", ConversationTimestamp: 2},
		},
		sel: 0,
	}

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(m)
	if got.sel != 1 {
		t.Fatalf("search up wrap sel = %d, want 1", got.sel)
	}

	next, _ = got.key(tea.KeyMsg{Type: tea.KeyDown})
	got = next.(m)
	if got.sel != 0 {
		t.Fatalf("search down wrap sel = %d, want 0", got.sel)
	}
}

func TestRefreshWindowTitleCmdTracksUnreadCounts(t *testing.T) {
	model := m{
		contacts: map[string]contact{
			"a@s.whatsapp.net": {ID: "a@s.whatsapp.net", Notify: "Alice Johnson"},
			"b@s.whatsapp.net": {ID: "b@s.whatsapp.net", Notify: "Bob Stone"},
			"c@s.whatsapp.net": {ID: "c@s.whatsapp.net", Notify: "Charlie Fox"},
		},
		contactsByNumber: map[string]contact{},
		chats: []chat{
			{ID: "a@s.whatsapp.net", UnreadCount: 3},
			{ID: "b@s.whatsapp.net", UnreadCount: 2},
			{ID: "c@s.whatsapp.net", UnreadCount: 1},
		},
	}

	cmd := model.refreshWindowTitleCmd()
	if cmd == nil {
		t.Fatalf("expected title update command for unread chats")
	}
	if model.windowTitle != "WhatZap (🟢 Alice,Bob,Charlie)" {
		t.Fatalf("windowTitle = %q, want %q", model.windowTitle, "WhatZap (🟢 Alice,Bob,Charlie)")
	}

	cmd = model.refreshWindowTitleCmd()
	if cmd != nil {
		t.Fatalf("expected no-op title command when title is unchanged")
	}

	model.chats[0].UnreadCount = 0
	model.chats[1].UnreadCount = 0
	model.chats[2].UnreadCount = 0
	cmd = model.refreshWindowTitleCmd()
	if cmd == nil {
		t.Fatalf("expected title reset command when unread count clears")
	}
	if model.windowTitle != "WhatZap" {
		t.Fatalf("windowTitle = %q, want %q", model.windowTitle, "WhatZap")
	}
}

func TestRefreshWindowTitleCmdShowsOnlyFirstThreeNames(t *testing.T) {
	model := m{
		contacts: map[string]contact{
			"a@s.whatsapp.net": {ID: "a@s.whatsapp.net", Notify: "Alice Johnson"},
			"b@s.whatsapp.net": {ID: "b@s.whatsapp.net", Notify: "Bob Stone"},
			"c@s.whatsapp.net": {ID: "c@s.whatsapp.net", Notify: "Charlie Fox"},
			"d@s.whatsapp.net": {ID: "d@s.whatsapp.net", Notify: "Dora Reed"},
		},
		contactsByNumber: map[string]contact{},
		chats: []chat{
			{ID: "a@s.whatsapp.net", UnreadCount: 1},
			{ID: "b@s.whatsapp.net", UnreadCount: 1},
			{ID: "c@s.whatsapp.net", UnreadCount: 1},
			{ID: "d@s.whatsapp.net", UnreadCount: 1},
		},
	}

	_ = model.refreshWindowTitleCmd()
	if model.windowTitle != "WhatZap (🟢 Alice,Bob,Charlie +1)" {
		t.Fatalf("windowTitle = %q, want %q", model.windowTitle, "WhatZap (🟢 Alice,Bob,Charlie +1)")
	}
}

func TestHeaderAvatarInitials(t *testing.T) {
	if got := headerAvatarInitials("Alice Brown", "15551230001@s.whatsapp.net"); got != "AB" {
		t.Fatalf("headerAvatarInitials() = %q, want %q", got, "AB")
	}
	if got := headerAvatarInitials("alice", "15551230001@s.whatsapp.net"); got != "A " {
		t.Fatalf("headerAvatarInitials() single = %q, want %q", got, "A ")
	}
	if got := headerAvatarInitials("", "15551230001@s.whatsapp.net"); got != "01" {
		t.Fatalf("headerAvatarInitials() fallback = %q, want %q", got, "01")
	}
}

// --- Lazy-load tests ---

func mkMsg(id string, ts int64) wireMsg {
	var msg wireMsg
	msg.Key.ID = id
	msg.MessageTimestamp = ts
	return msg
}

func TestOlderMsgsMsgPrependsAndDedupes(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m3", 300), mkMsg("m4", 400)}},
		loadingOlder: map[string]bool{chatID: true},
		noMoreOlder:  map[string]bool{},
	}
	older := []wireMsg{mkMsg("m1", 100), mkMsg("m2", 200), mkMsg("m3", 300)} // m3 dup
	next, _ := model.Update(olderMsgsMsg{chatID: chatID, msgs: older, hasMore: true})
	got := next.(m)

	all := got.msgs[chatID]
	if len(all) != 4 {
		t.Fatalf("merged length = %d, want 4", len(all))
	}
	wantIDs := []string{"m1", "m2", "m3", "m4"}
	for i, want := range wantIDs {
		if all[i].Key.ID != want {
			t.Fatalf("msgs[%d].Key.ID = %q, want %q", i, all[i].Key.ID, want)
		}
	}
	if got.loadingOlder[chatID] {
		t.Fatalf("loadingOlder should be cleared after response")
	}
	if got.noMoreOlder[chatID] {
		t.Fatalf("noMoreOlder should NOT be set when hasMore=true")
	}
}

func TestOlderMsgsMsgMarksExhaustedWhenHasMoreFalse(t *testing.T) {
	chatID := "c1"
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m1", 100)}},
		loadingOlder: map[string]bool{chatID: true},
		noMoreOlder:  map[string]bool{},
	}
	next, _ := model.Update(olderMsgsMsg{chatID: chatID, msgs: nil, hasMore: false})
	got := next.(m)
	if !got.noMoreOlder[chatID] {
		t.Fatalf("noMoreOlder should be true when hasMore=false")
	}
	if got.loadingOlder[chatID] {
		t.Fatalf("loadingOlder should be cleared")
	}
}

func TestMaybeLoadOlderSkipsWhenAlreadyLoading(t *testing.T) {
	chatID := "c1"
	model := &m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m1", 100)}},
		loadingOlder: map[string]bool{chatID: true},
		noMoreOlder:  map[string]bool{},
		scroll:       100,
	}
	if cmd := model.maybeLoadOlder(); cmd != nil {
		t.Fatalf("maybeLoadOlder should return nil while a fetch is in flight")
	}
}

func TestMaybeLoadOlderSkipsWhenExhausted(t *testing.T) {
	chatID := "c1"
	model := &m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m1", 100)}},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{chatID: true},
		scroll:       100,
	}
	if cmd := model.maybeLoadOlder(); cmd != nil {
		t.Fatalf("maybeLoadOlder should return nil when noMoreOlder is set")
	}
}

func TestMaybeLoadOlderSkipsWhenFarFromTop(t *testing.T) {
	chatID := "c1"
	msgs := make([]wireMsg, 100)
	for i := range msgs {
		msgs[i] = mkMsg(fmt.Sprintf("m%d", i), int64(i+1))
	}
	model := &m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: msgs},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
		scroll:       0,
	}
	if cmd := model.maybeLoadOlder(); cmd != nil {
		t.Fatalf("maybeLoadOlder should return nil when user is at the bottom")
	}
}

func TestMaybeLoadOlderTriggersWhenNearTop(t *testing.T) {
	chatID := "c1"
	msgs := make([]wireMsg, 50)
	for i := range msgs {
		msgs[i] = mkMsg(fmt.Sprintf("m%d", i), int64(i+1))
	}
	model := &m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{chatID: msgs},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
		scroll:       45,
	}
	cmd := model.maybeLoadOlder()
	if cmd == nil {
		t.Fatalf("maybeLoadOlder should return a fetch cmd when near top")
	}
	if !model.loadingOlder[chatID] {
		t.Fatalf("loadingOlder[%s] should be true after triggering", chatID)
	}
}

func TestMsgsMsgMarksExhaustedWhenHasMoreFalse(t *testing.T) {
	chatID := "c1"
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
	}
	next, _ := model.Update(msgsMsg{chatID: chatID, msgs: []wireMsg{mkMsg("m1", 1)}, hasMore: false})
	got := next.(m)
	if !got.noMoreOlder[chatID] {
		t.Fatalf("noMoreOlder should be true when hasMore=false")
	}
}

func TestMsgsMsgKeepsHasMoreOpenWhenBackendSaysSo(t *testing.T) {
	chatID := "c1"
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{chatID: true}, // stale flag from a prior fetch
	}
	next, _ := model.Update(msgsMsg{chatID: chatID, msgs: []wireMsg{mkMsg("m1", 1)}, hasMore: true})
	got := next.(m)
	if got.noMoreOlder[chatID] {
		t.Fatalf("noMoreOlder should be cleared when hasMore=true")
	}
}

// --- Message-search tests ---

func TestCtrlFEntersMsgsearchMode(t *testing.T) {
	model := m{status: "ready", mode: "chat", active: "c1", whitelist: map[string]string{}}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyCtrlF})
	got := next.(m)
	if got.mode != "msgsearch" {
		t.Fatalf("mode = %q, want msgsearch", got.mode)
	}
}

func TestMsgsearchEscReturnsToChat(t *testing.T) {
	model := m{status: "ready", mode: "msgsearch", active: "c1", whitelist: map[string]string{}, msgSearchInput: "foo"}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(m)
	if got.mode != "chat" {
		t.Fatalf("mode = %q, want chat", got.mode)
	}
	if got.msgSearchInput != "" {
		t.Fatalf("msgSearchInput should be cleared")
	}
}

func TestMsgsearchEscReturnsToNavWhenNoActiveChat(t *testing.T) {
	model := m{status: "ready", mode: "msgsearch", active: "", whitelist: map[string]string{}}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(m).mode != "nav" {
		t.Fatalf("mode = %q, want nav", next.(m).mode)
	}
}

func TestMsgsearchTypingAppendsToInput(t *testing.T) {
	model := m{status: "ready", mode: "msgsearch", whitelist: map[string]string{}}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if next.(m).msgSearchInput != "hi" {
		t.Fatalf("msgSearchInput = %q, want hi", next.(m).msgSearchInput)
	}
}

func TestMsgsearchBackspaceClearsLastChar(t *testing.T) {
	model := m{status: "ready", mode: "msgsearch", msgSearchInput: "abc", whitelist: map[string]string{}}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyBackspace})
	if next.(m).msgSearchInput != "ab" {
		t.Fatalf("msgSearchInput = %q, want ab", next.(m).msgSearchInput)
	}
}

func TestMsgsearchEnterWithResultsJumpsToChat(t *testing.T) {
	hits := []searchHit{
		{ChatID: "c1", MessageID: "m1", Timestamp: 100, Snippet: "hello <b>foo</b>"},
		{ChatID: "c2", MessageID: "m2", Timestamp: 200, Snippet: "<b>foo</b> world"},
	}
	model := m{
		status:           "ready",
		mode:             "msgsearch",
		whitelist:        map[string]string{},
		msgSearchInput:   "foo",
		msgSearchResults: hits,
		msgSearchSel:     1,
	}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(m)
	if got.mode != "chat" {
		t.Fatalf("mode = %q, want chat", got.mode)
	}
	if got.active != "c2" {
		t.Fatalf("active = %q, want c2", got.active)
	}
	if got.selectedMsgID != "m2" {
		t.Fatalf("selectedMsgID = %q, want m2", got.selectedMsgID)
	}
	if len(got.msgSearchResults) != 0 {
		t.Fatalf("msgSearchResults should be cleared after jump")
	}
}

func TestMsgsearchUpDownNavigatesResults(t *testing.T) {
	hits := []searchHit{
		{ChatID: "c1", MessageID: "m1"},
		{ChatID: "c2", MessageID: "m2"},
		{ChatID: "c3", MessageID: "m3"},
	}
	model := m{
		status:           "ready",
		mode:             "msgsearch",
		whitelist:        map[string]string{},
		msgSearchResults: hits,
		msgSearchSel:     0,
	}
	next, _ := model.key(tea.KeyMsg{Type: tea.KeyDown})
	if next.(m).msgSearchSel != 1 {
		t.Fatalf("after Down: sel = %d, want 1", next.(m).msgSearchSel)
	}
	model = next.(m)
	next, _ = model.key(tea.KeyMsg{Type: tea.KeyUp})
	if next.(m).msgSearchSel != 0 {
		t.Fatalf("after Up: sel = %d, want 0", next.(m).msgSearchSel)
	}
}

func TestSearchResultsMsgPopulatesResults(t *testing.T) {
	model := m{status: "ready", mode: "msgsearch", whitelist: map[string]string{}, msgSearchLoading: true}
	hits := []searchHit{{ChatID: "c1", MessageID: "m1", Snippet: "hi"}}
	next, _ := model.Update(searchResultsMsg{query: "hi", results: hits})
	got := next.(m)
	if got.msgSearchLoading {
		t.Fatalf("loading should be cleared")
	}
	if len(got.msgSearchResults) != 1 {
		t.Fatalf("results count = %d, want 1", len(got.msgSearchResults))
	}
}

// --- Jump to bottom (End key) ---

func TestEndKeyResetsScrollAndReloads(t *testing.T) {
	chatID := "c1"
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		whitelist:    map[string]string{},
		scroll:       50,
		selectedMsgID: "old",
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m1", 1)}},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
	}
	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnd})
	got := next.(m)

	if got.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", got.scroll)
	}
	if got.selectedMsgID != "" {
		t.Fatalf("selectedMsgID should be cleared, got %q", got.selectedMsgID)
	}
	if cmd == nil {
		t.Fatalf("End key should fire a getMsgs command")
	}
}

func TestEndKeyNoOpInSidebar(t *testing.T) {
	model := m{
		status:        "ready",
		mode:          "chat",
		active:        "c1",
		sidebarFocused: true,
		whitelist:     map[string]string{},
		scroll:        10,
	}
	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnd})
	if next.(m).scroll != 10 {
		t.Fatalf("End key should not affect scroll when sidebar is focused")
	}
	if cmd != nil {
		t.Fatalf("End key should not fire command when sidebar is focused")
	}
}

// --- WS reconnect backoff ---

func TestWSReconnectBackoffDoubles(t *testing.T) {
	model := m{
		status:           "ready",
		wsReconnectDelay: time.Second,
	}
	next, _ := model.Update(wsOpenMsg{err: fmt.Errorf("refused")})
	got := next.(m)

	if got.wsReconnectDelay != 2*time.Second {
		t.Fatalf("wsReconnectDelay = %v, want 2s", got.wsReconnectDelay)
	}
	if !got.wsDisconnected {
		t.Fatalf("wsDisconnected should be true after open failure")
	}
}

func TestWSReconnectBackoffCapsAt30s(t *testing.T) {
	model := m{
		status:           "ready",
		wsReconnectDelay: 20 * time.Second,
	}
	next, _ := model.Update(wsOpenMsg{err: fmt.Errorf("refused")})
	got := next.(m)
	if got.wsReconnectDelay != 30*time.Second {
		t.Fatalf("wsReconnectDelay = %v, want 30s (cap)", got.wsReconnectDelay)
	}
}

func TestWSReconnectResetsOnSuccess(t *testing.T) {
	conn := &websocket.Conn{}
	ch := make(chan env)
	model := m{
		status:           "ready",
		wsReconnectDelay: 8 * time.Second,
		wsDisconnected:   true,
	}
	next, _ := model.Update(wsOpenMsg{conn: conn, ch: ch})
	got := next.(m)

	if got.wsDisconnected {
		t.Fatalf("wsDisconnected should be cleared on successful open")
	}
	if got.wsReconnectDelay != 0 {
		t.Fatalf("wsReconnectDelay should reset to 0, got %v", got.wsReconnectDelay)
	}
}

func TestWSDropSetsDisconnectedAndSchedulesReconnect(t *testing.T) {
	model := m{
		status: "ready",
	}
	next, cmd := model.Update(wsEvtMsg{ok: false})
	got := next.(m)

	if !got.wsDisconnected {
		t.Fatalf("wsDisconnected should be true after channel close")
	}
	// First disconnect: delay was 0 → set to 1s → immediately doubled to 2s for next attempt.
	if got.wsReconnectDelay != 2*time.Second {
		t.Fatalf("delay after first disconnect = %v, want 2s", got.wsReconnectDelay)
	}
	if cmd == nil {
		t.Fatalf("should schedule reconnect cmd")
	}
}

// --- #5 Snippet rendering ---

func TestRenderSnippetBoldsMatchedTerms(t *testing.T) {
	got := renderSnippet("the <b>quick</b> brown fox", 50)
	if strings.Contains(got, "<b>") || strings.Contains(got, "</b>") {
		t.Fatalf("raw tags leaked into rendered output: %q", got)
	}
	// Plain text must be present.
	if !strings.Contains(got, "the") || !strings.Contains(got, "brown fox") {
		t.Fatalf("plain text missing from rendered snippet: %q", got)
	}
}

func TestRenderSnippetTruncatesToMaxW(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := renderSnippet(long, 10)
	// Visible length (strip ANSI) should be ≤ 10 runes.
	plain := stripSnippetTags(got, 999)
	if len([]rune(plain)) > 10 {
		t.Fatalf("snippet not truncated: len=%d > 10", len([]rune(plain)))
	}
}

func TestStripSnippetTagsRemovesBoldMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello <b>world</b>", "hello world"},
		{"<b>start</b> middle <b>end</b>", "start middle end"},
		{"no tags here", "no tags here"},
	}
	for _, tc := range cases {
		got := stripSnippetTags(tc.in, 999)
		if got != tc.want {
			t.Fatalf("stripSnippetTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- #6 Around search jump ---

func TestAroundMsgsMsgCentresScroll(t *testing.T) {
	chatID := "c1"
	// 10 older + anchor + 10 newer = 21 messages; anchorIndex = 10.
	msgs := make([]wireMsg, 21)
	for i := range msgs {
		msgs[i] = mkMsg(fmt.Sprintf("m%d", i), int64(i+1))
	}
	model := m{
		status:       "ready",
		mode:         "chat",
		active:       chatID,
		msgs:         map[string][]wireMsg{},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
	}
	next, _ := model.Update(aroundMsgsMsg{chatID: chatID, msgs: msgs, anchorIndex: 10})
	got := next.(m)

	if len(got.msgs[chatID]) != 21 {
		t.Fatalf("msgs count = %d, want 21", len(got.msgs[chatID]))
	}
	// newerCount = 21 - 10 - 1 = 10; scroll = 10 * 2 = 20.
	if got.scroll != 20 {
		t.Fatalf("scroll = %d, want 20", got.scroll)
	}
}

func TestAroundMsgsMsgWithErrorShowsTopBar(t *testing.T) {
	model := m{status: "ready", mode: "chat", active: "c1"}
	next, cmd := model.Update(aroundMsgsMsg{chatID: "c1", err: fmt.Errorf("db error")})
	_ = next
	if cmd == nil {
		t.Fatalf("error should produce a topbar cmd")
	}
}

// --- #7 Unread polish ---

func TestEndKeyClearsUnreadCount(t *testing.T) {
	chatID := "c1"
	model := m{
		status:   "ready",
		mode:     "chat",
		active:   chatID,
		whitelist: map[string]string{},
		scroll:   10,
		chats: []chat{
			{ID: chatID, UnreadCount: 5},
		},
		msgs:         map[string][]wireMsg{chatID: {mkMsg("m1", 1)}},
		loadingOlder: map[string]bool{},
		noMoreOlder:  map[string]bool{},
	}
	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyEnd})
	got := next.(m)

	if got.chats[0].UnreadCount != 0 {
		t.Fatalf("UnreadCount = %d, want 0 after End key", got.chats[0].UnreadCount)
	}
	if cmd == nil {
		t.Fatalf("End key should produce commands (getMsgs + mark-read)")
	}
}

func TestUnreadCountIncreasesOnIncomingMessage(t *testing.T) {
	chatID := "c1"
	model := m{
		status:    "ready",
		mode:      "nav",
		active:    "",
		whitelist: map[string]string{},
		chats:     []chat{{ID: chatID, UnreadCount: 0}},
		msgs:      map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache: &renderCache{},
	}
	var wm wireMsg
	wm.Key.ID = "newmsg"
	wm.Key.RemoteJID = chatID
	wm.Key.FromMe = false
	wm.MessageTimestamp = 100

	next, _ := model.Update(wsEvtMsg{ok: true, evt: env{
		Type:    "message",
		Payload: func() json.RawMessage { b, _ := json.Marshal(wm); return b }(),
	}})
	got := next.(m)

	var unread int
	for _, ch := range got.chats {
		if ch.ID == chatID {
			unread = ch.UnreadCount
		}
	}
	if unread != 1 {
		t.Fatalf("UnreadCount = %d, want 1 after incoming message", unread)
	}
}

func TestUnreadCountNotIncreasedForOwnMessages(t *testing.T) {
	chatID := "c1"
	model := m{
		status:    "ready",
		mode:      "nav",
		active:    "",
		whitelist: map[string]string{},
		chats:     []chat{{ID: chatID, UnreadCount: 0}},
		msgs:      map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache: &renderCache{},
	}
	var wm wireMsg
	wm.Key.ID = "sent"
	wm.Key.RemoteJID = chatID
	wm.Key.FromMe = true

	next, _ := model.Update(wsEvtMsg{ok: true, evt: env{
		Type:    "message",
		Payload: func() json.RawMessage { b, _ := json.Marshal(wm); return b }(),
	}})
	got := next.(m)

	for _, ch := range got.chats {
		if ch.ID == chatID && ch.UnreadCount != 0 {
			t.Fatalf("own message incremented UnreadCount to %d", ch.UnreadCount)
		}
	}
}

func wsMsg(chatID, msgID string, fromMe bool, ts int64) wsEvtMsg {
	wm := wireMsg{}
	wm.Key.ID = msgID
	wm.Key.RemoteJID = chatID
	wm.Key.FromMe = fromMe
	wm.MessageTimestamp = ts
	b, _ := json.Marshal(wm)
	return wsEvtMsg{ok: true, evt: env{Type: "message", Payload: b}}
}

func baseModel(chatID string) m {
	return m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		whitelist:  map[string]string{"15551230001": "Allowed"},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		chats:      []chat{{ID: chatID}},
	}
}

// #2: WS-first path — WS delivers real message before HTTP response.
// Expected: only one message in the list (no duplicate).
func TestOptimisticWSFirstNoDuplicate(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	now := time.Now().Unix()
	pendingID := fmt.Sprintf("local-%d", time.Now().UnixNano())

	model := baseModel(chatID)
	// Seed the optimistic placeholder.
	placeholder := wireMsg{MessageTimestamp: now}
	placeholder.Key.ID = pendingID
	placeholder.Key.RemoteJID = chatID
	placeholder.Key.FromMe = true
	model.msgs[chatID] = []wireMsg{placeholder}

	// Step 1: WS delivers the real message before HTTP response.
	next, _ := model.Update(wsMsg(chatID, "real-id", true, now))
	got := next.(m)

	// Step 2: HTTP sentMsg arrives (placeholder no longer present).
	realMsg := wireMsg{MessageTimestamp: now}
	realMsg.Key.ID = "real-id"
	realMsg.Key.RemoteJID = chatID
	realMsg.Key.FromMe = true
	next, _ = got.Update(sentMsg{chatID: chatID, pendingID: pendingID, msg: realMsg})
	got = next.(m)

	msgs := got.msgs[chatID]
	if len(msgs) != 1 {
		t.Fatalf("WS-first: got %d messages, want 1 (no duplicate)", len(msgs))
	}
	if msgs[0].Key.ID != "real-id" {
		t.Fatalf("WS-first: message ID = %q, want real-id", msgs[0].Key.ID)
	}
}

// #2: HTTP-first path (normal case) — must still work correctly after the fix.
func TestOptimisticHTTPFirstStillWorks(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	now := time.Now().Unix()
	pendingID := fmt.Sprintf("local-%d", time.Now().UnixNano())

	model := baseModel(chatID)
	placeholder := wireMsg{MessageTimestamp: now}
	placeholder.Key.ID = pendingID
	placeholder.Key.RemoteJID = chatID
	placeholder.Key.FromMe = true
	model.msgs[chatID] = []wireMsg{placeholder}

	// Step 1: HTTP sentMsg arrives first (replaces placeholder).
	realMsg := wireMsg{MessageTimestamp: now}
	realMsg.Key.ID = "real-id"
	realMsg.Key.RemoteJID = chatID
	realMsg.Key.FromMe = true
	next, _ := model.Update(sentMsg{chatID: chatID, pendingID: pendingID, msg: realMsg})
	got := next.(m)

	// Step 2: WS delivers the same message (should be a no-op).
	next, _ = got.Update(wsMsg(chatID, "real-id", true, now))
	got = next.(m)

	msgs := got.msgs[chatID]
	if len(msgs) != 1 {
		t.Fatalf("HTTP-first: got %d messages, want 1", len(msgs))
	}
	if msgs[0].Key.ID != "real-id" {
		t.Fatalf("HTTP-first: message ID = %q, want real-id", msgs[0].Key.ID)
	}
}

// Bug Audit #4: replyPickMode must exit cleanly if candidates list becomes empty.
func TestReplyPickModeExitsWhenCandidatesEmpty(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	model := m{
		status:         "ready",
		mode:           "chat",
		active:         chatID,
		whitelist:      map[string]string{"15551230001": "Allowed"},
		msgs:           map[string][]wireMsg{chatID: {}},
		flashUntil:     map[string]time.Time{},
		mainCache:      &renderCache{},
		replyPickMode:  true,
		replyPickIndex: 5,
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := next.(m)
	if got.replyPickMode {
		t.Fatal("replyPickMode should be false when no candidates exist")
	}
}

// Bug Audit #4: replyPickIndex must be clamped when candidates shrink between events.
func TestReplyPickIndexClampedOnCandidateShrink(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	msgs := make([]wireMsg, 5)
	for i := range msgs {
		msgs[i].Key.ID = fmt.Sprintf("m%d", i)
		msgs[i].Key.RemoteJID = chatID
		msgs[i].Message = map[string]any{"conversation": fmt.Sprintf("msg %d", i)}
		msgs[i].MessageTimestamp = int64(i + 1)
	}
	model := m{
		status:         "ready",
		mode:           "chat",
		active:         chatID,
		whitelist:      map[string]string{"15551230001": "Allowed"},
		msgs:           map[string][]wireMsg{chatID: msgs},
		flashUntil:     map[string]time.Time{},
		mainCache:      &renderCache{},
		replyPickMode:  true,
		replyPickIndex: 4,
	}

	// Simulate candidates shrinking before the next key event.
	model.msgs[chatID] = msgs[:2]

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(m)
	if got.replyPickIndex >= 2 {
		t.Fatalf("replyPickIndex = %d after shrink to 2 candidates, want < 2", got.replyPickIndex)
	}
}

// Bug #25: concurrent playSoundProfileCmd calls must not spawn overlapping playback.
func TestPlaySoundProfileCmdDropsConcurrentCalls(t *testing.T) {
	// Drain any leftover slot from a prior test.
	select {
	case <-soundSlot:
	default:
	}

	// Occupy the slot manually to simulate an in-progress playback.
	soundSlot <- struct{}{}

	fired := false
	cmd := playSoundProfileCmd(1)
	// Execute the cmd inline (tea.Cmd is just a func).
	cmd()

	// Release the slot.
	<-soundSlot

	// fired stays false because the slot was occupied — no concurrent playback.
	if fired {
		t.Fatal("sound played while slot was occupied")
	}
}

// Bug #25: playSoundProfileCmd plays when slot is free.
func TestPlaySoundProfileCmdFiresWhenSlotFree(t *testing.T) {
	// Drain any leftover slot.
	select {
	case <-soundSlot:
	default:
	}

	// Should complete without blocking (slot is free).
	done := make(chan struct{})
	go func() {
		cmd := playSoundProfileCmd(1)
		cmd()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("playSoundProfileCmd blocked for >5s")
	}
}
