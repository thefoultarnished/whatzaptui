package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsAllowed(t *testing.T) {
	cases := []struct {
		name           string
		defaultAllowed bool
		whitelist      map[string]string
		denied         map[string]bool
		phone          string
		want           bool
	}{
		{"deny mode listed", false, map[string]string{"1": "A"}, nil, "1", true},
		{"deny mode unlisted", false, map[string]string{"1": "A"}, nil, "2", false},
		{"deny mode nil maps", false, nil, nil, "1", false},
		{"allow mode unlisted", true, nil, nil, "1", true},
		{"allow mode denied", true, nil, map[string]bool{"1": true}, "1", false},
		{"allow mode nil denied", true, map[string]string{"1": "A"}, nil, "2", true},
		{"allow mode explicit row still allowed", true, map[string]string{"1": "A"}, map[string]bool{}, "1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := m{defaultAllowed: tc.defaultAllowed, whitelist: tc.whitelist, denied: tc.denied}
			if got := model.isAllowed(tc.phone); got != tc.want {
				t.Fatalf("isAllowed(%q) = %v, want %v", tc.phone, got, tc.want)
			}
		})
	}
}

func TestGetWhitelistParsesDefaultAndDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"contacts":[
			{"phone":"15551230001","name":"Alex","allowed":1},
			{"phone":"15551230002","name":"Sam","allowed":0}
		],"defaultAllowed":true}`))
	}))
	defer srv.Close()

	msg := getWhitelist(context.Background(), srv.Client(), srv.URL)()
	got, ok := msg.(whitelistLoadMsg)
	if !ok {
		t.Fatalf("msg type = %T, want whitelistLoadMsg", msg)
	}
	if got.err != nil {
		t.Fatalf("err = %v", got.err)
	}
	if !got.defaultAllowed {
		t.Fatal("defaultAllowed = false, want true")
	}
	if got.whitelist["15551230001"] != "Alex" {
		t.Fatalf("whitelist = %+v, want Alex listed", got.whitelist)
	}
	if !got.denied["15551230002"] {
		t.Fatalf("denied = %+v, want 15551230002 denied", got.denied)
	}
	if got.names["15551230002"] != "Sam" {
		t.Fatalf("names = %+v, want Sam kept for denied row", got.names)
	}
}

// buildDefaultTestServer records POSTs to /whitelist/default.
func buildDefaultTestServer(t *testing.T) (*httptest.Server, *[]int, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var captured []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/whitelist/default" && r.Method == http.MethodPost {
			var body struct {
				Allowed int `json:"allowed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				mu.Lock()
				captured = append(captured, body.Allowed)
				mu.Unlock()
			}
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	return srv, &captured, &mu
}

func baseTestModel(baseURL string, client *http.Client) m {
	return m{
		status:       "ready",
		mode:         "chat",
		baseURL:      baseURL,
		client:       client,
		whitelist:    map[string]string{},
		denied:       map[string]bool{},
		names:        map[string]string{},
		chats:        []chat{},
		msgs:         map[string][]wireMsg{},
		flashUntil:   map[string]time.Time{},
		mainCache:    &renderCache{},
		sidebarCache: &sidebarCache{},
	}
}

