package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPreReadyFetchErrorsSuppressed(t *testing.T) {
	err409 := fmt.Errorf("409 Conflict: not connected")
	msgs := []tea.Msg{
		msgsMsg{chatID: "c1", err: err409},
		chatsMsg{err: err409},
		contactsMsg{err: err409},
		whitelistLoadMsg{err: err409},
		olderMsgsMsg{chatID: "c1", err: err409},
		aroundMsgsMsg{chatID: "c1", err: err409},
	}
	for i, msg := range msgs {
		model := m{status: "Connecting...", msgs: map[string][]wireMsg{}}
		_, cmd := model.Update(msg)
		if cmd != nil {
			t.Fatalf("msg %d: pre-ready error must not surface a topBar cmd", i)
		}
	}
}

func TestReadyFetchErrorsSurface(t *testing.T) {
	err409 := fmt.Errorf("409 Conflict: not connected")
	model := m{status: "ready", msgs: map[string][]wireMsg{}}
	_, cmd := model.Update(msgsMsg{chatID: "c1", err: err409})
	if cmd == nil {
		t.Fatal("ready-state error must surface a topBar cmd")
	}
}
