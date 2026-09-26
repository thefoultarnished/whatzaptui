package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestExtractMentionQuery(t *testing.T) {
	cases := []struct {
		input   string
		wantQ   string
		wantIdx int
		wantOk  bool
	}{
		{"", "", -1, false},
		{"hello", "", -1, false},
		{"@", "", -1, false},
		{"hello @", "", -1, false},
		{"user@example.com", "", -1, false},
		{"@a", "a", 0, true},
		{"hello @a", "a", 6, true},
		{"hello @alice", "alice", 6, true},
		{"hello @Alice_99", "Alice_99", 6, true},
		{"hello @919876543210", "919876543210", 6, true},
		{"hello @alice ", "", -1, false}, // space after @ query closes trigger
		{"user@example.com @bob", "bob", 17, true},
		{"@alice\n@bob", "bob", 7, true},
		{"\t@charlie", "charlie", 1, true},
	}

	for _, tc := range cases {
		q, idx, ok := extractMentionQuery(tc.input)
		if ok != tc.wantOk || q != tc.wantQ || idx != tc.wantIdx {
			t.Errorf("extractMentionQuery(%q) = (%q, %d, %v); want (%q, %d, %v)",
				tc.input, q, idx, ok, tc.wantQ, tc.wantIdx, tc.wantOk)
		}
	}
}

func TestFilterMentionCandidates(t *testing.T) {
	model := m{
		active: "120363000@g.us",
		groupPreviews: map[string]groupPreview{
			"120363000@g.us": {
				participants: []groupParticipant{
					{JID: "15551111111@s.whatsapp.net", Phone: "15551111111", Name: "Alice Smith"},
					{JID: "15552222222@s.whatsapp.net", Phone: "15552222222", Name: "Bob Jones"},
					{JID: "15553333333@s.whatsapp.net", Phone: "15553333333", Name: "Charlie Brown"},
					{JID: "15554444444@s.whatsapp.net", Phone: "15554444444", Name: "Dave Miller"},
					{JID: "15555555555@s.whatsapp.net", Phone: "15555555555", Name: "Alicia Keys"},
				},
			},
		},
	}

	// Prefix match on "al"
	matches := model.filterMentionCandidates("al")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches for 'al', got %d", len(matches))
	}
	if matches[0].Name != "Alice Smith" && matches[1].Name != "Alice Smith" {
		t.Errorf("expected Alice Smith in matches, got %v", matches)
	}

	// Match by phone number prefix
	phoneMatches := model.filterMentionCandidates("15553")
	if len(phoneMatches) != 1 || phoneMatches[0].Name != "Charlie Brown" {
		t.Errorf("expected Charlie Brown for '15553', got %v", phoneMatches)
	}

	// No match
	none := model.filterMentionCandidates("zzz")
	if len(none) != 0 {
		t.Errorf("expected 0 matches for 'zzz', got %d", len(none))
	}
}

func TestApplySelectedMention(t *testing.T) {
	model := m{
		input:             "hello @ali",
		mentionPickerOpen: true,
		mentionMatches: []groupParticipant{
			{JID: "15551111111@s.whatsapp.net", Phone: "15551111111", Name: "Alice Smith"},
		},
		mentionSel: 0,
	}

	model.applySelectedMention()

	if model.input != "hello @Alice Smith " {
		t.Fatalf("input = %q, want %q", model.input, "hello @Alice Smith ")
	}
	if model.mentionPickerOpen {
		t.Fatalf("expected mentionPickerOpen = false")
	}
	if len(model.composerMentions) != 1 {
		t.Fatalf("expected 1 composer mention recorded, got %d", len(model.composerMentions))
	}
	if model.composerMentions[0].Phone != "15551111111" || model.composerMentions[0].TagText != "@Alice Smith" {
		t.Fatalf("unexpected composer mention: %+v", model.composerMentions[0])
	}
}

