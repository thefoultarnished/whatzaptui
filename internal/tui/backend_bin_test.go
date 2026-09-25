package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"whatzap/internal/buildid"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	time.Sleep(5 * time.Second)
	os.Exit(0)
}

func TestEnsureBackendReusesMatchingBuild(t *testing.T) {
	currentBuild := buildid.Current()
	var shutdownCalled atomic.Bool
	var spawnCalled atomic.Bool

	origSpawn := spawnBackendProcess
	defer func() { spawnBackendProcess = origSpawn }()
	spawnBackendProcess = func() (*exec.Cmd, error) {
		spawnCalled.Store(true)
		return nil, nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":        true,
				"connected": true,
				"build":     currentBuild,
			})
		case "/contacts":
			w.WriteHeader(http.StatusOK)
		case "/shutdown":
			shutdownCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := ensureBackend(context.Background(), srv.Client(), srv.URL, "tok-test")
	msg := cmd()
	init, ok := msg.(initMsg)
	if !ok {
		t.Fatalf("expected initMsg, got %T", msg)
	}
	if init.err != nil {
		t.Fatalf("unexpected error: %v", init.err)
	}
	if shutdownCalled.Load() {
		t.Fatal("shutdown should not have been called for matching build")
	}
	if spawnCalled.Load() {
		t.Fatal("spawn should not have been called for matching build")
	}
}

func TestEnsureBackendReplacesOutdatedBuild(t *testing.T) {
	var shutdownCalled atomic.Bool
	var spawnCalled atomic.Bool

	origSpawn := spawnBackendProcess
	defer func() { spawnBackendProcess = origSpawn }()
	spawnBackendProcess = func() (*exec.Cmd, error) {
		spawnCalled.Store(true)
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd, nil
	}

	var healthCallCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			count := healthCallCount.Add(1)
			if !shutdownCalled.Load() {
				// Old build before shutdown
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":        true,
					"connected": true,
					"build":     "outdated-build-id",
				})
			} else if count < 5 {
				// Port is down or transitioning
				w.WriteHeader(http.StatusServiceUnavailable)
			} else {
				// New backend became ready
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":        true,
					"connected": true,
					"build":     buildid.Current(),
				})
			}
		case "/shutdown":
			shutdownCalled.Store(true)
			w.WriteHeader(http.StatusOK)
		case "/contacts":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := ensureBackend(context.Background(), srv.Client(), srv.URL, "tok-test")
	msg := cmd()
	init, ok := msg.(initMsg)
	if !ok {
		t.Fatalf("expected initMsg, got %T", msg)
	}
	if init.cmd != nil && init.cmd.Process != nil {
		_ = init.cmd.Process.Kill()
		_, _ = init.cmd.Process.Wait()
	}
	if !shutdownCalled.Load() {
		t.Fatal("expected outdated backend to be shut down")
	}
	if !spawnCalled.Load() {
		t.Fatal("expected new backend to be spawned")
	}
	if init.err != nil {
		t.Fatalf("unexpected error: %v", init.err)
	}
}

func TestEnsureBackendErrorOnUnauthorizedShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":        true,
				"connected": true,
				"build":     "mismatched-build",
			})
		case "/shutdown":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cmd := ensureBackend(context.Background(), srv.Client(), srv.URL, "tok-test")
	msg := cmd()
	init, ok := msg.(initMsg)
	if !ok {
		t.Fatalf("expected initMsg, got %T", msg)
	}
	if init.err == nil || !strings.Contains(init.err.Error(), "cannot stop outdated backend") {
		t.Fatalf("expected cannot stop outdated backend error, got: %v", init.err)
	}
}
