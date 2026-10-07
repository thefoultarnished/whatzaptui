package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pollWithLimit(id string, limit any) wireMsg {
	msg := pollWithID(id)
	if limit != nil {
		msg.Message["pollCreationMessage"].(map[string]any)["selectableCount"] = limit
	}
	return msg
}

func TestPollSelectable(t *testing.T) {
	cases := []struct {
		in    any
		n     int
		known bool
	}{
		{float64(1), 1, true},
		{uint32(3), 3, true},
		{2, 2, true},
		{float64(0), 0, true},
		{nil, 0, false},
		{"1", 0, false},
	}
	for _, c := range cases {
		poll := map[string]any{}
		if c.in != nil {
			poll["selectableCount"] = c.in
		}
		if n, known := pollSelectable(poll); n != c.n || known != c.known {
			t.Errorf("%#v: got %d,%v want %d,%v", c.in, n, known, c.n, c.known)
		}
	}
}

func newVoteForm(single bool, maxPick int, options ...string) pollVoteForm {
	return pollVoteForm{
		open: true, pollID: "P1", question: "Lunch?", options: options,
		counts: make([]int, len(options)), ticks: make([]bool, len(options)),
		single: single, maxPick: maxPick,
	}
}

func TestSingleChoiceTickingClearsTheOthers(t *testing.T) {
	f := newVoteForm(true, 0, "Pizza", "Sushi", "Ramen")
	f.cursor = 0
	f.handleKey(press(tea.KeySpace))
	f.cursor = 2
	f.handleKey(press(tea.KeySpace))
	if got := strings.Join(f.ticked(), "|"); got != "Ramen" {
		t.Fatalf("ticked = %q, want only Ramen", got)
	}
	f.handleKey(press(tea.KeySpace))
	if len(f.ticked()) != 0 {
		t.Fatal("Space on the ticked option unticks it")
	}
}

func TestMultipleChoiceTicksAccumulateAndHonourTheCap(t *testing.T) {
	f := newVoteForm(false, 2, "Pizza", "Sushi", "Ramen")
	for i := 0; i < 3; i++ {
		f.cursor = i
		f.handleKey(press(tea.KeySpace))
	}
	if got := strings.Join(f.ticked(), "|"); got != "Pizza|Sushi" {
		t.Fatalf("ticked = %q, the cap of 2 must hold", got)
	}
	f.cursor = 2
	f.handleKey(press(tea.KeySpace))
	if !strings.Contains(f.msg, "at most 2") {
		t.Fatalf("hitting the cap should say so, got %q", f.msg)
	}
	f.cursor = 0
	f.handleKey(press(tea.KeySpace)) // untick one
	f.cursor = 2
	f.handleKey(press(tea.KeySpace))
	if got := strings.Join(f.ticked(), "|"); got != "Sushi|Ramen" {
		t.Fatalf("after unticking there is room again, got %q", got)
	}
	free := newVoteForm(false, 0, "A", "B", "C")
	for i := 0; i < 3; i++ {
		free.cursor = i
		free.handleKey(press(tea.KeySpace))
	}
	if len(free.ticked()) != 3 {
		t.Fatalf("no cap means all can be ticked, got %v", free.ticked())
	}
}

func TestVoteFormCursorWrapsThroughTheWithdrawRow(t *testing.T) {
	f := newVoteForm(false, 0, "A", "B")
	f.handleKey(press(tea.KeyUp))
	if f.cursor != f.withdrawRow() {
		t.Fatalf("Up from the top goes to Withdraw, got %d", f.cursor)
	}
	f.handleKey(press(tea.KeyDown))
	if f.cursor != 0 {
		t.Fatalf("Down from Withdraw wraps to the top, got %d", f.cursor)
	}
	f.handleKey(press(tea.KeyTab))
	if f.cursor != 1 {
		t.Fatalf("Tab moves down, got %d", f.cursor)
	}
	f.cursor = f.withdrawRow()
	f.handleKey(press(tea.KeySpace))
	if len(f.ticked()) != 0 {
		t.Fatal("Space on the Withdraw row ticks nothing")
	}
}

