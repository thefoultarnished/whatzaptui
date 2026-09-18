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
	if x.mainCache != nil {
		x.mainCache.result = ""
	}
	x.invalidateSidebarContacts()
}

func (x m) activeChatWhitelisted() bool {
	if x.active == "" {
		return false
	}
	_, ok := x.whitelist[num(x.active)]
	return ok
}

func (x m) chatInputLocked() bool {
	return x.active != "" && !x.activeChatWhitelisted()
}
