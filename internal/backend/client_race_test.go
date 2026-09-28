package backend

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"whatzap/internal/whatsapp"
)

// offlineClient builds a real, never-connected WhatsApp client on a
// throwaway device store, so handlers can be exercised without a network.
func offlineClient(t *testing.T) *whatsapp.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wa.db")
	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", waLog.Noop)
	if err != nil {
		t.Fatalf("sqlstore: %v", err)
	}
	// Close before t.TempDir's cleanup, or Windows can't delete the file.
	t.Cleanup(func() { _ = container.Close() })
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	return whatsapp.NewClient(device, "ERROR")
}

func call(h http.HandlerFunc, method, target, body string) (code int, panicked any) {
	defer func() { panicked = recover() }()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Code, nil
}

// handlerCases lists every handler touched by the fix, with a request that
// reaches its WhatsApp-client code.
func handlerCases(app *App) map[string]func() (int, any) {
	return map[string]func() (int, any){
		"syncGroups": func() (int, any) { return call(app.handleSyncGroups, http.MethodPost, "/groups/sync", "") },
		"groupMembers": func() (int, any) {
			return call(app.handleGroupMembers, http.MethodGet, "/group/members?jid=123@g.us", "")
		},
		"profilePic": func() (int, any) {
			return call(app.handleProfilePicture, http.MethodGet, "/profile-picture?jid=15551230001@s.whatsapp.net", "")
		},
		"mediaDownload": func() (int, any) {
			return call(app.handleMediaDownload, http.MethodGet, "/media/download?chatId=1@s.whatsapp.net&msgId=X", "")
		},
		"syncContacts": func() (int, any) { return call(app.handleSyncContacts, http.MethodPost, "/contacts/sync", "") },
		"block": func() (int, any) {
			return call(app.handleBlock, http.MethodPost, "/block", `{"chatId":"15551230001@s.whatsapp.net"}`)
		},
		"syncHistory": func() (int, any) { return call(app.handleSyncHistory, http.MethodPost, "/history/sync", `{}`) },
		"resolveLID": func() (int, any) {
			return call(app.handleResolveLIDPN, http.MethodGet, "/lid/resolve?id=15551230001@s.whatsapp.net", "")
		},
		"contacts": func() (int, any) { return call(app.handleContacts, http.MethodGet, "/contacts", "") },
		"chats":    func() (int, any) { return call(app.handleChats, http.MethodGet, "/chats", "") },
	}
}

// Negative: with no WhatsApp client (logged out), every handler answers
// cleanly instead of panicking on a nil client.
func TestHandlersWithNoClientDoNotPanic(t *testing.T) {
	app := newTestApp(t)
	want := map[string]int{
		"syncGroups": http.StatusConflict, "groupMembers": http.StatusConflict,
		"profilePic": http.StatusConflict, "mediaDownload": http.StatusConflict,
		"syncContacts": http.StatusConflict, "block": http.StatusConflict,
		"syncHistory": http.StatusConflict, "resolveLID": http.StatusInternalServerError,
		"contacts": http.StatusOK, "chats": http.StatusOK,
	}
	for name, run := range handlerCases(app) {
		code, p := run()
		if p != nil {
			t.Errorf("%s panicked with no client: %v", name, p)
			continue
		}
		if code != want[name] {
			t.Errorf("%s: status %d, want %d", name, code, want[name])
		}
	}
}

// Positive/edge: a real but not-yet-connected client is reported as "not
// connected" (409), not used half-way.
func TestHandlersWithOfflineClientReportNotConnected(t *testing.T) {
	app := newTestApp(t)
	app.client = offlineClient(t)
	for _, name := range []string{"syncGroups", "groupMembers", "profilePic", "mediaDownload", "syncContacts", "block", "syncHistory"} {
		code, p := handlerCases(app)[name]()
		if p != nil {
			t.Fatalf("%s panicked: %v", name, p)
		}
		if code != http.StatusConflict {
			t.Errorf("%s: status %d, want 409", name, code)
		}
	}
}

