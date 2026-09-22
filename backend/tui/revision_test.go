package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testViewModel() m {
	return m{
		status:    "ready",
		mode:      "nav",
		w:         100,
		h:         30,
		chats:     []chat{{ID: "c1", Name: "C1"}},
		contacts:  map[string]contact{},
		names:     map[string]string{},
		msgs:      map[string][]wireMsg{},
		mainCache: &renderCache{},
	}
}

func TestRevisionBumpsOnEveryUpdate(t *testing.T) {
	model := testViewModel()
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	got := next.(m)
	if got.revision != model.revision+1 {
		t.Fatalf("revision = %d, want %d", got.revision, model.revision+1)
	}
}

func TestViewCachesAcrossViews(t *testing.T) {
	model := testViewModel()
	first := model.View()
	if model.mainCache == nil || model.mainCache.result == "" {
		t.Fatal("first View did not populate cache")
	}
	// View has a value receiver; the stored cache is shared via pointer.
	second := model.View()
	if first != second {
		t.Fatal("second View with no Update should hit cache and match")
	}
	if model.mainCache.revision != model.revision {
		t.Fatalf("cache revision = %d, model = %d", model.mainCache.revision, model.revision)
	}
}

func TestViewInvalidatesAfterUpdate(t *testing.T) {
	model := testViewModel()
	_ = model.View()
	next, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	updated := next.(m)
	if updated.revision != model.revision+1 {
		t.Fatalf("revision = %d, want %d", updated.revision, model.revision+1)
	}
	if updated.mainCache.revision == updated.revision {
		t.Fatal("cache should be stale immediately after Update")
	}
	_ = updated.View()
	if updated.mainCache.revision != updated.revision {
		t.Fatal("View after Update did not refresh cache revision")
	}
}
