package tui

import "testing"

func renameModel() m {
	return m{
		status:     "ready",
		demoMode:   true,
		sidebarTab: "contacts",
		contacts: map[string]contact{
			"911111111111@s.whatsapp.net": {ID: "911111111111@s.whatsapp.net", Notify: "Alice", Stored: true},
			"922222222222@s.whatsapp.net": {ID: "922222222222@s.whatsapp.net", Notify: "Bob", Stored: true},
		},
		names:     map[string]string{},
		whitelist: map[string]string{},
	}
}

// Positive: with no chat open, the command bar renames the highlighted person.
func TestRenameCommandBarUsesHighlightedContact(t *testing.T) {
	x := renameModel()
	x.sel = 1 // sorted list: Alice, Bob
	x.handleGlobalCommand("/rename Bobby")
	if got := x.names["922222222222"]; got != "Bobby" {
		t.Fatalf("names = %v, want Bob renamed to Bobby", x.names)
	}
	if _, ok := x.names["911111111111"]; ok {
		t.Fatal("renamed the wrong contact")
	}
}

// Positive: the highlight wins over the open chat.
func TestRenameCommandBarPrefersHighlightOverOpenChat(t *testing.T) {
	x := renameModel()
	x.active = "911111111111@s.whatsapp.net"
	x.sel = 1
	x.handleGlobalCommand("/rename Bobby")
	if x.names["922222222222"] != "Bobby" || x.names["911111111111"] != "" {
		t.Fatalf("names = %v, want only the highlighted Bob renamed", x.names)
	}
}

// Edge: the highlight indexes the search-filtered list, not the full list.
func TestRenameCommandBarRespectsSearchFilter(t *testing.T) {
	x := renameModel()
	x.search = "bob"
	x.sel = 0 // first (only) search result is Bob
	x.handleGlobalCommand("/rename Bobby")
	if x.names["922222222222"] != "Bobby" {
		t.Fatalf("names = %v, want the filtered result (Bob) renamed", x.names)
	}
}

// Edge: nothing highlighted falls back to the open chat.
func TestRenameCommandBarFallsBackToOpenChat(t *testing.T) {
	x := renameModel()
	x.sel = 99
	x.active = "911111111111@s.whatsapp.net"
	x.handleGlobalCommand("/rename Ally")
	if x.names["911111111111"] != "Ally" {
		t.Fatalf("names = %v, want the open chat renamed", x.names)
	}
}

// Negative: nothing highlighted and no open chat renames nobody.
func TestRenameCommandBarNothingSelected(t *testing.T) {
	x := renameModel()
	x.contacts = map[string]contact{}
	x.handleGlobalCommand("/rename Nobody")
	if len(x.names) != 0 {
		t.Fatalf("names = %v, want nothing renamed", x.names)
	}
}

// Positive: /rename typed in the message box still renames the open chat,
// even if a different person is highlighted.
func TestRenameFromComposerUsesOpenChat(t *testing.T) {
	x := renameModel()
	x.active = "911111111111@s.whatsapp.net"
	x.sel = 1
	x.handleSlash("/rename Ally")
	if x.names["911111111111"] != "Ally" || x.names["922222222222"] != "" {
		t.Fatalf("names = %v, want only the open chat renamed", x.names)
	}
}
