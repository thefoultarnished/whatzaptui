package whatsapp

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateVote(t *testing.T) {
	options := []string{"Pizza", "Sushi", "Ramen"}
	cases := []struct {
		name       string
		selectable int
		chosen     []string
		want       string
		err        string
	}{
		{"one option on a single choice poll", 1, []string{"Sushi"}, "Sushi", ""},
		{"withdraw", 1, nil, "", ""},
		{"withdraw with an empty list", 0, []string{}, "", ""},
		{"several on an any-number poll", 0, []string{"Ramen", "Pizza"}, "Ramen|Pizza", ""},
		{"several on a poll of unknown limit", -1, []string{"Pizza", "Sushi", "Ramen"}, "Pizza|Sushi|Ramen", ""},
		{"exactly the cap", 2, []string{"Pizza", "Sushi"}, "Pizza|Sushi", ""},
		{"one over the cap", 2, []string{"Pizza", "Sushi", "Ramen"}, "", "at most 2"},
		{"two on a single choice poll", 1, []string{"Pizza", "Sushi"}, "", "only one answer"},
		{"an option the poll does not have", 0, []string{"Burger"}, "", "not in this poll"},
		{"a real option and a fake one", 0, []string{"Pizza", "Burger"}, "", "not in this poll"},
		{"different case is a different option", 0, []string{"pizza"}, "", "not in this poll"},
		{"the same option twice", 0, []string{"Pizza", "Pizza"}, "", "twice"},
	}
	for _, c := range cases {
		got, err := ValidateVote(options, c.selectable, c.chosen)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) || got != nil {
				t.Errorf("%s: got %q, %v, want an error mentioning %q", c.name, got, err, c.err)
			}
			continue
		}
		if err != nil || strings.Join(got, "|") != c.want {
			t.Errorf("%s: got %q, %v, want %q", c.name, got, err, c.want)
		}
		if got == nil {
			t.Errorf("%s: a valid choice is never nil (withdraw is an empty list)", c.name)
		}
	}
	if _, err := ValidateVote(options, 0, []string{"Burger"}); !errors.Is(err, ErrPollVoteUnknownOption) {
		t.Errorf("unknown option error = %v, want ErrPollVoteUnknownOption", err)
	}
	if _, err := ValidateVote(nil, 0, []string{"Pizza"}); err == nil {
		t.Error("a poll with no options accepts no vote")
	}
	if got, err := ValidateVote(nil, 0, nil); err != nil || len(got) != 0 {
		t.Errorf("withdrawing from any poll is allowed, got %q %v", got, err)
	}
}
