package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (x *m) toggleWhitelistForSelection() tea.Cmd {
	if x.status != "ready" {
		return x.setTopBar("Not ready — wait for the chat to load")
	}
	if x.leftInputFocused {
		return x.setTopBar("Finish the /command first (Esc)")
	}
	if x.themePicker.open || x.pointerPicker.open || x.helpPicker.open || x.settingsPicker.open || x.typingAnimationPicker.open || x.mediaIconPicker.open || x.mediaViewPicker.open || x.userlistIconPicker.open || x.fontTestOpen {
		return x.setTopBar("Close the picker first (Esc)")
	}
	if x.fileBrowserOpen {
		return x.setTopBar("Close the file browser first (Esc)")
	}
	// Always target the highlighted sidebar item (the "selected" chat),
	// regardless of whether the sidebar has focus. Falls back to the open
	// chat only if the sidebar selection is out of range (e.g. empty list).
	targetID := ""
	items := x.sidebarItems()
	if x.sel >= 0 && x.sel < len(items) {
		targetID = items[x.sel].ID
	}
	if targetID == "" && x.active != "" {
		targetID = x.active
	}
	if targetID == "" {
		return x.setTopBar("No contact selected")
	}
	n := num(targetID)
	if n == "" {
		return x.setTopBar("No contact selected")
	}
	name := x.nameFor(targetID)
	wasAllowed := x.isAllowed(n)
	allowed := 0
	if x.defaultAllowed {
		if x.denied == nil {
			x.denied = map[string]bool{}
		}
		if wasAllowed {
			x.denied[n] = true
		} else {
			delete(x.denied, n)
		}
	} else if wasAllowed {
		delete(x.whitelist, n)
	} else {
		x.whitelist[n] = name
	}
	wasWhitelisted := wasAllowed
	x.markIdentityChanged()
	if wasWhitelisted && x.active != "" && num(x.active) == n {
		x.clearChatComposer()
	}
	verb := "Blacklisted"
	allowed = 0
	if !wasWhitelisted {
		verb = "Whitelisted"
		allowed = 1
	}
	msg := fmt.Sprintf("%s: %s (%s)", verb, name, n)
	if x.demoMode {
		return x.setTopBar(msg)
	}
	return tea.Batch(
		setWhitelistEntry(x.reqCtx(), x.client, x.baseURL, n, x.whitelist[n], allowed),
		x.setTopBar(msg),
	)
}

// doWhitelistAll flips the global default to allow with a single call.
// Confirmed via the A-6 confirm dialog before this runs. Server-side every
// per-chat row aligns to allowed, so local denied overrides clear into the
// whitelist map to mirror.
func (x *m) doWhitelistAll() tea.Cmd {
	if x.denied == nil {
		x.denied = map[string]bool{}
	}
	if x.whitelist == nil {
		x.whitelist = map[string]string{}
	}
	for n := range x.denied {
		if _, ok := x.whitelist[n]; !ok {
			x.whitelist[n] = x.names[n]
		}
		delete(x.denied, n)
	}
	x.defaultAllowed = true
	x.markIdentityChanged()
	msg := "Whitelist default: allow all"
	if x.demoMode {
		return x.setTopBar(msg)
	}
	return tea.Batch(setWhitelistDefault(x.reqCtx(), x.client, x.baseURL, 1), x.setTopBar(msg))
}

// doBlacklistAll flips the global default to deny with a single call.
// Confirmed via the A-6 confirm dialog before this runs. Server-side every
// per-chat row aligns to denied, so the local whitelist map moves into
// denied overrides to mirror (names are preserved server-side).
func (x *m) doBlacklistAll() tea.Cmd {
	if x.denied == nil {
		x.denied = map[string]bool{}
	}
	for n := range x.whitelist {
		x.denied[n] = true
	}
	x.whitelist = map[string]string{}
	x.defaultAllowed = false
	x.markIdentityChanged()
	if x.active != "" {
		x.clearChatComposer()
	}
	msg := "Whitelist default: block all"
	if x.demoMode {
		return x.setTopBar(msg)
	}
	return tea.Batch(setWhitelistDefault(x.reqCtx(), x.client, x.baseURL, 0), x.setTopBar(msg))
}

func (x *m) handleSlash(txt string) (tea.Cmd, bool) {
	return x.runCommand(txt, false)
}

func (x *m) handleGlobalCommand(txt string) (tea.Cmd, bool) {
	return x.runCommand(txt, true)
}

func (x *m) runCommand(txt string, includeGlobal bool) (tea.Cmd, bool) {
	if includeGlobal {
		if cmd, handled := x.runGlobalCommand(txt); handled {
			return cmd, true
		}
	}
	if cmd, handled := x.runPermissionCommand(txt, includeGlobal); handled {
		return cmd, true
	}
	if cmd, handled := x.runMediaSendCommand(txt, includeGlobal); handled {
		return cmd, true
	}
	if cmd, handled := x.runUICommand(txt); handled {
		return cmd, true
	}
	if strings.HasPrefix(txt, "/") {
		return x.setTopBar("unknown command: " + txt), true
	}
	return nil, false
}

