package backend

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func fakeGetAllContacts(m map[types.JID]types.ContactInfo) func(context.Context) (map[types.JID]types.ContactInfo, error) {
	return func(context.Context) (map[types.JID]types.ContactInfo, error) { return m, nil }
}

// The People tab's real bug: WhatsApp's own address-book sync often
// finishes seconds or minutes after the initial bootstrap already
// returned, so the first pass sees only a handful of real names.
// seedContactsFromStore must be safe to call again later and pick up
// contacts the first call missed, without wiping ones it already found.
func TestSeedContactsFromStoreIsAdditive(t *testing.T) {
	app := newTestApp(t)
	jid := types.JID{User: "15551230001", Server: types.DefaultUserServer}

	// First pass: WhatsApp has only sent the push name so far.
	n := app.seedContactsFromStore(context.Background(), fakeGetAllContacts(map[types.JID]types.ContactInfo{
		jid: {PushName: "Alice"},
	}))
	if n != 0 {
		// No hasChat and no real name yet: nothing to seed, matches
		// existing bootstrap behavior (a bare push name alone is not
		// enough without a chat already present).
		t.Fatalf("first pass seeded = %d, want 0 (no chat, no real name yet)", n)
	}
	if app.state.Contacts[jid.String()].Stored {
		t.Fatal("must not be marked Stored from a push name alone")
	}

	// Second pass, later: the real address-book entry has now arrived.
	n = app.seedContactsFromStore(context.Background(), fakeGetAllContacts(map[types.JID]types.ContactInfo{
		jid: {FullName: "Alice Real Name", PushName: "Alice"},
	}))
	if n != 1 {
		t.Fatalf("second pass seeded = %d, want 1", n)
	}
	ct := app.state.Contacts[jid.String()]
	if !ct.Stored {
		t.Fatal("contact with a real FullName must be marked Stored")
	}
	if ct.Notify != "Alice Real Name" {
		t.Fatalf("Notify = %q, want the real name", ct.Notify)
	}
}

// A saved-contact name with no existing chat must not synthesize a
// phantom zero-message Chats entry — the same phantom-chat bug as the
// historysync pushname path (events.go), reachable here through a fresh
// login's normal bootstrap/reseed of the address book. handleChats already
// fills a real chat's name in from Contacts at serve time, so nothing is
// lost by not writing Chats here.
func TestSeedContactsFromStoreDoesNotCreatePhantomChat(t *testing.T) {
	app := newTestApp(t)
	jid := types.JID{User: "15551230003", Server: types.DefaultUserServer}

	n := app.seedContactsFromStore(context.Background(), fakeGetAllContacts(map[types.JID]types.ContactInfo{
		jid: {FullName: "Saved Person"},
	}))
	if n != 1 {
		t.Fatalf("seeded = %d, want 1", n)
	}
	if _, ok := app.state.Chats[jid.String()]; ok {
		t.Fatalf("a saved contact with no chat history must not create a Chats entry, got %+v", app.state.Chats[jid.String()])
	}
	if !app.state.Contacts[jid.String()].Stored {
		t.Fatal("contact should still be marked Stored")
	}
}

func TestSeedContactsFromStoreHandlesNilAndError(t *testing.T) {
	app := newTestApp(t)
	if n := app.seedContactsFromStore(context.Background(), nil); n != 0 {
		t.Fatalf("nil getAllContacts: seeded = %d, want 0", n)
	}
	errFn := func(context.Context) (map[types.JID]types.ContactInfo, error) {
		return nil, errors.New("store unavailable")
	}
	if n := app.seedContactsFromStore(context.Background(), errFn); n != 0 {
		t.Fatalf("erroring getAllContacts: seeded = %d, want 0", n)
	}
}

// The whole point of extracting seedContactsFromStore out of a.db-touching
// code: it must finish fast even while a history-sync transaction holds
// the app's single SQLite connection, because it never touches a.db at
// all (GetAllContacts is whatsmeow's own store). This is the same class
// of bug as the history-sync deadlock — proving this path doesn't share it.
func TestSeedContactsFromStoreIndependentOfDBLock(t *testing.T) {
	app := newTestApp(t)
	app.db.SetMaxOpenConns(1)
	tx, err := app.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	jid := types.JID{User: "15551230001", Server: types.DefaultUserServer}
	done := make(chan int, 1)
	go func() {
		done <- app.seedContactsFromStore(context.Background(), fakeGetAllContacts(map[types.JID]types.ContactInfo{
			jid: {FullName: "Alice"},
		}))
	}()
	select {
	case n := <-done:
		if n != 1 {
			t.Fatalf("seeded = %d, want 1", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("seedContactsFromStore blocked behind a DB transaction it should never touch")
	}
}

// A burst of AppStateSyncComplete events (one per patch category) must
// coalesce into a single reseed, not one per event.
func TestScheduleContactReseedDebounces(t *testing.T) {
	old := contactReseedDebounce
	contactReseedDebounce = 30 * time.Millisecond
	defer func() { contactReseedDebounce = old }()

	app := newTestApp(t)
	var calls atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	app.onContactReseed = func() {
		calls.Add(1)
		wg.Done()
	}

	for i := 0; i < 5; i++ {
		app.scheduleContactReseed()
		time.Sleep(5 * time.Millisecond)
	}

	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("debounced reseed never ran")
	}
	time.Sleep(100 * time.Millisecond) // catch any extra late fire
	if n := calls.Load(); n != 1 {
		t.Fatalf("reseed ran %d times, want 1 (debounce should coalesce the burst)", n)
	}
}

// handleAppStateSyncComplete is the wiring from the whatsmeow event to the
// debounce; a nil event or a shutting-down app must not schedule anything.
func TestHandleAppStateSyncCompleteGuards(t *testing.T) {
	app := newTestApp(t)
	app.shuttingDown = true
	var calls atomic.Int32
	app.onContactReseed = func() { calls.Add(1) }
	app.handleAppStateSyncComplete("regular")
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("must not schedule a reseed while shutting down")
	}
}

// runContactReseed's default action (no test override) must no-op cleanly
// when there is no logged-in client, mirroring bootstrapFromStore's own
// login guard, and must not panic.
func TestRunContactReseedNoopWithoutClient(t *testing.T) {
	app := newTestApp(t)
	app.runContactReseed() // client is nil; must return without panicking
}
