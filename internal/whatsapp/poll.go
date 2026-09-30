package whatsapp

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// Limits WhatsApp puts on a poll. The TUI repeats them (internal/tui/poll.go)
// to check a poll before it is sent, so change both together.
const (
	MinPollOptions       = 2
	MaxPollOptions       = 12
	MaxPollQuestionRunes = 255
	MaxPollOptionRunes   = 100
)

// Errors returned for a poll that cannot be sent.
var (
	ErrPollNoQuestion   = errors.New("a poll needs a question")
	ErrPollLongQuestion = fmt.Errorf("the question is too long (max %d characters)", MaxPollQuestionRunes)
	ErrPollFewOptions   = fmt.Errorf("a poll needs at least %d options", MinPollOptions)
	ErrPollManyOptions  = fmt.Errorf("a poll can have at most %d options", MaxPollOptions)
	ErrPollLongOption   = fmt.Errorf("an option is too long (max %d characters)", MaxPollOptionRunes)
)

// tidyPollText puts a question or option on one line with single spaces.
func tidyPollText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// CleanPoll tidies a poll and checks it against WhatsApp's limits. Blank
// options are dropped, so an empty row in a form is harmless. Two options that
// only differ in letter case count as the same option.
func CleanPoll(question string, options []string) (string, []string, error) {
	question = tidyPollText(question)
	if question == "" {
		return "", nil, ErrPollNoQuestion
	}
	if utf8.RuneCountInString(question) > MaxPollQuestionRunes {
		return "", nil, ErrPollLongQuestion
	}
	cleaned := make([]string, 0, len(options))
	for _, o := range options {
		if o = tidyPollText(o); o != "" {
			cleaned = append(cleaned, o)
		}
	}
	if len(cleaned) < MinPollOptions {
		return "", nil, ErrPollFewOptions
	}
	if len(cleaned) > MaxPollOptions {
		return "", nil, ErrPollManyOptions
	}
	seen := make(map[string]bool, len(cleaned))
	for _, o := range cleaned {
		if utf8.RuneCountInString(o) > MaxPollOptionRunes {
			return "", nil, ErrPollLongOption
		}
		key := strings.ToLower(o)
		if seen[key] {
			return "", nil, fmt.Errorf("options must all be different: %q appears twice", o)
		}
		seen[key] = true
	}
	return question, cleaned, nil
}

// BuildPollCreationMessage builds a poll message. multiple lets people pick
// more than one option. The random secret is what encrypts the votes, and it is
// stored by whatsmeow when the message is sent so votes can be read later.
// The question and options are expected to be clean already (see CleanPoll).
func BuildPollCreationMessage(question string, options []string, multiple bool) (*waE2E.Message, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("poll secret: %w", err)
	}
	opts := make([]*waE2E.PollCreationMessage_Option, len(options))
	for i, o := range options {
		opts[i] = &waE2E.PollCreationMessage_Option{OptionName: proto.String(o)}
	}
	// 1 means a single choice, 0 means any number of options.
	selectable := uint32(1)
	if multiple {
		selectable = 0
	}
	return &waE2E.Message{
		PollCreationMessage: &waE2E.PollCreationMessage{
			Name:                   proto.String(question),
			Options:                opts,
			SelectableOptionsCount: proto.Uint32(selectable),
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: secret},
	}, nil
}
