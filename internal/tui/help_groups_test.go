package tui

import "testing"

// The help screen groups commands by hard-coded counts. If they drift from the
// command list, commands land under the wrong heading (or under none).
func TestHelpGroupCountsCoverEveryCommand(t *testing.T) {
	total := 0
	for _, g := range helpGroupDefs {
		total += g.Count
	}
	if total != len(helpCommands) {
		t.Fatalf("help groups cover %d commands, but there are %d", total, len(helpCommands))
	}
}

func TestHelpGroupsStartAtTheRightCommands(t *testing.T) {
	first := map[string]string{
		"Interface": "/help",
		"Contacts":  "/pin",
		"Sounds":    "/soundon",
		"Session":   "/logout",
	}
	idx := 0
	for _, g := range helpGroupDefs {
		if got := helpCommands[idx].cmd; got != first[g.Name] {
			t.Errorf("group %s starts at %q, want %q", g.Name, got, first[g.Name])
		}
		idx += g.Count
	}
}