func TestEnterOnASingleChoiceOptionVotesForIt(t *testing.T) {
	f := newVoteForm(true, 0, "Pizza", "Sushi")
	f.ticks[0] = true // an earlier vote for Pizza
	f.cursor = 1
	if got := f.handleKey(press(tea.KeyEnter)); got != voteSend {
		t.Fatalf("Enter on an option = %v, want send", got)
	}
	if got := strings.Join(f.ticked(), "|"); got != "Sushi" {
		t.Fatalf("ticked = %q, Enter votes for the option under the cursor", got)
	}
}

func TestEnterOnAMultipleChoicePollNeedsATick(t *testing.T) {
	f := newVoteForm(false, 0, "Pizza", "Sushi")
	if got := f.handleKey(press(tea.KeyEnter)); got != voteNone || !strings.Contains(f.msg, "Tick at least one") {
		t.Fatalf("Enter with nothing ticked: action=%v msg=%q", got, f.msg)
	}
	f.cursor = 1
	f.handleKey(press(tea.KeySpace))
	f.cursor = 0
	f.handleKey(press(tea.KeySpace))
	if got := f.handleKey(press(tea.KeyEnter)); got != voteSend {
		t.Fatalf("Enter with ticks = %v, want send", got)
	}
	if got := strings.Join(f.ticked(), "|"); got != "Pizza|Sushi" {
		t.Fatalf("ticked = %q, in the poll's order", got)
	}
}

func TestWithdrawRow(t *testing.T) {
	f := newVoteForm(false, 0, "Pizza")
	f.cursor = f.withdrawRow()
	if got := f.handleKey(press(tea.KeyEnter)); got != voteNone || !strings.Contains(f.msg, "not voted") {
		t.Fatalf("nothing to withdraw: action=%v msg=%q", got, f.msg)
	}
	f.hadVote = true
	if got := f.handleKey(press(tea.KeyEnter)); got != voteClear {
		t.Fatalf("Enter on Withdraw with a vote = %v, want clear", got)
	}
}

func TestVoteFormEscCloses(t *testing.T) {
	f := newVoteForm(false, 0, "A")
	if got := f.handleKey(press(tea.KeyEsc)); got != voteClose {
		t.Fatalf("Esc = %v, want close", got)
	}
}

func TestVoteFormMessageClearsOnTheNextKey(t *testing.T) {
	f := newVoteForm(false, 0, "A", "B")
	f.handleKey(press(tea.KeyEnter))
	if f.msg == "" {
		t.Fatal("expected a hint")
	}
	f.handleKey(press(tea.KeyDown))
	if f.msg != "" {
		t.Fatalf("the hint should go away, got %q", f.msg)
	}
}

func TestPollListItemsAreNewestFirstWithVoteCounts(t *testing.T) {
	x := pollModel()
	old := pollWithID("OLD")
	old.Message["pollCreationMessage"].(map[string]any)["name"] = "Old question"
	fresh := pollWithID("NEW")
	fresh.Message["pollCreationMessage"].(map[string]any)["name"] = "Fresh question"
	x.msgs[fwdHere] = append(x.msgs[fwdHere], old, fresh,
		voteMsg("v1", "NEW", false, "", []string{"Pizza"}),
		voteMsg("v2", "NEW", true, "", []string{"Sushi"}),
		voteMsg("v3", "OLD", false, "", []string{"Pizza"}),
	)
	items := x.pollListItems()
	if len(items) != 2 || items[0].Key != "NEW" || items[1].Key != "OLD" {
		t.Fatalf("expected the newest poll first: %+v", items)
	}
	if !strings.Contains(items[0].Label, "Fresh question") || !strings.Contains(items[0].Label, "(2 votes)") {
		t.Errorf("label = %q, want the question and 2 votes", items[0].Label)
	}
	if !strings.Contains(items[1].Label, "(1 vote)") || strings.Contains(items[1].Label, "1 votes") {
		t.Errorf("label = %q, want singular", items[1].Label)
	}
}

