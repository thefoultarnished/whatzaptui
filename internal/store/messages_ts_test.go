package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestGetMessageTimestamp(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.InsertMessage(MessageRecord{ID: "m1", ChatID: "c1", Timestamp: 1234, MessageJSON: "{}"}, ""); err != nil {
		t.Fatal(err)
	}
	if ts, err := s.GetMessageTimestamp("c1", "m1"); err != nil || ts != 1234 {
		t.Fatalf("ts=%d err=%v, want 1234", ts, err)
	}
	if _, err := s.GetMessageTimestamp("c1", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing message err = %v, want sql.ErrNoRows", err)
	}
	if _, err := s.GetMessageTimestamp("other", "m1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong chat err = %v, want sql.ErrNoRows", err)
	}
	var nilStore *Store
	if ts, err := nilStore.GetMessageTimestamp("c1", "m1"); ts != 0 || err != nil {
		t.Fatalf("nil store: ts=%d err=%v, want 0,nil", ts, err)
	}
}

func TestGetMessageOrigin(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.InsertMessage(MessageRecord{ID: "mine", ChatID: "c1", FromMe: true, MessageJSON: "{}"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertMessage(MessageRecord{ID: "theirs", ChatID: "g1", Participant: "1555@s.whatsapp.net", MessageJSON: "{}"}, ""); err != nil {
		t.Fatal(err)
	}
	if fromMe, participant, err := s.GetMessageOrigin("c1", "mine"); err != nil || !fromMe || participant != "" {
		t.Fatalf("own message: fromMe=%v participant=%q err=%v", fromMe, participant, err)
	}
	if fromMe, participant, err := s.GetMessageOrigin("g1", "theirs"); err != nil || fromMe || participant != "1555@s.whatsapp.net" {
		t.Fatalf("group message: fromMe=%v participant=%q err=%v", fromMe, participant, err)
	}
	if _, _, err := s.GetMessageOrigin("c1", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing message err = %v, want sql.ErrNoRows", err)
	}
	var nilStore *Store
	if fromMe, participant, err := nilStore.GetMessageOrigin("c1", "mine"); fromMe || participant != "" || err != nil {
		t.Fatal("a nil store finds nothing")
	}
}
