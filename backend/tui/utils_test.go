package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestDraftLineCountCountsExplicitNewlines(t *testing.T) {
	got := draftLineCount("hello\nworld", 20)
	if got != 2 {
		t.Fatalf("draftLineCount() = %d, want 2", got)
	}
}

func TestRenderQRStopsGrowingWhenSpaceAllows(t *testing.T) {
	currentTheme = Monokai
	rehashStyles()

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
	if msg := downloadMedia(client, srv.URL, "chat-1", "msg&1")(); !strings.Contains(msg.(mediaDownloadMsg).err.Error(), "404 Not Found: media not found") {
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
