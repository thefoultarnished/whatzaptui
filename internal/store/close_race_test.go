package store

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// Close must not nil out s.db: initPersistentResources (backend package)
// starts BackfillFTS in a background goroutine right after Open, with no
// synchronization against a later Close (e.g. the backend closes almost
// immediately after starting, or /logout races the backfill on a large
// account). Close racing ahead used to nil s.db out from under the
// goroutine's next s.db.Query call, which panics on a nil *sql.DB. A
// closed-but-non-nil db instead makes that call return a normal
// "sql: database is closed" error, which BackfillFTS already returns
// through its ordinary err path — no panic either way.
func TestCloseThenBackfillFTSReturnsErrorNotPanic(t *testing.T) {
	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BackfillFTS on a closed store panicked instead of returning an error: %v", r)
		}
	}()
	err := s.BackfillFTS(func(string) string { return "" })
	if err == nil {
		t.Fatal("BackfillFTS on a closed store should return an error, not silently succeed")
	}
}

// The same invariant for the actual concurrent case: BackfillFTS running in
// a goroutine while Close() runs on the main goroutine must never panic,
// regardless of which one reaches the database first.
func TestCloseRacesBackfillFTSWithoutPanic(t *testing.T) {
	s := newTestStore(t)
	// BackfillFTS pages through messages batchSize (500) rows at a time,
	// re-reading s.db on every batch's Query call; more rows than one
	// batch gives Close() an actual window to land between iterations,
	// the same shape as the real race.
	tx, err := s.DB().Begin()
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	for i := 0; i < 1500; i++ {
		if _, err := tx.Exec(`INSERT INTO messages (id, chat_id, from_me, ts, message_json) VALUES (?,'c1',0,1,'{}')`, strconv.Itoa(i)); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- errors.New("panic: " + errAsString(r))
				return
			}
			done <- nil
		}()
		_ = s.BackfillFTS(func(string) string { return "hello" })
	}()

	_ = s.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("BackfillFTS: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("BackfillFTS never returned after Close")
	}
}

func errAsString(r any) string {
	if e, ok := r.(error); ok {
		return e.Error()
	}
	return "unknown panic"
}
