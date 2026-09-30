package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	fwdHere = "15550000001@s.whatsapp.net"
	fwdBob  = "15550000002@s.whatsapp.net"
	fwdCat  = "15550000003@s.whatsapp.net"
	fwdOld  = "15550000004@s.whatsapp.net"
)

func fwdModel() m {
	text := wireMsg{Message: map[string]any{"conversation": "lunch at noon?"}, MessageTimestamp: 100}
	text.Key.ID = "t1"
	text.Key.RemoteJID = fwdHere
	image := wireMsg{Message: map[string]any{"imageMessage": map[string]any{"caption": "pic"}}, MessageTimestamp: 101}
	image.Key.ID = "i1"
	image.Key.RemoteJID = fwdHere
	return m{
		status:    "ready",
		mode:      "chat",
		active:    fwdHere,
		msgs:      map[string][]wireMsg{fwdHere: {text, image}},
		whitelist: map[string]string{"15550000002": "Bob"},
		names:     map[string]string{},
		chats: []chat{
			{ID: fwdHere, Name: "Alice", ConversationTimestamp: 300},
			{ID: fwdBob, Name: "Bob", ConversationTimestamp: 200},
			{ID: fwdCat, Name: "Cat", ConversationTimestamp: 100},
			{ID: fwdOld, Name: "Dan", ConversationTimestamp: 50, Archived: true},
			{ID: "status@broadcast", Name: "Status", ConversationTimestamp: 10},
		},
	}
}

func altT() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t"), Alt: true} }

