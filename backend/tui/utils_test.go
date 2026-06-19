package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Config path check.
func TestSaveAndLoadConfigUsesOverride(t *testing.T) {
	t.Setenv("WHATZAP_DATA_DIR", t.TempDir())

	currentConfig = Config{
		ThemeName:    "aurora",
		MouseEnabled: false,
		SoundEnabled: false,
		SoundProfile: 5,
	}
	saveConfig()

	currentConfig = Config{}
	loadConfig()

	if currentConfig.ThemeName != "aurora" {
		t.Fatalf("theme name = %q, want aurora", currentConfig.ThemeName)
	}
	if currentConfig.MouseEnabled {
		t.Fatalf("mouse enabled = true, want false")
	}
	if currentConfig.SoundEnabled {
		t.Fatalf("sound enabled = true, want false")
	}
	if currentConfig.SoundProfile != 5 {
		t.Fatalf("sound profile = %d, want 5", currentConfig.SoundProfile)
	}
}

func TestRenderMessageBodyProtocolMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  map[string]any
		want string
	}{
		{
			name: "revoke",
			msg: map[string]any{
				"protocolMessage": map[string]any{"type": "REVOKE"},
			},
			want: "[message deleted]",
		},
		{
			name: "edit",
			msg: map[string]any{
				"protocolMessage": map[string]any{"type": "MESSAGE_EDIT", "editedText": "fixed text"},
			},
			want: "[message edited] fixed text",
		},
		{
			name: "ephemeral setting",
			msg: map[string]any{
				"protocolMessage": map[string]any{"type": "EPHEMERAL_SETTING", "ephemeralExpiration": float64(604800)},
			},
			want: "[disappearing messages] 7d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderMessageBody(tt.msg); got != tt.want {
				t.Fatalf("renderMessageBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrapTextWithPrefixReservesFirstLineWidth(t *testing.T) {
	got := wrapTextWithPrefix("alpha beta gamma delta", 13, 8)
	want := "alpha\nbeta gamma\ndelta"
	if got != want {
		t.Fatalf("wrapTextWithPrefix() = %q, want %q", got, want)
	}
}

func TestSanitizeOutgoingTextPreservesNewlines(t *testing.T) {
	got := sanitizeOutgoingText("hello\r\nworld\ragain")
	want := "hello\nworld\nagain"
	if got != want {
		t.Fatalf("sanitizeOutgoingText() = %q, want %q", got, want)
	}
}

func TestSanitizeIncomingTextStripsEscapeSequences(t *testing.T) {
	got := sanitizeIncomingText("\x1b[31mred\x1b[0m")
	want := "[31mred[0m"
	if got != want {
		t.Fatalf("sanitizeIncomingText() = %q, want %q", got, want)
	}
}

func TestSanitizeIncomingTextStripsOSCSequence(t *testing.T) {
	got := sanitizeIncomingText("\x1b]0;evil title\x07hi")
	want := "]0;evil titlehi"
	if got != want {
		t.Fatalf("sanitizeIncomingText() = %q, want %q", got, want)
	}
}

func TestSanitizeIncomingTextStripsC1Controls(t *testing.T) {
	got := sanitizeIncomingText("abc")
	want := "abc"
	if got != want {
		t.Fatalf("sanitizeIncomingText() = %q, want %q", got, want)
	}
}

func TestSanitizeIncomingTextPreservesEmojiAndZWJ(t *testing.T) {
	for _, s := range []string{
		"👨‍👩‍👧‍👦", // family emoji built from ZWJ joiners
		"❤️",                // heart + variation selector
	} {
		if got := sanitizeIncomingText(s); got != s {
			t.Fatalf("sanitizeIncomingText(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestSanitizeIncomingTextPreservesCombiningMarks(t *testing.T) {
	s := "زَرٰ"
	if got := sanitizeIncomingText(s); got != s {
		t.Fatalf("sanitizeIncomingText(%q) = %q, want unchanged", s, got)
	}
}

func TestSanitizeIncomingTextNormalizesCRLF(t *testing.T) {
	got := sanitizeIncomingText("hello\r\nworld\ragain")
	want := "hello\nworld\nagain"
	if got != want {
		t.Fatalf("sanitizeIncomingText() = %q, want %q", got, want)
	}
}

func TestWireMsgUnmarshalJSONSanitizesMessageFields(t *testing.T) {
	esc := "\x1b"
	type wireKeyJSON struct {
		ID        string `json:"id"`
		RemoteJID string `json:"remoteJid"`
		FromMe    bool   `json:"fromMe"`
	}
	type wireMsgJSON struct {
		Key              wireKeyJSON    `json:"key"`
		MessageTimestamp int64          `json:"messageTimestamp"`
		PushName         string         `json:"pushName"`
		Message          map[string]any `json:"message"`
	}
	src := wireMsgJSON{
		Key:              wireKeyJSON{ID: "ABC123", RemoteJID: "123@s.whatsapp.net"},
		MessageTimestamp: 1700000000,
		PushName:         "evil" + esc + "[31mname",
		Message: map[string]any{
			"conversation": "hi" + esc + "[31mthere",
			"extendedTextMessage": map[string]any{
				"text":       "fancy" + esc + "[0mtext",
				"quotedText": "quoted" + esc + "]0;pwnedtext",
			},
		},
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var w wireMsg
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if w.PushName != "evil[31mname" {
		t.Fatalf("PushName = %q", w.PushName)
	}
	if got := w.Message["conversation"]; got != "hi[31mthere" {
		t.Fatalf("conversation = %q", got)
	}
	ext, _ := w.Message["extendedTextMessage"].(map[string]any)
	if got := ext["text"]; got != "fancy[0mtext" {
		t.Fatalf("extendedTextMessage.text = %q", got)
	}
	if got := ext["quotedText"]; got != "quoted]0;pwnedtext" {
		t.Fatalf("quotedText = %q", got)
	}
}

func TestWireMsgUnmarshalJSONPreservesNonStringFields(t *testing.T) {
	raw := `{
		"key": {"id": "ABC123", "remoteJid": "123@s.whatsapp.net", "fromMe": true, "participant": "456@s.whatsapp.net"},
		"messageTimestamp": 1700000000,
		"message": {"conversation": "hello"}
	}`
	var w wireMsg
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if w.Key.ID != "ABC123" || w.Key.RemoteJID != "123@s.whatsapp.net" || w.Key.Participant != "456@s.whatsapp.net" {
		t.Fatalf("Key = %+v", w.Key)
	}
	if !w.Key.FromMe {
		t.Fatalf("FromMe = false, want true")
	}
	if w.MessageTimestamp != 1700000000 {
		t.Fatalf("MessageTimestamp = %d", w.MessageTimestamp)
	}
}

func TestChatUnmarshalJSONSanitizesNameAndSubject(t *testing.T) {
	esc := "\x1b"
	src := struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Subject string `json:"subject"`
	}{ID: "123@g.us", Name: "evil" + esc + "[31mname", Subject: "evil" + esc + "[32msubject"}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var c chat
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if c.Name != "evil[31mname" {
		t.Fatalf("Name = %q", c.Name)
	}
	if c.Subject != "evil[32msubject" {
		t.Fatalf("Subject = %q", c.Subject)
	}
}

func TestContactUnmarshalJSONSanitizesNameAndNotify(t *testing.T) {
	esc := "\x1b"
	src := struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Notify string `json:"notify"`
	}{ID: "123@s.whatsapp.net", Name: "evil" + esc + "[31mname", Notify: "evil" + esc + "[32mnotify"}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var c contact
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if c.Name != "evil[31mname" {
		t.Fatalf("Name = %q", c.Name)
	}
	if c.Notify != "evil[32mnotify" {
		t.Fatalf("Notify = %q", c.Notify)
	}
}

func TestSearchHitUnmarshalJSONSanitizesSnippet(t *testing.T) {
	esc := "\x1b"
	src := struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
		FromMe    bool   `json:"fromMe"`
		Timestamp int64  `json:"timestamp"`
		Snippet   string `json:"snippet"`
	}{ChatID: "123@s.whatsapp.net", MessageID: "ABC", Timestamp: 1700000000, Snippet: "evil" + esc + "[31msnippet"}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var h searchHit
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h.Snippet != "evil[31msnippet" {
		t.Fatalf("Snippet = %q", h.Snippet)
	}
}

func TestDraftLineCountCountsExplicitNewlines(t *testing.T) {
	got := draftLineCount("hello\nworld", 20)
	if got != 2 {
		t.Fatalf("draftLineCount() = %d, want 2", got)
	}
}

func TestRenderQRStopsGrowingWhenSpaceAllows(t *testing.T) {
	setTestTheme(t, Monokai)

	small := renderQR("test-qr-payload", 20, 10)
	large := renderQR("test-qr-payload", 80, 40)

	smallFirst := strings.Split(small, "\n")[0]
	largeFirst := strings.Split(large, "\n")[0]
	if len(largeFirst) != len(smallFirst) {
		t.Fatalf("expected QR width to stop growing, small=%d large=%d", len(smallFirst), len(largeFirst))
	}
}

func TestAPICommandsSurfaceBackendErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/chats":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad api token"}`))
		case "/contacts":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"contacts store unavailable"}`))
		case "/messages":
			if got := r.URL.Query().Get("chatId"); got != "chat&1" {
				t.Fatalf("messages chatId query = %q, want chat&1", got)
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"not connected"}`))
		case "/whitelist":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"db offline"}`))
		case "/media/download":
			if got := r.URL.Query().Get("msgId"); got != "msg&1" {
				t.Fatalf("media msgId query = %q, want msg&1", got)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"media not found"}`))
		case "/names/set":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"phone is required"}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := srv.Client()

	if msg := getChats(client, srv.URL)(); !strings.Contains(msg.(chatsMsg).err.Error(), "401 Unauthorized: bad api token") {
		t.Fatalf("unexpected chats error: %v", msg.(chatsMsg).err)
	}
	if msg := getContacts(client, srv.URL)(); !strings.Contains(msg.(contactsMsg).err.Error(), "500 Internal Server Error: contacts store unavailable") {
		t.Fatalf("unexpected contacts error: %v", msg.(contactsMsg).err)
	}
	if msg := getMsgs(client, srv.URL, "chat&1", 10)(); !strings.Contains(msg.(msgsMsg).err.Error(), "409 Conflict: not connected") {
		t.Fatalf("unexpected messages error: %v", msg.(msgsMsg).err)
	}
	if msg := getWhitelist(client, srv.URL)(); !strings.Contains(msg.(whitelistLoadMsg).err.Error(), "500 Internal Server Error: db offline") {
		t.Fatalf("unexpected whitelist error: %v", msg.(whitelistLoadMsg).err)
	}
	if msg := downloadMedia(client, srv.URL, "chat-1", "msg&1", false)(); !strings.Contains(msg.(mediaDownloadMsg).err.Error(), "404 Not Found: media not found") {
		t.Fatalf("unexpected media error: %v", msg.(mediaDownloadMsg).err)
	}
	if msg := setName(client, srv.URL, "", "Alex")(); !strings.Contains(msg.(whitelistSetMsg).err.Error(), "400 Bad Request: phone is required") {
		t.Fatalf("unexpected rename error: %v", msg.(whitelistSetMsg).err)
	}
}

func TestSearchMsgsBuildsQueryAndDecodes(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		capturedQuery = r.URL.Query().Get("q")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"chatId":"c1","messageId":"m1","timestamp":42,"snippet":"x <b>foo</b> y"}]}`))
	}))
	defer srv.Close()

	msg := searchMsgs(srv.Client(), srv.URL, "foo")()
	res, ok := msg.(searchResultsMsg)
	if !ok {
		t.Fatalf("expected searchResultsMsg, got %T", msg)
	}
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if capturedQuery != "foo" {
		t.Fatalf("query sent = %q, want foo", capturedQuery)
	}
	if len(res.results) != 1 {
		t.Fatalf("results = %d, want 1", len(res.results))
	}
	if res.results[0].ChatID != "c1" || res.results[0].MessageID != "m1" || res.results[0].Timestamp != 42 {
		t.Fatalf("result fields wrong: %+v", res.results[0])
	}
	if !strings.Contains(res.results[0].Snippet, "<b>foo</b>") {
		t.Fatalf("snippet missing highlight tags: %q", res.results[0].Snippet)
	}
}

func TestPadRightAccountsForEmojiWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
	}{
		{name: "no emoji short", in: "hello", n: 10},
		{name: "no emoji exact", in: "hello", n: 5},
		{name: "trailing boot emoji", in: "03. Tartu Hiking Group \U0001F97E", n: 25},
		{name: "leading emoji", in: "\U0001F44B wave", n: 12},
		{name: "multiple emojis", in: "\U0001F680 \U0001F4A1 \U0001F525", n: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padRight(tt.in, tt.n)
			if w := runeDisplayWidth(got); w != tt.n {
				t.Fatalf("padRight(%q, %d) display width = %d, want %d (got %q)", tt.in, tt.n, w, tt.n, got)
			}
		})
	}
}

func TestPadRightTruncatesByDisplayWidth(t *testing.T) {
	got := padRight("abcdef\U0001F97Eghi", 8)
	if w := runeDisplayWidth(got); w > 8 {
		t.Fatalf("padRight display width = %d, want <= 8 (got %q)", w, got)
	}
}

func TestSearchMsgsReturnsErrorOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db offline"}`))
	}))
	defer srv.Close()

	msg := searchMsgs(srv.Client(), srv.URL, "anything")()
	res := msg.(searchResultsMsg)
	if res.err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(res.err.Error(), "500") {
		t.Fatalf("unexpected error message: %v", res.err)
	}
}

