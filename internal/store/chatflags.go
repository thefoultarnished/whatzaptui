package store

import "fmt"

// Chat flags (pinned, archived) are one-column tables keyed by chat ID. Each
// is a cache of WhatsApp's own app state, kept apart from the chats table so
// chat upserts can never overwrite it and old databases need no ALTER.
const (
	pinsTable     = "chat_pins"
	archivesTable = "chat_archives"
)

// setFlag adds or removes chatID in table and reports whether that changed
// anything. table is always one of the constants above, never user input.
func (s *Store) setFlag(table, chatID string, on bool) (bool, error) {
	if s == nil || s.db == nil || chatID == "" {
		return false, nil
	}
	query := `DELETE FROM ` + table + ` WHERE chat_id = ?`
	if on {
		query = `INSERT OR IGNORE INTO ` + table + ` (chat_id) VALUES (?)`
	}
	res, err := s.db.Exec(query, chatID)
	if err != nil {
		return false, fmt.Errorf("store: set %s: %w", table, err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// loadFlags returns every chat ID in table.
func (s *Store) loadFlags(table string) (map[string]bool, error) {
	out := map[string]bool{}
	if s == nil || s.db == nil {
		return out, nil
	}
	rows, err := s.db.Query(`SELECT chat_id FROM ` + table)
	if err != nil {
		return nil, fmt.Errorf("store: query %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan %s: %w", table, err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// keepOnlyFlags removes every chat ID in table that is not in keep and
// reports whether anything was removed.
func (s *Store) keepOnlyFlags(table string, keep map[string]bool) (bool, error) {
	current, err := s.loadFlags(table)
	if err != nil {
		return false, err
	}
	changed := false
	for id := range current {
		if keep[id] {
			continue
		}
		if _, err := s.setFlag(table, id, false); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// SetChatArchived records whether a chat is archived. It reports whether the
// stored state actually changed.
func (s *Store) SetChatArchived(chatID string, archived bool) (bool, error) {
	return s.setFlag(archivesTable, chatID, archived)
}

// LoadArchivedChats returns the IDs of every archived chat.
func (s *Store) LoadArchivedChats() (map[string]bool, error) {
	return s.loadFlags(archivesTable)
}

// KeepOnlyArchived unarchives every chat not in keep. It is used after a full
// sync, where WhatsApp lists only the chats that are archived right now.
func (s *Store) KeepOnlyArchived(keep map[string]bool) (bool, error) {
	return s.keepOnlyFlags(archivesTable, keep)
}
