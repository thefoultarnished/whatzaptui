package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// processAlive(os.Getpid()) must be true — this test process is, by
// definition, alive. Covers the cross-platform liveness check used by
// A-1's token-rotation logic.
func TestProcessAliveCurrentProcess(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatalf("processAlive(%d) = false, want true (own pid)", os.Getpid())
	}
}

// processAlive rejects non-positive PIDs outright on every platform.
func TestProcessAliveInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if processAlive(pid) {
			t.Fatalf("processAlive(%d) = true, want false", pid)
		}
	}
}

// POST /session/register records the caller's PID, which
// rotateTokenIfSessionDead later uses to detect whether that TUI process
// is still running (A-1).
func TestHandleSessionRegister(t *testing.T) {
	app := newTestApp(t)

	body, _ := json.Marshal(map[string]int{"pid": 12345})
	req := authorizedRequest(httptest.NewRequest(http.MethodPost, "/session/register", strings.NewReader(string(body))), app)
	rec := httptest.NewRecorder()

	app.handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	app.mu.RLock()
	gotPID, gotAt := app.sessionPID, app.sessionRegisteredAt
	app.mu.RUnlock()
	if gotPID != 12345 {
		t.Fatalf("sessionPID = %d, want 12345", gotPID)
	}
	if time.Since(gotAt) > time.Minute {
		t.Fatalf("sessionRegisteredAt = %v, want recent", gotAt)
	}
}

// /session/register rejects malformed bodies and non-POST methods.
func TestHandleSessionRegisterInvalid(t *testing.T) {
	app := newTestApp(t)

	cases := []struct {
		name   string
		method string
		body   string
	}{
		{"wrong method", http.MethodGet, ""},
		{"missing pid", http.MethodPost, `{}`},
		{"zero pid", http.MethodPost, `{"pid":0}`},
		{"bad json", http.MethodPost, `not json`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := authorizedRequest(httptest.NewRequest(c.method, "/session/register", strings.NewReader(c.body)), app)
			rec := httptest.NewRecorder()
			app.handler().ServeHTTP(rec, req)
			if rec.Code/100 == 2 {
				t.Fatalf("status = %d, want non-2xx, body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// rotateTokenIfSessionDead is a no-op until the grace period elapses and
// the registered PID is gone.
func TestRotateTokenIfSessionDeadNoopWhileAlive(t *testing.T) {
	app := newTestApp(t)
	app.tokenPath = filepath.Join(t.TempDir(), "session.token")
	if err := os.WriteFile(app.tokenPath, []byte(app.apiToken), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	app.mu.Lock()
	app.sessionPID = os.Getpid() // alive
	app.sessionRegisteredAt = time.Now().Add(-2 * sessionGraceDuration)
	app.mu.Unlock()

	before := app.apiToken
	app.rotateTokenIfSessionDead()

	app.mu.RLock()
	after := app.apiToken
	app.mu.RUnlock()
	if after != before {
		t.Fatalf("apiToken changed while session pid is alive: %q -> %q", before, after)
	}
}

// When the registered TUI process is gone and the grace period has
// elapsed, rotateTokenIfSessionDead generates a fresh token, writes it to
// disk, and updates a.apiToken — so a leaked copy of the old token stops
// working (A-1).
func TestRotateTokenIfSessionDeadRotates(t *testing.T) {
	app := newTestApp(t)
	app.tokenPath = filepath.Join(t.TempDir(), "session.token")
	if err := os.WriteFile(app.tokenPath, []byte(app.apiToken), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	// A pid that (almost certainly) does not correspond to a running
	// process on any platform.
	deadPID := 1<<31 - 1

	app.mu.Lock()
	app.sessionPID = deadPID
	app.sessionRegisteredAt = time.Now().Add(-2 * sessionGraceDuration)
	app.mu.Unlock()

	oldToken := app.apiToken
	app.rotateTokenIfSessionDead()

	app.mu.RLock()
	newToken, newPID := app.apiToken, app.sessionPID
	app.mu.RUnlock()

	if newToken == oldToken {
		t.Fatalf("apiToken did not change after rotation")
	}
	if newPID != 0 {
		t.Fatalf("sessionPID = %d, want 0 after rotation", newPID)
	}

	onDisk, err := os.ReadFile(app.tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if got := strings.TrimSpace(string(onDisk)); got != newToken {
		t.Fatalf("token file = %q, want %q", got, newToken)
	}

	// The rotated token must still authorize requests.
	req := httptest.NewRequest(http.MethodGet, "/contacts", nil)
	req.Header.Set(authHeaderName, "Bearer "+newToken)
	rec := httptest.NewRecorder()
	app.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized with new token: status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// The old token must no longer authorize requests.
	req2 := httptest.NewRequest(http.MethodGet, "/contacts", nil)
	req2.Header.Set(authHeaderName, "Bearer "+oldToken)
	rec2 := httptest.NewRecorder()
	app.handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("authorized with old token: status = %d, want %d", rec2.Code, http.StatusUnauthorized)
	}
}

// rotateTokenIfSessionDead is a no-op when no session has been registered.
func TestRotateTokenIfSessionDeadNoSession(t *testing.T) {
	app := newTestApp(t)
	app.tokenPath = filepath.Join(t.TempDir(), "session.token")

	before := app.apiToken
	app.rotateTokenIfSessionDead()

	app.mu.RLock()
	after := app.apiToken
	app.mu.RUnlock()
	if after != before {
		t.Fatalf("apiToken changed with no registered session: %q -> %q", before, after)
	}
}
