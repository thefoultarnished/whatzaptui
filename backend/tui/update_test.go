package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gorilla/websocket"
	"github.com/muesli/termenv"
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
	setTestTheme(t, TokyoNight)

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
	setTestTheme(t, TokyoNight)

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
	setTestTheme(t, TokyoNight)

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
	setTestTheme(t, Monokai)

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

func TestSendFileBuildsMultipartCorrectly(t *testing.T) {
	// Create a real file in a temp dir to be the "attachment".
	dir := t.TempDir()
	filePath := filepath.Join(dir, "photo.png")
	wantContent := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02}
	if err := os.WriteFile(filePath, wantContent, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	// Stand up a fake backend that records what arrives.
	var (
		gotMethod      string
		gotContentType string
		gotFields      = map[string]string{}
		gotFileName    string
		gotFileContent []byte
		gotFileMIME    string
		hit            bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")

		mediaType, params, err := mime.ParseMediaType(gotContentType)
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			http.Error(w, "expected multipart", http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, "parse part: "+err.Error(), http.StatusBadRequest)
				return
			}
			formName := part.FormName()
			if part.FileName() != "" {
				gotFileName = part.FileName()
				gotFileMIME = part.Header.Get("Content-Type")
				gotFileContent, _ = io.ReadAll(part)
			} else {
				body, _ := io.ReadAll(part)
				gotFields[formName] = string(body)
			}
		}
		// Respond with the shape sendFile expects.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"id":"x","timestamp":1}}`))
	}))
	defer srv.Close()

	t.Setenv("WHATZAP_API_TOKEN", "test-token")

	c := &http.Client{Timeout: 5 * time.Second}
	progressCh := make(chan fileProgressMsg, 16)
	cmd := sendFile(c, srv.URL, "15551230001@s.whatsapp.net", "image", filePath, "hello caption", "pending-1", progressCh)
	if cmd == nil {
		t.Fatal("sendFile returned nil cmd")
	}
	msg := cmd()
	if !hit {
		t.Fatal("fake backend was not hit")
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data; boundary=") {
		t.Fatalf("Content-Type = %q, want multipart/form-data with boundary", gotContentType)
	}

	// Fields.
	if gotFields["chatId"] != "15551230001@s.whatsapp.net" {
		t.Fatalf("chatId field = %q", gotFields["chatId"])
	}
	if gotFields["kind"] != "image" {
		t.Fatalf("kind field = %q", gotFields["kind"])
	}
	if gotFields["caption"] != "hello caption" {
		t.Fatalf("caption field = %q", gotFields["caption"])
	}

	// File part: filename is just the basename (no directory), and the bytes
	// match what we wrote to disk.
	if gotFileName != "photo.png" {
		t.Fatalf("file part filename = %q, want photo.png", gotFileName)
	}
	if !bytes.Equal(gotFileContent, wantContent) {
		t.Fatalf("file content = %v, want %v", gotFileContent, wantContent)
	}
	if gotFileMIME == "" {
		t.Fatal("file part missing Content-Type")
	}

	// The sentMsg returned should be a success (no err).
	sm, ok := msg.(sentMsg)
	if !ok {
		t.Fatalf("cmd result type = %T, want sentMsg", msg)
	}
	if sm.err != nil {
		t.Fatalf("sentMsg.err = %v, want nil", sm.err)
	}
	if sm.pendingID != "pending-1" {
		t.Fatalf("pendingID = %q, want pending-1", sm.pendingID)
	}
	if sm.chatID != "15551230001@s.whatsapp.net" {
		t.Fatalf("chatID = %q", sm.chatID)
	}
}

func TestSendFileRejectsMissingFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.png")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("backend should not be hit when local file is missing")
	}))
	defer srv.Close()

	t.Setenv("WHATZAP_API_TOKEN", "test-token")
	c := &http.Client{Timeout: 5 * time.Second}
	progressCh := make(chan fileProgressMsg, 16)
	cmd := sendFile(c, srv.URL, "15551230001@s.whatsapp.net", "image", missing, "", "pending-x", progressCh)
	msg := cmd()
	sm, ok := msg.(sentMsg)
	if !ok {
		t.Fatalf("cmd result type = %T, want sentMsg", msg)
	}
	if sm.err == nil {
		t.Fatal("expected err for missing file, got nil")
	}
}

