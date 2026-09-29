package tui

import "strings"

// commandStep is what Tab types in the command box: one step along the
// suggested command, not the whole command.
//
// The suggestion is the ghost text (commandBestMatch). Tab commits to that
// suggestion's branch: it types every letter shared by all the commands that
// continue the same way, and stops where they split. So "/s" steps to "/sync"
// (contacts, groups and history split there), the next Tab completes
// "/synccontacts", and typing "g" at "/sync" switches the ghost to
// "/syncgroups" first. It returns "" when there is no suggestion.
func commandStep(input string) string {
	in := strings.ToLower(strings.TrimSpace(input))
	best := commandBestMatch(in)
	if best == "" || len(best) <= len(in) || !strings.HasPrefix(best, in) {
		return ""
	}
	// Commands on the suggestion's branch: they match what is typed and have
	// the same next letter as the suggestion.
	branch := in + best[len(in):len(in)+1]
	step := ""
	for _, c := range systemCommands {
		if !strings.HasPrefix(c, branch) {
			continue
		}
		if step == "" {
			step = c
			continue
		}
		step = commonPrefix(step, c)
	}
	if step == "" {
		// The suggestion is not a listed command (a theme shortcut): use it whole.
		return best
	}
	return step
}

// commonPrefix is the longest shared start of a and b.
func commonPrefix(a, b string) string {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