func TestDoWhitelistAllSingleCall(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildDefaultTestServer(t)
	defer srv.Close()

	model := baseTestModel(srv.URL, srv.Client())
	model.denied = map[string]bool{"15551230001": true}
	model.names = map[string]string{"15551230001": "Alex"}

	cmd := model.doWhitelistAll()
	runBatch(t, cmd)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := len(*captured)
		val := 0
		if got > 0 {
			val = (*captured)[0]
		}
		mu.Unlock()
		if got == 1 {
			if val != 1 {
				t.Fatalf("default call allowed = %d, want 1", val)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected exactly 1 default call with allowed=1, got %+v", *captured)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !model.defaultAllowed {
		t.Fatal("defaultAllowed = false, want true")
	}
	if len(model.denied) != 0 {
		t.Fatalf("denied = %+v, want cleared", model.denied)
	}
	if model.whitelist["15551230001"] != "Alex" {
		t.Fatalf("whitelist = %+v, want denied entry mirrored", model.whitelist)
	}
}

func TestDoBlacklistAllSingleCall(t *testing.T) {
	withTempAPIEnv(t)
	srv, captured, mu := buildDefaultTestServer(t)
	defer srv.Close()

	model := baseTestModel(srv.URL, srv.Client())
	model.active = "15551230001@s.whatsapp.net"
	model.whitelist = map[string]string{"15551230001": "Alex", "15551230002": "Sam"}

	cmd := model.doBlacklistAll()
	runBatch(t, cmd)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := len(*captured)
		val := 0
		if got > 0 {
			val = (*captured)[0]
		}
		mu.Unlock()
		if got == 1 {
			if val != 0 {
				t.Fatalf("default call allowed = %d, want 0", val)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected exactly 1 default call with allowed=0, got %+v", *captured)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if model.defaultAllowed {
		t.Fatal("defaultAllowed = true, want false")
	}
	if len(model.whitelist) != 0 {
		t.Fatalf("whitelist = %+v, want emptied", model.whitelist)
	}
	if !model.denied["15551230001"] || !model.denied["15551230002"] {
		t.Fatalf("denied = %+v, want both moved over", model.denied)
	}
}

func TestToggleWhitelistUnderDefaultAllowed(t *testing.T) {
	// demoMode avoids network; only local map changes are asserted.
	model := baseTestModel("", nil)
	model.demoMode = true
	model.defaultAllowed = true
	model.active = "15551230001@s.whatsapp.net"
	model.chats = []chat{{ID: "15551230001@s.whatsapp.net", Name: "Alex"}}
	model.rebuildContactIndex()

	// Allowed by default -> toggle denies.
	runBatch(t, model.toggleWhitelistForSelection())
	got := model
	if !got.denied["15551230001"] {
		t.Fatalf("denied = %+v, want 15551230001 denied", got.denied)
	}
	if got.isAllowed("15551230001") {
		t.Fatal("isAllowed = true after deny toggle, want false")
	}
	if !strings.Contains(got.topBarMsg, "Blacklisted") {
		t.Fatalf("top-bar = %q, want Blacklisted", got.topBarMsg)
	}

	// Denied -> toggle restores default-allow.
	model.topBarMsg = ""
	runBatch(t, model.toggleWhitelistForSelection())
	got = model
	if got.denied["15551230001"] {
		t.Fatalf("denied = %+v, want cleared", got.denied)
	}
	if !got.isAllowed("15551230001") {
		t.Fatal("isAllowed = false after un-deny toggle, want true")
	}
	if !strings.Contains(got.topBarMsg, "Whitelisted") {
		t.Fatalf("top-bar = %q, want Whitelisted", got.topBarMsg)
	}
}

func TestWhitelistLoadMsgAppliesDefault(t *testing.T) {
	model := baseTestModel("", nil)
	next, _ := model.Update(whitelistLoadMsg{
		whitelist:      map[string]string{"15551230001": "Alex"},
		denied:         map[string]bool{"15551230002": true},
		defaultAllowed: true,
		names:          map[string]string{"15551230001": "Alex"},
	})
	got := next.(m)
	if !got.defaultAllowed || !got.denied["15551230002"] || got.whitelist["15551230001"] != "Alex" {
		t.Fatalf("load not applied: default=%v denied=%+v whitelist=%+v",
			got.defaultAllowed, got.denied, got.whitelist)
	}
}

func TestSetWhitelistDefault404HintsStaleBackend(t *testing.T) {
	withTempAPIEnv(t)
	// Simulate a stale backend that predates POST /whitelist/default:
	// the default Go mux answers 404 with plain "404 page not found".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	msg := setWhitelistDefault(context.Background(), srv.Client(), srv.URL, 1)()
	got, ok := msg.(whitelistSetMsg)
	if !ok {
		t.Fatalf("msg type = %T, want whitelistSetMsg", msg)
	}
	if got.err == nil {
		t.Fatal("err = nil, want stale-backend hint")
	}
	if !strings.Contains(got.err.Error(), "out of date") {
		t.Fatalf("err = %q, want stale-backend hint", got.err.Error())
	}
}
