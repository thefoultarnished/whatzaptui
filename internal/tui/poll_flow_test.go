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

func TestPollCommandOpensTheForm(t *testing.T) {
	x := pollModel()
	cmd, handled := x.runCommand("/poll", false)
	if !handled || cmd != nil || !x.poll.open {
		t.Fatalf("/poll should open the form: handled=%v cmd=%v open=%v", handled, cmd != nil, x.poll.open)
	}
}

func TestPollCommandRefusals(t *testing.T) {
	noChat := pollModel()
	noChat.active = ""
	blocked := fwdModel() // Alice is not whitelisted
	demo := pollModel()
	demo.demoMode = true
	for name, tc := range map[string]struct {
		x    m
		want string
	}{
		"no open chat": {noChat, "Open a chat"},
		"not allowed":  {blocked, "Not whitelisted"},
		"demo mode":    {demo, "Demo mode"},
	} {
		for _, txt := range []string{"/poll", "/poll Q | a | b"} {
			x := tc.x
			cmd, handled := x.runCommand(txt, true)
			if !handled || cmd == nil || x.poll.open {
				t.Errorf("%s %q: handled=%v cmd=%v open=%v", name, txt, handled, cmd != nil, x.poll.open)
			}
			if !strings.Contains(x.topBarMsg, tc.want) {
				t.Errorf("%s %q: top bar = %q, want %q", name, txt, x.topBarMsg, tc.want)
			}
		}
	}
}

func TestInlinePollWithProblemsShowsUsage(t *testing.T) {
	x := pollModel()
	cmd, handled := x.runCommand("/poll Just a question", false)
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd != nil)
	}
	if !strings.Contains(x.topBarMsg, "at least 2") || !strings.Contains(x.topBarMsg, "Usage: /poll") {
		t.Fatalf("top bar = %q, want the problem and the usage", x.topBarMsg)
	}
	if x.poll.open {
		t.Fatal("a bad inline poll must not open the form")
	}
}

func TestCommandsThatOnlyLookLikePollAreNotPollCommands(t *testing.T) {
	x := pollModel()
	if _, handled := x.runPollCommand("/pollx"); handled {
		t.Error("/pollx is not /poll")
	}
	if _, handled := x.runPollCommand("/polls a | b"); handled {
		t.Error("/polls is not /poll")
	}
}

// pollServer records the poll requests it receives.
func pollServer(t *testing.T, bodies *[]map[string]any, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages/poll" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		*bodies = append(*bodies, b)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		if status/100 != 2 {
			_, _ = w.Write([]byte(`{"error":"chat not whitelisted"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"key":{"id":"poll1","remoteJid":"15550000001@s.whatsapp.net","fromMe":true},"message":{"pollCreationMessage":{"name":"Lunch?","options":["Pizza","Sushi"]}},"messageTimestamp":500}}`))
	}))
}

func TestInlinePollIsSentToTheOpenChat(t *testing.T) {
	var bodies []map[string]any
	srv := pollServer(t, &bodies, http.StatusOK)
	defer srv.Close()

	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	cmd, handled := x.runCommand("/poll -m Lunch? | Pizza | | Sushi", false)
	if !handled || cmd == nil {
		t.Fatalf("handled=%v cmd=%v", handled, cmd != nil)
	}
	var sent *pollSentMsg
	for _, msg := range collectMsgs(cmd) {
		if p, ok := msg.(pollSentMsg); ok {
			sent = &p
		}
	}
	if len(bodies) != 1 {
		t.Fatalf("expected one request, got %d", len(bodies))
	}
	b := bodies[0]
	opts, _ := b["options"].([]any)
	if b["chatId"] != fwdHere || b["question"] != "Lunch?" || b["multiple"] != true || len(opts) != 2 || opts[0] != "Pizza" || opts[1] != "Sushi" {
		t.Fatalf("request body = %v, want the cleaned poll for Alice", b)
	}
	if sent == nil || sent.err != nil || sent.msg.Key.ID != "poll1" {
		t.Fatalf("expected a pollSentMsg for poll1, got %+v", sent)
	}
}

