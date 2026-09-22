package main

import (
	"strings"
)

func preferredContact(a, b contact) contact {
	score := func(c contact) int {
		switch {
		case strings.TrimSpace(c.Notify) != "":
			return 2
		case strings.TrimSpace(c.Name) != "":
			return 1
		default:
			return 0
		}
	}
	if score(a) >= score(b) {
		return a
	}
	return b
}

func (x *m) rebuildContactIndex() {
	x.contactsByNumber = make(map[string]contact, len(x.contacts))
	for _, ct := range x.contacts {
		n := num(ct.ID)
		if n == "" {
			continue
		}
		if prev, ok := x.contactsByNumber[n]; ok {
			x.contactsByNumber[n] = preferredContact(ct, prev)
			continue
		}
		x.contactsByNumber[n] = ct
	}
}

func (x *m) invalidateSidebarContacts() {
	if x.sidebarCache == nil {
		x.sidebarCache = &sidebarCache{}
	}
	x.sidebarCache.contacts = nil
	x.sidebarCache.contactsValid = false
}

func (x *m) markIdentityChanged() {
	x.identityVersion++
	x.invalidate()
	x.invalidateSidebarContacts()
}

func (x m) activeChatWhitelisted() bool {
	if x.active == "" {
		return false
	}
	return x.isAllowed(num(x.active))
}

// isAllowed reports whether a phone may be messaged. An explicit per-chat
// row wins; otherwise the global default applies: default-allow means
// everything except denied overrides, default-deny means only whitelisted.
func (x m) isAllowed(n string) bool {
	if x.defaultAllowed {
		return !x.denied[n]
	}
	_, ok := x.whitelist[n]
	return ok
}

func (x m) chatInputLocked() bool {
	return x.active != "" && !x.activeChatWhitelisted()
}