func TestPollListItemsAreCappedAndSkipNonPolls(t *testing.T) {
	x := pollModel()
	for i := 0; i < 12; i++ {
		x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID(fmt.Sprintf("P%d", i)))
	}
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID(""))
	items := x.pollListItems()
	if len(items) != maxPollListRows {
		t.Fatalf("expected the %d newest polls, got %d", maxPollListRows, len(items))
	}
	if items[0].Key != "P11" {
		t.Fatalf("newest first (a poll with no ID is skipped), got %q", items[0].Key)
	}
	x.active = fwdBob
	if got := x.pollListItems(); len(got) != 0 {
		t.Fatalf("another chat has no polls, got %v", got)
	}
}

func TestPollsCommandWithNoPollsSaysSo(t *testing.T) {
	x := pollModel()
	cmd, handled := x.runCommand("/polls", false)
	if !handled || cmd == nil || x.pollListPicker.IsOpen || !strings.Contains(x.topBarMsg, "No polls") {
		t.Fatalf("handled=%v open=%v topBar=%q", handled, x.pollListPicker.IsOpen, x.topBarMsg)
	}
}

func TestPollsCommandOpensTheList(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	cmd, handled := x.runCommand("/polls", false)
	if !handled || cmd != nil || !x.pollListPicker.IsOpen {
		t.Fatalf("handled=%v cmd=%v open=%v", handled, cmd != nil, x.pollListPicker.IsOpen)
	}
}

func TestPollsCommandRefusals(t *testing.T) {
	noChat := pollModel()
	noChat.active = ""
	blocked := fwdModel()
	blocked.msgs[fwdHere] = append(blocked.msgs[fwdHere], pollWithID("P1"))
	demo := pollModel()
	demo.msgs[fwdHere] = append(demo.msgs[fwdHere], pollWithID("P1"))
	demo.demoMode = true
	for name, tc := range map[string]struct {
		x    m
		want string
	}{
		"no open chat": {noChat, "Open a chat"},
		"not allowed":  {blocked, "Not whitelisted"},
		"demo mode":    {demo, "Demo mode"},
	} {
		x := tc.x
		cmd, handled := x.runCommand("/polls", true)
		if !handled || cmd == nil || x.pollListPicker.IsOpen {
			t.Errorf("%s: handled=%v cmd=%v open=%v", name, handled, cmd != nil, x.pollListPicker.IsOpen)
		}
		if !strings.Contains(x.topBarMsg, tc.want) {
			t.Errorf("%s: top bar = %q, want %q", name, x.topBarMsg, tc.want)
		}
	}
}

func TestPollsCommandIsNotConfusedWithCreatePoll(t *testing.T) {
	x := pollModel()
	if _, handled := x.runPollsCommand("/polls now"); handled {
		t.Error("/polls takes no arguments")
	}
	if _, handled := x.runPollsCommand("/poll"); handled {
		t.Error("/poll is the create command")
	}
	if _, handled := x.runPollCommand("/polls"); handled {
		t.Error("/polls is not a create command")
	}
	for _, txt := range []string{"/createpoll", "/poll", "/createpoll Q | a | b", "/poll Q | a | b"} {
		if _, matched := pollCommandArgs(txt); !matched {
			t.Errorf("%q should be a create command", txt)
		}
	}
	for _, txt := range []string{"/createpolls", "/pollx", "/polls", "createpoll"} {
		if _, matched := pollCommandArgs(txt); matched {
			t.Errorf("%q must not be a create command", txt)
		}
	}
}

