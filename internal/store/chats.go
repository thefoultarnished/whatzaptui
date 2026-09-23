package store

import (
	"database/sql"
	"fmt"
)

// ChatRecord represents a row in the chats table.
type ChatRecord struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Subject               string `json:"subject"`
	ConversationTimestamp int64  `json:"conversationTimestamp"`
	UnreadCount           int    `json:"unreadCount"`
}

// ContactRecord represents a row in the contacts table.
type ContactRecord struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Notify string `json:"notify"`
	Stored bool   `json:"stored"`
}

// UpsertChat writes or updates a chat record in the database.
func (s *Store) UpsertChat(chat ChatRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO chats (id, name, subject, conv_ts, unread_count)
		VALUES (?, ?, ?, ?, ?)
	`, chat.ID, chat.Name, chat.Subject, chat.ConversationTimestamp, chat.UnreadCount)
	return err
}

// LoadChats reads all chats from the database.
func (s *Store) LoadChats() (map[string]ChatRecord, error) {
	if s == nil || s.db == nil {
		return map[string]ChatRecord{}, nil
	}
	rows, err := s.db.Query(`SELECT id, name, subject, conv_ts, unread_count FROM chats`)
	if err != nil {
		return nil, fmt.Errorf("store: query chats: %w", err)
	}
	defer rows.Close()

	out := make(map[string]ChatRecord)
	for rows.Next() {
		var c ChatRecord
		if err := rows.Scan(&c.ID, &c.Name, &c.Subject, &c.ConversationTimestamp, &c.UnreadCount); err != nil {
			return nil, fmt.Errorf("store: scan chat: %w", err)
		}
		out[c.ID] = c
	}
	return out, rows.Err()
}

// UpsertContact writes or updates a contact record in the database.
func (s *Store) UpsertContact(contact ContactRecord) error {
	if s == nil || s.db == nil {
		return nil
	}
	storedInt := 0
	if contact.Stored {
		storedInt = 1
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO contacts (id, name, notify, stored)
		VALUES (?, ?, ?, ?)
	`, contact.ID, contact.Name, contact.Notify, storedInt)
	return err
}

// LoadContacts reads all contacts from the database.
func (s *Store) LoadContacts() (map[string]ContactRecord, error) {
	if s == nil || s.db == nil {
		return map[string]ContactRecord{}, nil
	}
	rows, err := s.db.Query(`SELECT id, name, notify, stored FROM contacts`)
	if err != nil {
		return nil, fmt.Errorf("store: query contacts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]ContactRecord)
	for rows.Next() {
		var c ContactRecord
		var stored int
		if err := rows.Scan(&c.ID, &c.Name, &c.Notify, &stored); err != nil {
			return nil, fmt.Errorf("store: scan contact: %w", err)
		}
		c.Stored = (stored == 1)
		out[c.ID] = c
	}
	return out, rows.Err()
}

// GetMaxMessageTS returns the newest message timestamp for a chat.
func (s *Store) GetMaxMessageTS(chatID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var maxTS int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM messages WHERE chat_id = ? AND ts > 0`, chatID).Scan(&maxTS)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return maxTS, nil
}