func TestForwardTextAndForwardedMark(t *testing.T) {
	cases := []struct {
		name string
		msg  map[string]any
		want string
		ok   bool
	}{
		{"plain", map[string]any{"conversation": "hi"}, "hi", true},
		{"extended", map[string]any{"extendedTextMessage": map[string]any{"text": "reply"}}, "reply", true},
		{"blank", map[string]any{"conversation": "  "}, "", false},
		{"image", map[string]any{"imageMessage": map[string]any{}}, "", false},
		{"reaction", map[string]any{"reactionMessage": map[string]any{"emoji": "fire"}}, "", false},
	}
	for _, c := range cases {
		got, ok := forwardText(wireMsg{Message: c.msg})
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: forwardText = %q,%v want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
	if !isForwarded(map[string]any{"extendedTextMessage": map[string]any{"text": "x", "forwarded": true}}) {
		t.Error("a message with forwarded=true must be recognised")
	}
	if isForwarded(map[string]any{"extendedTextMessage": map[string]any{"text": "x"}}) || isForwarded(map[string]any{"conversation": "x"}) {
		t.Error("normal messages must not be marked forwarded")
	}
}

func TestOpenForwardPickerNeedsATextMessage(t *testing.T) {
	x := fwdModel()
	if cmd := x.openForwardPicker(); cmd == nil || x.forwardPicker.IsOpen {
		t.Fatal("no selection: expected a message and no list")
	}
	x.selectedMsgID = "ghost"
	if cmd := x.openForwardPicker(); cmd == nil || x.forwardPicker.IsOpen {
		t.Fatal("unknown message: expected a message and no list")
	}
	x.selectedMsgID = "i1"
	if cmd := x.openForwardPicker(); cmd == nil || x.forwardPicker.IsOpen {
		t.Fatal("an image cannot be forwarded yet: expected a message and no list")
	}
	x.selectedMsgID = "t1"
	x.replyPickMode = true
	if cmd := x.openForwardPicker(); cmd != nil || !x.forwardPicker.IsOpen || x.replyPickMode {
		t.Fatalf("a text message should open the list silently: open=%v replyPick=%v", x.forwardPicker.IsOpen, x.replyPickMode)
	}
}

func TestForwardListShowsOtherChatsWithNotAllowedDimmed(t *testing.T) {
	x := fwdModel()
	x.selectedMsgID = "t1"
	x.openForwardPicker()
	items := x.forwardPicker.Items
	labels := make([]string, len(items))
	for i, it := range items {
		labels[i] = it.Label
	}
	if strings.Join(labels, ",") != "Bob,Cat" {
		t.Fatalf("list = %v, want Bob,Cat (not the open chat, an archived chat or Status)", labels)
	}
	if items[0].Dim || !items[1].Dim || items[1].Desc != "not allowed" {
		t.Fatalf("Cat is not whitelisted and should be dimmed with a note: %+v", items)
	}
}

func TestForwardWithNoOtherChatSaysSo(t *testing.T) {
	x := fwdModel()
	x.chats = []chat{{ID: fwdHere, Name: "Alice"}}
	x.selectedMsgID = "t1"
	if cmd := x.openForwardPicker(); cmd == nil || x.forwardPicker.IsOpen {
		t.Fatal("expected a message and no list when there is nowhere to forward to")
	}
}

func TestAltTOpensTheListOnlyInAReadyChat(t *testing.T) {
	x := fwdModel()
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	if !next.(m).forwardPicker.IsOpen {
		t.Fatal("alt+t should open the forward list")
	}
	y := fwdModel()
	y.selectedMsgID = "t1"
	y.mode = "nav"
	next, _ = y.key(altT())
	if next.(m).forwardPicker.IsOpen {
		t.Fatal("alt+t must only work inside an open chat")
	}
	z := fwdModel()
	z.selectedMsgID = "t1"
	z.status = "Connecting..."
	next, _ = z.key(altT())
	if next.(m).forwardPicker.IsOpen {
		t.Fatal("alt+t must wait until the session is ready")
	}
}

func TestAltTWorksWhileReplyPickerIsActive(t *testing.T) {
	x := fwdModel()
	x.replyPickMode = true
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	got := next.(m)
	if !got.forwardPicker.IsOpen || got.replyPickMode {
		t.Fatalf("open=%v replyPick=%v, want true,false", got.forwardPicker.IsOpen, got.replyPickMode)
	}
}

func TestTypingNarrowsTheForwardList(t *testing.T) {
	x := fwdModel()
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)

	next, _ = x.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	x = next.(m)
	vis := x.forwardPicker.VisibleItems()
	if len(vis) != 1 || vis[0].Label != "Cat" {
		t.Fatalf("after typing c the list is %+v, want only Cat", vis)
	}
	if x.input != "" {
		t.Fatalf("typing must go to the filter, not the message box: %q", x.input)
	}
	next, _ = x.key(tea.KeyMsg{Type: tea.KeyBackspace})
	x = next.(m)
	if len(x.forwardPicker.VisibleItems()) != 2 {
		t.Fatalf("Backspace should widen the list again: %+v", x.forwardPicker.VisibleItems())
	}
}

