package whatsapp

import (
	"errors"
	"strings"
	"testing"
)

func opts(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "option " + string(rune('a'+i))
	}
	return out
}

func TestCleanPollAcceptsAValidPoll(t *testing.T) {
	q, o, err := CleanPoll("  Lunch   on\nFriday? ", []string{" Pizza ", "Sushi", "  ", "Ramen  bowl"})
	if err != nil {
		t.Fatal(err)
	}
	if q != "Lunch on Friday?" {
		t.Errorf("question = %q, want whitespace tidied", q)
	}
	if strings.Join(o, "|") != "Pizza|Sushi|Ramen bowl" {
		t.Errorf("options = %q, want blanks dropped and whitespace tidied", o)
	}
}

func TestCleanPollLimitsAreInclusive(t *testing.T) {
	if _, _, err := CleanPoll("q", opts(MinPollOptions)); err != nil {
		t.Errorf("exactly %d options must be accepted: %v", MinPollOptions, err)
	}
	if _, _, err := CleanPoll("q", opts(MaxPollOptions)); err != nil {
		t.Errorf("exactly %d options must be accepted: %v", MaxPollOptions, err)
	}
	if _, _, err := CleanPoll(strings.Repeat("q", MaxPollQuestionRunes), opts(2)); err != nil {
		t.Errorf("a question of exactly %d characters must be accepted: %v", MaxPollQuestionRunes, err)
	}
	long := []string{strings.Repeat("o", MaxPollOptionRunes), "short"}
	if _, _, err := CleanPoll("q", long); err != nil {
		t.Errorf("an option of exactly %d characters must be accepted: %v", MaxPollOptionRunes, err)
	}
}

func TestCleanPollRefusals(t *testing.T) {
	cases := []struct {
		name     string
		question string
		options  []string
		want     error
	}{
		{"empty question", "", opts(2), ErrPollNoQuestion},
		{"blank question", "  \n\t ", opts(2), ErrPollNoQuestion},
		{"question too long", strings.Repeat("q", MaxPollQuestionRunes+1), opts(2), ErrPollLongQuestion},
		{"no options", "q", nil, ErrPollFewOptions},
		{"one option", "q", opts(1), ErrPollFewOptions},
		{"two options but one is blank", "q", []string{"a", "  "}, ErrPollFewOptions},
		{"too many options", "q", opts(MaxPollOptions + 1), ErrPollManyOptions},
		{"option too long", "q", []string{strings.Repeat("o", MaxPollOptionRunes+1), "b"}, ErrPollLongOption},
	}
	for _, c := range cases {
		q, o, err := CleanPoll(c.question, c.options)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if q != "" || o != nil {
			t.Errorf("%s: nothing must be returned on failure, got %q %q", c.name, q, o)
		}
	}
}

func TestCleanPollCountsCharactersNotBytes(t *testing.T) {
	// 255 emoji is 1020 bytes but 255 characters.
	if _, _, err := CleanPoll(strings.Repeat("😀", MaxPollQuestionRunes), opts(2)); err != nil {
		t.Errorf("a question of %d emoji must be accepted: %v", MaxPollQuestionRunes, err)
	}
	if _, _, err := CleanPoll(strings.Repeat("😀", MaxPollQuestionRunes+1), opts(2)); !errors.Is(err, ErrPollLongQuestion) {
		t.Errorf("one emoji too many must be refused, got %v", err)
	}
}

func TestCleanPollRefusesDuplicateOptions(t *testing.T) {
	for name, options := range map[string][]string{
		"same":                  {"Pizza", "Pizza"},
		"different case":        {"Pizza", "PIZZA"},
		"only spacing differs":  {"Pizza  pie", "pizza pie"},
		"duplicate among three": {"a", "b", "A"},
	} {
		if _, _, err := CleanPoll("q", options); err == nil || !strings.Contains(err.Error(), "different") {
			t.Errorf("%s: err = %v, want a duplicate error", name, err)
		}
	}
	if _, _, err := CleanPoll("q", []string{"Pizza", "Pizza!"}); err != nil {
		t.Errorf("options that differ by a character are fine: %v", err)
	}
}

func TestBuildPollCreationMessage(t *testing.T) {
	msg, err := BuildPollCreationMessage("Lunch?", []string{"Pizza", "Sushi"}, false)
	if err != nil {
		t.Fatal(err)
	}
	poll := msg.GetPollCreationMessage()
	if poll == nil || poll.GetName() != "Lunch?" || len(poll.GetOptions()) != 2 {
		t.Fatalf("unexpected poll: %+v", msg)
	}
	if poll.GetOptions()[0].GetOptionName() != "Pizza" || poll.GetOptions()[1].GetOptionName() != "Sushi" {
		t.Fatalf("options are out of order: %+v", poll.GetOptions())
	}
	if poll.GetSelectableOptionsCount() != 1 {
		t.Errorf("single choice = %d, want 1", poll.GetSelectableOptionsCount())
	}
	secret := msg.GetMessageContextInfo().GetMessageSecret()
	if len(secret) != 32 {
		t.Fatalf("the vote secret must be 32 bytes, got %d", len(secret))
	}

	multi, err := BuildPollCreationMessage("Lunch?", []string{"Pizza", "Sushi"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if multi.GetPollCreationMessage().GetSelectableOptionsCount() != 0 {
		t.Errorf("several answers = %d, want 0 (any number)", multi.GetPollCreationMessage().GetSelectableOptionsCount())
	}
	if string(multi.GetMessageContextInfo().GetMessageSecret()) == string(secret) {
		t.Error("every poll needs its own secret")
	}
}
