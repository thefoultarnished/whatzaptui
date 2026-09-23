package backend

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/gorilla/websocket"

	"whatzap/internal/tokenlock"
)

var maxUploadBytes int64 = 150 * 1024 * 1024
var maxHeaderBytes = 64 * 1024

// S-4: the backend's own listen address. Used in two places: the
// http.Server bind address (line ~174) and the CORS allowlist
// (allowedBackendOrigin below). Keep them in lockstep — if you
// change the bind address, the CORS allowlist changes too, so a
// browser pointed at the new address still works.
const backendHost = "127.0.0.1"
const backendPort = "8787"

// backendListenPort is backendPort unless WHATZAP_PORT overrides it
// (used by process tests to bind a throwaway port instead of :8787).
func backendListenPort() string {
	if p := strings.TrimSpace(os.Getenv("WHATZAP_PORT")); p != "" {
		return p
	}
	return backendPort
}

// allowedBackendOrigin is the single browser origin allowed to
// drive the backend. Pre-fix, any loopback origin was allowed
// (any port, any hostname resolving to 127.0.0.0/8 or ::1) — a
// malicious local page on localhost:3000 could do a CORS
// preflight against the backend and learn that the API was alive.
// Now only the backend's own URL is allowed.
var allowedBackendOrigin = "http://" + backendHost + ":" + backendPort

// A-2: HTTP server timeouts. ReadHeaderTimeout defends against
// slowloris (a client that opens a connection and dribbles bytes
// forever to hold a server goroutine). ReadTimeout caps the total
// request-read time — generous enough for 150MB uploads on
// localhost (which finish in <10s on LAN) but tight enough that
// a stalled body can't pin a goroutine for minutes. WriteTimeout
// caps a stuck response. IdleTimeout caps how long a keep-alive
// connection can sit open doing nothing. All are package-level
// vars (matching maxUploadBytes / maxHeaderBytes) so a future
// /settings entry can override them.
var httpReadHeaderTimeout = 10 * time.Second
var httpReadTimeout = 30 * time.Second
var httpWriteTimeout = 30 * time.Second
var httpIdleTimeout = 2 * time.Minute

// sessionGraceDuration is how long a registered TUI PID (see
// /session/register) is given before the backend will consider it gone
// and rotate the session token (A-1). Paired with the 30s eviction
// ticker in main(), so a dead session's token is rotated within roughly
// sessionGraceDuration to sessionGraceDuration+30s.
var sessionGraceDuration = 60 * time.Second

// A-12: WebSocket heartbeat. The server pings every client every
// wsPingPeriod; a client that neither sends data nor answers pings within
// wsPongWait is dropped, so a stalled connection can't park its read-loop
// goroutine (and its wsClients entry) forever. Vars, not consts, so tests
// can shrink them.
var wsPongWait = 60 * time.Second
var wsPingPeriod = 54 * time.Second
var wsReadLimit int64 = 64 << 10

func (a *App) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/ws", a.handleWS)
	mux.HandleFunc("/start", a.handleStart)
	mux.HandleFunc("/chats", a.handleChats)
	mux.HandleFunc("/contacts", a.handleContacts)
	mux.HandleFunc("/resolve/lidpn", a.handleResolveLIDPN)
	mux.HandleFunc("/sync/contacts", a.handleSyncContacts)
	mux.HandleFunc("/sync/groups", a.handleSyncGroups)
	mux.HandleFunc("/messages", a.handleMessages)
	mux.HandleFunc("/messages/send", a.handleSendMessage)
	mux.HandleFunc("/messages/send-file", a.handleSendFile)
	mux.HandleFunc("/messages/read", a.handleMarkRead)
	mux.HandleFunc("/profile-picture", a.handleProfilePicture)
	mux.HandleFunc("/logout", a.handleLogout)
	mux.HandleFunc("/whitelist", a.handleGetWhitelist)
	mux.HandleFunc("/whitelist/set", a.handleSetWhitelist)
	mux.HandleFunc("/whitelist/default", a.handleSetWhitelistDefault)
	mux.HandleFunc("/names/set", a.handleSetName)
	mux.HandleFunc("/media/download", a.handleMediaDownload)
	mux.HandleFunc("/typing", a.handleTyping)
	mux.HandleFunc("/messages/react", a.handleReact)
	mux.HandleFunc("/messages/delete", a.handleDeleteMessage)
	mux.HandleFunc("/messages/edit", a.handleEditMessage)
	mux.HandleFunc("/search", a.handleSearch)
	mux.HandleFunc("/block", a.handleBlock)
	mux.HandleFunc("/group/members", a.handleGroupMembers)
	mux.HandleFunc("/session/register", a.handleSessionRegister)
	return withRecovery(withCORS(a.withAuth(mux)))
}