func (x *m) runGlobalCommand(txt string) (tea.Cmd, bool) {
	switch txt {
	case "/exit":
		x.cancelRequests()
		return tea.Quit, true
	case "/restart":
		x.restartRequested = true
		x.cancelRequests()
		return tea.Quit, true
	case "/synccontacts":
		if x.demoMode {
			return x.setTopBar("Demo mode: contacts already fake"), true
		}
		x.syncingContacts = true
		return tea.Batch(x.setTopBar("Syncing contacts..."), syncContacts(x.reqCtx(), x.client, x.baseURL)), true
	case "/syncgroups":
		if x.demoMode {
			return x.setTopBar("Demo mode: groups already fake"), true
		}
		x.syncingGroups = true
		return tea.Batch(x.setTopBar("Syncing groups..."), syncGroups(x.reqCtx(), x.client, x.baseURL)), true
	case "/allcontacts":
		currentConfig.ShowAllContacts = !currentConfig.ShowAllContacts
		saveConfig()
		x.invalidateSidebarContacts()
		if currentConfig.ShowAllContacts {
			return x.setTopBar("People shows all contacts (stored + strangers)"), true
		}
		return x.setTopBar("People shows stored contacts only"), true
	}
	return nil, false
}

func (x *m) runPermissionCommand(txt string, includeGlobal bool) (tea.Cmd, bool) {
	switch {
	case txt == "/whitelistall":
		if len(x.chats) == 0 {
			if includeGlobal {
				return x.setTopBar("No chats loaded yet"), true
			}
			x.err = "no chats loaded yet"
			return nil, true
		}
		x.confirmDialog.Open("Whitelist all chats?",
			"Are you sure you want to whitelist all contacts?", "whitelistall")
		return nil, true
	case txt == "/blacklistall":
		if len(x.whitelist) == 0 && !x.defaultAllowed {
			return x.setTopBar("Whitelist already empty"), true
		}
		x.confirmDialog.Open("Clear the whitelist?",
			"Are you sure you want to blacklist all contacts?", "blacklistall")
		return nil, true
	case txt == "/whitelist":
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		_, already := x.whitelist[n]
		name := x.nameFor(x.active)
		x.whitelist[n] = name
		delete(x.denied, n)
		x.markIdentityChanged()
		msg := "Added to whitelist"
		if already {
			msg = "Already in whitelist"
		}
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		return tea.Batch(setWhitelistEntry(x.reqCtx(), x.client, x.baseURL, n, name, 1), x.setTopBar(msg)), true
	case txt == "/blacklist":
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		_, was := x.whitelist[n]
		delete(x.whitelist, n)
		if x.denied == nil {
			x.denied = map[string]bool{}
		}
		x.denied[n] = true
		x.markIdentityChanged()
		if x.active != "" && num(x.active) == n {
			x.clearChatComposer()
		}
		msg := "Removed from whitelist"
		if !was {
			msg = "Not in whitelist"
		}
		if x.demoMode {
			return x.setTopBar(msg), true
		}
		return tea.Batch(setWhitelistEntry(x.reqCtx(), x.client, x.baseURL, n, "", 0), x.setTopBar(msg)), true
	case txt == "/block":
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		delete(x.whitelist, n)
		if x.denied == nil {
			x.denied = map[string]bool{}
		}
		x.denied[n] = true
		x.markIdentityChanged()
		if x.active != "" && num(x.active) == n {
			x.clearChatComposer()
		}
		if x.demoMode {
			return x.setTopBar("Demo mode: block disabled"), true
		}
		return tea.Batch(
			x.setTopBar("Blocking contact..."),
			blockContact(x.reqCtx(), x.client, x.baseURL, x.active),
			setWhitelistEntry(x.reqCtx(), x.client, x.baseURL, n, "", 0),
		), true
	case strings.HasPrefix(txt, "/rename "):
		name := strings.TrimSpace(strings.TrimPrefix(txt, "/rename "))
		if name == "" {
			return x.setTopBar("usage: /rename <name>"), true
		}
		if includeGlobal && x.active == "" {
			return x.setTopBar("No active chat"), true
		}
		n := num(x.active)
		x.names[n] = name
		if _, ok := x.whitelist[n]; ok {
			x.whitelist[n] = name
		}
		x.markIdentityChanged()
		if x.demoMode {
			return x.setTopBar("Renamed"), true
		}
		return tea.Batch(setName(x.reqCtx(), x.client, x.baseURL, n, name), x.setTopBar("Renamed")), true
	case txt == "/rename":
		return x.setTopBar("usage: /rename <name>"), true
	}
	return nil, false
}