func TestMediaIconLabelDefault(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = ""
	for _, kind := range []string{"image", "video", "file", "audio", "voice", "sticker", "contact", "poll", "location", "anomaly"} {
		got := mediaIconLabel(kind)
		want := "[" + kind + "]"
		if got != want {
			t.Errorf("mediaIconLabel(%q) default = %q, want %q", kind, got, want)
		}
	}
}

func TestMediaIconLabelNerd(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = "nerd"
	for _, kind := range []string{"image", "video", "file", "audio", "voice", "sticker", "contact", "poll", "location", "anomaly"} {
		glyph := nerdIconFor(kind)
		if glyph == "" {
			t.Errorf("nerdIconFor(%q) is empty; expected a Nerd Font codepoint", kind)
			continue
		}
		// Nerd mode now returns "<glyph> <kind>" so the kind name
		// carries the meaning alongside the small visual marker.
		want := glyph + " " + kind
		got := mediaIconLabel(kind)
		if got != want {
			t.Errorf("mediaIconLabel(%q) nerd = %q, want %q", kind, got, want)
		}
	}
}

func TestMediaIconLabelUnknownKindFallsBackToBracketed(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = "nerd"
	got := mediaIconLabel("notarealkind")
	want := "[notarealkind]"
	if got != want {
		t.Errorf("mediaIconLabel(\"notarealkind\") nerd fallback = %q, want %q", got, want)
	}
}

