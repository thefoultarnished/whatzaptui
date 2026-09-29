package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestPrefetchOnWSOpen fires both cached-data fetches so `ready` can render
// instantly instead of waiting for more round trips. It also re-sends the
// show-online setting, since a fresh backend connection starts hidden.
func TestPrefetchOnWSOpen(t *testing.T) {
	var chatsHit, contactsHit, presenceHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/chats":
			chatsHit = true
			_, _ = w.Write([]byte(`{"chats":[]}`))
		case "/contacts":
			contactsHit = true
			_, _ = w.Write([]byte(`{"contacts":[]}`))
		case "/presence":
			presenceHit = true
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	x := m{client: srv.Client(), baseURL: srv.URL}
	cmd := x.prefetchOnWSOpen()
	if cmd == nil {
		t.Fatalf("prefetchOnWSOpen returned nil cmd")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("prefetch cmd returned %T, want tea.BatchMsg", cmd())
	}
	for _, c := range batch {
		_ = c()
	}
	if !chatsHit || !contactsHit || !presenceHit {
		t.Fatalf("prefetch hits: chats=%v contacts=%v presence=%v, want all true", chatsHit, contactsHit, presenceHit)
	}
}