func (x *m) runMediaSendCommand(txt string, includeGlobal bool) (tea.Cmd, bool) {
	if !strings.HasPrefix(txt, "/send ") && txt != "/send" && !strings.HasPrefix(txt, "/sendimage") && !strings.HasPrefix(txt, "/sendvideo") && !strings.HasPrefix(txt, "/sendfile") {
		return nil, false
	}
	cmd, usage, matched := parseMediaSendCommand(txt)
	if !matched {
		return nil, false
	}
	if usage != "" {
		return x.setTopBar(usage), true
	}
	if includeGlobal && x.active == "" {
		return x.setTopBar("No active chat"), true
	}
	if !x.isAllowed(num(x.active)) {
		return x.setTopBar("Not whitelisted - use /whitelist to enable"), true
	}
	if x.demoMode {
		return x.setTopBar("Demo mode: media send disabled"), true
	}
	kind := cmd.kind
	if kind == "" {
		var err error
		kind, err = detectMediaSendKind(cmd.path)
		if err != nil {
			return x.setTopBar(err.Error()), true
		}
	}
	fileName := filepath.Base(cmd.path)
	pendingID := fmt.Sprintf("local-%d", time.Now().UnixNano())
	x.msgs[x.active] = append(x.msgs[x.active],
		optimisticOutgoingMediaMessage(x.active, kind, fileName, cmd.caption, pendingID))
	x.invalidate()
	now := time.Now()
	selectedID := x.selectedChatID()
	for i := range x.chats {
		if x.chats[i].ID == x.active {
			x.chats[i].ConversationTimestamp = now.Unix()
			break
		}
	}
	x.resortChats(selectedID)
	x.scroll = 0
	x.msgActivityUntil = time.Now().Add(3 * time.Second)
	x.msgActivityType = "sent"
	x.replyTo = nil
	progressCh := make(chan fileProgressMsg, 16)
	if x.uploadChans == nil {
		x.uploadChans = map[string]chan fileProgressMsg{}
	}
	x.uploadChans[pendingID] = progressCh
	return tea.Batch(
		sendFile(x.reqCtx(), x.client, x.baseURL, x.active, kind, cmd.path, cmd.caption, pendingID, progressCh),
		listenFileProgress(progressCh),
	), true
}

func (x *m) runUICommand(txt string) (tea.Cmd, bool) {
	switch {
	case txt == "/logout":
		x.confirmDialog.Open("Log out?", "Are you sure you want to log out?", "logout")
		return nil, true
	case txt == "/emoji":
		x.openEmojiPicker()
		return nil, true
	case txt == "/theme":
		x.themePicker.Open(currentConfig.ThemeName)
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case txt == "/pointer":
		x.pointerPicker.Open(receivedMsgIcon)
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case txt == "/typinganimation":
		x.typingAnimationPicker = picker{title: "Typing Animation", items: buildTypingAnimationPickerItems()}
		x.typingAnimationPicker.Open(currentConfig.TypingAnimationStyle)
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case txt == "/help":
		x.helpPicker.Open("")
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case txt == "/settings":
		x.settingsPicker = picker{title: "Settings", items: buildSettingsPickerItems()}
		x.settingsPicker.Open("")
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case txt == "/fonttest":
		x.fontTestOpen = true
		x.leftInput = ""
		x.leftInputFocused = false
		x.invalidate()
		return nil, true
	case strings.HasPrefix(txt, "/theme") && txt != "/theme":
		suffix := txt[len("/theme"):]
		// Strip optional leading digit (e.g. "/theme1linen" → "linen").
		if len(suffix) > 0 && suffix[0] >= '0' && suffix[0] <= '9' {
			suffix = suffix[1:]
		}
		for _, t := range themeList {
			if t.name == suffix {
				applyThemeByName(t.name)
				saveConfig()
				x.invalidate()
				return x.setTopBar("Theme: " + t.displayName), true
			}
		}
	case txt == "/mouseon":
		x.mouseEnabled = true
		currentConfig.MouseEnabled = true
		saveConfig()
		return tea.Batch(x.setTopBar("Mouse: Enabled"), mouseModeCmd(true)), true
	case txt == "/mouseoff":
		x.mouseEnabled = false
		currentConfig.MouseEnabled = false
		saveConfig()
		return tea.Batch(x.setTopBar("Mouse: Disabled"), mouseModeCmd(false)), true
	case txt == "/sound1", txt == "/sound2", txt == "/sound3", txt == "/sound4", txt == "/sound5":
		profile := int(txt[len(txt)-1] - '0')
		profile = normalizeSoundProfile(profile)
		x.soundProfile = profile
		x.soundEnabled = true
		currentConfig.SoundProfile = profile
		currentConfig.SoundEnabled = true
		saveConfig()
		// Quick preview helps you pick a tone without guesswork.
		return tea.Batch(x.setTopBar("Sound: "+soundName(profile)), playSoundProfileCmd(profile)), true
	case txt == "/soundoff":
		x.soundEnabled = false
		currentConfig.SoundEnabled = false
		saveConfig()
		return x.setTopBar("Sound: Off"), true
	case txt == "/soundon":
		x.soundEnabled = true
		x.soundProfile = normalizeSoundProfile(x.soundProfile)
		currentConfig.SoundEnabled = true
		currentConfig.SoundProfile = x.soundProfile
		saveConfig()
		return tea.Batch(x.setTopBar("Sound: "+soundName(x.soundProfile)), playSoundProfileCmd(x.soundProfile)), true
	}
	return nil, false
}