func TestRenderMessageBodyImageDefault(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = ""
	msg := map[string]any{
		"imageMessage": map[string]any{"fileName": "cat.png", "caption": "fluffy"},
	}
	got := renderMessageBody(msg)
	// New layout: "[image]" on line 1, "cat.png" on line 2, "fluffy" on line 3.
	want := "[image]\ncat.png\nfluffy"
	if got != want {
		t.Fatalf("default render = %q, want %q", got, want)
	}
}

func TestRenderMessageBodyImageDefaultNameOnly(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = ""
	msg := map[string]any{
		"imageMessage": map[string]any{"fileName": "cat.png"},
	}
	got := renderMessageBody(msg)
	want := "[image]\ncat.png"
	if got != want {
		t.Fatalf("default render = %q, want %q", got, want)
	}
}

func TestRenderMessageBodyImageDefaultCaptionOnly(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = ""
	msg := map[string]any{
		"imageMessage": map[string]any{"caption": "fluffy"},
	}
	got := renderMessageBody(msg)
	want := "[image]\nfluffy"
	if got != want {
		t.Fatalf("default render = %q, want %q", got, want)
	}
}

func TestRenderMessageBodyImageDefaultNoMeta(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = ""
	msg := map[string]any{
		"imageMessage": map[string]any{},
	}
	got := renderMessageBody(msg)
	want := "[image]"
	if got != want {
		t.Fatalf("default render = %q, want %q", got, want)
	}
}

