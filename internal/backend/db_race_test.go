package backend

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
)

// loggedOut simulates the state right after a logout: storage handles gone.
func loggedOut(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.mu.Lock()
	// Close like a real logout does (also lets Windows delete the temp dir).
	_ = app.db.Close()
	app.db = nil
	app.store = nil
	app.mu.Unlock()
	return app
}

// dbHelpers is every function the fix touched that runs without a request,
// each wrapped so it can be called with no arguments.
func dbHelpers(app *App) map[string]func() {
	return map[string]func(){
		"getLIDMap":                      func() { _ = app.getLIDMap() },
		"loadChatsFromDB":                func() { _, _ = app.loadChatsFromDB() },
		"loadContactsFromDB":             func() { _, _ = app.loadContactsFromDB() },
		"upsertChatToDB":                 func() { _ = app.upsertChatToDB(Chat{ID: "1@s.whatsapp.net"}) },
		"upsertContactToDB":              func() { _ = app.upsertContactToDB(Contact{ID: "1@s.whatsapp.net", Name: "A"}) },
		"backfillFTS":                    app.backfillFTS,
		"purgeContactsWithName":          func() { _, _ = app.purgeContactsWithName("nobody") },
		"purgeGroupSenderNames":          app.purgeGroupSenderNames,
		"backfillReceipt":                app.backfillReceipt,
		"purgeInvisibleProtocolMessages": app.purgeInvisibleProtocolMessages,
		"purgeEmptyPhantomChats":         app.purgeEmptyPhantomChats,
		"reconcileChatTimestampsFromDB":  app.reconcileChatTimestampsFromDB,
		"withTx":                         func() { _ = app.withTx(func(tx *sql.Tx) error { return nil }) },
		"migrateLIDPermissions":          func() { app.migrateLIDPermissions("111", "222") },
		"applyHistorySync":               func() { app.applyHistorySync(&waHistorySync.HistorySync{}) },
	}
}

// Negative: after logout (no DB, no store) every helper is a clean no-op or
// error — never a nil-pointer panic.
func TestDBHelpersAfterLogoutDoNotPanic(t *testing.T) {
	app := loggedOut(t)
	for name, f := range dbHelpers(app) {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked with no database: %v", name, r)
				}
			}()
			f()
		}()
	}
	if err := app.withTx(func(*sql.Tx) error { return nil }); err == nil {
		t.Error("withTx with no database should return an error")
	}
	if m := app.getLIDMap(); m == nil {
		t.Error("getLIDMap should return an empty map, not nil")
	}
}

// Edge: loadState with no database must not panic (it runs again after a
// logout/relogin while other goroutines are live).
func TestLoadStateAfterLogoutDoesNotPanic(t *testing.T) {
	app := loggedOut(t)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("loadState panicked with no database: %v", r)
		}
	}()
	app.loadState()
}

// Positive: with a database the helpers still read and write through the
// snapshot.
func TestDBHelpersUseLiveDatabase(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.db.Exec(`CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec(`INSERT INTO whatsmeow_lid_map VALUES ('111', '222')`); err != nil {
		t.Fatal(err)
	}
	if got := app.getLIDMap()["111"]; got != "222" {
		t.Fatalf("getLIDMap()[111] = %q, want 222", got)
	}

	sentinel := errors.New("rollback me")
	err := app.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('5', 'x', 1)`); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("withTx err = %v, want the callback's error", err)
	}
	var n int
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chat_permissions`).Scan(&n)
	if n != 0 {
		t.Fatalf("withTx did not roll back: %d rows", n)
	}
	if err := app.withTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO chat_permissions (phone, name, allowed) VALUES ('5', 'x', 1)`)
		return err
	}); err != nil {
		t.Fatalf("withTx commit: %v", err)
	}
	_ = app.db.QueryRow(`SELECT COUNT(*) FROM chat_permissions`).Scan(&n)
	if n != 1 {
		t.Fatalf("withTx did not commit: %d rows", n)
	}
}

func TestDBHandlesSnapshot(t *testing.T) {
	var nilApp *App
	if s, db := nilApp.dbHandles(); s != nil || db != nil {
		t.Fatal("dbHandles on a nil App should return nils")
	}
	app := newTestApp(t)
	if _, db := app.dbHandles(); db != app.db {
		t.Fatal("dbHandles did not return the live database")
	}
}

// Stress: helpers keep running while another goroutine repeatedly removes
// and restores the database handle, the way logout/relogin swaps it.
// Nothing may panic.
func TestDBSwapDuringWorkNoPanic(t *testing.T) {
	app := newTestApp(t)
	live := app.db
	if _, err := live.Exec(`CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT)`); err != nil {
		t.Fatal(err)
	}

	var panics atomic.Int64
	var first atomic.Value
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, f := range dbHelpers(app) {
		wg.Add(1)
		go func(f func()) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							panics.Add(1)
							first.CompareAndSwap(nil, fmt.Sprint(r))
						}
					}()
					f()
				}()
			}
		}(f)
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		app.mu.Lock()
		if i%2 == 0 {
			app.db = nil
		} else {
			app.db = live
		}
		app.mu.Unlock()
	}
	close(stop)
	wg.Wait()
	app.mu.Lock()
	app.db = live
	app.mu.Unlock()
	if n := panics.Load(); n > 0 {
		t.Fatalf("%d panics while the database was swapped; first: %v", n, first.Load())
	}
}