func TestCreatePollCommandWorksUnderBothNames(t *testing.T) {
	for _, name := range []string{"/createpoll", "/poll"} {
		x := pollModel()
		if cmd, handled := x.runCommand(name, false); !handled || cmd != nil || !x.poll.open {
			t.Errorf("%s should open the create form", name)
		}
		q, opts, multiple, ok := parseInlinePoll(name + " -m Lunch? | Pizza | Sushi")
		if !ok || !multiple || q != "Lunch? " || len(opts) != 2 {
			t.Errorf("%s inline form: %q %q %v %v", name, q, opts, multiple, ok)
		}
	}
}

func TestOpenVoteFormPresetsMyVoteAndReadsTheLimit(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", uint32(1)),
		voteMsg("v1", "P1", true, "", []string{"Sushi"}),
		voteMsg("v2", "P1", false, "", []string{"Pizza"}),
	)
	if cmd := x.openVoteForm("P1"); cmd != nil || !x.voteForm.open {
		t.Fatal("the form should open")
	}
	f := x.voteForm
	if !f.single || f.maxPick != 0 || !f.hadVote {
		t.Fatalf("single=%v maxPick=%d hadVote=%v", f.single, f.maxPick, f.hadVote)
	}
	if strings.Join(f.ticked(), "|") != "Sushi" {
		t.Fatalf("my current vote should be ticked, got %v", f.ticked())
	}
	if fmt.Sprint(f.counts) != "[1 1]" {
		t.Fatalf("counts = %v", f.counts)
	}
	if f.question != "Lunch on Friday?" {
		t.Fatalf("question = %q", f.question)
	}
}

func TestOpenVoteFormLimits(t *testing.T) {
	cases := []struct {
		limit  any
		single bool
		max    int
	}{
		{uint32(1), true, 0},
		{float64(2), false, 2},
		{uint32(0), false, 0},
		{nil, false, 0},
	}
	for _, c := range cases {
		x := pollModel()
		x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", c.limit))
		x.openVoteForm("P1")
		if x.voteForm.single != c.single || x.voteForm.maxPick != c.max {
			t.Errorf("limit %#v: single=%v maxPick=%d, want %v %d", c.limit, x.voteForm.single, x.voteForm.maxPick, c.single, c.max)
		}
		if x.voteForm.hadVote {
			t.Errorf("limit %#v: no vote yet", c.limit)
		}
	}
}

func TestOpenVoteFormForAMissingPoll(t *testing.T) {
	x := pollModel()
	if cmd := x.openVoteForm("nope"); cmd == nil || x.voteForm.open || !strings.Contains(x.topBarMsg, "not available") {
		t.Fatalf("open=%v topBar=%q", x.voteForm.open, x.topBarMsg)
	}
	x.msgs[fwdHere] = append(x.msgs[fwdHere], wireMsg{Message: map[string]any{"conversation": "hi"}, Key: wireMsg{}.Key})
	if cmd := x.openVoteForm("t1"); cmd == nil || x.voteForm.open {
		t.Fatal("a text message is not a poll")
	}
}

func TestPollListEnterOpensTheVoteForm(t *testing.T) {
	x := pollModel()
	old := pollWithID("OLD")
	old.Message["pollCreationMessage"].(map[string]any)["name"] = "Old question"
	x.msgs[fwdHere] = append(x.msgs[fwdHere], old, pollWithID("NEW"))
	x.openPollList()
	next, _ := x.key(press(tea.KeyDown)) // NEW is first, move to OLD
	x = next.(m)
	next, _ = x.key(press(tea.KeyEnter))
	x = next.(m)
	if x.pollListPicker.IsOpen || !x.voteForm.open || x.voteForm.pollID != "OLD" {
		t.Fatalf("list open=%v form open=%v poll=%q", x.pollListPicker.IsOpen, x.voteForm.open, x.voteForm.pollID)
	}
}

