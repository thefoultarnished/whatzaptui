package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWSCloseEnvCarriesReason(t *testing.T) {
	err := &websocket.CloseError{Code: websocket.CloseTryAgainLater, Text: "Too many windows open"}
	e, ok := wsCloseEnv(err)
	if !ok || e.Type != wsCloseEvent {
		t.Fatalf("close error with text should become %q event, got %+v ok=%v", wsCloseEvent, e, ok)
	}
	if !strings.Contains(string(e.Payload), "Too many windows open") {
		t.Fatalf("payload missing reason: %s", e.Payload)
	}

	// No reason text, or not a close error at all: nothing to report.
	if _, ok := wsCloseEnv(&websocket.CloseError{Code: websocket.CloseGoingAway}); ok {
		t.Fatal("close error without text should not produce an event")
	}
	if _, ok := wsCloseEnv(errors.New("connection reset")); ok {
		t.Fatal("plain network error should not produce an event")
	}
}

func TestWSDisconnectShowsBackendReason(t *testing.T) {
	model := m{status: "ready"}
	e, _ := wsCloseEnv(&websocket.CloseError{Code: websocket.CloseGoingAway, Text: "Backend shut down"})

	next, _ := model.Update(wsEvtMsg{evt: e, ok: true})
	next, _ = next.(m).Update(wsEvtMsg{ok: false})
	got := next.(m)

	if !strings.HasPrefix(got.topBarMsg, "Backend shut down, retrying in ") {
		t.Fatalf("top bar should show the backend's reason, got %q", got.topBarMsg)
	}
	if got.wsCloseReason != "" {
		t.Fatal("close reason should be cleared once shown")
	}

	// A later plain drop falls back to the generic notice.
	next, _ = got.Update(wsEvtMsg{ok: false})
	if msg := next.(m).topBarMsg; !strings.HasPrefix(msg, "Disconnected, reconnecting") {
		t.Fatalf("plain drop should show generic notice, got %q", msg)
	}
}