func fwdServer(t *testing.T, bodies *[]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages/forward" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]string
		_ = json.Unmarshal(raw, &b)
		*bodies = append(*bodies, b)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"message":{}}`))
	}))
}

// collectMsgs runs a command and every sub-command, returning the messages.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, collectMsgs(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestEnterForwardsToTheHighlightedAllowedChat(t *testing.T) {
	var bodies []map[string]string
	srv := fwdServer(t, &bodies)
	defer srv.Close()

	x := fwdModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	// Bob is first in the list and allowed.
	next, cmd := x.key(tea.KeyMsg{Type: tea.KeyEnter})
	x = next.(m)

	if x.forwardPicker.IsOpen || x.selectedMsgID != "" {
		t.Fatalf("Enter should close the list and clear the selection: open=%v sel=%q", x.forwardPicker.IsOpen, x.selectedMsgID)
	}
	var done *forwardedMsg
	for _, msg := range collectMsgs(cmd) {
		if f, ok := msg.(forwardedMsg); ok {
			done = &f
		}
	}
	if len(bodies) != 1 || bodies[0]["fromChatId"] != fwdHere || bodies[0]["messageId"] != "t1" || bodies[0]["toChatId"] != fwdBob {
		t.Fatalf("request bodies = %v, want one forward of t1 from Alice to Bob", bodies)
	}
	if done == nil || done.to != "Bob" {
		t.Fatalf("expected a forwardedMsg for Bob, got %+v", done)
	}
}

func TestEnterOnANotAllowedChatSendsNothing(t *testing.T) {
	var bodies []map[string]string
	srv := fwdServer(t, &bodies)
	defer srv.Close()

	x := fwdModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	next, _ = x.key(tea.KeyMsg{Type: tea.KeyDown}) // Cat, not whitelisted
	x = next.(m)
	next, cmd := x.key(tea.KeyMsg{Type: tea.KeyEnter})
	x = next.(m)
	collectMsgs(cmd)

	if len(bodies) != 0 {
		t.Fatalf("a chat that is not allowed must not be sent to, got %v", bodies)
	}
	if x.forwardPicker.IsOpen {
		t.Fatal("the list should close and explain")
	}
}

func TestEscCancelsForwarding(t *testing.T) {
	var bodies []map[string]string
	srv := fwdServer(t, &bodies)
	defer srv.Close()

	x := fwdModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	next, cmd := x.key(tea.KeyMsg{Type: tea.KeyEsc})
	x = next.(m)
	collectMsgs(cmd)
	if x.forwardPicker.IsOpen || x.selectedMsgID != "" || len(bodies) != 0 {
		t.Fatalf("Esc must close without sending: open=%v sel=%q sent=%v", x.forwardPicker.IsOpen, x.selectedMsgID, bodies)
	}
}

func TestEnterWithNothingMatchingDoesNothing(t *testing.T) {
	var bodies []map[string]string
	srv := fwdServer(t, &bodies)
	defer srv.Close()

	x := fwdModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	next, _ = x.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	x = next.(m)
	next, cmd := x.key(tea.KeyMsg{Type: tea.KeyEnter})
	collectMsgs(cmd)
	if len(bodies) != 0 || next.(m).forwardPicker.IsOpen {
		t.Fatalf("no match: nothing should be sent and the list should close (sent=%v)", bodies)
	}
}

func TestForwardedMessageUpdatesTheTopBarAndRefreshesChats(t *testing.T) {
	x := fwdModel()
	next, cmd := x.Update(forwardedMsg{to: "Bob"})
	if cmd == nil {
		t.Fatal("expected commands (top bar and a chat refresh)")
	}
	if !strings.Contains(next.(m).topBarMsg, "Forwarded to Bob") {
		t.Fatalf("top bar = %q, want it to say Forwarded to Bob", next.(m).topBarMsg)
	}
}

func TestForwardedMessageIsLabelledInTheChat(t *testing.T) {
	setTestTheme(t, TokyoNight)
	x := fwdModel()
	fwd := wireMsg{Message: map[string]any{"extendedTextMessage": map[string]any{"text": "passed along", "forwarded": true}}, MessageTimestamp: 200}
	fwd.Key.ID = "f1"
	fwd.Key.RemoteJID = fwdHere
	x.msgs[fwdHere] = append(x.msgs[fwdHere], fwd)
	rendered := ansiStripRe.ReplaceAllString(x.renderMain(100, 14), "")
	if !strings.Contains(rendered, forwardedLabel) || !strings.Contains(rendered, "passed along") {
		t.Fatalf("forwarded message should show its label and text:\n%s", rendered)
	}
	if strings.Count(rendered, forwardedLabel) != 1 {
		t.Fatalf("only the forwarded message gets the label:\n%s", rendered)
	}
}

func TestForwardListFloatsOverTheChat(t *testing.T) {
	useTokyoNight(t)
	x := fwdModel()
	x.w, x.h = 120, 80
	x.selectedMsgID = "t1"
	x.openForwardPicker()
	plain := ansiStripRe.ReplaceAllString(x.renderRightMain(100, 60), "")
	for _, want := range []string{"lunch at noon?", "Forward to", "Bob", "Cat", "not allowed"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q with the chat still visible:\n%s", want, plain)
		}
	}
}

func TestForwardIsOnTheHelpScreen(t *testing.T) {
	found := false
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			if e.key == "Alt+T" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("Alt+T is missing from the help shortcuts")
	}
}