func TestPollListCanBeFilteredAndCancelled(t *testing.T) {
	x := pollModel()
	old := pollWithID("OLD")
	old.Message["pollCreationMessage"].(map[string]any)["name"] = "Movie night"
	x.msgs[fwdHere] = append(x.msgs[fwdHere], old, pollWithID("NEW"))
	x.openPollList()
	for _, r := range "movie" {
		next, _ := x.key(typed(string(r)))
		x = next.(m)
	}
	next, _ := x.key(press(tea.KeyEnter))
	x = next.(m)
	if !x.voteForm.open || x.voteForm.pollID != "OLD" {
		t.Fatalf("typing narrows the list to the movie poll, got %q", x.voteForm.pollID)
	}

	y := pollModel()
	y.msgs[fwdHere] = append(y.msgs[fwdHere], pollWithID("P1"))
	y.openPollList()
	next, _ = y.key(press(tea.KeyEsc))
	y = next.(m)
	if y.pollListPicker.IsOpen || y.voteForm.open {
		t.Fatal("Esc closes the list and opens nothing")
	}
	z := pollModel()
	z.msgs[fwdHere] = append(z.msgs[fwdHere], pollWithID("P1"))
	z.openPollList()
	for _, r := range "zzzz" {
		next, _ = z.key(typed(string(r)))
		z = next.(m)
	}
	next, _ = z.key(press(tea.KeyEnter))
	z = next.(m)
	if z.voteForm.open {
		t.Fatal("Enter with nothing matching opens nothing")
	}
}

func voteServer(t *testing.T, bodies *[]map[string]any, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages/poll/vote" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		*bodies = append(*bodies, b)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		if status/100 != 2 {
			_, _ = w.Write([]byte(`{"error":"this poll cannot be voted on here"}`))
			return
		}
		names, _ := json.Marshal(b["options"])
		_, _ = fmt.Fprintf(w, `{"message":{"key":{"id":"vote1","remoteJid":"15550000001@s.whatsapp.net","fromMe":true},"message":{"pollUpdateMessage":{"pollChatID":"15550000001@s.whatsapp.net","pollMsgID":"P1","selectedOptionNames":%s}},"messageTimestamp":600}}`, names)
	}))
}

func TestVotingThroughTheWholeFlow(t *testing.T) {
	useTokyoNight(t)
	var bodies []map[string]any
	srv := voteServer(t, &bodies, http.StatusOK)
	defer srv.Close()

	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", uint32(1)))
	if cmd, _ := x.runCommand("/polls", false); cmd != nil {
		t.Fatal("the list opens silently")
	}
	next, _ := x.key(press(tea.KeyEnter)) // pick the poll
	x = next.(m)
	next, _ = x.key(press(tea.KeyDown)) // Sushi
	x = next.(m)
	next, cmd := x.key(press(tea.KeyEnter)) // vote
	x = next.(m)
	if x.voteForm.open {
		t.Fatal("the panel closes when the vote is sent")
	}
	var voted pollVotedMsg
	for _, msg := range collectMsgs(cmd) {
		if v, ok := msg.(pollVotedMsg); ok {
			voted = v
		}
	}
	if len(bodies) != 1 {
		t.Fatalf("expected one request, got %d", len(bodies))
	}
	b := bodies[0]
	opts, _ := b["options"].([]any)
	if b["chatId"] != fwdHere || b["pollMessageId"] != "P1" || len(opts) != 1 || opts[0] != "Sushi" {
		t.Fatalf("request = %v, want Sushi on P1 in Alice's chat", b)
	}
	next, _ = x.Update(voted)
	x = next.(m)
	if !strings.Contains(x.topBarMsg, "Vote sent") {
		t.Fatalf("top bar = %q", x.topBarMsg)
	}
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, `You voted "Sushi"`) || !strings.Contains(rendered, "1 vote") || !strings.Contains(rendered, "● Sushi") {
		t.Fatalf("the card should show my vote inside it:\n%s", rendered)
	}
}