func TestOpenFormKeysAreNotLeakedToTheChat(t *testing.T) {
	x := pollModel()
	x.poll.openForm()
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	if x.forwardPicker.IsOpen {
		t.Fatal("Alt+T must not open the forward list while the poll form is open")
	}
	next, _ = x.key(typed("h"))
	x = next.(m)
	next, _ = x.key(typed("i"))
	x = next.(m)
	if x.poll.question != "hi" || x.input != "" || x.inputBuf != "" {
		t.Fatalf("typing should go to the form only: question=%q input=%q", x.poll.question, x.input+x.inputBuf)
	}
}

func TestFormSendsThroughTheModelAndClosesIt(t *testing.T) {
	var bodies []map[string]any
	srv := pollServer(t, &bodies, http.StatusOK)
	defer srv.Close()

	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.poll.openForm()
	x.poll.question = "Lunch?"
	x.poll.options = []string{"Pizza", "Sushi"}
	next, cmd := x.key(altS())
	x = next.(m)
	if x.poll.open {
		t.Fatal("the form should close when the poll is sent")
	}
	var sent pollSentMsg
	for _, msg := range collectMsgs(cmd) {
		if p, ok := msg.(pollSentMsg); ok {
			sent = p
		}
	}
	if len(bodies) != 1 || bodies[0]["multiple"] != false {
		t.Fatalf("bodies = %v, want one single-choice request", bodies)
	}
	next, _ = x.Update(sent)
	x = next.(m)
	found := false
	for _, msg := range x.msgs[fwdHere] {
		if msg.Key.ID == "poll1" {
			found = true
		}
	}
	if !found || !strings.Contains(x.topBarMsg, "Poll sent") {
		t.Fatalf("the sent poll should appear in the chat: found=%v topBar=%q", found, x.topBarMsg)
	}
}

func TestFormStaysOpenWhenThePollIsNotValid(t *testing.T) {
	x := pollModel()
	x.poll.openForm()
	x.poll.question = "Lunch?"
	next, cmd := x.key(altS())
	x = next.(m)
	if !x.poll.open || cmd != nil || !strings.Contains(x.poll.msg, "at least 2") {
		t.Fatalf("open=%v cmd=%v msg=%q, want the form open with a reason", x.poll.open, cmd != nil, x.poll.msg)
	}
	if x.poll.question != "Lunch?" {
		t.Fatal("a refused send must not lose what was typed")
	}
}

func TestEscClosesTheFormFromTheModel(t *testing.T) {
	x := pollModel()
	x.poll.openForm()
	next, cmd := x.key(press(tea.KeyEsc))
	x = next.(m)
	if x.poll.open || cmd == nil || !strings.Contains(x.topBarMsg, "Poll cancelled") {
		t.Fatalf("open=%v topBar=%q", x.poll.open, x.topBarMsg)
	}
}

func TestCtrlCStillQuitsWhileTheFormIsOpen(t *testing.T) {
	x := pollModel()
	x.poll.openForm()
	_, cmd := x.key(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C must still quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C should produce a quit")
	}
}

func TestBackendRefusalShowsInTheTopBar(t *testing.T) {
	var bodies []map[string]any
	srv := pollServer(t, &bodies, http.StatusForbidden)
	defer srv.Close()

	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	cmd, _ := x.runCommand("/poll Q | a | b", false)
	var sent pollSentMsg
	for _, msg := range collectMsgs(cmd) {
		if p, ok := msg.(pollSentMsg); ok {
			sent = p
		}
	}
	if sent.err == nil {
		t.Fatal("a 403 must come back as an error")
	}
	next, _ := x.Update(sent)
	x = next.(m)
	if !strings.Contains(x.topBarMsg, "Poll failed") || !strings.Contains(x.topBarMsg, "not whitelisted") {
		t.Fatalf("top bar = %q, want the failure and its reason", x.topBarMsg)
	}
	if len(x.msgs[fwdHere]) != 2 {
		t.Fatalf("a failed poll must not add a message, chat has %d", len(x.msgs[fwdHere]))
	}
}

