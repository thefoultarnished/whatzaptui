package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type emojiItem struct {
	Char     string
	Name     string
	Keywords []string
	Group    string
}

var emojiCatalog = []emojiItem{
	{Char: "😀", Name: "grinning face", Keywords: []string{"happy", "smile", "grin"}, Group: "Smileys"},
	{Char: "😃", Name: "grinning face with big eyes", Keywords: []string{"happy", "smile"}, Group: "Smileys"},
	{Char: "😄", Name: "grinning face with smiling eyes", Keywords: []string{"happy", "smile"}, Group: "Smileys"},
	{Char: "😁", Name: "beaming face", Keywords: []string{"happy", "grin"}, Group: "Smileys"},
	{Char: "😆", Name: "grinning squinting face", Keywords: []string{"laugh", "funny"}, Group: "Smileys"},
	{Char: "😂", Name: "face with tears of joy", Keywords: []string{"lol", "laugh", "cry"}, Group: "Smileys"},
	{Char: "🤣", Name: "rolling on the floor laughing", Keywords: []string{"lol", "laugh"}, Group: "Smileys"},
	{Char: "🙂", Name: "slightly smiling face", Keywords: []string{"smile", "nice"}, Group: "Smileys"},
	{Char: "🙃", Name: "upside-down face", Keywords: []string{"sarcasm", "playful"}, Group: "Smileys"},
	{Char: "😉", Name: "winking face", Keywords: []string{"wink", "flirt"}, Group: "Smileys"},
	{Char: "😊", Name: "smiling face with smiling eyes", Keywords: []string{"blush", "happy"}, Group: "Smileys"},
	{Char: "😇", Name: "smiling face with halo", Keywords: []string{"angel", "innocent"}, Group: "Smileys"},
	{Char: "🥰", Name: "smiling face with hearts", Keywords: []string{"love", "affection"}, Group: "Smileys"},
	{Char: "😍", Name: "smiling face with heart-eyes", Keywords: []string{"love", "crush"}, Group: "Smileys"},
	{Char: "😘", Name: "face blowing a kiss", Keywords: []string{"kiss", "love"}, Group: "Smileys"},
	{Char: "😎", Name: "smiling face with sunglasses", Keywords: []string{"cool"}, Group: "Smileys"},
	{Char: "🤩", Name: "star-struck", Keywords: []string{"wow", "excited"}, Group: "Smileys"},
	{Char: "🥳", Name: "partying face", Keywords: []string{"party", "celebrate"}, Group: "Smileys"},
	{Char: "😏", Name: "smirking face", Keywords: []string{"smirk"}, Group: "Smileys"},
	{Char: "😒", Name: "unamused face", Keywords: []string{"annoyed"}, Group: "Smileys"},
	{Char: "😢", Name: "crying face", Keywords: []string{"sad", "tear"}, Group: "Smileys"},
	{Char: "😭", Name: "loudly crying face", Keywords: []string{"sad", "cry"}, Group: "Smileys"},
	{Char: "😡", Name: "pouting face", Keywords: []string{"angry", "mad"}, Group: "Smileys"},
	{Char: "🤬", Name: "face with symbols on mouth", Keywords: []string{"swear", "angry"}, Group: "Smileys"},
	{Char: "🤯", Name: "exploding head", Keywords: []string{"mind blown"}, Group: "Smileys"},
	{Char: "😱", Name: "face screaming in fear", Keywords: []string{"shock", "scared"}, Group: "Smileys"},
	{Char: "🤔", Name: "thinking face", Keywords: []string{"hmm", "think"}, Group: "Smileys"},
	{Char: "🫡", Name: "saluting face", Keywords: []string{"respect", "salute"}, Group: "Smileys"},
	{Char: "🤗", Name: "hugging face", Keywords: []string{"hug"}, Group: "Smileys"},
	{Char: "👍", Name: "thumbs up", Keywords: []string{"yes", "approve", "ok"}, Group: "Gestures"},
	{Char: "👎", Name: "thumbs down", Keywords: []string{"no", "disapprove"}, Group: "Gestures"},
	{Char: "👌", Name: "ok hand", Keywords: []string{"okay", "perfect"}, Group: "Gestures"},
	{Char: "✌️", Name: "victory hand", Keywords: []string{"peace"}, Group: "Gestures"},
	{Char: "🤞", Name: "crossed fingers", Keywords: []string{"luck"}, Group: "Gestures"},
	{Char: "👏", Name: "clapping hands", Keywords: []string{"applause"}, Group: "Gestures"},
	{Char: "🙌", Name: "raising hands", Keywords: []string{"celebrate", "praise"}, Group: "Gestures"},
	{Char: "🙏", Name: "folded hands", Keywords: []string{"please", "thanks", "pray"}, Group: "Gestures"},
	{Char: "💪", Name: "flexed biceps", Keywords: []string{"strong", "gym"}, Group: "Gestures"},
	{Char: "🫶", Name: "heart hands", Keywords: []string{"love"}, Group: "Gestures"},
	{Char: "👀", Name: "eyes", Keywords: []string{"look", "watch"}, Group: "Gestures"},
	{Char: "❤️", Name: "red heart", Keywords: []string{"love", "heart"}, Group: "Hearts"},
	{Char: "🧡", Name: "orange heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "💛", Name: "yellow heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "💚", Name: "green heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "💙", Name: "blue heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "💜", Name: "purple heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "🖤", Name: "black heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "🤍", Name: "white heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "🤎", Name: "brown heart", Keywords: []string{"heart"}, Group: "Hearts"},
	{Char: "💔", Name: "broken heart", Keywords: []string{"heart", "sad"}, Group: "Hearts"},
	{Char: "💕", Name: "two hearts", Keywords: []string{"love"}, Group: "Hearts"},
	{Char: "💯", Name: "hundred points", Keywords: []string{"100", "keep it real"}, Group: "Symbols"},
	{Char: "🔥", Name: "fire", Keywords: []string{"lit", "hot"}, Group: "Symbols"},
	{Char: "✨", Name: "sparkles", Keywords: []string{"shine"}, Group: "Symbols"},
	{Char: "⚡", Name: "high voltage", Keywords: []string{"lightning"}, Group: "Symbols"},
	{Char: "💥", Name: "collision", Keywords: []string{"boom"}, Group: "Symbols"},
	{Char: "⭐", Name: "star", Keywords: []string{"favorite"}, Group: "Symbols"},
	{Char: "✅", Name: "check mark button", Keywords: []string{"done", "yes"}, Group: "Symbols"},
	{Char: "❌", Name: "cross mark", Keywords: []string{"no", "wrong"}, Group: "Symbols"},
	{Char: "⚠️", Name: "warning", Keywords: []string{"alert"}, Group: "Symbols"},
	{Char: "🚀", Name: "rocket", Keywords: []string{"launch"}, Group: "Objects"},
	{Char: "🎉", Name: "party popper", Keywords: []string{"celebrate", "party"}, Group: "Objects"},
	{Char: "🎂", Name: "birthday cake", Keywords: []string{"birthday", "cake"}, Group: "Objects"},
	{Char: "☕", Name: "hot beverage", Keywords: []string{"coffee", "tea"}, Group: "Objects"},
	{Char: "🍕", Name: "pizza", Keywords: []string{"food"}, Group: "Objects"},
	{Char: "🍔", Name: "hamburger", Keywords: []string{"food"}, Group: "Objects"},
	{Char: "🍟", Name: "french fries", Keywords: []string{"food"}, Group: "Objects"},
	{Char: "🍿", Name: "popcorn", Keywords: []string{"movie"}, Group: "Objects"},
	{Char: "🍻", Name: "clinking beer mugs", Keywords: []string{"cheers", "beer"}, Group: "Objects"},
	{Char: "🥂", Name: "clinking glasses", Keywords: []string{"cheers"}, Group: "Objects"},
	{Char: "⚽", Name: "soccer ball", Keywords: []string{"sports"}, Group: "Objects"},
	{Char: "🏀", Name: "basketball", Keywords: []string{"sports"}, Group: "Objects"},
	{Char: "🐶", Name: "dog face", Keywords: []string{"dog", "pet"}, Group: "Animals"},
	{Char: "🐱", Name: "cat face", Keywords: []string{"cat", "pet"}, Group: "Animals"},
	{Char: "🐼", Name: "panda", Keywords: []string{"bear"}, Group: "Animals"},
	{Char: "🦄", Name: "unicorn", Keywords: []string{"magic"}, Group: "Animals"},
}

func filterEmojiCatalog(query string) []emojiItem {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return append([]emojiItem(nil), emojiCatalog...)
	}
	terms := strings.Fields(query)
	out := make([]emojiItem, 0, len(emojiCatalog))
	for _, item := range emojiCatalog {
		haystack := strings.ToLower(item.Name + " " + strings.Join(item.Keywords, " ") + " " + item.Group)
		ok := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		li := strings.HasPrefix(strings.ToLower(out[i].Name), query)
		lj := strings.HasPrefix(strings.ToLower(out[j].Name), query)
		if li != lj {
			return li
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (x *m) emojiResults() []emojiItem {
	if x.emojiResultsCache == nil || x.emojiResultsDirty {
		x.emojiResultsCache = filterEmojiCatalog(x.emojiQuery)
		x.emojiResultsDirty = false
	}
	return x.emojiResultsCache
}

func (x *m) clampEmojiSelection() {
	results := x.emojiResults()
	switch {
	case len(results) == 0:
		x.emojiSel = 0
		x.emojiScroll = 0
	case x.emojiSel >= len(results):
		x.emojiSel = len(results) - 1
	case x.emojiSel < 0:
		x.emojiSel = 0
	}
}

func (x *m) emojiVisibleRows() int {
	return max(5, min(10, x.h-12))
}

func (x *m) ensureEmojiVisible(rows int) {
	x.clampEmojiSelection()
	if x.emojiSel < x.emojiScroll {
		x.emojiScroll = x.emojiSel
	}
	if x.emojiSel >= x.emojiScroll+rows {
		x.emojiScroll = x.emojiSel - rows + 1
	}
	if x.emojiScroll < 0 {
		x.emojiScroll = 0
	}
}

func (x *m) openEmojiPicker() {
	x.input += x.inputBuf
	x.inputBuf = ""
	x.inputFlushScheduled = false
	x.inputAllSelected = false
	x.sidebarFocused = false
	x.leftInputFocused = false
	x.emojiPickerOpen = true
	x.emojiQuery = ""
	x.emojiSel = 0
	x.emojiScroll = 0
	x.emojiResultsCache = nil
	x.emojiResultsDirty = true
}

func (x *m) closeEmojiPicker() {
	x.emojiPickerOpen = false
	x.emojiQuery = ""
	x.emojiSel = 0
	x.emojiScroll = 0
	x.emojiResultsCache = nil
	x.emojiResultsDirty = true
}

func (x *m) insertSelectedEmoji() bool {
	results := x.emojiResults()
	if len(results) == 0 {
		return false
	}
	if x.inputAllSelected {
		x.input = results[x.emojiSel].Char
		x.inputAllSelected = false
	} else {
		x.input += results[x.emojiSel].Char
	}
	x.closeEmojiPicker()
	return true
}

func (x m) renderEmojiPicker() string {
	results := x.emojiResults()
	rows := x.emojiVisibleRows()
	x.ensureEmojiVisible(rows)

	width := min(max(54, x.w-14), 76)
	title := accentStyle.Render("Emoji Picker") + "  " + mutedStyle.Render("Esc close  Enter insert")
	if x.reactPickMode {
		title = accentStyle.Render("React with Emoji") + "  " + mutedStyle.Render("Esc cancel  Enter react")
	}
	body := []string{
		title,
		lipgloss.NewStyle().Foreground(text).Render("Search: ") + renderPickerQuery(x.emojiQuery, x.cursorOn),
		"",
	}
	if len(results) == 0 {
		body = append(body, redStyle.Copy().Bold(false).Render("No emojis match that search."))
	} else {
		end := min(len(results), x.emojiScroll+rows)
		lastGroup := ""
		for i := x.emojiScroll; i < end; i++ {
			item := results[i]
			if item.Group != lastGroup {
				body = append(body, mutedStyle.Render(strings.ToUpper(item.Group)))
				lastGroup = item.Group
			}
			line := item.Char + "  " + item.Name
			if i == x.emojiSel {
				line = lipgloss.NewStyle().Foreground(buttonInk).Background(accent).Bold(true).Render(" " + line + " ")
			} else {
				line = lipgloss.NewStyle().Foreground(text).Render(line)
			}
			body = append(body, line)
		}
		if end < len(results) {
			body = append(body, mutedStyle.Render("..."))
		}
	}
	return baseBoxStyle.Copy().
		Align(lipgloss.Left, lipgloss.Top).
		Width(width).
		Render(strings.Join(body, "\n"))
}

func (x m) renderEmojiPickerPane(width, height int) string {
	panel := x.renderEmojiPicker()
	return lipgloss.NewStyle().
		Width(width).
		Height(max(1, height)).
		Render(lipgloss.Place(width, max(1, height), lipgloss.Center, lipgloss.Center, panel))
}

func renderPickerQuery(query string, cursorOn bool) string {
	if query == "" {
		if cursorOn {
			return ghostStyle.Render("type to search hearts, laugh, fire, thumbs") + lipgloss.NewStyle().Foreground(accent).Render("|")
		}
		return ghostStyle.Render("type to search hearts, laugh, fire, thumbs") + " "
	}
	if cursorOn {
		return lipgloss.NewStyle().Foreground(text).Render(query) + lipgloss.NewStyle().Foreground(accent).Render("|")
	}
	return lipgloss.NewStyle().Foreground(text).Render(query) + " "
}