func (a *App) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if !a.isAuthorized(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Cap request bodies at 256 KB. Plenty of headroom for any JSON
		// the TUI sends (the largest real payload, a 10,000-word message
		// in a multi-byte language, is ~180 KB). /messages/send-file is
		// excluded: http.MaxBytesReader wraps the existing r.Body, so a
		// 256 KB cap here would shadow the handler's own 150 MB+ cap and
		// reject any file over 256 KB. Without this cap, a multi-GB body
		// to any other endpoint would be fully buffered into RAM before
		// the handler ran.
		if r.URL.Path != "/messages/send-file" {
			r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) isAuthorized(r *http.Request) bool {
	authz := strings.TrimSpace(r.Header.Get(authHeaderName))
	if !strings.HasPrefix(authz, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" {
		return false
	}
	// apiToken can change at runtime (A-1 rotation), so it's read under
	// the same lock rotateTokenIfSessionDead writes it under.
	a.mu.RLock()
	current := a.apiToken
	a.mu.RUnlock()
	if current == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(current)) == 1
}

// handleSessionRegister records which TUI process (by PID) is the active
// session (A-1). Called once by the TUI right after it confirms the
// backend is up. rotateTokenIfSessionDead uses this to detect when that
// TUI process has exited and rotate the session token.
func (a *App) handleSessionRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		PID int `json:"pid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid pid")
		return
	}
	a.mu.Lock()
	a.sessionPID = req.PID
	a.sessionRegisteredAt = time.Now()
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// rotateTokenIfSessionDead is called from the 30s ticker in main(). If a
// TUI session was registered and the grace period has elapsed without it
// being re-registered, and that PID is no longer running, a fresh token is
// generated, written to disk (S-1), and swapped into a.apiToken — so any
// copy of the old token (e.g. from a leaked log or a crashed process'
// memory) stops working.
func (a *App) rotateTokenIfSessionDead() {
	a.mu.RLock()
	pid := a.sessionPID
	registeredAt := a.sessionRegisteredAt
	a.mu.RUnlock()

	if pid == 0 || time.Since(registeredAt) < sessionGraceDuration || processAlive(pid) {
		return
	}

	newToken, err := generateSessionToken()
	if err != nil {
		log.Printf("[session] rotate token: %v", err)
		return
	}
	if err := os.WriteFile(a.tokenPath, []byte(newToken), 0o600); err != nil {
		log.Printf("[session] rotate token: write %s: %v", a.tokenPath, err)
		return
	}
	// 0600 is a no-op on Windows (files inherit the folder DACL), so
	// restrict the ACL explicitly. A failure is logged, not fatal: an
	// unrestricted fresh token still beats keeping a possibly leaked one.
	if err := tokenlock.RestrictFileToCurrentUser(a.tokenPath); err != nil {
		log.Printf("[session] rotate token: restrict %s: %v", a.tokenPath, err)
	}

	a.mu.Lock()
	a.apiToken = newToken
	a.sessionPID = 0
	a.mu.Unlock()
	log.Printf("[session] TUI session (pid %d) ended; rotated session token", pid)
}

// isAllowedOrigin returns true only for the backend's own URL.
// Pre-fix, this accepted ANY loopback origin (any port, any
// scheme, any hostname that resolved to 127.0.0.0/8 or ::1 or
// matched "localhost"). S-4 closed that to a single fixed value.
// TUI is unaffected because Go HTTP clients don't send an Origin
// header, so this function is only called for browser-initiated
// requests and WebSocket upgrades.
func isAllowedOrigin(origin string) bool {
	return origin == allowedBackendOrigin
}

func (a *App) startSession() error {
	// Serialize concurrent /start calls: a repeat call while a connect
	// is in flight must be a no-op, not a second GetQRChannel/Connect.
	a.startMu.Lock()
	defer a.startMu.Unlock()

	a.mu.RLock()
	alreadyStarted := a.started
	connected := a.connected
	a.mu.RUnlock()
	if alreadyStarted {
		if connected && a.client.IsConnected() && a.client.IsLoggedIn() {
			return nil
		}
		// A connect is already in flight (or failed and will be
		// retried by the next /start). Re-attaching is enough.
		if a.client.IsConnected() {
			return nil
		}
		// Fall through and try again only if nothing is in flight.
		// whatsmeow reports connecting state via IsConnected; if a
		// previous Connect goroutine is still running, don't start
		// a second one.
		a.mu.RLock()
		state := a.connState
		a.mu.RUnlock()
		if state == "connecting" || state == "waiting-qr" {
			return nil
		}
	}

	a.mu.Lock()
	a.started = true
	if a.client.Store.ID == nil {
		a.connState = "waiting-qr"
	} else {
		a.connState = "connecting"
	}
	state := a.connState
	a.mu.Unlock()
	a.broadcast(EventEnvelope{Type: "status", Payload: state})

	if a.client.Store.ID == nil {
		qrChan, err := a.client.GetQRChannel(context.Background())
		if err != nil {
			a.mu.Lock()
			a.started = false
			a.connState = "connect-failed: " + err.Error()
			a.mu.Unlock()
			a.broadcast(EventEnvelope{Type: "status", Payload: "connect-failed: " + err.Error()})
			return err
		}
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					a.mu.Lock()
					a.lastQR = evt.Code
					a.mu.Unlock()
					a.broadcast(EventEnvelope{Type: "qr", Payload: evt.Code})
				} else if evt.Event == "timeout" {
					a.broadcast(EventEnvelope{Type: "status", Payload: "QR timed out, retrying..."})
				}
			}
		}()
	}
	// Connect in a goroutine so /start returns immediately.
	// The TUI learns the session is ready via the WS "ready" event,
	// or the failure via the WS "status" connect-failed event.
	go func() {
		if err := a.client.Connect(); err != nil {
			a.mu.Lock()
			a.started = false
			a.connState = "connect-failed: " + err.Error()
			a.mu.Unlock()
			log.Printf("startSession: connect failed: %v", err)
			a.broadcast(EventEnvelope{Type: "status", Payload: "connect-failed: " + err.Error()})
			return
		}
		a.recanonicalizeState()
		a.mu.RLock()
		bootstrap := a.needsBootstrapSync
		a.mu.RUnlock()
		if bootstrap {
			go a.bootstrapFromStore()
		}
		go a.refreshGroupMetadata()
	}()
	return nil
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connected": connected})
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true
		}
		return isAllowedOrigin(origin)
	},
}

func (a *App) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &wsClient{conn: conn}
	a.wsMu.Lock()
	a.wsClients[conn] = client
	a.wsMu.Unlock()
	a.mu.RLock()
	connected := a.connected
	lastQR := a.lastQR
	connState := a.connState
	a.mu.RUnlock()
	if connected {
		if data, err := json.Marshal(EventEnvelope{Type: "ready"}); err == nil {
			if err := client.write(data); err != nil {
				a.wsMu.Lock()
				delete(a.wsClients, conn)
				a.wsMu.Unlock()
				_ = conn.Close()
				return
			}
		}
		if data, err := json.Marshal(EventEnvelope{Type: "chats:loaded"}); err == nil {
			if err := client.write(data); err != nil {
				a.wsMu.Lock()
				delete(a.wsClients, conn)
				a.wsMu.Unlock()
				_ = conn.Close()
				return
			}
		}
	} else {
		// Late joiner while not connected: replay the current state so
		// the TUI never sits on "Connecting..." with no further events.
		var snapshot EventEnvelope
		switch {
		case lastQR != "":
			snapshot = EventEnvelope{Type: "qr", Payload: lastQR}
		case connState != "":
			snapshot = EventEnvelope{Type: "status", Payload: connState}
		default:
			snapshot = EventEnvelope{Type: "status", Payload: "connecting"}
		}
		if data, err := json.Marshal(snapshot); err == nil {
			if err := client.write(data); err != nil {
				a.wsMu.Lock()
				delete(a.wsClients, conn)
				a.wsMu.Unlock()
				_ = conn.Close()
				return
			}
		}
	}

	// A-12: bound the read loop. Without a read deadline a stalled
	// connection parks the goroutine below (and its wsClients entry)
	// forever. The server pings every wsPingPeriod; a client silent for
	// wsPongWait is dropped. The TUI answers pings automatically
	// (gorilla/websocket default), so healthy idle clients stay connected.
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := client.ping(); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	go func() {
		defer close(done)
		defer func() {
			a.wsMu.Lock()
			delete(a.wsClients, conn)
			a.wsMu.Unlock()
			_ = conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (a *App) broadcast(evt EventEnvelope) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	a.wsMu.Lock()
	clients := make([]*wsClient, 0, len(a.wsClients))
	for _, client := range a.wsClients {
		clients = append(clients, client)
	}
	a.wsMu.Unlock()
	for _, client := range clients {
		go func() {
			if err := client.write(data); err != nil {
				a.wsMu.Lock()
				_, stillPresent := a.wsClients[client.conn]
				if stillPresent {
					delete(a.wsClients, client.conn)
				}
				a.wsMu.Unlock()
				if stillPresent {
					_ = client.conn.Close()
				}
			}
		}()
	}
}

func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := a.startSession(); err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type permissionBackup struct {
	Phone   string
	Name    string
	Allowed int
}

func (a *App) hasRuntimeResources() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil || a.storeContainer != nil
}

func (a *App) backupPermissions(dbPath string) ([]permissionBackup, bool) {
	var backup []permissionBackup
	var defaultAllowed bool
	var tempDB *sql.DB
	a.mu.Lock()
	targetDB := a.db
	a.mu.Unlock()
	if targetDB == nil {
		if _, err := os.Stat(dbPath); err == nil {
			var err error
			tempDB, err = sql.Open("sqlite", "file:"+dbPath)
			if err != nil {
				log.Printf("logout backup open: %v", err)
				tempDB = nil
			} else {
				targetDB = tempDB
			}
		}
	}
	if targetDB != nil {
		rows, err := targetDB.Query(`SELECT phone, name, allowed FROM chat_permissions`)
		if err == nil {
			for rows.Next() {
				var p permissionBackup
				if err := rows.Scan(&p.Phone, &p.Name, &p.Allowed); err == nil {
					backup = append(backup, p)
				}
			}
			rows.Close()
		}
		var def int
		if err := targetDB.QueryRow(`SELECT allowed FROM whitelist_default WHERE id = 1`).Scan(&def); err == nil {
			defaultAllowed = def == 1
		}
		if tempDB != nil {
			_ = tempDB.Close()
		}
	}
	return backup, defaultAllowed
}

func (a *App) disconnectAndLogoutClient() []error {
	if a.client == nil {
		return nil
	}
	var errs []error
	// Remote logout is best-effort. Even if the server is unreachable, we
	// want to tear down local state. Cap the network call at 5s so a hung
	// WhatsApp server can't hold logoutMu forever.
	if a.client.Store != nil && a.client.Store.ID != nil {
		logoutCtx, logoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.client.Logout(logoutCtx); err != nil {
			errs = append(errs, fmt.Errorf("remote logout failed: %w", err))
		}
		logoutCancel()
	}
	a.client.Disconnect()
	if a.client.Store != nil && a.client.Store.ID != nil {
		if err := a.client.Store.Delete(context.Background()); err != nil {
			errs = append(errs, fmt.Errorf("store delete failed: %w", err))
		} else {
			a.client.Store.ID = nil
		}
	}
	return errs
}

func (a *App) resetRuntimeState() []error {
	var errs []error
	a.mu.Lock()
	a.client = nil
	a.started = false
	a.connected = false
	a.needsBootstrapSync = false
	a.state = PersistedState{
		Chats:    map[string]Chat{},
		Contacts: map[string]Contact{},
	}
	// Close all DB connections before deleting files (required on Windows to release file locks).
	if a.storeContainer != nil {
		if err := a.storeContainer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store container close failed: %w", err))
		}
		a.storeContainer = nil
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("store close failed: %w", err))
		}
		a.store = nil
		a.db = nil
	} else if a.db != nil {
		if err := a.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("db close failed: %w", err))
		}
		a.db = nil
	}
	a.mu.Unlock()
	return errs
}

func (a *App) restorePermissions(dbPath string, backup []permissionBackup, defaultAllowed bool) {
	if len(backup) == 0 && !defaultAllowed {
		return
	}
	var restoreDB *sql.DB
	var tempRestoreDB *sql.DB
	a.mu.Lock()
	if a.db != nil {
		restoreDB = a.db
	}
	a.mu.Unlock()
	if restoreDB == nil {
		if err := os.MkdirAll(a.cacheDir, 0o700); err == nil {
			tempRestoreDB, err = sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
			if err == nil {
				restoreDB = tempRestoreDB
			}
		}
	}
	if restoreDB != nil {
		if _, err := restoreDB.Exec(`CREATE TABLE IF NOT EXISTS chat_permissions (
			phone   TEXT PRIMARY KEY,
			name    TEXT NOT NULL DEFAULT '',
			allowed INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
			log.Printf("logout restore schema: %v", err)
		} else if _, err := restoreDB.Exec(`CREATE TABLE IF NOT EXISTS whitelist_default (
			id      INTEGER PRIMARY KEY CHECK (id = 1),
			allowed INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
			log.Printf("logout restore default schema: %v", err)
		} else {
			tx, err := restoreDB.Begin()
			if err == nil {
				def := 0
				if defaultAllowed {
					def = 1
				}
				if _, err := tx.Exec(`INSERT INTO whitelist_default (id, allowed) VALUES (1, ?)
					ON CONFLICT(id) DO UPDATE SET allowed=excluded.allowed`, def); err != nil {
					log.Printf("logout restore default insert: %v", err)
				}
				for _, p := range backup {
					if _, err := tx.Exec(`INSERT OR REPLACE INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)`, p.Phone, p.Name, p.Allowed); err != nil {
						log.Printf("logout restore insert: %v", err)
					}
				}
				if err := tx.Commit(); err != nil {
					log.Printf("logout restore commit: %v", err)
				}
			} else {
				log.Printf("logout restore begin: %v", err)
			}
		}
		if tempRestoreDB != nil {
			_ = tempRestoreDB.Close()
		}
	}
}

func (a *App) teardownAndReinitStorage(hadRuntimeResources bool, dbPath string, backup []permissionBackup, defaultAllowed bool) error {
	if strings.TrimSpace(a.cacheDir) == "" {
		return a.persistStateWithErr()
	}
	if hadRuntimeResources {
		if err := a.resetPersistentStorage(); err != nil {
			return fmt.Errorf("state cleanup failed: %w", err)
		}
	} else {
		if err := os.RemoveAll(a.cacheDir); err != nil {
			return fmt.Errorf("state cleanup failed: %w", err)
		} else if err := os.MkdirAll(a.cacheDir, 0o700); err != nil {
			return fmt.Errorf("state cleanup failed: %w", err)
		}
	}
	a.restorePermissions(dbPath, backup, defaultAllowed)
	if err := a.persistStateWithErr(); err != nil {
		return fmt.Errorf("state cleanup failed: %w", err)
	}
	return nil
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// A-5: serialize the whole teardown against any other concurrent
	// /logout so two callers can't race through os.RemoveAll + DB reopen.
	// Held for the full body, not just the in-memory state mutation —
	// the data-folder teardown is what corrupts under concurrency, and
	// it's the part that needs the lock.
	a.logoutMu.Lock()
	defer a.logoutMu.Unlock()

	// shuttingDown is also a "logout in flight" signal that other code
	// paths (persistState, persistStateWithErr) already check. Cheap to
	// set under the same lock.
	if a.shuttingDown {
		writeErr(w, http.StatusConflict, "logout already in progress")
		return
	}
	a.shuttingDown = true
	defer func() {
		a.shuttingDown = false
	}()

	dbPath := filepath.Join(a.cacheDir, "store.db")
	backup, defaultAllowed := a.backupPermissions(dbPath)
	hadRuntimeResources := a.hasRuntimeResources()

	var errs []error
	errs = append(errs, a.disconnectAndLogoutClient()...)
	errs = append(errs, a.resetRuntimeState()...)

	if err := a.teardownAndReinitStorage(hadRuntimeResources, dbPath, backup, defaultAllowed); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		writeInternalErr(w, errors.Join(errs...))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Logged out successfully"})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		w.Header().Add("Vary", "Origin")
		if isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "authorization,content-type")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && !isAllowedOrigin(origin) {
				writeErr(w, http.StatusForbidden, "origin not allowed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// nextInternalErrID returns a short opaque ID used to correlate a client-facing
// "internal error" response with the real error in the server log, without
// exposing the real error text (which may contain SQL/file/library internals).
var internalErrCounter atomic.Uint64

func nextInternalErrID() string {
	return fmt.Sprintf("err-%d", internalErrCounter.Add(1))
}

// writeInternalErr logs err server-side (redacted by the writer set up in
// main()) under an opaque ID, then returns that ID to the client instead of
// the raw error text.
func writeInternalErr(w http.ResponseWriter, err error) {
	id := nextInternalErrID()
	log.Printf("[%s] %v", id, err)
	writeErr(w, http.StatusInternalServerError, fmt.Sprintf("internal error (ref: %s)", id))
}

func sanitizeOutgoingText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		if !unicode.IsPrint(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasVisibleText(s string) bool {
	return strings.TrimSpace(sanitizeOutgoingText(s)) != ""
}
