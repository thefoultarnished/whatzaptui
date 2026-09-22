package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestChatsMsgDoesNotAutoOpen: a fresh session (active == "") stays on
// the chat list — no chat opens until the user picks one.
func TestChatsMsgDoesNotAutoOpen(t *testing.T) {
	var msgsHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/messages":
			msgsHit = true
			_, _ = w.Write([]byte(`{"messages":[]}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	x := m{
		client:        srv.Client(),
		baseURL:       srv.URL,
		status:        "ready",
		sidebarTab:    "chats",
		msgs:          map[string][]wireMsg{},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{"111": "Recent"},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
		sidebarCache:  &sidebarCache{},
		mainCache:     &renderCache{},
	}
	newChats := []chat{
		{ID: "111@s.whatsapp.net", ConversationTimestamp: 200},
		{ID: "222@s.whatsapp.net", ConversationTimestamp: 100},
	}
	mdl, cmd := x.updateInner(chatsMsg{chats: newChats})
	got := mdl.(m)
	if got.active != "" {
		t.Fatalf("active = %q, want empty (no auto-open)", got.active)
	}
	if cmd != nil {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					_ = c()
				}
			}
		}
	}
	if msgsHit {
		t.Fatalf("no /messages fetch expected without an opened chat")
	}
}

// TestChatsMsgPreservesExistingActive: a set active chat is never hijacked.
func TestChatsMsgPreservesExistingActive(t *testing.T) {
	x := m{
		status:        "ready",
		sidebarTab:    "chats",
		active:        "222@s.whatsapp.net",
		mode:          "chat",
		msgs:          map[string][]wireMsg{},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
		sidebarCache:  &sidebarCache{},
		mainCache:     &renderCache{},
	}
	newChats := []chat{
		{ID: "111@s.whatsapp.net", ConversationTimestamp: 200},
		{ID: "222@s.whatsapp.net", ConversationTimestamp: 100},
	}
	mdl, _ := x.updateInner(chatsMsg{chats: newChats})
	if got := mdl.(m); got.active != "222@s.whatsapp.net" {
		t.Fatalf("active = %q, want preserved 222", got.active)
	}
}

func TestChatsMsgDoesNotMarkReadBeforeReady(t *testing.T) {
	var markReadHit bool
	var msgsHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/messages":
			msgsHit = true
			_, _ = w.Write([]byte(`{"messages":[]}`))
		case "/messages/read":
			markReadHit = true
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	x := m{
		client:        srv.Client(),
		baseURL:       srv.URL,
		status:        "Connecting...",
		sidebarTab:    "chats",
		msgs:          map[string][]wireMsg{},
		contacts:      map[string]contact{},
		whitelist:     map[string]string{},
		names:         map[string]string{},
		drafts:        map[string]string{},
		groupPreviews: map[string]groupPreview{},
		sidebarCache:  &sidebarCache{},
		mainCache:     &renderCache{},
	}
	newChats := []chat{
		{ID: "111@s.whatsapp.net", ConversationTimestamp: 200},
	}
	mdl, cmd := x.updateInner(chatsMsg{chats: newChats})
	got := mdl.(m)
	if got.active != "" {
		t.Fatalf("active = %q, want empty (no auto-open)", got.active)
	}
	if cmd != nil {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					_ = c()
				}
			}
		}
	}
	if msgsHit {
		t.Fatalf("no /messages fetch expected without an opened chat")
	}
	if markReadHit {
		t.Fatalf("did not expect /messages/read while status is Connecting...")
	}
}

func TestNotConnectedDataErrDoesNotCrashLoadingScreen(t *testing.T) {
	x := m{
		status: "Connecting...",
	}
	mdl, _ := x.updateInner(dataErr{err: http.ErrHandlerTimeout})
	got := mdl.(m)
	if got.status != "Error: "+http.ErrHandlerTimeout.Error() {
		t.Fatalf("status = %q, want fatal Error", got.status)
	}

	// "not connected" error must NOT overwrite loading status
	x = m{status: "Connecting..."}
	mdl, _ = x.updateInner(dataErr{err: &mockStatusErr{"409 Conflict: not connected"}})
	got = mdl.(m)
	if got.status != "Connecting..." {
		t.Fatalf("status = %q, want preserved Connecting...", got.status)
	}

	// "not connected" error must NOT flash in topBar when ready
	x = m{status: "ready"}
	mdl, cmd := x.updateInner(dataErr{err: &mockStatusErr{"409 Conflict: not connected"}})
	got = mdl.(m)
	if got.topBarMsg != "" || cmd != nil {
		t.Fatalf("not connected error should not be shown in topBar when ready: got %q", got.topBarMsg)
	}
}

func TestReadyEventNeverSendsReadReceipt(t *testing.T) {
	var markReadHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/messages/read":
			markReadHit = true
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	wsCh := make(chan env, 1)
	wsCh <- env{}
	x := m{
		client:     srv.Client(),
		baseURL:    srv.URL,
		status:     "Connecting...",
		active:     "111@s.whatsapp.net",
		sidebarTab: "chats",
		wsCh:       wsCh,
	}
	_, cmd := x.updateInner(wsEvtMsg{evt: env{Type: "ready"}, ok: true})
	if cmd != nil {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					_ = c()
				}
			}
		}
	}
	if markReadHit {
		t.Fatalf("ready event must never automatically send /messages/read on bootup")
	}
}

type mockStatusErr struct{ msg string }

func (e *mockStatusErr) Error() string { return e.msg }
