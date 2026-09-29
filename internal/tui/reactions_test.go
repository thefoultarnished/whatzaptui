package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const reactChat = "120363@g.us"

func reactMsg(id, target, emoji, person string, ts int64) wireMsg {
	r := wireMsg{
		Message:          map[string]any{"reactionMessage": map[string]any{"targetMsgID": target, "emoji": emoji}},
		MessageTimestamp: ts,
	}
	r.Key.ID = id
	r.Key.RemoteJID = reactChat
	r.Key.Participant = person + "@s.whatsapp.net"
	return r
}

func reactModel(reactions ...wireMsg) m {
	target := wireMsg{Message: map[string]any{"conversation": "lunch?"}, MessageTimestamp: 100}
	target.Key.ID = "m1"
	target.Key.RemoteJID = reactChat
	names := map[string]string{"1": "Ann Lee", "2": "Bob", "3": "Cat", "4": "Dan", "5": "Eve"}
	contacts := map[string]contact{}
	byNumber := map[string]contact{}
	for n, name := range names {
		jid := n + "@s.whatsapp.net"
		contacts[jid] = contact{ID: jid, Notify: name}
		byNumber[n] = contact{ID: jid, Notify: name}
	}
	return m{
		status:           "ready",
		mode:             "chat",
		active:           reactChat,
		msgs:             map[string][]wireMsg{reactChat: append([]wireMsg{target}, reactions...)},
		contacts:         contacts,
		contactsByNumber: byNumber,
		whitelist:        map[string]string{},
	}
}

func TestReactionGroupsOrderAndFullNames(t *testing.T) {
	x := reactModel(
		reactMsg("r1", "m1", "heart", "5", 101),
		reactMsg("r2", "m1", "fire", "1", 102),
		reactMsg("r3", "m1", "fire", "2", 103),
		reactMsg("r4", "m1", "fire", "3", 104),
	)
	groups := x.reactionGroups(x.msgs[reactChat], "m1")
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}
	if groups[0].emoji != "fire" || strings.Join(groups[0].names, ",") != "Ann Lee,Bob,Cat" {
		t.Fatalf("first group = %+v, want fire: Ann Lee,Bob,Cat (most used first, full names)", groups[0])
	}
	if groups[1].emoji != "heart" || groups[1].names[0] != "Eve" {
		t.Fatalf("second group = %+v", groups[1])
	}
	if got := x.reactionGroups(x.msgs[reactChat], "nope"); len(got) != 0 {
		t.Fatalf("unknown message should have no groups, got %+v", got)
	}
}

func TestReactionsOnePerPersonNewestWinsAndEmptyRemoves(t *testing.T) {
	x := reactModel(
		reactMsg("r1", "m1", "fire", "1", 101),
		reactMsg("r2", "m1", "heart", "1", 102), // Ann changes her reaction
		reactMsg("r3", "m1", "fire", "2", 103),
		reactMsg("r4", "m1", "", "2", 104), // Bob removes his
	)
	groups := x.reactionGroups(x.msgs[reactChat], "m1")
	if len(groups) != 1 || groups[0].emoji != "heart" || len(groups[0].names) != 1 || groups[0].names[0] != "Ann Lee" {
		t.Fatalf("groups = %+v, want only Ann's heart", groups)
	}
}

func TestMyReactionShownAsYou(t *testing.T) {
	mine := reactMsg("r1", "m1", "fire", "9", 101)
	mine.Key.FromMe = true
	x := reactModel(mine)
	groups := x.reactionGroups(x.msgs[reactChat], "m1")
	if len(groups) != 1 || groups[0].names[0] != "You" {
		t.Fatalf("groups = %+v, want You", groups)
	}
}