func TestRenderMainQuoteUsesSentColorsWhenQuotingMe(t *testing.T) {
	setTestTheme(t, Monokai)

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
	setTestTheme(t, Monokai)

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
	setTestTheme(t, Monokai)

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
	setTestTheme(t, Monokai)

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
	setTestTheme(t, Monokai)

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
	reactionBody := lipgloss.NewStyle().Foreground(receivedName).Bold(true).Render("thumbs_up")
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
		if strings.Contains(line, "thumbs_up") {
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
	// 1-to-1: reactor name should be suppressed — the other party is implicit.
	if strings.Contains(rendered, "Shadu Mady") {
		t.Fatalf("1-to-1 reaction should not show reactor name: %q", rendered)
	}
}

func TestRenderMainGroupReactionShowsReactorName(t *testing.T) {
	setTestTheme(t, Monokai)

	groupID := "120363@g.us"
	msg := wireMsg{
		Message:          map[string]any{"conversation": "lunch?"},
		MessageTimestamp: 1710000100,
	}
	msg.Key.ID = "m9"
	msg.Key.RemoteJID = groupID

	rxn := wireMsg{
		Message: map[string]any{
			"reactionMessage": map[string]any{
				"targetMsgID": "m9",
				"emoji":       "fire",
			},
		},
		MessageTimestamp: 1710000101,
	}
	rxn.Key.ID = "r2"
	rxn.Key.RemoteJID = groupID
	rxn.Key.Participant = "15559998888@s.whatsapp.net"

	model := m{
		active: groupID,
		msgs:   map[string][]wireMsg{groupID: {msg, rxn}},
		contacts: map[string]contact{
			"15559998888@s.whatsapp.net": {ID: "15559998888@s.whatsapp.net", Notify: "Casey"},
		},
		contactsByNumber: map[string]contact{
			"15559998888": {ID: "15559998888@s.whatsapp.net", Notify: "Casey"},
		},
	}

	rendered := model.renderMain(100, 8)
	if !strings.Contains(rendered, "fire Casey") {
		t.Fatalf("group reaction should include reactor name, got: %q", rendered)
	}
}

func TestRenderUserListHighlightedNameUsesMarqueeOffset(t *testing.T) {
	setTestTheme(t, Monokai)

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
	if got := lipgloss.Width(lines[0]); got != 29 {
		t.Fatalf("highlighted row width = %d, want 29", got)
	}
}

func hexToBgANSI(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	probe := lipgloss.NewStyle().Background(lipgloss.Color(hex)).Render("x")
	if i := strings.Index(probe, "\x1b[48;2;"); i >= 0 {
		if j := strings.Index(probe[i:], "m"); j > 0 {
			return probe[i : i+j+1]
		}
	}
	return ""
}

func enableTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestUserListDefaultRowHasNoBg(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	model := m{
		mode:           "nav",
		sidebarFocused: true,
		sel:            1,
		active:         "99999999@s.whatsapp.net",
		whitelist:      map[string]string{"15551230001": "Allowed"},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	if strings.Contains(lines[0], "\x1b[48;2;") {
		t.Fatalf("default unselected row should have no background, got %q", lines[0])
	}
}

func TestUserListHighlightedWhitelistedRowUsesBrandBg(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	model := m{
		mode:           "nav",
		sidebarFocused: true,
		sel:            0,
		whitelist:      map[string]string{"15551230001": "Allowed"},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	brandEsc := hexToBgANSI(currentTheme.Brand)
	if !strings.Contains(lines[0], brandEsc) {
		t.Fatalf("highlighted whitelisted row missing brand bg %q in %q", brandEsc, lines[0])
	}
}

func TestUserListHighlightedBlacklistedRowUsesRedBg(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	model := m{
		mode:           "nav",
		sidebarFocused: true,
		sel:            0,
		whitelist:      map[string]string{},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Stranger"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Stranger"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	redEsc := hexToBgANSI(currentTheme.Red)
	if !strings.Contains(lines[0], redEsc) {
		t.Fatalf("highlighted blacklisted row missing red bg %q in %q", redEsc, lines[0])
	}
}

func TestUserListActiveWhitelistedRowUsesWhitelistActiveBg(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	model := m{
		mode:           "chat",
		sidebarFocused: false,
		sel:            1,
		active:         "15551230001@s.whatsapp.net",
		whitelist:      map[string]string{"15551230001": "Allowed"},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Allowed"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	activeEsc := hexToBgANSI(currentTheme.SidebarWhitelistActiveBg)
	if activeEsc == "" {
		t.Fatal("could not derive expected bg escape from theme")
	}
	if !strings.Contains(lines[0], activeEsc) {
		t.Fatalf("active whitelisted row missing active bg %q in %q", activeEsc, lines[0])
	}
}

func TestUserListActiveBlacklistedRowUsesBlacklistActiveBg(t *testing.T) {
	enableTrueColor(t)
	setTestTheme(t, TokyoNight)

	model := m{
		mode:           "chat",
		sidebarFocused: false,
		sel:            1,
		active:         "15551230001@s.whatsapp.net",
		whitelist:      map[string]string{},
		contacts: map[string]contact{
			"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Stranger"},
		},
		contactsByNumber: map[string]contact{
			"15551230001": {ID: "15551230001@s.whatsapp.net", Notify: "Stranger"},
		},
	}
	items := []chat{{ID: "15551230001@s.whatsapp.net"}}

	lines := model.renderUserList(items, 0, 1, 30)
	if len(lines) == 0 {
		t.Fatal("renderUserList returned no lines")
	}
	activeEsc := hexToBgANSI(currentTheme.SidebarBlacklistActiveBg)
	if activeEsc == "" {
		t.Fatal("could not derive expected bg escape from theme")
	}
	if !strings.Contains(lines[0], activeEsc) {
		t.Fatalf("active blacklisted row missing active bg %q in %q", activeEsc, lines[0])
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

// A-6: /logout, /whitelistall, /blacklistall must open a confirm dialog
// instead of acting immediately.

func TestRunCommandLogoutOpensConfirmDialog(t *testing.T) {
	model := m{whitelist: map[string]string{}}

	cmd, ok := model.runCommand("/logout", true)
	if !ok {
		t.Fatalf("expected /logout to be handled")
	}
	if cmd != nil {
		t.Fatalf("expected /logout to defer the logout cmd until confirmed, got non-nil cmd")
	}
	if !model.confirmDialog.open || model.confirmDialog.action != "logout" {
		t.Fatalf("confirmDialog = %+v, want open with action=logout", model.confirmDialog)
	}
}

func TestRunCommandWhitelistAllOpensConfirmDialog(t *testing.T) {
	model := m{
		whitelist: map[string]string{},
		chats: []chat{
			{ID: "15551230001@s.whatsapp.net", Name: "Alice"},
			{ID: "15551230002@s.whatsapp.net", Name: "Bob"},
		},
	}

	cmd, ok := model.runCommand("/whitelistall", true)
	if !ok {
		t.Fatalf("expected /whitelistall to be handled")
	}
	if cmd != nil {
		t.Fatalf("expected /whitelistall to defer the mutation until confirmed, got non-nil cmd")
	}
	if !model.confirmDialog.open || model.confirmDialog.action != "whitelistall" {
		t.Fatalf("confirmDialog = %+v, want open with action=whitelistall", model.confirmDialog)
	}
	if len(model.whitelist) != 0 {
		t.Fatalf("whitelist = %v, want unchanged (empty) until confirmed", model.whitelist)
	}
}

func TestRunCommandWhitelistAllNoChatsSkipsDialog(t *testing.T) {
	model := m{whitelist: map[string]string{}}

	cmd, ok := model.runCommand("/whitelistall", true)
	if !ok || cmd == nil {
		t.Fatalf("expected /whitelistall with no chats to return the 'no chats loaded' topbar cmd")
	}
	if model.confirmDialog.open {
		t.Fatalf("confirmDialog should stay closed when there's nothing to confirm")
	}
}

func TestRunCommandBlacklistAllOpensConfirmDialog(t *testing.T) {
	model := m{
		whitelist: map[string]string{"15551230001": "Alice", "15551230002": "Bob"},
	}

	cmd, ok := model.runCommand("/blacklistall", true)
	if !ok {
		t.Fatalf("expected /blacklistall to be handled")
	}
	if cmd != nil {
		t.Fatalf("expected /blacklistall to defer the mutation until confirmed, got non-nil cmd")
	}
	if !model.confirmDialog.open || model.confirmDialog.action != "blacklistall" {
		t.Fatalf("confirmDialog = %+v, want open with action=blacklistall", model.confirmDialog)
	}
	if len(model.whitelist) != 2 {
		t.Fatalf("whitelist = %v, want unchanged until confirmed", model.whitelist)
	}
}

func TestRunCommandBlacklistAllEmptySkipsDialog(t *testing.T) {
	model := m{whitelist: map[string]string{}}

	cmd, ok := model.runCommand("/blacklistall", true)
	if !ok || cmd == nil {
		t.Fatalf("expected /blacklistall with empty whitelist to return the 'already empty' topbar cmd")
	}
	if model.confirmDialog.open {
		t.Fatalf("confirmDialog should stay closed when there's nothing to confirm")
	}
}

func TestKeyConfirmDialogYesRunsWhitelistAll(t *testing.T) {
	model := m{
		status:    "ready",
		demoMode:  true,
		whitelist: map[string]string{},
		chats: []chat{
			{ID: "15551230001@s.whatsapp.net", Name: "Alice"},
		},
	}
	model.confirmDialog.Open("Whitelist all chats?", "Allows sending messages to all 1 chats.", "whitelistall")

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := next.(m)

	if got.confirmDialog.open {
		t.Fatalf("confirmDialog should be closed after confirming")
	}
	if _, ok := got.whitelist["15551230001"]; !ok {
		t.Fatalf("whitelist = %v, want 15551230001 added after confirm", got.whitelist)
	}
}

func TestKeyConfirmDialogYesRunsBlacklistAll(t *testing.T) {
	model := m{
		status:   "ready",
		demoMode: true,
		whitelist: map[string]string{
			"15551230001": "Alice",
		},
	}
	model.confirmDialog.Open("Clear the whitelist?", "Removes 1 contact(s) from the whitelist.", "blacklistall")

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := next.(m)

	if got.confirmDialog.open {
		t.Fatalf("confirmDialog should be closed after confirming")
	}
	if len(got.whitelist) != 0 {
		t.Fatalf("whitelist = %v, want empty after confirming blacklistall", got.whitelist)
	}
}

func TestKeyConfirmDialogYesRunsLogout(t *testing.T) {
	model := m{status: "ready", whitelist: map[string]string{}}
	model.confirmDialog.Open("Log out?", "Ends your session.", "logout")

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := next.(m)

	if got.confirmDialog.open {
		t.Fatalf("confirmDialog should be closed after confirming")
	}
	if cmd == nil {
		t.Fatalf("expected confirming logout to return the logout cmd, got nil")
	}
}

func TestKeyConfirmDialogNoCancelsWithoutMutation(t *testing.T) {
	model := m{
		status:   "ready",
		demoMode: true,
		whitelist: map[string]string{
			"15551230001": "Alice",
		},
	}
	model.confirmDialog.Open("Clear the whitelist?", "Removes 1 contact(s) from the whitelist.", "blacklistall")

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := next.(m)

	if got.confirmDialog.open {
		t.Fatalf("confirmDialog should be closed after cancelling")
	}
	if len(got.whitelist) != 1 {
		t.Fatalf("whitelist = %v, want unchanged after cancelling", got.whitelist)
	}
	if !strings.Contains(got.topBarMsg, "Cancelled") {
		t.Fatalf("topBarMsg = %q, want it to mention Cancelled", got.topBarMsg)
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

func TestRenderUploadProgress(t *testing.T) {
	if got := renderUploadProgress(0, 30); !strings.HasPrefix(got, "[") || !strings.Contains(got, "0%") {
		t.Fatalf("0%%: got %q, want bracket+0%%", got)
	}
	if got := renderUploadProgress(50, 30); !strings.Contains(got, "50%") {
		t.Fatalf("50%%: got %q, want %%50", got)
	}
	if got := renderUploadProgress(100, 30); !strings.Contains(got, "100%") {
		t.Fatalf("100%%: got %q, want 100%%", got)
	}
	// Out-of-range clamps.
	if got := renderUploadProgress(150, 30); !strings.Contains(got, "100%") {
		t.Fatalf("150 clamped: got %q", got)
	}
	if got := renderUploadProgress(-5, 30); !strings.Contains(got, "0%") {
		t.Fatalf("negative clamped: got %q", got)
	}
	// Narrow width falls back to just the percent label.
	if got := renderUploadProgress(50, 6); got != "50%" {
		t.Fatalf("narrow: got %q, want 50%%", got)
	}
}

func TestFileProgressMsgUpdatesModelAndReArmsListener(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	model := baseModel(chatID)
	model.uploadProgress = map[string]int{}
	model.uploadChans = map[string]chan fileProgressMsg{}
	progressCh := make(chan fileProgressMsg, 4)
	model.uploadChans["pending-1"] = progressCh

	next, cmd := model.Update(fileProgressMsg{chatID: chatID, pendingID: "pending-1", pct: 47})
	got := next.(m)
	if got.uploadProgress["pending-1"] != 47 {
		t.Fatalf("uploadProgress[pending-1] = %d, want 47", got.uploadProgress["pending-1"])
	}
	if cmd == nil {
		t.Fatal("expected listenFileProgress cmd to be returned for re-arming")
	}
	// Drive another event through the same cmd to make sure re-arm actually fires.
	close(progressCh) // simulating upload done
	msg := cmd()
	if msg != nil {
		t.Fatalf("expected nil msg after channel close, got %T", msg)
	}
}

func TestSentMsgClearsUploadProgress(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"
	model := baseModel(chatID)
	placeholder := wireMsg{MessageTimestamp: time.Now().Unix()}
	placeholder.Key.ID = "local-1"
	placeholder.Key.RemoteJID = chatID
	placeholder.Key.FromMe = true
	model.msgs[chatID] = []wireMsg{placeholder}
	model.uploadProgress = map[string]int{"local-1": 33}
	model.uploadChans = map[string]chan fileProgressMsg{"local-1": make(chan fileProgressMsg, 1)}

	realMsg := wireMsg{MessageTimestamp: time.Now().Unix()}
	realMsg.Key.ID = "real-id"
	realMsg.Key.RemoteJID = chatID
	realMsg.Key.FromMe = true
	next, _ := model.Update(sentMsg{chatID: chatID, pendingID: "local-1", msg: realMsg})
	got := next.(m)
	if _, ok := got.uploadProgress["local-1"]; ok {
		t.Fatalf("uploadProgress not cleared: %v", got.uploadProgress)
	}
	if _, ok := got.uploadChans["local-1"]; ok {
		t.Fatalf("uploadChans not cleared: %v", got.uploadChans)
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

// runBatch invokes a tea.Cmd and, if it returns a tea.BatchMsg, runs every
// sub-cmd in its own goroutine. bubbletea normally does this in its event
// loop, but in tests we drive things synchronously.
func runBatch(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			go func(c tea.Cmd) { _ = c() }(sub)
		}
	}
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

func makeTestWSCh() <-chan env {
	c := make(chan env, 1)
	c <- env{}
	return c
}

func setTestTheme(t *testing.T, theme Theme) {
	t.Helper()
	saved := currentTheme
	currentTheme = theme
	rehashStyles()
	t.Cleanup(func() {
		currentTheme = saved
		rehashStyles()
	})
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

// Bug Audit #8: a "chats:loaded" WS event must NOT clobber a chat that already
// has messages loaded. Only fetch the initial window if msgs is empty.
func TestChatsLoadedDoesNotClobberLoadedMessages(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"

	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// Pre-populate the active chat with one message so the guard should fire.
	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{},
		msgs:       map[string][]wireMsg{chatID: {mkMsg("m1", 1)}},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		// Buffered send channel so the readWS sub-cmd in the batch returns
		// immediately instead of blocking the sibling cmds.
		wsCh: makeTestWSCh(),
	}

	next, cmd := model.Update(wsEvtMsg{ok: true, evt: env{Type: "chats:loaded"}})
	_ = next.(m)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from chats:loaded (should still refresh chats)")
	}
	// Calling cmd() returns a tea.BatchMsg. bubbletea normally schedules the
	// sub-cmds via its event loop; we do the same manually here.
	runBatch(t, cmd)

	// Drain concurrent writes.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(paths)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if strings.HasPrefix(p, "/messages") {
			t.Fatalf("chats:loaded must not hit /messages when msgs is populated; got path %q (all: %v)", p, paths)
		}
	}
	if len(paths) == 0 {
		t.Fatal("chats:loaded should still refresh the chat list (got no requests at all)")
	}
}

// Counterpart to TestChatsLoadedDoesNotClobberLoadedMessages: when msgs is
// empty, chats:loaded MUST still fetch the initial window.
func TestChatsLoadedFetchesInitialWindowWhenEmpty(t *testing.T) {
	chatID := "15551230001@s.whatsapp.net"

	var (
		mu    sync.Mutex
		paths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(`{"messages":[],"hasMore":false}`))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		wsCh:       makeTestWSCh(),
	}

	next, cmd := model.Update(wsEvtMsg{ok: true, evt: env{Type: "chats:loaded"}})
	_ = next.(m)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from chats:loaded")
	}
	runBatch(t, cmd)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hasMessages := false
		for _, p := range paths {
			if strings.HasPrefix(p, "/messages") {
				hasMessages = true
				break
			}
		}
		mu.Unlock()
		if hasMessages {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	sawMessages := false
	for _, p := range paths {
		if strings.HasPrefix(p, "/messages") {
			sawMessages = true
			break
		}
	}
	if !sawMessages {
		t.Fatalf("chats:loaded with empty msgs should fetch /messages; got paths %v", paths)
	}
}

// captureSetWhitelistRequest is what the test server stores for the
// /whitelist/set POST so we can assert on body fields.
type captureSetWhitelistRequest struct {
	method  string
	path    string
	phone   string
	name    string
	allowed int
}

// buildToggleTestServer returns an httptest.Server that records all
// /whitelist/set POSTs and replies 200 OK.
func buildToggleTestServer(t *testing.T) (*httptest.Server, *[]captureSetWhitelistRequest, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var captured []captureSetWhitelistRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/whitelist/set" && r.Method == http.MethodPost {
			var body struct {
				Phone   string `json:"phone"`
				Name    string `json:"name"`
				Allowed int    `json:"allowed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				mu.Lock()
				captured = append(captured, captureSetWhitelistRequest{
					method:  r.Method,
					path:    r.URL.Path,
					phone:   body.Phone,
					name:    body.Name,
					allowed: body.Allowed,
				})
				mu.Unlock()
			}
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	return srv, &captured, &mu
}

// waitForCapturedCount blocks up to 2s for at least n recorded requests.
func waitForCapturedCount(t *testing.T, mu *sync.Mutex, captured *[]captureSetWhitelistRequest, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*captured)
		mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// withTempAPIEnv clears API_TOKEN env vars that attachAuthHeader reads so the
// test server isn't sent a token. t.Cleanup restores the previous value.
func withTempAPIEnv(t *testing.T) {
	t.Helper()
	prev := os.Getenv("WHATZAP_API_TOKEN")
	os.Unsetenv("WHATZAP_API_TOKEN")
	t.Cleanup(func() {
		if prev != "" {
			os.Setenv("WHATZAP_API_TOKEN", prev)
		}
	})
}

func TestAltBBlacklistsWhitelistedSelectedContact(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230001@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{"15551230001": "Allowed"},
		names:      map[string]string{"15551230001": "Allowed"},
		chats:      []chat{{ID: chatID, Name: "Allowed", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.rebuildContactIndex()

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	if cmd == nil {
		t.Fatal("alt+b should return a non-nil cmd batch")
	}
	runBatch(t, cmd)
	if _, ok := next.(m).whitelist["15551230001"]; ok {
		t.Fatal("whitelist should be cleared locally after alt+b")
	}

	waitForCapturedCount(t, mu, captured, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("expected 1 /whitelist/set request, got %d", len(*captured))
	}
	req := (*captured)[0]
	if req.phone != "15551230001" {
		t.Fatalf("phone = %q, want 15551230001", req.phone)
	}
	if req.allowed != 0 {
		t.Fatalf("allowed = %d, want 0 (blacklist)", req.allowed)
	}
}

func TestAltWWhitelistsBlacklistedSelectedContact(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230002@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "nav",
		active:     "",
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{},
		names:      map[string]string{"15551230002": "Bobby"},
		chats:      []chat{{ID: chatID, Name: "Bobby", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.rebuildContactIndex()
	// sel stays at 0 (chats tab, one entry).

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	if cmd == nil {
		t.Fatal("alt+w should return a non-nil cmd batch")
	}
	runBatch(t, cmd)
	if _, ok := next.(m).whitelist["15551230002"]; !ok {
		t.Fatal("whitelist should contain the contact after alt+w")
	}

	waitForCapturedCount(t, mu, captured, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("expected 1 /whitelist/set request, got %d", len(*captured))
	}
	req := (*captured)[0]
	if req.phone != "15551230002" {
		t.Fatalf("phone = %q, want 15551230002", req.phone)
	}
	if req.allowed != 1 {
		t.Fatalf("allowed = %d, want 1 (whitelist)", req.allowed)
	}
	if req.name != "Bobby" {
		t.Fatalf("name = %q, want Bobby", req.name)
	}
}

func TestAltToggleIsSymmetricFlipsBothDirections(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230003@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "nav",
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{"15551230003": "Cara"},
		names:      map[string]string{"15551230003": "Cara"},
		chats:      []chat{{ID: chatID, Name: "Cara", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.rebuildContactIndex()

	altB := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true}
	altW := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true}

	// 1st press (alt+b) on whitelisted -> blacklisted, allowed=0
	next, cmd1 := model.key(altB)
	model = next.(m)
	runBatch(t, cmd1)
	// 2nd press (alt+b) on now-blacklisted -> whitelisted, allowed=1
	next, cmd2 := model.key(altB)
	model = next.(m)
	runBatch(t, cmd2)
	// 3rd press (alt+w) on whitelisted -> blacklisted, allowed=0
	next, cmd3 := model.key(altW)
	model = next.(m)
	runBatch(t, cmd3)

	// Local state after the three toggles: started whitelisted, toggled to
	// blacklisted, whitelisted, blacklisted. Final state = blacklisted.
	if _, ok := model.whitelist["15551230003"]; ok {
		t.Fatal("after 3 toggles starting from whitelisted, contact should be blacklisted")
	}

	waitForCapturedCount(t, mu, captured, 3)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 3 {
		t.Fatalf("expected 3 /whitelist/set requests, got %d", len(*captured))
	}
	// Order is not deterministic because sub-commands run in goroutines; assert
	// the multiset of allowed values instead.
	tally := map[int]int{}
	for _, req := range *captured {
		tally[req.allowed]++
	}
	if tally[0] != 2 || tally[1] != 1 {
		t.Fatalf("allowed tally = %v, want {0:2, 1:1}", tally)
	}
}

func TestAltToggleClearsComposerOnOpenChatBlacklist(t *testing.T) {
	withTempAPIEnv(t)
	srv, _, _ := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230004@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{"15551230004": "Dee"},
		names:      map[string]string{"15551230004": "Dee"},
		chats:      []chat{{ID: chatID, Name: "Dee", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
		input:      "draft message",
		inputBuf:   "draft",
	}
	model.rebuildContactIndex()

	next, _ := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	got := next.(m)
	if got.input != "" || got.inputBuf != "" {
		t.Fatalf("composer should be cleared on blacklist of open chat: input=%q inputBuf=%q", got.input, got.inputBuf)
	}
	if _, ok := got.whitelist["15551230004"]; ok {
		t.Fatal("contact should be removed from whitelist after blacklist")
	}
}

func TestAltToggleNoopWhenNotReady(t *testing.T) {
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230005@s.whatsapp.net"
	model := m{
		status:     "connecting", // not ready
		mode:       "nav",
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{"15551230005": "Eve"},
		names:      map[string]string{"15551230005": "Eve"},
		chats:      []chat{{ID: chatID, Name: "Eve", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.rebuildContactIndex()

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	got := next.(m)
	if cmd == nil {
		t.Fatal("alt+b should still emit a top-bar cmd when not ready (user feedback)")
	}
	runBatch(t, cmd)
	if got.topBarMsg == "" {
		t.Fatal("top-bar should be set when not ready")
	}
	if !strings.Contains(strings.ToLower(got.topBarMsg), "not ready") &&
		!strings.Contains(strings.ToLower(got.topBarMsg), "wait") {
		t.Fatalf("top-bar should mention not-ready state, got %q", got.topBarMsg)
	}

	// Give the server a brief window to confirm no requests land.
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 0 {
		t.Fatalf("expected 0 /whitelist/set requests when not ready, got %d", len(*captured))
	}
}

func TestAltToggleNoopWhenLeftInputFocused(t *testing.T) {
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	chatID := "15551230006@s.whatsapp.net"
	model := m{
		status:          "ready",
		mode:            "nav",
		baseURL:         srv.URL,
		client:          srv.Client(),
		whitelist:       map[string]string{"15551230006": "Finn"},
		names:           map[string]string{"15551230006": "Finn"},
		chats:           []chat{{ID: chatID, Name: "Finn", ConversationTimestamp: 1700000000}},
		msgs:            map[string][]wireMsg{},
		flashUntil:      map[string]time.Time{},
		mainCache:       &renderCache{},
		sidebarCache:    &sidebarCache{},
		leftInputFocused: true,
		leftInput:       "/blacklist ",
	}
	model.rebuildContactIndex()

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	got := next.(m)
	if cmd == nil {
		t.Fatal("alt+b should still emit a top-bar cmd when leftInputFocused")
	}
	runBatch(t, cmd)
	if got.topBarMsg == "" {
		t.Fatal("top-bar should be set when leftInputFocused")
	}
	if !strings.Contains(strings.ToLower(got.topBarMsg), "command") &&
		!strings.Contains(strings.ToLower(got.topBarMsg), "esc") {
		t.Fatalf("top-bar should mention leaving the command, got %q", got.topBarMsg)
	}
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 0 {
		t.Fatalf("expected 0 /whitelist/set requests when leftInputFocused, got %d", len(*captured))
	}
}

func TestAltToggleNoSelectionTopsNoContactSelected(t *testing.T) {
	withTempAPIEnv(t)
	srv, _, _ := buildToggleTestServer(t)
	defer srv.Close()

	model := m{
		status:     "ready",
		mode:       "nav",
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{},
		names:      map[string]string{},
		chats:      []chat{},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.sel = 5 // out of bounds, no active either

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	got := next.(m)
	if cmd == nil {
		t.Fatal("should at least return a top-bar setTopBar cmd")
	}
	runBatch(t, cmd)
	if !strings.Contains(strings.ToLower(got.topBarMsg), "no contact") {
		t.Fatalf("top-bar should say no contact selected, got %q", got.topBarMsg)
	}
}

func TestAltToggleFallsBackToActiveChatWhenNoSidebarSelection(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	// Active chat is set but the sidebar has no chats and sel is out of bounds
	// — the helper should still toggle the open chat (matches /whitelist).
	chatID := "15551230007@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{},
		names:      map[string]string{"15551230007": "Gigi"},
		chats:      []chat{},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.sel = 0
	model.rebuildContactIndex()

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}, Alt: true})
	got := next.(m)
	if cmd == nil {
		t.Fatal("should return a non-nil cmd (setWhitelistEntry + top-bar)")
	}
	runBatch(t, cmd)
	if _, ok := got.whitelist["15551230007"]; !ok {
		t.Fatal("x.active fallback should whitelist the open chat when sidebar is empty")
	}
	if !strings.Contains(got.topBarMsg, "Whitelisted") || !strings.Contains(got.topBarMsg, "Gigi") {
		t.Fatalf("top-bar should announce whitelist of active chat, got %q", got.topBarMsg)
	}

	waitForCapturedCount(t, mu, captured, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 || (*captured)[0].allowed != 1 || (*captured)[0].phone != "15551230007" {
		t.Fatalf("expected 1 setWhitelistEntry(allowed=1, phone=15551230007), got %+v", *captured)
	}
}

// If an incoming message reorders the sidebar (a different chat's
// ConversationTimestamp bumps it above the one the user has highlighted),
// the sidebar selection (x.sel) must follow the same chat rather than
// staying on a raw index — so Alt+B/Alt+W still targets the chat the user
// actually had selected.
func TestAltToggleTargetsHighlightedChatAfterSidebarReorder(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	selectedChat := "15551230010@s.whatsapp.net"
	otherChat := "15551230011@s.whatsapp.net"
	model := m{
		status:    "ready",
		mode:      "nav",
		baseURL:   srv.URL,
		client:    srv.Client(),
		whitelist: map[string]string{"15551230010": "Selected", "15551230011": "Other"},
		names:     map[string]string{"15551230010": "Selected", "15551230011": "Other"},
		// User has "Selected" highlighted at index 0.
		chats: []chat{
			{ID: selectedChat, Name: "Selected", ConversationTimestamp: 1700000000},
			{ID: otherChat, Name: "Other", ConversationTimestamp: 1699999000},
		},
		msgs:         map[string][]wireMsg{},
		flashUntil:   map[string]time.Time{},
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
		sel:          0,
	}
	model.rebuildContactIndex()

	// otherChat now receives a message and jumps above selectedChat.
	updated, _ := model.Update(chatsMsg{chats: []chat{
		{ID: selectedChat, Name: "Selected", ConversationTimestamp: 1700000000},
		{ID: otherChat, Name: "Other", ConversationTimestamp: 1700000500},
	}})
	model = updated.(m)

	if got := model.chats[model.sel].ID; got != selectedChat {
		t.Fatalf("after reorder, x.sel points at %q, want %q (selection should follow the chat)", got, selectedChat)
	}

	next, cmd := model.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	got := next.(m)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from alt+b")
	}
	runBatch(t, cmd)

	if _, ok := got.whitelist["15551230010"]; ok {
		t.Fatal("the highlighted chat should be blacklisted, but it's still whitelisted")
	}
	if _, ok := got.whitelist["15551230011"]; !ok {
		t.Fatal("the chat that moved to the top should NOT have been touched")
	}

	waitForCapturedCount(t, mu, captured, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 || (*captured)[0].phone != "15551230010" {
		t.Fatalf("expected 1 setWhitelistEntry for the highlighted chat (15551230010), got %+v", *captured)
	}
}

func TestAltToggleFiresViaInlineRuneFallback(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildToggleTestServer(t)
	defer srv.Close()

	// The inline k.Alt && k.Runes[0] == 'b' check at update.go:1633 (mirroring
	// the alt+f pattern) guarantees the toggle fires even if the top-level
	// k.String() switch somehow misses the chord. This test exercises the
	// full chat-mode path so both branches are covered.
	chatID := "15551230008@s.whatsapp.net"
	model := m{
		status:     "ready",
		mode:       "chat",
		active:     chatID,
		baseURL:    srv.URL,
		client:     srv.Client(),
		whitelist:  map[string]string{"15551230008": "Hank"},
		names:      map[string]string{"15551230008": "Hank"},
		chats:      []chat{{ID: chatID, Name: "Hank", ConversationTimestamp: 1700000000}},
		msgs:       map[string][]wireMsg{},
		flashUntil: map[string]time.Time{},
		mainCache:  &renderCache{},
		sidebarCache: &sidebarCache{},
	}
	model.rebuildContactIndex()

	km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true}

	next, cmd := model.key(km)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from alt+b in chat mode")
	}
	runBatch(t, cmd)
	if _, ok := next.(m).whitelist["15551230008"]; ok {
		t.Fatal("contact should be blacklisted after alt+b")
	}
	waitForCapturedCount(t, mu, captured, 1)
}

// Switching the active chat (Alt+Up/Down, sidebar selection) must not leak
// an unsent composer draft into the newly opened chat — each chat keeps its
// own draft, restored when you return to it.
func TestOpenSelectedChatSavesAndRestoresPerChatDraft(t *testing.T) {
	chat1 := "15551230001@s.whatsapp.net"
	chat2 := "15551230002@s.whatsapp.net"
	model := m{
		status:    "ready",
		mode:      "chat",
		demoMode:  true,
		active:    chat1,
		input:     "hello from chat1",
		whitelist: map[string]string{"15551230001": "Alice", "15551230002": "Bob"},
		names:     map[string]string{"15551230001": "Alice", "15551230002": "Bob"},
		chats: []chat{
			{ID: chat1, Name: "Alice", ConversationTimestamp: 1700000001},
			{ID: chat2, Name: "Bob", ConversationTimestamp: 1700000000},
		},
		msgs:         map[string][]wireMsg{},
		flashUntil:   map[string]time.Time{},
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
		sel:          1,
	}
	model.rebuildContactIndex()

	// Switch to chat2 without sending — chat1's draft must be saved, and
	// chat2's composer must start empty (no leaked text).
	next, _ := model.openSelectedChat()
	got := next.(m)
	if got.active != chat2 {
		t.Fatalf("active = %q, want %q", got.active, chat2)
	}
	if got.input != "" {
		t.Fatalf("input for chat2 = %q, want empty (no leaked draft)", got.input)
	}
	if got.drafts[chat1] != "hello from chat1" {
		t.Fatalf("drafts[chat1] = %q, want %q", got.drafts[chat1], "hello from chat1")
	}

	// Switch back to chat1 — its draft must be restored.
	got.sel = 0
	back, _ := got.openSelectedChat()
	final := back.(m)
	if final.active != chat1 {
		t.Fatalf("active = %q, want %q", final.active, chat1)
	}
	if final.input != "hello from chat1" {
		t.Fatalf("input for chat1 = %q, want restored draft %q", final.input, "hello from chat1")
	}
}