func TestRenderMessageBodyImageNerd(t *testing.T) {
	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	currentConfig.MediaIconStyle = "nerd"
	msg := map[string]any{
		"imageMessage": map[string]any{"fileName": "cat.png", "caption": "fluffy"},
	}
	got := renderMessageBody(msg)
	glyph := nerdIconFor("image")
	if glyph == "" {
		t.Skip("nerdIconFor(\"image\") returned empty; cannot run assertion")
	}
	// New layout: "<glyph> image" on line 1, "cat.png" on line 2, "fluffy" on line 3.
	want := glyph + " image\ncat.png\nfluffy"
	if got != want {
		t.Fatalf("nerd render = %q, want %q", got, want)
	}
}

func TestRenderFontTestRendersAllKinds(t *testing.T) {
	setTestTheme(t, Monokai)

	out := renderFontTest(100, 40)
	if out == "" {
		t.Fatal("renderFontTest returned empty")
	}
	for _, kind := range []string{"image", "video", "file", "audio", "voice", "sticker", "contact", "poll", "location", "anomaly"} {
		// The kind label appears as a word in the panel.
		if !strings.Contains(out, kind) {
			t.Errorf("renderFontTest missing kind label %q", kind)
		}
		// The text fallback "[kind]" is shown alongside the Nerd glyph.
		if !strings.Contains(out, "["+kind+"]") {
			t.Errorf("renderFontTest missing text fallback [%s]", kind)
		}
	}
	if !strings.Contains(out, "Nerd Font") {
		t.Errorf("renderFontTest should mention Nerd Font in help text")
	}
}

func TestNerdIconForAllKindsNonEmpty(t *testing.T) {
	// All media kinds we expose in the chat must have a Nerd Font glyph;
	// otherwise the toggle silently degrades to bracketed text for that kind.
	kinds := []string{"image", "video", "file", "audio", "voice", "sticker", "contact", "poll", "location", "anomaly"}
	for _, k := range kinds {
		got := nerdIconFor(k)
		if got == "" {
			t.Errorf("nerdIconFor(%q) is empty; expected a Material Design codepoint", k)
		}
		// Sanity: the codepoint should be in the SMP PUA where Nerd Fonts
		// Material Design lives (U+F0000 - U+FFFFF).
		if len(got) > 0 {
			r := []rune(got)[0]
			if r < 0xF0000 || r > 0xFFFFF {
				t.Errorf("nerdIconFor(%q) = U+%04X; expected SMP PUA (U+F0000-U+FFFFF)", k, r)
			}
		}
	}
}

func TestMediaTagStyle(t *testing.T) {
	setTestTheme(t, Monokai)

	saved := currentConfig.MediaIconStyle
	t.Cleanup(func() { currentConfig.MediaIconStyle = saved })

	// Text mode (default): pill style = dark foreground, saturated background.
	currentConfig.MediaIconStyle = ""
	st := mediaTagStyle("image")
	if got, _ := st.GetForeground().(lipgloss.Color); got != tagInk {
		t.Errorf("text mode foreground = %v, want %v", got, tagInk)
	}
	if got, _ := st.GetBackground().(lipgloss.Color); got != imageTag {
		t.Errorf("text mode background = %v, want %v", got, imageTag)
	}

	// Nerd mode: file-browser style = saturated foreground, no background.
	// Without a background, the icon stays visible on the chat's dark panel
	// rather than reading as a dark blob on a saturated pill.
	currentConfig.MediaIconStyle = "nerd"
	st = mediaTagStyle("image")
	if got, _ := st.GetForeground().(lipgloss.Color); got != imageTag {
		t.Errorf("nerd mode foreground = %v, want %v", got, imageTag)
	}
	if got, _ := st.GetBackground().(lipgloss.Color); got != "" {
		t.Errorf("nerd mode should have no background, got %v", got)
	}
}

func TestHasVisibleText(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"tabs and newlines", "\t\n\r", false},
		{"control chars only", "\x00\x01\x1b", false},
		{"normal text", "hello", true},
		{"text with spaces", " hello ", true},
		{"mixed control and text", "\x00hello\x1b", true},
		{"emoji", "👋", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasVisibleText(tt.s); got != tt.want {
				t.Errorf("hasVisibleText(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}