func TestPrepareOutgoingMentions(t *testing.T) {
	model := m{
		composerMentions: []mentionTag{
			{TagText: "@Alice", JID: "15551111111@s.whatsapp.net", Phone: "15551111111"},
		},
	}

	wireText, jids := model.prepareOutgoingMentions("hello @Alice and @15552222222!")
	if wireText != "hello @15551111111 and @15552222222!" {
		t.Fatalf("wireText = %q, want 'hello @15551111111 and @15552222222!'", wireText)
	}
	if len(jids) != 2 {
		t.Fatalf("expected 2 mentioned JIDs, got %v", jids)
	}
	if jids[0] != "15551111111@s.whatsapp.net" || jids[1] != "15552222222@s.whatsapp.net" {
		t.Fatalf("unexpected JIDs: %v", jids)
	}
}

func TestMentionPickerKeyNavigation(t *testing.T) {
	model := m{
		mode:              "chat",
		input:             "@al",
		mentionPickerOpen: true,
		mentionMatches: []groupParticipant{
			{JID: "15551111111@s.whatsapp.net", Phone: "15551111111", Name: "Alice"},
			{JID: "15552222222@s.whatsapp.net", Phone: "15552222222", Name: "Alicia"},
		},
		mentionSel: 0,
	}

	// Press Down
	next, _ := model.handleChatKey(tea.KeyMsg{Type: tea.KeyDown})
	mNext := next.(m)
	if mNext.mentionSel != 1 {
		t.Fatalf("after KeyDown, mentionSel = %d, want 1", mNext.mentionSel)
	}

	// Press Up
	next2, _ := mNext.handleChatKey(tea.KeyMsg{Type: tea.KeyUp})
	mNext2 := next2.(m)
	if mNext2.mentionSel != 0 {
		t.Fatalf("after KeyUp, mentionSel = %d, want 0", mNext2.mentionSel)
	}

	// Press Esc
	next3, _ := mNext2.handleChatKey(tea.KeyMsg{Type: tea.KeyEsc})
	mNext3 := next3.(m)
	if mNext3.mentionPickerOpen {
		t.Fatalf("after KeyEsc, expected mentionPickerOpen = false")
	}
	if !mNext3.mentionDismissed {
		t.Fatalf("after KeyEsc, expected mentionDismissed = true")
	}

	// Press Tab to complete
	mNext.mentionPickerOpen = true
	mNext.mentionSel = 1
	next4, _ := mNext.handleChatKey(tea.KeyMsg{Type: tea.KeyTab})
	mNext4 := next4.(m)
	if mNext4.input != "@Alicia " {
		t.Fatalf("after KeyTab, input = %q, want '@Alicia '", mNext4.input)
	}
	if mNext4.mentionPickerOpen {
		t.Fatalf("after KeyTab, expected mentionPickerOpen = false")
	}
}

func TestRenderMentionPopup(t *testing.T) {
	model := m{
		mentionPickerOpen: true,
		mentionMatches: []groupParticipant{
			{JID: "15551111111@s.whatsapp.net", Phone: "15551111111", Name: "Alice"},
			{JID: "15552222222@s.whatsapp.net", Phone: "15552222222", Name: "Bob"},
		},
		mentionSel: 0,
	}

	popup := model.renderMentionPopup(40)
	if popup == "" {
		t.Fatalf("expected non-empty popup rendering")
	}
	if !strings.Contains(popup, "Alice") || !strings.Contains(popup, "Bob") {
		t.Fatalf("popup should contain Alice and Bob: %q", popup)
	}
	model.mentionPickerOpen = false
	if closedPopup := model.renderMentionPopup(40); closedPopup != "" {
		t.Fatalf("expected empty popup when picker is closed")
	}
}

func TestRenderSegmentWithMentions(t *testing.T) {
	base := lipgloss.NewStyle()

	// Mention should be highlighted
	s1 := renderSegmentWithMentions("hello @alice world", base)
	if !strings.Contains(s1, "@alice") {
		t.Fatalf("expected '@alice' in rendered string: %q", s1)
	}

	// Email should NOT be highlighted as a mention
	s2 := renderSegmentWithMentions("user@example.com", base)
	plain2 := base.Render("user@example.com")
	if s2 != plain2 {
		t.Fatalf("email should not receive mention highlighting: got %q, want %q", s2, plain2)
	}
}