func TestWithdrawingThroughTheFlow(t *testing.T) {
	useTokyoNight(t)
	var bodies []map[string]any
	srv := voteServer(t, &bodies, http.StatusOK)
	defer srv.Close()

	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	mine := voteMsg("v0", "P1", true, "", []string{"Pizza"})
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", uint32(0)), mine)
	x.openVoteForm("P1")
	next, _ := x.key(press(tea.KeyUp)) // Withdraw row
	x = next.(m)
	next, cmd := x.key(press(tea.KeyEnter))
	x = next.(m)
	var voted pollVotedMsg
	for _, msg := range collectMsgs(cmd) {
		if v, ok := msg.(pollVotedMsg); ok {
			voted = v
		}
	}
	opts, isList := bodies[0]["options"].([]any)
	if !isList || len(opts) != 0 {
		t.Fatalf("withdrawing sends an empty list, not null: %v", bodies[0])
	}
	next, _ = x.Update(voted)
	x = next.(m)
	if !strings.Contains(x.topBarMsg, "withdrawn") {
		t.Fatalf("top bar = %q", x.topBarMsg)
	}
	rendered := plainLine(x.renderMain(100, 40))
	if !strings.Contains(rendered, `You withdrew "Pizza"`) {
		t.Fatalf("the card should say what was withdrawn:\n%s", rendered)
	}
}

func TestBackendRefusalOfAVoteShowsInTheTopBar(t *testing.T) {
	var bodies []map[string]any
	srv := voteServer(t, &bodies, http.StatusUnprocessableEntity)
	defer srv.Close()
	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	cmd := x.sendVoteCmd("P1", []string{"Pizza"})
	var voted pollVotedMsg
	for _, msg := range collectMsgs(cmd) {
		if v, ok := msg.(pollVotedMsg); ok {
			voted = v
		}
	}
	if voted.err == nil {
		t.Fatal("a 422 must come back as an error")
	}
	before := len(x.msgs[fwdHere])
	next, _ := x.Update(voted)
	x = next.(m)
	if !strings.Contains(x.topBarMsg, "Vote failed") || !strings.Contains(x.topBarMsg, "cannot be voted on") {
		t.Fatalf("top bar = %q", x.topBarMsg)
	}
	if len(x.msgs[fwdHere]) != before {
		t.Fatal("a failed vote adds no message")
	}
}

func TestVoteInDemoModeSendsNothing(t *testing.T) {
	x := pollModel()
	x.demoMode = true
	cmd := x.sendVoteCmd("P1", []string{"Pizza"})
	if cmd == nil || !strings.Contains(x.topBarMsg, "Demo mode") {
		t.Fatalf("topBar = %q", x.topBarMsg)
	}
}

func TestAppliedVoteIsNotAddedTwice(t *testing.T) {
	x := pollModel()
	vote := voteMsg("vote1", "P1", true, "", []string{"Pizza"})
	x.msgs[fwdHere] = append(x.msgs[fwdHere], vote)
	before := len(x.msgs[fwdHere])
	x.applyPollVoted(pollVotedMsg{chatID: fwdHere, msg: vote})
	if len(x.msgs[fwdHere]) != before {
		t.Fatal("the vote had already arrived over the socket")
	}
}

func TestVoteKeysAreNotLeakedToTheChat(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	x.openVoteForm("P1")
	x.selectedMsgID = "t1"
	next, _ := x.key(altT())
	x = next.(m)
	if x.forwardPicker.IsOpen || !x.voteForm.open {
		t.Fatal("Alt+T must not open the forward list while the vote panel is open")
	}
	y := pollModel()
	y.msgs[fwdHere] = append(y.msgs[fwdHere], pollWithID("P1"))
	y.openPollList()
	next, _ = y.key(altT())
	y = next.(m)
	if y.forwardPicker.IsOpen || !y.pollListPicker.IsOpen {
		t.Fatal("Alt+T must not open the forward list while the poll list is open")
	}
}