func TestSentPollIsNotAddedTwice(t *testing.T) {
	x := pollModel()
	msg := wireMsg{Message: map[string]any{"pollCreationMessage": map[string]any{"name": "q"}}}
	msg.Key.ID = "poll9"
	msg.Key.RemoteJID = fwdHere
	x.msgs[fwdHere] = append(x.msgs[fwdHere], msg)
	before := len(x.msgs[fwdHere])
	x.applyPollSent(pollSentMsg{chatID: fwdHere, msg: msg})
	if len(x.msgs[fwdHere]) != before {
		t.Fatalf("the poll was already in the chat (it arrived over the socket): %d messages, want %d", len(x.msgs[fwdHere]), before)
	}
}

func TestPollFormRendersAndFits(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 30
	x.cursorOn = true
	x.poll.openForm()
	x.poll.question = "Lunch on Friday?"
	x.poll.options = []string{"Pizza", "Sushi"}
	plain := ansiStripRe.ReplaceAllString(x.renderPollForm(100, 30), "")
	for _, want := range []string{"New poll", "Question", "Lunch on Friday?", "Options (2/12)", "Pizza", "Sushi", "Allow several answers", "Send poll", "Alt+S send"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in the form:\n%s", want, plain)
		}
	}
	x.poll.multiple = true
	if plain := ansiStripRe.ReplaceAllString(x.renderPollForm(100, 30), ""); !strings.Contains(plain, "[x] Allow several answers") {
		t.Errorf("the switch should show as on:\n%s", plain)
	}
}

func TestPollFormFitsShortAndNarrowTerminals(t *testing.T) {
	useTokyoNight(t)
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {140, 50}} {
		x := pollModel()
		x.w, x.h = size[0], size[1]
		x.poll.openForm()
		x.poll.question = strings.Repeat("a long question ", 20)
		x.poll.options = make([]string, maxPollOptions)
		for i := range x.poll.options {
			x.poll.options[i] = strings.Repeat("option ", 20)
		}
		x.poll.msg = strings.Repeat("a long message ", 10)
		for _, field := range []int{0, 1, 6, 12, x.poll.multipleField(), x.poll.sendField()} {
			x.poll.field = field
			lines := strings.Split(ansiStripRe.ReplaceAllString(x.renderPollForm(size[0], size[1]), ""), "\n")
			if len(lines) > size[1] {
				t.Errorf("%dx%d focus %d: form is %d lines tall", size[0], size[1], field, len(lines))
			}
			for _, l := range lines {
				if w := runeDisplayWidth(l); w > size[0] {
					t.Errorf("%dx%d focus %d: a line is %d cells wide: %q", size[0], size[1], field, w, l)
				}
			}
		}
	}
}

func TestFocusedOptionStaysVisibleInAShortForm(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 20
	x.poll.openForm()
	x.poll.options = make([]string, maxPollOptions)
	for i := range x.poll.options {
		x.poll.options[i] = "opt-" + string(rune('A'+i))
	}
	for _, focus := range []int{1, 6, 12} {
		x.poll.field = focus
		plain := ansiStripRe.ReplaceAllString(x.renderPollForm(100, 20), "")
		want := "opt-" + string(rune('A'+focus-1))
		if !strings.Contains(plain, want) {
			t.Errorf("focused option %q must be on screen:\n%s", want, plain)
		}
	}
}

func TestPollFormFloatsOverTheChat(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 120, 80
	x.poll.openForm()
	plain := ansiStripRe.ReplaceAllString(x.renderRightMain(100, 60), "")
	for _, want := range []string{"lunch at noon?", "New poll", "Question"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q with the chat still visible:\n%s", want, plain)
		}
	}
	x.closeFloatingPanels()
	if x.poll.open {
		t.Fatal("closeFloatingPanels should close the poll form on that copy")
	}
}

func TestWhitelistShortcutIsBlockedWhileTheFormIsOpen(t *testing.T) {
	x := pollModel()
	x.poll.openForm()
	before := len(x.whitelist)
	if cmd := x.toggleWhitelistForSelection(); cmd == nil || len(x.whitelist) != before {
		t.Fatal("the whitelist must not change behind the open form")
	}
}

func TestPollIsOnTheHelpScreen(t *testing.T) {
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			if e.key == "/poll" {
				return
			}
		}
	}
	t.Fatal("/poll is missing from the help commands")
}