// connectedClient hands back the snapshot it checked, or nil + 409.
func TestConnectedClientSnapshot(t *testing.T) {
	app := newTestApp(t)
	rec := httptest.NewRecorder()
	if c := app.connectedClient(rec); c != nil || rec.Code != http.StatusConflict {
		t.Fatalf("no client: got %v / %d, want nil / 409", c, rec.Code)
	}
	app.client = offlineClient(t)
	rec = httptest.NewRecorder()
	if c := app.connectedClient(rec); c != nil || rec.Code != http.StatusConflict {
		t.Fatalf("offline client: got %v / %d, want nil / 409", c, rec.Code)
	}
	var nilApp *App
	if nilApp.getClient() != nil {
		t.Fatal("getClient on a nil App should return nil")
	}
}

// Negative: background jobs and helpers are no-ops without a client.
func TestBackgroundHelpersWithNoClient(t *testing.T) {
	app := newTestApp(t)
	mustNotPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s panicked with no client: %v", name, r)
			}
		}()
		f()
	}
	mustNotPanic("refreshGroupMetadata", func() {
		if n := app.refreshGroupMetadata(); n != 0 {
			t.Errorf("refreshGroupMetadata = %d, want 0", n)
		}
	})
	mustNotPanic("reconcileLIDChats", app.reconcileLIDChats)
	mustNotPanic("purgeOwnPushNameFromContacts", app.purgeOwnPushNameFromContacts)
	mustNotPanic("bootstrapFromStore", app.bootstrapFromStore)
	mustNotPanic("safeFetchAppState", func() { app.safeFetchAppState(context.Background(), "regular") })
	mustNotPanic("getLIDMap", func() { _ = app.getLIDMap() })
	mustNotPanic("disconnectAndLogoutClient", func() {
		if errs := app.disconnectAndLogoutClient(); len(errs) != 0 {
			t.Errorf("disconnectAndLogoutClient errs = %v, want none", errs)
		}
	})
	mustNotPanic("getPNForLID", func() {
		lid, _ := types.ParseJID("123@lid")
		if _, err := app.getPNForLID(lid); err == nil {
			t.Error("getPNForLID with no client should return an error")
		}
	})
	mustNotPanic("canonicalizeChatID", func() {
		if got := app.canonicalizeChatID("15551230001@s.whatsapp.net"); got != "15551230001@s.whatsapp.net" {
			t.Errorf("canonicalizeChatID = %q, want unchanged", got)
		}
	})
	mustNotPanic("startSession", func() {
		if err := app.startSession(); err == nil {
			t.Error("startSession with no client should return an error")
		}
	})
}

// Stress: requests and helpers keep running while another goroutine
// repeatedly "logs out" (sets the client to nil) and back in, the way a
// real logout/relogin swaps it. Nothing may panic.
func TestClientSwapDuringRequestsNoPanic(t *testing.T) {
	app := newTestApp(t)
	offline := offlineClient(t)
	app.client = offline

	var panics atomic.Int64
	var firstPanic atomic.Value
	stop := make(chan struct{})
	var wg sync.WaitGroup
	worker := func(f func()) {
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
						firstPanic.CompareAndSwap(nil, fmt.Sprint(r))
					}
				}()
				f()
			}()
		}
	}
	for _, run := range handlerCases(app) {
		wg.Add(1)
		go worker(func() {
			_, p := run()
			if p != nil {
				panic(p)
			}
		})
	}
	wg.Add(3)
	go worker(func() { _ = app.canonicalizeChatID("15551230001@s.whatsapp.net") })
	go worker(func() { _ = app.getLIDMap() })
	go worker(func() { _ = app.refreshGroupMetadata() })

	deadline := time.Now().Add(400 * time.Millisecond)
	for i := 0; time.Now().Before(deadline); i++ {
		app.mu.Lock()
		if i%2 == 0 {
			app.client = nil
		} else {
			app.client = offline
		}
		app.mu.Unlock()
	}
	close(stop)
	wg.Wait()
	if n := panics.Load(); n > 0 {
		t.Fatalf("%d panics while the client was swapped; first: %v", n, firstPanic.Load())
	}
}