func TestCtrlCStillQuitsInTheVotePanels(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	x.openVoteForm("P1")
	if _, cmd := x.key(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("Ctrl+C must quit from the vote panel")
	}
	y := pollModel()
	y.msgs[fwdHere] = append(y.msgs[fwdHere], pollWithID("P1"))
	y.openPollList()
	if _, cmd := y.key(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("Ctrl+C must quit from the poll list")
	}
}

func TestVotePanelRendersAndFits(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 100, 30
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", uint32(0)),
		voteMsg("v1", "P1", false, "", []string{"Pizza"}))
	x.openVoteForm("P1")
	x.voteForm.ticks[1] = true
	plain := plainLine(x.renderVoteForm(100, 30))
	for _, want := range []string{"Vote", "Lunch on Friday?", "[ ] Pizza", "[x] Sushi", "Withdraw my vote", "Space tick"} {
		if !strings.Contains(plain, want) {
			t.Errorf("expected %q in:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "Pizza  1") {
		t.Errorf("the vote count belongs beside the option:\n%s", plain)
	}
	single := pollModel()
	single.msgs[fwdHere] = append(single.msgs[fwdHere], pollWithLimit("P1", uint32(1)))
	single.openVoteForm("P1")
	single.voteForm.ticks[0] = true
	plain = plainLine(single.renderVoteForm(100, 30))
	if !strings.Contains(plain, "(●) Pizza") || !strings.Contains(plain, "( ) Sushi") || strings.Contains(plain, "Space tick") {
		t.Errorf("a single choice poll uses radio marks and no Space hint:\n%s", plain)
	}
	for _, size := range [][2]int{{60, 20}, {80, 24}, {140, 50}} {
		y := pollModel()
		y.w, y.h = size[0], size[1]
		y.msgs[fwdHere] = append(y.msgs[fwdHere], pollWithID("P1"))
		y.openVoteForm("P1")
		y.voteForm.question = strings.Repeat("a long question ", 20)
		y.voteForm.msg = strings.Repeat("a long message ", 10)
		for i := range y.voteForm.options {
			y.voteForm.options[i] = strings.Repeat("option ", 20)
		}
		lines := strings.Split(plainLine(y.renderVoteForm(size[0], size[1])), "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: panel is %d lines tall", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if w := runeDisplayWidth(l); w > size[0] {
				t.Errorf("%dx%d: a line is %d cells wide: %q", size[0], size[1], w, l)
			}
		}
	}
}

func TestVotePanelsFloatOverTheChat(t *testing.T) {
	useTokyoNight(t)
	x := pollModel()
	x.w, x.h = 120, 80
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	x.openPollList()
	plain := plainLine(x.renderRightMain(100, 60))
	if !strings.Contains(plain, "Polls") || !strings.Contains(plain, "lunch at noon?") {
		t.Fatalf("the list should float over the chat:\n%s", plain)
	}
	x.pollListPicker.Close(false)
	x.openVoteForm("P1")
	plain = plainLine(x.renderRightMain(100, 60))
	if !strings.Contains(plain, "Withdraw my vote") || !strings.Contains(plain, "lunch at noon?") {
		t.Fatalf("the vote panel should float over the chat:\n%s", plain)
	}
	x.closeFloatingPanels()
	if x.voteForm.open || x.pollListPicker.IsOpen {
		t.Fatal("closeFloatingPanels closes both")
	}
}

func TestWhitelistShortcutIsBlockedWhileVoting(t *testing.T) {
	x := pollModel()
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithID("P1"))
	x.openVoteForm("P1")
	before := len(x.whitelist)
	if cmd := x.toggleWhitelistForSelection(); cmd == nil || len(x.whitelist) != before {
		t.Fatal("the whitelist must not change behind the vote panel")
	}
	y := pollModel()
	y.msgs[fwdHere] = append(y.msgs[fwdHere], pollWithID("P1"))
	y.openPollList()
	if cmd := y.toggleWhitelistForSelection(); cmd == nil || len(y.whitelist) != before {
		t.Fatal("the whitelist must not change behind the poll list")
	}
}

