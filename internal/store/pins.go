package store

// SetChatPinned records whether a chat is pinned to the top of the list. It
// reports whether the stored state actually changed, so callers can skip
// redundant refreshes when WhatsApp repeats a pin it already told us about.
func (s *Store) SetChatPinned(chatID string, pinned bool) (bool, error) {
	return s.setFlag(pinsTable, chatID, pinned)
}

// LoadPinnedChats returns the IDs of every pinned chat.
func (s *Store) LoadPinnedChats() (map[string]bool, error) {
	return s.loadFlags(pinsTable)
}

// KeepOnlyPinned unpins every chat that is not in keep and reports whether
// anything was removed. It is used after a full sync, where WhatsApp lists
// only the chats that are pinned right now.
func (s *Store) KeepOnlyPinned(keep map[string]bool) (bool, error) {
	return s.keepOnlyFlags(pinsTable, keep)
}