func TestResolvePhoneToNameAndFormatMessageMentions(t *testing.T) {
	model := m{
		active:    "120363000@g.us",
		selfPhone: "37253984574",
		selfLID:   "57712197017667",
		selfName:  "Prab",
		lidMap: map[string]string{
			"12345678901234": "15553333333",
		},
		groupPreviews: map[string]groupPreview{
			"120363000@g.us": {
				participants: []groupParticipant{
					{JID: "15551111111@s.whatsapp.net", Phone: "15551111111", Name: "Alice Smith"},
				},
			},
		},
		contactsByNumber: map[string]contact{
			"37253984574": {Notify: "Me Estonia"},
			"15552222222": {Name: "Bob Jones"},
			"15553333333": {Name: "Charlie Brown"},
		},
	}

	// 1. Custom name takes priority over selfName
	if name := model.resolvePhoneToName("57712197017667"); name != "Me Estonia" {
		t.Fatalf("resolvePhoneToName(selfLID) = %q, want 'Me Estonia' (custom name precedence)", name)
	}
	if name := model.resolvePhoneToName("37253984574"); name != "Me Estonia" {
		t.Fatalf("resolvePhoneToName(selfPhone) = %q, want 'Me Estonia' (custom name precedence)", name)
	}

	// 2. WhatsApp profile name when no custom name saved
	delete(model.contactsByNumber, "37253984574")
	if name := model.resolvePhoneToName("57712197017667"); name != "Prab" {
		t.Fatalf("resolvePhoneToName(selfLID without custom name) = %q, want 'Prab'", name)
	}

	// 3. Phone number when neither custom name nor profile name saved
	model.selfName = ""
	if name := model.resolvePhoneToName("57712197017667"); name != "37253984574" {
		t.Fatalf("resolvePhoneToName(selfLID without names) = %q, want '37253984574'", name)
	}

	// 4. Mapped LID to contact name
	if name := model.resolvePhoneToName("12345678901234"); name != "Charlie Brown" {
		t.Fatalf("resolvePhoneToName(mappedLID) = %q, want 'Charlie Brown'", name)
	}

	// Resolve group participant
	if name := model.resolvePhoneToName("15551111111"); name != "Alice Smith" {
		t.Fatalf("resolvePhoneToName(15551111111) = %q, want 'Alice Smith'", name)
	}

	// Resolve address book contact
	if name := model.resolvePhoneToName("15552222222"); name != "Bob Jones" {
		t.Fatalf("resolvePhoneToName(15552222222) = %q, want 'Bob Jones'", name)
	}

	// Unknown phone
	if name := model.resolvePhoneToName("15559999999"); name != "" {
		t.Fatalf("resolvePhoneToName(15559999999) = %q, want empty", name)
	}
	// formatMessageMentions converts known phones to @<Name>
	msg := "hello @15551111111 and @15552222222, but not @15559999999 or user@example.com"
	formatted, tags := model.formatMessageMentions(msg)
	wantFormatted := "hello @Alice Smith and @Bob Jones, but not @15559999999 or user@example.com"
	if formatted != wantFormatted {
		t.Fatalf("formatted = %q, want %q", formatted, wantFormatted)
	}
	if len(tags) != 2 || tags[0] != "@Alice Smith" || tags[1] != "@Bob Jones" {
		t.Fatalf("unexpected tags: %v", tags)
	}

	// renderSegmentWithMentions highlights the multi-word tag
	rendered := renderSegmentWithMentions(formatted, lipgloss.NewStyle(), tags...)
	if !strings.Contains(rendered, "@Alice Smith") {
		t.Fatalf("rendered string should contain @Alice Smith: %q", rendered)
	}
}