func TestPollsIsOnTheHelpScreen(t *testing.T) {
	for _, sec := range helpSections {
		for _, e := range sec.entries {
			if e.key == "/polls" {
				return
			}
		}
	}
	t.Fatal("/polls is missing from the help commands")
}

// typedSpace is Space as some terminals send it: a typed character.
func typedSpace() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")} }

func TestTypedSpaceTicksAnOption(t *testing.T) {
	f := newVoteForm(false, 0, "Pizza", "Sushi")
	f.handleKey(typedSpace())
	if got := strings.Join(f.ticked(), "|"); got != "Pizza" {
		t.Fatalf("a typed space should tick the option, ticked = %q", got)
	}
	f.handleKey(typedSpace())
	if len(f.ticked()) != 0 {
		t.Fatal("a second typed space unticks it")
	}
}

func TestTypedSpaceBehavesLikeTheSpaceKey(t *testing.T) {
	single := newVoteForm(true, 0, "Pizza", "Sushi")
	single.handleKey(typedSpace())
	single.cursor = 1
	single.handleKey(typedSpace())
	if got := strings.Join(single.ticked(), "|"); got != "Sushi" {
		t.Fatalf("single choice: ticked = %q, want only Sushi", got)
	}
	capped := newVoteForm(false, 1, "Pizza", "Sushi")
	capped.handleKey(typedSpace())
	capped.cursor = 1
	capped.handleKey(typedSpace())
	if len(capped.ticked()) != 1 || !strings.Contains(capped.msg, "at most 1") {
		t.Fatalf("the cap holds for a typed space too: %v %q", capped.ticked(), capped.msg)
	}
	f := newVoteForm(false, 0, "A")
	f.cursor = f.withdrawRow()
	f.handleKey(typedSpace())
	if len(f.ticked()) != 0 {
		t.Fatal("a typed space on the Withdraw row ticks nothing")
	}
}

func TestOtherTypedCharactersDoNothingInTheVotePanel(t *testing.T) {
	f := newVoteForm(false, 0, "Pizza", "Sushi")
	f.handleKey(typed("x"))
	f.handleKey(typed("  ")) // two spaces at once is not the Space key
	f.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" "), Alt: true})
	if len(f.ticked()) != 0 || f.cursor != 0 {
		t.Fatalf("stray characters must not tick or move: ticked=%v cursor=%d", f.ticked(), f.cursor)
	}
}

func TestVotingWithATypedSpaceThroughTheModel(t *testing.T) {
	var bodies []map[string]any
	srv := voteServer(t, &bodies, http.StatusOK)
	defer srv.Close()
	x := pollModel()
	x.client, x.baseURL = srv.Client(), srv.URL
	x.msgs[fwdHere] = append(x.msgs[fwdHere], pollWithLimit("P1", uint32(0)))
	x.openVoteForm("P1")
	next, _ := x.key(typedSpace()) // tick Pizza
	x = next.(m)
	next, _ = x.key(press(tea.KeyDown))
	x = next.(m)
	next, _ = x.key(typedSpace()) // tick Sushi
	x = next.(m)
	next, cmd := x.key(press(tea.KeyEnter))
	x = next.(m)
	collectMsgs(cmd)
	if len(bodies) != 1 {
		t.Fatalf("expected one vote request, got %d", len(bodies))
	}
	opts, _ := bodies[0]["options"].([]any)
	if len(opts) != 2 || opts[0] != "Pizza" || opts[1] != "Sushi" {
		t.Fatalf("options = %v, want Pizza and Sushi", opts)
	}
}