func TestReactionRowStopsCountingChangedReaction(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := reactModel(
		reactMsg("r1", "m1", "fire", "1", 101),
		reactMsg("r2", "m1", "heart", "1", 102),
	)
	rendered := ansiStripRe.ReplaceAllString(x.renderMain(100, 8), "")
	if strings.Contains(rendered, "fire") || !strings.Contains(rendered, "heart") {
		t.Fatalf("row should show only Ann's latest reaction, got %q", rendered)
	}
}

func TestOpenReactionListNeedsSelectedMessageWithReactions(t *testing.T) {
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	if cmd := x.openReactionList(); cmd == nil || x.reactionPicker.IsOpen {
		t.Fatal("no selection: expected a message and no panel")
	}
	x.selectedMsgID = "other"
	if cmd := x.openReactionList(); cmd == nil || x.reactionPicker.IsOpen {
		t.Fatal("message without reactions: expected a message and no panel")
	}
	x.selectedMsgID = "m1"
	x.replyPickMode = true
	if cmd := x.openReactionList(); cmd != nil {
		t.Fatalf("expected the panel to open silently")
	}
	if !x.reactionPicker.IsOpen || x.replyPickMode {
		t.Fatalf("panel open=%v replyPickMode=%v, want true,false", x.reactionPicker.IsOpen, x.replyPickMode)
	}
	if len(x.reactionPicker.Items) != 1 || x.reactionPicker.Items[0].Key != "fire 1" || x.reactionPicker.Items[0].Desc != "Ann Lee" {
		t.Fatalf("items = %+v", x.reactionPicker.Items)
	}
}

func altG() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g"), Alt: true} }

func TestAltGOpensListAndEscClosesAndClearsSelection(t *testing.T) {
	x := reactModel(
		reactMsg("r1", "m1", "fire", "1", 101),
		reactMsg("r2", "m1", "fire", "2", 102),
	)
	x.selectedMsgID = "m1"
	next, _ := x.key(altG())
	got := next.(m)
	if !got.reactionPicker.IsOpen {
		t.Fatal("alt+g should open the list")
	}
	pane := ansiStripRe.ReplaceAllString(got.renderRightMain(80, 24), "")
	for _, want := range []string{"Reactions", "fire 2", "Ann Lee, Bob"} {
		if !strings.Contains(pane, want) {
			t.Fatalf("panel missing %q: %q", want, pane)
		}
	}
	// Other keys do not leak into the chat while it is open.
	next, _ = got.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if next.(m).input != "" || !next.(m).reactionPicker.IsOpen {
		t.Fatalf("typing must be ignored while the list is open: input=%q", next.(m).input)
	}
	next, _ = next.(m).key(tea.KeyMsg{Type: tea.KeyEsc})
	closed := next.(m)
	if closed.reactionPicker.IsOpen || closed.selectedMsgID != "" || closed.replyTo != nil {
		t.Fatalf("Esc should close, clear selection and not start a reply: open=%v sel=%q reply=%v",
			closed.reactionPicker.IsOpen, closed.selectedMsgID, closed.replyTo != nil)
	}
}

func TestAltGOnlyInAReadyChat(t *testing.T) {
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.selectedMsgID = "m1"
	x.mode = "nav"
	next, _ := x.key(altG())
	if next.(m).reactionPicker.IsOpen {
		t.Fatal("alt+g must only work inside an open chat")
	}
	y := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	y.selectedMsgID = "m1"
	y.status = "Connecting..."
	next, _ = y.key(altG())
	if next.(m).reactionPicker.IsOpen {
		t.Fatal("alt+g must wait until the session is ready")
	}
}

func TestAltGWorksWhileReplyPickerIsActive(t *testing.T) {
	x := reactModel(reactMsg("r1", "m1", "fire", "1", 101))
	x.replyPickMode = true
	x.selectedMsgID = "m1"
	next, _ := x.key(altG())
	got := next.(m)
	if !got.reactionPicker.IsOpen || got.replyPickMode {
		t.Fatalf("open=%v replyPickMode=%v, want true,false", got.reactionPicker.IsOpen, got.replyPickMode)
	}
}
