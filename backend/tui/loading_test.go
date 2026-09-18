package main

import (
	"testing"
)

func TestLoadingStages(t *testing.T) {
	fresh := m{status: "Starting backend..."}
	for _, s := range fresh.loadingStages() {
		if s.state != "pending" {
			t.Fatalf("fresh boot stage %q = %q, want pending", s.label, s.state)
		}
	}

	connecting := m{status: "Connecting..."}
	stages := connecting.loadingStages()
	if stages[0].state != "done" {
		t.Fatalf("backend stage = %q, want done", stages[0].state)
	}
	if stages[1].state != "active" {
		t.Fatalf("session stage = %q, want active", stages[1].state)
	}

	loaded := m{
		status:   "Connecting...",
		chats:    []chat{{ID: "a@s.whatsapp.net"}, {ID: "b@s.whatsapp.net"}},
		contacts: map[string]contact{"c@s.whatsapp.net": {ID: "c@s.whatsapp.net"}},
	}
	stages = loaded.loadingStages()
	if stages[2].state != "done" || stages[2].detail != "2 chats" {
		t.Fatalf("chats stage = %+v, want done/2 chats", stages[2])
	}
	if stages[3].state != "done" || stages[3].detail != "1 contact" {
		t.Fatalf("contacts stage = %+v, want done/1 contact", stages[3])
	}

	ready := m{status: "ready"}
	for _, s := range ready.loadingStages() {
		if s.label == "Session" && s.state != "done" {
			t.Fatalf("session stage at ready = %q, want done", s.state)
		}
	}
}
