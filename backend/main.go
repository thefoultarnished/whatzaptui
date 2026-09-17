package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// S-3: minimum log level for the whatsmeow loggers (both the
// client and the sqlstore). Default is "WARN" — pre-fix was
// "DEBUG" for the client, which spammed stdout with every
// protobuf marshal/unmarshal, every retry, and every received
// message body. Override at startup with WHATZAP_LOG_LEVEL=DEBUG
// (or INFO/WARN/ERROR) for troubleshooting; main() prints the
// active level to stdout so the user can verify the env var
// took effect. Mutable var (not const) so tests can swap it.
// Valid values per waLog/util/log/log.go:levelToInt are
// "" (all), "DEBUG", "INFO", "WARN", "ERROR" (case-insensitive).
var whatsmeowLogLevel = "WARN"

// resolveWhatsmeowLogLevel returns the active log level string
// for the whatsmeow loggers. Priority: WHATZAP_LOG_LEVEL env
// var (if non-empty after trim+upper) > "WARN" default. Exposed
// as a helper so main() and NewApp() use the same logic and
// tests can exercise the resolution path without spinning up
// the full NewApp() stack (which would try to talk to WhatsApp).
func resolveWhatsmeowLogLevel() string {
	if override := strings.TrimSpace(strings.ToUpper(os.Getenv("WHATZAP_LOG_LEVEL"))); override != "" {
		return override
	}
	return "WARN"
}

const maxMessagesResponseLimit = 200
const appDataDirName = "WhatZAP"
const authHeaderName = "Authorization"

// sessionTokenFileName is the name of the file (under
// <data-root>/backend/) that holds the bearer token shared between the
// TUI and the backend. Replaces the old WHATZAP_API_TOKEN env var (S-1):
// env vars are inherited by every child process and visible to anything
// that can inspect the process, whereas this file is written with 0600
// permissions in the same per-user data directory as store.db.
const sessionTokenFileName = "session.token"

// redactPlaceholder is what a leaked secret is replaced with in logs.
const redactPlaceholder = "[REDACTED]"

// redactingWriter wraps an io.Writer and scrubs a secret string out of
// every write before it reaches the underlying sink. It is the S-16
// defense: even if some future code path does log.Printf("%v", r) and
// dumps an *http.Request (whose Header map holds "Authorization: Bearer
// <token>"), the token value is replaced with [REDACTED] before it
// touches stderr. The stdlib logger buffers a full line and calls Write
// once per entry, so a token never straddles two Write calls.
type redactingWriter struct {
	w      io.Writer
	secret func() string
}

// newRedactingWriter scrubs whatever secret() currently returns out of
// every write. secret is called per-write (not once at construction) so
// that a token rotated later (A-1) is still redacted — without this, a
// rotated token logged after rotation would bypass a redactor built from
// the token's old value.
func newRedactingWriter(w io.Writer, secret func() string) io.Writer {
	if secret == nil {
		return w
	}
	return &redactingWriter{w: w, secret: secret}
}

func (rw *redactingWriter) Write(p []byte) (int, error) {
	s := strings.TrimSpace(rw.secret())
	if s == "" || !bytes.Contains(p, []byte(s)) {
		return rw.w.Write(p)
	}
	cleaned := bytes.ReplaceAll(p, []byte(s), []byte(redactPlaceholder))
	// Report len(p) as written, not len(cleaned): callers expect the
	// number of input bytes consumed, and a short count would trip the
	// stdlib logger into thinking the write failed.
	if _, err := rw.w.Write(cleaned); err != nil {
		return 0, err
	}
	return len(p), nil
}

// globalApp lets the S-16 log redactors (set up in main() before NewApp()
// runs) look up the *current* token after rotation (A-1). Stored exactly
// once, right after NewApp() succeeds.
var globalApp atomic.Pointer[App]

func main() {
	tokenPath, err := sessionTokenPath()
	if err != nil {
		log.Fatalf("resolve session token path: %v", err)
	}
	token, err := readSessionToken(tokenPath)
	if err != nil {
		log.Fatalf("init failed: %v", err)
	}

	// secretFn always returns the *current* token: before NewApp() runs
	// it's the token just read from disk; afterwards it's a.apiToken,
	// which the A-1 rotation ticker may have replaced.
	secretFn := func() string {
		if a := globalApp.Load(); a != nil {
			a.mu.RLock()
			defer a.mu.RUnlock()
			return a.apiToken
		}
		return token
	}

	// S-16: route the default logger through a redactor before anything
	// is logged, so the bearer token can never leak into stderr even if
	// a future code path logs a raw *http.Request.
	log.SetOutput(newRedactingWriter(os.Stderr, secretFn))

	// S-3: print the active whatsmeow log level as the very first
	// line so users can verify WHATZAP_LOG_LEVEL took effect. Goes
	// to stdout (which the TUI's 8KB ring buffer also captures,
	// so it shows up in startup-error messages). Uses the same
	// helper as NewApp() so the printed value always matches the
	// value the loggers will actually use.
	log.Printf("[backend] log level: %s (set WHATZAP_LOG_LEVEL to change)", resolveWhatsmeowLogLevel())

	app, err := NewApp(token, tokenPath)
	if err != nil {
		log.Fatalf("init failed: %v", err)
	}
	globalApp.Store(app)

	// A-13: periodic eviction of in-memory maps. Runs every 30s
	// to keep Chats/Contacts/lidCache within their caps. Uses
	// a background goroutine because the maps are not persisted
	// as a batch anywhere we could piggyback on — lidCache in
	// particular has no persistence path at all.
	//
	// A-1: the same tick also checks whether the registered TUI session
	// (see /session/register) has gone away and, if so, rotates the
	// session token so any leaked copy of the old token stops working.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			app.mu.Lock()
			cappedEvict(app.state.Chats, maxChatCaps)
			cappedEvict(app.state.Contacts, maxContactCaps)
			app.mu.Unlock()
			app.lidCacheMu.Lock()
			cappedEvict(app.lidCache, maxLIDCacheCaps)
			app.lidCacheMu.Unlock()

			app.rotateTokenIfSessionDead()
		}
	}()

	addr := backendHost + ":" + backendPort
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.handler(),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// S-16: same redactor as the default logger. The stdlib http
		// server can log request details here on malformed input; route
		// it through the scrubber so a token can't leak via that path.
		ErrorLog: log.New(newRedactingWriter(os.Stderr, secretFn), "[http] ", log.LstdFlags),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("whatsmeow backend listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down…")

	app.mu.Lock()
	app.shuttingDown = true
	app.mu.Unlock()

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)

	if app.client != nil {
		app.client.Disconnect()
	}
	if app.db != nil {
		_ = app.db.Close()
	}
}

func whatzapDataRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("WHATZAP_DATA_DIR")); override != "" {
		return filepath.Clean(override), nil
	}
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		return filepath.Join(base, appDataDirName), nil
	}
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, appDataDirName), nil
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".whatzap"), nil
	}
	return "", fmt.Errorf("failed to resolve app data directory")
}

// sessionTokenPath returns the path to the session token file (S-1):
// <data-root>/backend/session.token. The TUI writes/reads this same file
// (it computes the same path independently — see backend/tui/main.go).
func sessionTokenPath() (string, error) {
	dataRoot, err := whatzapDataRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataRoot, "backend")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionTokenFileName), nil
}

// readSessionToken reads and validates the session token file written by
// the TUI. Returns an error (with a message pointing at the cause) if the
// file is missing or empty — the backend should not start with no token,
// since isAuthorized() would then reject every request.
func readSessionToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("session token file %s not found; start the TUI first: %w", path, err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("session token file %s is empty; start the TUI first", path)
	}
	return token, nil
}

// generateSessionToken returns a fresh random 32-byte token, base64url
// encoded. Used both for the initial token (TUI) and for A-1 rotation
// (backend, when a registered TUI session goes away).
func generateSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func resolveBackendCacheDir(workDir string) (string, error) {
	dataRoot, err := whatzapDataRoot()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dataRoot, "backend")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}

	legacyDir := filepath.Join(workDir, ".whatsmeow_cache")
	shouldMigrate, err := shouldMigrateLegacyDir(cacheDir, legacyDir)
	if err != nil {
		return "", err
	}
	if shouldMigrate {
		if err := copyDirContents(legacyDir, cacheDir); err != nil {
			return "", err
		}
	}

	return cacheDir, nil
}

func shouldMigrateLegacyDir(targetDir, legacyDir string) (bool, error) {
	if filepath.Clean(targetDir) == filepath.Clean(legacyDir) {
		return false, nil
	}
	info, err := os.Stat(legacyDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

func copyDirContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// S-8: skip symlinks. The legacy `.whatsmeow_cache` dir may
		// contain a symlink the user (or an attacker with write access
		// to the working dir) placed there pointing at, say,
		// ~/.ssh/id_rsa — without this check we'd open the symlink
		// and copy the target's contents into the live data folder.
		// entry.Info() uses Lstat semantics so info.Mode() reflects
		// the symlink itself, not its target; the IsRegular() check
		// below would also skip, but being explicit makes the intent
		// obvious and protects against an accidental IsRegular()
		// edit later. Junctions (Windows directory reparse points)
		// are not covered by this check and would still be recursed
		// into — out of scope for S-8, see security_easy.md.
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDirContents(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(srcPath, dstPath string, perm os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return nil
}

func NewApp(apiToken, tokenPath string) (*App, error) {
	apiToken = strings.TrimSpace(apiToken)
	if apiToken == "" {
		return nil, fmt.Errorf("session token must not be empty")
	}
	// S-3: read the WHATZAP_LOG_LEVEL env var via the shared
	// helper so main() and NewApp() see the same value (and tests
	// can exercise the resolution logic in isolation). Must run
	// before the waLog.Stdout calls a few hundred lines down so
	// the value is in effect by the time the whatsmeow
	// client/sqlstore loggers are constructed.
	whatsmeowLogLevel = resolveWhatsmeowLogLevel()
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cacheDir, err := resolveBackendCacheDir(workDir)
	if err != nil {
		return nil, err
	}
	app := &App{
		cacheDir:  cacheDir,
		apiToken:  apiToken,
		tokenPath: tokenPath,
		wsClients: map[*websocket.Conn]*wsClient{},
		lidCache:  map[string]string{},
		state: PersistedState{
			Chats:    map[string]Chat{},
			Contacts: map[string]Contact{},
		},
	}
	if err := app.initPersistentResources(); err != nil {
		return nil, err
	}
	app.loadState()
	app.startPersistWorker()
	return app, nil
}
