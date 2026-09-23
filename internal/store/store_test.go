package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func TestStoreOpenAndMigrate(t *testing.T) {
	s := newTestStore(t)
	if s.DB() == nil {
		t.Fatal("expected non-nil DB handle")
	}

	tables := []string{"chat_permissions", "whitelist_default", "messages", "chats", "contacts", "messages_fts", "fts_backfill_meta"}
	for _, tbl := range tables {
		var name string
		err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type IN ('table', 'shadow') AND name = ?`, tbl).Scan(&name)
		if err != nil || name != tbl {
			// Virtual tables like messages_fts might appear as 'table'
			var count int
			_ = s.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, tbl).Scan(&count)
			if count == 0 {
				t.Errorf("missing table %q", tbl)
			}
		}
	}

	if err := s.Vacuum(); err != nil {
		t.Errorf("Vacuum failed: %v", err)
	}
}

func TestMessageStorageAndFTS(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Unix()
	msg := MessageRecord{
		ID:          "m1",
		ChatID:      "15551234567@s.whatsapp.net",
		FromMe:      false,
		Participant: "",
		Timestamp:   now,
		PushName:    "Alice",
		Receipt:     "delivered",
		MessageJSON: `{"conversation":"hello world searchterm"}`,
		MediaProto:  "",
	}

	inserted, err := s.InsertMessage(msg, "hello world searchterm")
	if err != nil {
		t.Fatalf("InsertMessage failed: %v", err)
	}
	if !inserted {
		t.Fatal("expected message to be inserted")
	}

	// Duplicate insert should be deduped
	dup, err := s.InsertMessage(msg, "hello world searchterm")
	if err != nil {
		t.Fatalf("duplicate InsertMessage failed: %v", err)
	}
	if dup {
		t.Fatal("expected duplicate message to not be inserted")
	}

	// Query messages
	msgs, err := s.GetMessages(msg.ChatID, 0, 10)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Fatalf("expected 1 message m1, got %+v", msgs)
	}

	// Full-text search
	hits, err := s.SearchMessages("searchterm", msg.ChatID, 10)
	if err != nil {
		t.Fatalf("SearchMessages failed: %v", err)
	}
	if len(hits) != 1 || hits[0].MessageID != "m1" {
		t.Fatalf("expected search hit for m1, got %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "searchterm") {
		t.Errorf("expected snippet to contain searchterm, got %q", hits[0].Snippet)
	}

	// Edit message
	edited, ok, err := s.EditMessage(msg.ChatID, msg.ID, msg.FromMe, func(oldJSON string) (string, string, error) {
		return `{"conversation":"edited term"}`, "edited term", nil
	})
	if err != nil || !ok {
		t.Fatalf("EditMessage failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(edited.MessageJSON, "edited term") {
		t.Errorf("edited message JSON incorrect: %q", edited.MessageJSON)
	}

	// Verify old search term no longer matches and new term matches
	oldHits, _ := s.SearchMessages("searchterm", msg.ChatID, 10)
	if len(oldHits) != 0 {
		t.Errorf("expected 0 hits for old search term, got %d", len(oldHits))
	}
	newHits, _ := s.SearchMessages("edited", msg.ChatID, 10)
	if len(newHits) != 1 {
		t.Errorf("expected 1 hit for new search term, got %d", len(newHits))
	}

	// Delete message
	if err := s.DeleteMessage(msg.ChatID, msg.ID, msg.FromMe); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}
	delMsgs, _ := s.GetMessages(msg.ChatID, 0, 10)
	if len(delMsgs) != 0 {
		t.Errorf("expected 0 messages after delete, got %d", len(delMsgs))
	}
}

func TestGetMessagesAround(t *testing.T) {
	s := newTestStore(t)
	chatID := "chat1"

	for i := 1; i <= 5; i++ {
		_, err := s.InsertMessage(MessageRecord{
			ID:          string(rune('0' + i)),
			ChatID:      chatID,
			FromMe:      false,
			Timestamp:   int64(100 * i),
			MessageJSON: "{}",
		}, "")
		if err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	anchorTS, older, anchor, newer, err := s.GetMessagesAround(chatID, "3", 10)
	if err != nil {
		t.Fatalf("GetMessagesAround failed: %v", err)
	}
	if anchorTS != 300 {
		t.Errorf("expected anchorTS 300, got %d", anchorTS)
	}
	if len(anchor) != 1 || anchor[0].ID != "3" {
		t.Errorf("expected anchor ID '3', got %+v", anchor)
	}
	if len(older) != 2 {
		t.Errorf("expected 2 older messages, got %d", len(older))
	}
	if len(newer) != 2 {
		t.Errorf("expected 2 newer messages, got %d", len(newer))
	}
}

func TestPermissionsAndWhitelist(t *testing.T) {
	s := newTestStore(t)

	// Default whitelist
	def, err := s.GetWhitelistDefault()
	if err != nil {
		t.Fatalf("GetWhitelistDefault failed: %v", err)
	}
	if def != 0 {
		t.Errorf("expected initial default whitelist 0, got %d", def)
	}

	if err := s.SetWhitelistDefault(1); err != nil {
		t.Fatalf("SetWhitelistDefault failed: %v", err)
	}
	def, _ = s.GetWhitelistDefault()
	if def != 1 {
		t.Errorf("expected updated default whitelist 1, got %d", def)
	}

	// Permissions
	if err := s.SetPermission("15551234567", "Alice", 1); err != nil {
		t.Fatalf("SetPermission failed: %v", err)
	}

	allowed, exists, err := s.GetPermission("15551234567")
	if err != nil || !exists || allowed != 1 {
		t.Fatalf("GetPermission: allowed=%d exists=%v err=%v", allowed, exists, err)
	}

	perms, err := s.GetAllowedPermissions()
	if err != nil {
		t.Fatalf("GetAllowedPermissions failed: %v", err)
	}
	if perms["15551234567"] != "Alice" {
		t.Errorf("expected Alice in allowed permissions, got %v", perms)
	}

	// Backup and Restore
	backups, ok := s.BackupPermissions()
	if !ok || len(backups) != 1 {
		t.Fatalf("BackupPermissions failed: ok=%v len=%d", ok, len(backups))
	}

	// Clear and restore
	_ = s.SetPermission("15551234567", "Alice", 0)
	if err := s.RestorePermissions(backups); err != nil {
		t.Fatalf("RestorePermissions failed: %v", err)
	}
	allowed, _, _ = s.GetPermission("15551234567")
	if allowed != 1 {
		t.Errorf("expected restored permission 1, got %d", allowed)
	}

	// Purge
	n, err := s.PurgePermissionName("Alice")
	if err != nil || n != 1 {
		t.Errorf("PurgePermissionName: n=%d err=%v", n, err)
	}
}

func TestChatsAndContacts(t *testing.T) {
	s := newTestStore(t)

	// Chats
	ch := ChatRecord{
		ID:                    "c1@s.whatsapp.net",
		Name:                  "Bob",
		Subject:               "Subject",
		ConversationTimestamp: 12345678,
		UnreadCount:           3,
	}
	if err := s.UpsertChat(ch); err != nil {
		t.Fatalf("UpsertChat failed: %v", err)
	}

	chats, err := s.LoadChats()
	if err != nil {
		t.Fatalf("LoadChats failed: %v", err)
	}
	if chats["c1@s.whatsapp.net"].Name != "Bob" {
		t.Errorf("expected chat Bob, got %+v", chats)
	}

	// Contacts
	ct := ContactRecord{
		ID:     "c1@s.whatsapp.net",
		Name:   "Bob Contact",
		Notify: "Bobbie",
		Stored: true,
	}
	if err := s.UpsertContact(ct); err != nil {
		t.Fatalf("UpsertContact failed: %v", err)
	}

	contacts, err := s.LoadContacts()
	if err != nil {
		t.Fatalf("LoadContacts failed: %v", err)
	}
	if contacts["c1@s.whatsapp.net"].Notify != "Bobbie" {
		t.Errorf("expected contact Bobbie, got %+v", contacts)
	}
}
