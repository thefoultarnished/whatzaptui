package backend

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"whatzap/internal/store"
	"whatzap/internal/whatsapp"
)

type EventEnvelope struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

type WireKey struct {
	ID          string `json:"id"`
	RemoteJID   string `json:"remoteJid"`
	FromMe      bool   `json:"fromMe"`
	Participant string `json:"participant,omitempty"`
}

type WireMessage struct {
	Key              WireKey        `json:"key"`
	Message          map[string]any `json:"message"`
	MessageTimestamp int64          `json:"messageTimestamp"`
	PushName         string         `json:"pushName,omitempty"`
	MediaProto       string         `json:"mediaProto,omitempty"` // base64 proto bytes for media messages
	ReceiptStatus    string         `json:"receiptStatus,omitempty"`
}

type WireReceiptUpdate struct {
	ChatID        string   `json:"chatId"`
	MessageIDs    []string `json:"messageIds"`
	ReceiptStatus string   `json:"receiptStatus"`
}

type WireCallEvent struct {
	Status   string `json:"status"`
	CallerID string `json:"callerId,omitempty"`
	GroupID  string `json:"groupId,omitempty"`
	CallID   string `json:"callId,omitempty"`
	Media    string `json:"media,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type Chat struct {
	ID                    string `json:"id"`
	Name                  string `json:"name,omitempty"`
	Subject               string `json:"subject,omitempty"`
	ConversationTimestamp int64  `json:"conversationTimestamp"`
	UnreadCount           int    `json:"unreadCount"`
}

type Contact struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Notify string `json:"notify,omitempty"`
	// Stored is true when the name came from the phone's address book
	// (FullName/FirstName/BusinessName), false for push-name-only
	// contacts observed in messages/groups.
	Stored bool `json:"stored,omitempty"`
}

type PersistedState struct {
	Chats    map[string]Chat    `json:"chats"`
	Contacts map[string]Contact `json:"contacts"`
}

// wsSendQueueSize bounds each client's outbound event queue. broadcast()
// enqueues into this instead of spawning a goroutine per client per event
// (the old behavior, which had no cap on concurrent goroutines when many
// clients were connected or events arrived in a burst). A single writer
// goroutine per client (started in handleWS) drains the queue in order.
const wsSendQueueSize = 64

type wsClient struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	sendCh   chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

func newWSClient(conn *websocket.Conn) *wsClient {
	return &wsClient{
		conn:   conn,
		sendCh: make(chan []byte, wsSendQueueSize),
		done:   make(chan struct{}),
	}
}

func (c *wsClient) write(data []byte) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("websocket client unavailable")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// ping sends a WebSocket ping under the client's write lock so it can run
// concurrently with broadcast writes. gorilla/websocket answers pings
// automatically on the peer side.
func (c *wsClient) ping() error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("websocket client unavailable")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return c.conn.WriteMessage(websocket.PingMessage, nil)
}

// enqueue queues data for delivery by this client's writer goroutine. It
// never blocks the caller: if the client's outbound queue is full (a slow
// or stuck peer) or the client has already been torn down, the event is
// dropped for that one client instead of blocking broadcast or spawning a
// goroutine. Returns false when the event was dropped.
func (c *wsClient) enqueue(data []byte) bool {
	// Checked as its own select first: if both this and the send below were
	// combined in one select, a closed done alongside room in sendCh would
	// make Go's random case selection sometimes queue the message anyway.
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.sendCh <- data:
		return true
	default:
		return false
	}
}

// runWriter drains sendCh and writes each queued message in order, on one
// dedicated goroutine per client for the client's whole lifetime (started
// once in handleWS) rather than one goroutine per event. onWriteErr is
// called exactly once if a write fails, so the caller can tear the client
// down; runWriter then stops.
func (c *wsClient) runWriter(onWriteErr func()) {
	for {
		select {
		case data := <-c.sendCh:
			if err := c.write(data); err != nil {
				onWriteErr()
				return
			}
		case <-c.done:
			return
		}
	}
}

// stop signals runWriter (and anything else selecting on done) to exit.
// Safe to call more than once or concurrently.
func (c *wsClient) stop() {
	c.stopOnce.Do(func() { close(c.done) })
}

type App struct {
	mu sync.RWMutex

	client         *whatsapp.Client
	db             *sql.DB
	store          *store.Store
	storeContainer *sqlstore.Container
	started        bool
	connected      bool

	cacheDir string
	state    PersistedState

	apiToken string

	// tokenPath is where the session token (S-1) lives on disk. Used by
	// rotateTokenIfSessionDead (A-1) to write a fresh token when the
	// registered TUI session goes away.
	tokenPath string

	// sessionPID/sessionRegisteredAt track the TUI process that called
	// POST /session/register (A-1). Guarded by mu, like apiToken.
	sessionPID          int
	sessionRegisteredAt time.Time

	wsMu      sync.Mutex
	wsClients map[*websocket.Conn]*wsClient

	needsBootstrapSync bool
	historySyncing     bool
	shuttingDown       bool
	dbLifecycleMu      sync.RWMutex
	persistDirty       uint32 // atomic: 1 = needs persist
	stopPersist        chan struct{}
	stopPersistOnce    sync.Once
	lidCacheMu         sync.RWMutex
	lidCache           map[string]string
	lidMigrateOnce     sync.Once
	lidMigrateJobs     chan lidMigrateJob

	// contactReseedMu/contactReseedTimer debounce handleAppStateSyncComplete
	// (see state.go) so a burst of app-state patches triggers one contact
	// reseed, not one per patch. onContactReseed lets tests observe a
	// scheduled reseed without a live whatsmeow client; nil in production.
	contactReseedMu    sync.Mutex
	contactReseedTimer *time.Timer
	onContactReseed    func()

	// logoutMu serializes the /logout handler against itself so two
	// concurrent calls can't race through the data-folder teardown.
	// Kept separate from mu so a logout's network call to WhatsApp
	// doesn't stall every other request that needs a read of a.state.
	logoutMu sync.Mutex

	// onShutdown is set by main Run() to the func that drives graceful
	// shutdown (stops the signal context so the srv.Shutdown path runs).
	// POST /shutdown invokes it asynchronously. Nil in tests unless set.
	onShutdown func()

	// startMu serializes /start so two concurrent calls can't race
	// GetQRChannel against Connect (whatsmeow rejects GetQRChannel
	// once connecting). Repeat calls while connecting just re-attach.
	startMu sync.Mutex

	// connState is the last known WhatsApp connection state for WS
	// late-joiners ("connecting", "waiting-qr", "ready",
	// "connect-failed: ...", "disconnected", "logged-out").
	// lastQR keeps the most recent QR code so a TUI that opens its
	// WebSocket after the qr event still gets it. Guarded by mu.
	connState string
	lastQR    string

	// actionLog is the per-session structured event log. One file per
	// backend startup, written next to the DB (inside <data-root>/backend/
	// logs/, owner-only). Used for triage: durations, counts, error class,
	// redacted identifiers — never bodies or tokens. Nil before NewApp
	// completes and after Close; helpers are safe on a nil receiver.
	actionLog *actionLog
}

type lidMigrateJob struct {
	lidUser string
	pnUser  string
}

type dbExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

type searchHit struct {
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	FromMe    bool   `json:"fromMe"`
	Timestamp int64  `json:"timestamp"`
	Snippet   string `json:"snippet"`
}
