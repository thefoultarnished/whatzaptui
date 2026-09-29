package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSetChatPinnedReportsChanges(t *testing.T) {
	s := newTestStore(t)
	const id = "15551230001@s.whatsapp.net"

	if changed, err := s.SetChatPinned(id, true); err != nil || !changed {
		t.Fatalf("first pin: changed=%v err=%v, want true,nil", changed, err)
	}
	if changed, err := s.SetChatPinned(id, true); err != nil || changed {
		t.Fatalf("repeat pin: changed=%v err=%v, want false,nil", changed, err)
	}
	pins, err := s.LoadPinnedChats()
	if err != nil || !pins[id] || len(pins) != 1 {
		t.Fatalf("pins = %v err=%v, want only %s", pins, err, id)
	}
	if changed, err := s.SetChatPinned(id, false); err != nil || !changed {
		t.Fatalf("unpin: changed=%v err=%v, want true,nil", changed, err)
	}
	if changed, err := s.SetChatPinned(id, false); err != nil || changed {
		t.Fatalf("repeat unpin: changed=%v err=%v, want false,nil", changed, err)
	}
	if pins, _ := s.LoadPinnedChats(); len(pins) != 0 {
		t.Fatalf("pins after unpin = %v, want none", pins)
	}
}

func TestSetChatPinnedIgnoresEmptyID(t *testing.T) {
	s := newTestStore(t)
	if changed, err := s.SetChatPinned("", true); err != nil || changed {
		t.Fatalf("empty id: changed=%v err=%v, want false,nil", changed, err)
	}
	var nilStore *Store
	if changed, err := nilStore.SetChatPinned("a", true); err != nil || changed {
		t.Fatalf("nil store: changed=%v err=%v, want false,nil", changed, err)
	}
	if pins, err := nilStore.LoadPinnedChats(); err != nil || len(pins) != 0 {
		t.Fatalf("nil store load: %v %v", pins, err)
	}
}

func TestKeepOnlyPinnedDropsStalePins(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"a@s.whatsapp.net", "b@s.whatsapp.net", "c@g.us"} {
		if _, err := s.SetChatPinned(id, true); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := s.KeepOnlyPinned(map[string]bool{"a@s.whatsapp.net": true, "c@g.us": true})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want true,nil", changed, err)
	}
	pins, _ := s.LoadPinnedChats()
	if len(pins) != 2 || !pins["a@s.whatsapp.net"] || !pins["c@g.us"] || pins["b@s.whatsapp.net"] {
		t.Fatalf("pins = %v, want a and c only", pins)
	}
	if changed, err := s.KeepOnlyPinned(map[string]bool{"a@s.whatsapp.net": true, "c@g.us": true}); err != nil || changed {
		t.Fatalf("second pass changed=%v err=%v, want false,nil", changed, err)
	}
	// An empty snapshot (nothing pinned on the phone) clears everything.
	if _, err := s.KeepOnlyPinned(nil); err != nil {
		t.Fatal(err)
	}
	if pins, _ := s.LoadPinnedChats(); len(pins) != 0 {
		t.Fatalf("pins = %v, want none", pins)
	}
}

// A database created before pins existed must still open, keep its data, and
// gain the pin table.
func TestOpenOldDatabaseWithoutPinTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE chats (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL DEFAULT '',
			subject      TEXT NOT NULL DEFAULT '',
			conv_ts      INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO chats (id, name, conv_ts) VALUES ('15551230001@s.whatsapp.net', 'Alice', 100);
		CREATE TABLE contacts (
			id     TEXT PRIMARY KEY,
			name   TEXT NOT NULL DEFAULT '',
			notify TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open old database: %v", err)
	}
	defer s.Close()

	chats, err := s.LoadChats()
	if err != nil || chats["15551230001@s.whatsapp.net"].Name != "Alice" {
		t.Fatalf("old chat lost: %v err=%v", chats, err)
	}
	if changed, err := s.SetChatPinned("15551230001@s.whatsapp.net", true); err != nil || !changed {
		t.Fatalf("pin on migrated db: changed=%v err=%v", changed, err)
	}
	if changed, err := s.SetChatArchived("15551230001@s.whatsapp.net", true); err != nil || !changed {
		t.Fatalf("archive on migrated db: changed=%v err=%v", changed, err)
	}
}

func TestSetChatArchivedReportsChangesAndIsSeparateFromPins(t *testing.T) {
	s := newTestStore(t)
	const id = "15551230001@s.whatsapp.net"

	if changed, err := s.SetChatArchived(id, true); err != nil || !changed {
		t.Fatalf("archive: changed=%v err=%v, want true,nil", changed, err)
	}
	if changed, err := s.SetChatArchived(id, true); err != nil || changed {
		t.Fatalf("repeat archive: changed=%v err=%v, want false,nil", changed, err)
	}
	archived, err := s.LoadArchivedChats()
	if err != nil || !archived[id] || len(archived) != 1 {
		t.Fatalf("archived = %v err=%v, want only %s", archived, err, id)
	}
	if pins, _ := s.LoadPinnedChats(); len(pins) != 0 {
		t.Fatalf("archiving must not touch pins, got %v", pins)
	}
	if changed, err := s.SetChatArchived(id, false); err != nil || !changed {
		t.Fatalf("unarchive: changed=%v err=%v, want true,nil", changed, err)
	}
	if archived, _ := s.LoadArchivedChats(); len(archived) != 0 {
		t.Fatalf("archived after unarchive = %v, want none", archived)
	}
}

func TestKeepOnlyArchivedDropsStaleArchives(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"a@s.whatsapp.net", "b@s.whatsapp.net"} {
		if _, err := s.SetChatArchived(id, true); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := s.KeepOnlyArchived(map[string]bool{"a@s.whatsapp.net": true})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v, want true,nil", changed, err)
	}
	archived, _ := s.LoadArchivedChats()
	if len(archived) != 1 || !archived["a@s.whatsapp.net"] {
		t.Fatalf("archived = %v, want only a", archived)
	}
}
