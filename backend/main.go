package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/gorilla/websocket"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	_ "modernc.org/sqlite"
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
}

type PersistedState struct {
	Chats    map[string]Chat    `json:"chats"`
	Contacts map[string]Contact `json:"contacts"`
}

type wsClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
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

type App struct {
	mu sync.RWMutex

	client         *whatsmeow.Client
	db             *sql.DB
	storeContainer *sqlstore.Container
	started        bool
	connected      bool

	cacheDir string
	state    PersistedState

	apiToken string

	wsMu      sync.Mutex
	wsClients map[*websocket.Conn]*wsClient

	needsBootstrapSync bool
	historySyncing     bool
	shuttingDown       bool
	persistDirty       uint32 // atomic: 1 = needs persist
	lidCacheMu sync.RWMutex
	lidCache    map[string]string
}

const maxUploadBytes = 100 * 1024 * 1024
const maxMessagesResponseLimit = 200
const appDataDirName = "WhatZAP"
const apiTokenEnvVar = "WHATZAP_API_TOKEN"
const authHeaderName = "Authorization"

func main() {
	app, err := NewApp()
	if err != nil {
		log.Fatalf("init failed: %v", err)
	}

	addr := "127.0.0.1:8787"
	srv := &http.Server{Addr: addr, Handler: app.handler()}

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

func resolveBackendCacheDir(workDir string) (string, error) {
	dataRoot, err := whatzapDataRoot()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dataRoot, "backend")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
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

func NewApp() (*App, error) {
	apiToken := strings.TrimSpace(os.Getenv(apiTokenEnvVar))
	if apiToken == "" {
		return nil, fmt.Errorf("%s must be set", apiTokenEnvVar)
	}
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

func (a *App) initPersistentResources() error {
	cacheDir := strings.TrimSpace(a.cacheDir)
	if cacheDir == "" {
		return fmt.Errorf("cache directory is not configured")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	a.cacheDir = cacheDir

	dbPath := filepath.Join(cacheDir, "store.db")

	rawDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	if _, err := rawDB.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		_ = rawDB.Close()
		return err
	}

	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", waLog.Stdout("db", "WARN", true))
	if err != nil {
		_ = rawDB.Close()
		return err
	}
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`CREATE TABLE IF NOT EXISTS chat_permissions (
		phone   TEXT PRIMARY KEY,
		name    TEXT NOT NULL DEFAULT '',
		allowed INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id           TEXT NOT NULL,
			chat_id      TEXT NOT NULL,
			from_me      INTEGER NOT NULL DEFAULT 0,
			participant  TEXT NOT NULL DEFAULT '',
			ts           INTEGER NOT NULL DEFAULT 0,
			push_name    TEXT NOT NULL DEFAULT '',
			receipt      TEXT NOT NULL DEFAULT '',
			message_json TEXT NOT NULL DEFAULT '{}',
			media_proto  TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (chat_id, id, from_me)
		);
		CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages (chat_id, ts);
	`); err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			id           TEXT PRIMARY KEY,
			name         TEXT NOT NULL DEFAULT '',
			subject      TEXT NOT NULL DEFAULT '',
			conv_ts      INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS contacts (
			id     TEXT PRIMARY KEY,
			name   TEXT NOT NULL DEFAULT '',
			notify TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			chat_id UNINDEXED,
			msg_id  UNINDEXED,
			from_me UNINDEXED,
			body,
			tokenize = 'porter unicode61'
		);
	`); err != nil {
		_ = rawDB.Close()
		return err
	}

	a.db = rawDB
	a.storeContainer = container
	a.backfillFTS()
	a.client = whatsmeow.NewClient(device, waLog.Stdout("client", "DEBUG", true))
	a.bindEvents()
	return nil
}

func (a *App) resetPersistentStorage() error {
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			return err
		}
		a.db = nil
	}

	if strings.TrimSpace(a.cacheDir) != "" {
		if err := os.RemoveAll(a.cacheDir); err != nil {
			return err
		}
		return a.initPersistentResources()
	}
	return nil
}

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
	mux.HandleFunc("/names/set", a.handleSetName)
	mux.HandleFunc("/media/download", a.handleMediaDownload)
	mux.HandleFunc("/typing", a.handleTyping)
	mux.HandleFunc("/messages/react", a.handleReact)
	mux.HandleFunc("/messages/delete", a.handleDeleteMessage)
	mux.HandleFunc("/search", a.handleSearch)
	mux.HandleFunc("/block", a.handleBlock)
	mux.HandleFunc("/group/members", a.handleGroupMembers)
	return withCORS(a.withAuth(mux))
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
		next.ServeHTTP(w, r)
	})
}

func (a *App) isAuthorized(r *http.Request) bool {
	authz := strings.TrimSpace(r.Header.Get(authHeaderName))
	if !strings.HasPrefix(authz, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" || a.apiToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.apiToken)) == 1
}

func isAllowedOrigin(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) bindEvents() {
	a.client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Connected:
			a.mu.Lock()
			a.connected = true
			a.mu.Unlock()
			go func() {
				presCtx, presCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer presCancel()
				_ = a.client.SendPresence(presCtx, types.PresenceAvailable)
			}()
			a.broadcast(EventEnvelope{Type: "ready"})
			a.broadcast(EventEnvelope{Type: "chats:loaded"})
		case *events.Disconnected:
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			a.broadcast(EventEnvelope{Type: "disconnected", Payload: "connection closed"})
		case *events.LoggedOut:
			a.mu.Lock()
			a.connected = false
			a.started = false
			a.mu.Unlock()
			a.broadcast(EventEnvelope{Type: "status", Payload: "Logged out. Start again to scan QR."})
			if a.client != nil && a.client.Store != nil {
				_ = a.client.Store.Delete(context.Background())
				a.client.Store.ID = nil
			}
		case *events.PushName:
			jid := a.canonicalizeChatID(v.JID.String())
			a.mu.Lock()
			a.state.Contacts[jid] = Contact{ID: jid, Notify: v.NewPushName}
			a.mu.Unlock()
			a.persistState()
			a.broadcast(EventEnvelope{Type: "contacts:updated"})
		case *events.Message:
			if v == nil || v.Message == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.Info.Chat.String())
			if chatID == "" || chatID == "status@broadcast" {
				return
			}
			msg := a.toWireMessage(v)
			msg.Key.RemoteJID = chatID
			if msg.Key.Participant != "" {
				msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
			}
			a.upsertMessage(chatID, msg)
			a.broadcast(EventEnvelope{Type: "message", Payload: msg})
			a.broadcast(EventEnvelope{Type: "chats:loaded"})
		case *events.Receipt:
			if v == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.Chat.String())
			status := receiptStatusFromType(v.Type)
			if chatID == "" || status == "" || len(v.MessageIDs) == 0 {
				return
			}
			ids := make([]string, 0, len(v.MessageIDs))
			for _, id := range v.MessageIDs {
				if id != "" {
					ids = append(ids, string(id))
				}
			}
			if len(ids) == 0 {
				return
			}
			if a.updateReceiptStatus(chatID, ids, status) {
				a.broadcast(EventEnvelope{Type: "receipt", Payload: WireReceiptUpdate{
					ChatID:        chatID,
					MessageIDs:    ids,
					ReceiptStatus: status,
				}})
			}
			if v.Type == types.ReceiptTypeReadSelf {
				a.mu.Lock()
				chat := a.state.Chats[chatID]
				chat.ID = chatID
				chat.UnreadCount = 0
				a.state.Chats[chatID] = chat
				a.mu.Unlock()
				a.persistState()
				a.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		case *events.MarkChatAsRead:
			if v == nil {
				return
			}
			chatID := a.canonicalizeChatID(v.JID.String())
			if chatID == "" {
				return
			}
			if v.Action != nil && v.Action.GetRead() {
				a.mu.Lock()
				chat := a.state.Chats[chatID]
				chat.ID = chatID
				chat.UnreadCount = 0
				a.state.Chats[chatID] = chat
				a.mu.Unlock()
				a.persistState()
				a.broadcast(EventEnvelope{Type: "chats:loaded"})
			}
		case *events.HistorySync:
			a.applyHistorySync(v.Data)
		case *events.CallOffer:
			a.broadcast(EventEnvelope{Type: "call", Payload: a.toWireCallEvent("incoming", v.BasicCallMeta, "")})
		case *events.CallOfferNotice:
			a.broadcast(EventEnvelope{Type: "call", Payload: a.toWireCallEvent("incoming", v.BasicCallMeta, v.Media)})
		case *events.CallTerminate:
			ev := a.toWireCallEvent("ended", v.BasicCallMeta, "")
			ev.Reason = strings.TrimSpace(v.Reason)
			a.broadcast(EventEnvelope{Type: "call", Payload: ev})
		case *events.ChatPresence:
			chatID := a.canonicalizeChatID(v.Chat.String())
			senderID := a.canonicalizeChatID(v.Sender.String())
			if chatID != "" {
				a.broadcast(EventEnvelope{Type: "typing", Payload: map[string]string{
					"chatId": chatID,
					"sender": senderID,
					"state":  string(v.State),
				}})
			}
		}
	})
}

func (a *App) toWireCallEvent(status string, meta types.BasicCallMeta, media string) WireCallEvent {
	callerID := a.canonicalizeChatID(meta.CallCreator.String())
	if callerID == "" {
		callerID = a.canonicalizeChatID(meta.CallCreatorAlt.String())
	}
	if callerID == "" {
		callerID = a.canonicalizeChatID(meta.From.String())
	}
	groupID := a.canonicalizeChatID(meta.GroupJID.String())
	return WireCallEvent{
		Status:   status,
		CallerID: callerID,
		GroupID:  groupID,
		CallID:   meta.CallID,
		Media:    strings.TrimSpace(media),
	}
}

func (a *App) applyHistorySync(data *waHistorySync.HistorySync) {
	if data == nil {
		return
	}

	a.mu.Lock()
	a.historySyncing = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.historySyncing = false
		a.mu.Unlock()
	}()

	changedChats := false

	// Phase 1: pre-compute all updates without holding a.mu.
	// canonicalizeChatID uses a.lidCacheMu (its own lock), not a.mu, so this is safe.
	type pushnameUpdate struct {
		id   string
		name string
	}
	var pushnameUpdates []pushnameUpdate
	for _, p := range data.GetPushnames() {
		id := a.canonicalizeChatID(strings.TrimSpace(p.GetID()))
		if id == "" {
			continue
		}
		if name := strings.TrimSpace(p.GetPushname()); name != "" {
			pushnameUpdates = append(pushnameUpdates, pushnameUpdate{id, name})
		}
	}

	type convMetadata struct {
		chatID string
		name   string
		ts     int64
		uc     int
	}
	var convMeta []convMetadata
	for _, conv := range data.GetConversations() {
		chatID := a.historyConversationChatID(conv)
		if chatID == "" || chatID == "status@broadcast" {
			continue
		}
		name := strings.TrimSpace(conv.GetDisplayName())
		if name == "" {
			name = strings.TrimSpace(conv.GetName())
		}
		ts := int64(conv.GetConversationTimestamp())
		if ts == 0 {
			ts = int64(conv.GetLastMsgTimestamp())
		}
		convMeta = append(convMeta, convMetadata{chatID, name, ts, int(conv.GetUnreadCount())})
		changedChats = true
	}

	// Phase 2: apply pre-computed updates under one short lock.
	a.mu.Lock()
	for _, u := range pushnameUpdates {
		contact := a.state.Contacts[u.id]
		contact.ID = u.id
		contact.Notify = u.name
		a.state.Contacts[u.id] = contact
		chat := a.state.Chats[u.id]
		chat.ID = u.id
		if chat.Name == "" {
			chat.Name = u.name
		}
		a.state.Chats[u.id] = chat
	}
	for _, c := range convMeta {
		chat := a.state.Chats[c.chatID]
		chat.ID = c.chatID
		if c.name != "" {
			chat.Name = c.name
		}
		if chat.Name == "" {
			if ct, ok := a.state.Contacts[c.chatID]; ok {
				if n := strings.TrimSpace(ct.Notify); n != "" {
					chat.Name = n
				} else if n := strings.TrimSpace(ct.Name); n != "" {
					chat.Name = n
				}
			}
		}
		if c.ts > chat.ConversationTimestamp {
			chat.ConversationTimestamp = c.ts
		}
		if c.uc > chat.UnreadCount {
			chat.UnreadCount = c.uc
		}
		a.state.Chats[c.chatID] = chat
	}
	a.mu.Unlock()

	var tx *sql.Tx
	var err error
	if a.db != nil {
		tx, err = a.db.Begin()
		if err != nil {
			log.Printf("applyHistorySync: begin tx: %v", err)
		}
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var exec dbExecutor = a.db
	if tx != nil {
		exec = tx
	}

	for _, conv := range data.GetConversations() {
		chatID := a.historyConversationChatID(conv)
		if chatID == "" || chatID == "status@broadcast" {
			continue
		}
		for _, item := range conv.GetMessages() {
			wm := item.GetMessage()
			if wm == nil {
				continue
			}
			msg := a.toWireMessageFromHistory(wm)
			if msg.Key.RemoteJID == "" {
				msg.Key.RemoteJID = chatID
			}
			msg.Key.RemoteJID = a.canonicalizeChatID(msg.Key.RemoteJID)
			if msg.Key.Participant != "" {
				msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
			}
			if msg.Key.RemoteJID == "status@broadcast" {
				continue
			}
			// History sync doesn't carry a live receipt state for previously-sent
			// messages. Any FromMe row that survived into history is, by
			// definition, at minimum delivered (you can't have a history row for
			// a message WhatsApp never accepted). Default to "delivered" so the
			// TUI renders ✓✓. Live send paths set "sent" explicitly and receipt
			// events upgrade from there.
			if msg.Key.FromMe && msg.ReceiptStatus == "" {
				msg.ReceiptStatus = "delivered"
			}
			a.upsertMessageTx(exec, msg.Key.RemoteJID, msg)
			changedChats = true
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			log.Printf("applyHistorySync: commit tx: %v", err)
		} else {
			tx = nil
		}
	}

	a.reconcileLIDChats()

	if changedChats {
		a.persistState()
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
		a.broadcast(EventEnvelope{Type: "contacts:updated"})
	}
}

// reconcileLIDChats merges any in-memory chat/contact entries stored under a
// raw @lid JID into their resolved phone-number JID equivalents. It runs after
// history sync when the LID cache is warmest. DB rows are not re-keyed — only
// the in-memory sidebar state is fixed.
func (a *App) reconcileLIDChats() {
	if a.client == nil || a.client.Store == nil || a.client.Store.LIDs == nil {
		return
	}

	a.mu.RLock()
	var lidIDs []string
	for id := range a.state.Chats {
		if strings.HasSuffix(id, "@lid") {
			lidIDs = append(lidIDs, id)
		}
	}
	a.mu.RUnlock()

	for _, lidID := range lidIDs {
		resolved := a.canonicalizeChatID(lidID)
		if resolved == "" || resolved == lidID {
			continue
		}
		a.mu.Lock()
		lidChat, ok := a.state.Chats[lidID]
		if !ok {
			a.mu.Unlock()
			continue
		}
		phoneChat := a.state.Chats[resolved]
		merged := mergeChat(phoneChat, lidChat)
		merged.ID = resolved
		a.state.Chats[resolved] = merged
		delete(a.state.Chats, lidID)
		if lidContact, ok := a.state.Contacts[lidID]; ok {
			phoneContact := a.state.Contacts[resolved]
			mergedContact := mergeContact(phoneContact, lidContact)
			mergedContact.ID = resolved
			a.state.Contacts[resolved] = mergedContact
			delete(a.state.Contacts, lidID)
		}
		a.mu.Unlock()
		log.Printf("reconcileLIDChats: merged %s → %s", lidID, resolved)
	}
}

// extractSearchableText pulls user-visible text from a WireMessage payload
// for full-text indexing. Returns empty string for messages with no text content
// (stickers, reactions, audio without caption, etc).
func extractSearchableText(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	if s, ok := msg["conversation"].(string); ok {
		add(s)
	}
	if ext, ok := msg["extendedTextMessage"].(map[string]any); ok {
		if s, ok := ext["text"].(string); ok {
			add(s)
		}
		if s, ok := ext["quotedText"].(string); ok {
			add(s)
		}
	}
	for _, kind := range []string{"imageMessage", "videoMessage", "documentMessage"} {
		media, ok := msg[kind].(map[string]any)
		if !ok {
			continue
		}
		if s, ok := media["caption"].(string); ok {
			add(s)
		}
		if kind == "documentMessage" {
			if s, ok := media["fileName"].(string); ok {
				add(s)
			}
		}
	}
	return b.String()
}

type dbExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func (a *App) upsertMessageFTS(chatID, msgID string, fromMe int, body string) {
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return
	}
	a.upsertMessageFTSTx(db, chatID, msgID, fromMe, body)
}

func (a *App) upsertMessageFTSTx(exec dbExecutor, chatID, msgID string, fromMe int, body string) {
	if exec == nil {
		return
	}
	if _, err := exec.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, chatID, msgID, fromMe); err != nil {
		log.Printf("upsertMessageFTS delete: %v", err)
		return
	}
	if body == "" {
		return
	}
	if _, err := exec.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`, chatID, msgID, fromMe, body); err != nil {
		log.Printf("upsertMessageFTS insert: %v", err)
	}
}

func (a *App) insertMessageToDB(chatID string, msg WireMessage) error {
	_, err := a.insertMessageToDBTx(a.db, chatID, msg)
	return err
}

// insertMessageToDBTx writes msg to the DB and returns (isNew, error).
// isNew is true only when the row did not previously exist (INSERT OR IGNORE affected a row).
func (a *App) insertMessageToDBTx(exec dbExecutor, chatID string, msg WireMessage) (bool, error) {
	if exec == nil {
		return false, fmt.Errorf("no db executor")
	}
	id := msg.Key.ID
	if id == "" {
		id = dedupeKey(msg)
	}
	fromMe := 0
	if msg.Key.FromMe {
		fromMe = 1
	}
	msgJSON, err := json.Marshal(msg.Message)
	if err != nil {
		return false, err
	}
	res, err := exec.Exec(`
		INSERT OR IGNORE INTO messages (id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, chatID, fromMe, msg.Key.Participant, msg.MessageTimestamp, msg.PushName, msg.ReceiptStatus, string(msgJSON), msg.MediaProto)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	isNew := n > 0
	if _, err := exec.Exec(`
		UPDATE messages SET push_name = ?, message_json = ?, media_proto = ?
		WHERE id = ? AND chat_id = ? AND from_me = ?
	`, msg.PushName, string(msgJSON), msg.MediaProto, id, chatID, fromMe); err != nil {
		return false, err
	}
	a.upsertMessageFTSTx(exec, chatID, id, fromMe, extractSearchableText(msg.Message))
	return isNew, nil
}

func scanMessageRow(rows *sql.Rows) (WireMessage, error) {
	var (
		id, chatID, participant, pushName, receipt, messageJSON, mediaProto string
		fromMe                                                               int
		ts                                                                   int64
	)
	if err := rows.Scan(&id, &chatID, &fromMe, &participant, &ts, &pushName, &receipt, &messageJSON, &mediaProto); err != nil {
		return WireMessage{}, err
	}
	var message map[string]any
	_ = json.Unmarshal([]byte(messageJSON), &message)
	return WireMessage{
		Key:              WireKey{ID: id, RemoteJID: chatID, FromMe: fromMe == 1, Participant: participant},
		MessageTimestamp: ts,
		PushName:         pushName,
		ReceiptStatus:    receipt,
		Message:          message,
		MediaProto:       mediaProto,
	}, nil
}

func (a *App) upsertChatToDB(chat Chat) error {
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO chats (id, name, subject, conv_ts, unread_count)
		VALUES (?, ?, ?, ?, ?)
	`, chat.ID, chat.Name, chat.Subject, chat.ConversationTimestamp, chat.UnreadCount)
	return err
}

func (a *App) upsertContactToDB(contact Contact) error {
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO contacts (id, name, notify)
		VALUES (?, ?, ?)
	`, contact.ID, contact.Name, contact.Notify)
	return err
}

func (a *App) loadChatsFromDB() (map[string]Chat, error) {
	rows, err := a.db.Query(`SELECT id, name, subject, conv_ts, unread_count FROM chats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := map[string]Chat{}
	for rows.Next() {
		var c Chat
		if err := rows.Scan(&c.ID, &c.Name, &c.Subject, &c.ConversationTimestamp, &c.UnreadCount); err != nil {
			return nil, err
		}
		chats[c.ID] = c
	}
	return chats, rows.Err()
}

func (a *App) loadContactsFromDB() (map[string]Contact, error) {
	rows, err := a.db.Query(`SELECT id, name, notify FROM contacts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := map[string]Contact{}
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Notify); err != nil {
			return nil, err
		}
		contacts[c.ID] = c
	}
	return contacts, rows.Err()
}

func (a *App) upsertMessage(chatID string, msg WireMessage) {
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return
	}
	tx, err := db.Begin()
	if err != nil {
		log.Printf("upsertMessage: begin tx: %v", err)
		a.upsertMessageTx(db, chatID, msg)
		return
	}
	a.upsertMessageTx(tx, chatID, msg)
	if err := tx.Commit(); err != nil {
		log.Printf("upsertMessage: commit: %v", err)
		_ = tx.Rollback()
	}
}

func (a *App) upsertMessageTx(exec dbExecutor, chatID string, msg WireMessage) {
	if exec == nil {
		return
	}
	chatID = a.canonicalizeChatID(chatID)
	if chatID == "" {
		return
	}
	msg.Key.RemoteJID = chatID
	if msg.Key.Participant != "" {
		msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
	}

	isNew, err := a.insertMessageToDBTx(exec, chatID, msg)
	if err != nil {
		log.Printf("upsertMessage: db write: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	chat := a.state.Chats[chatID]
	chat.ID = chatID
	if msg.MessageTimestamp > chat.ConversationTimestamp {
		chat.ConversationTimestamp = msg.MessageTimestamp
	}
	if isNew && !msg.Key.FromMe && !a.historySyncing {
		chat.UnreadCount++
	}
	if chat.Name == "" && msg.PushName != "" && !msg.Key.FromMe && !strings.HasSuffix(chatID, "@g.us") {
		chat.Name = msg.PushName
	}
	a.state.Chats[chatID] = chat

	if msg.PushName != "" && !msg.Key.FromMe && !strings.HasSuffix(chatID, "@g.us") {
		a.state.Contacts[chatID] = Contact{ID: chatID, Notify: msg.PushName}
	}
	if !a.historySyncing {
		atomic.StoreUint32(&a.persistDirty, 1)
	}
	permName := msg.PushName
	if msg.Key.FromMe {
		permName = ""
	}
	go a.upsertPermission(phoneFromJID(chatID), permName)
}

func receiptStatusFromType(t types.ReceiptType) string {
	switch t {
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	case types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return "played"
	default:
		return ""
	}
}

func receiptStatusRank(status string) int {
	switch status {
	case "sent":
		return 1
	case "delivered":
		return 2
	case "read":
		return 3
	case "played":
		return 4
	default:
		return 0
	}
}

func (a *App) updateReceiptStatus(chatID string, ids []string, status string) bool {
	if receiptStatusRank(status) == 0 {
		return false
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return false
	}

	uniqueIDs := make([]string, 0, len(idSet))
	for id := range idSet {
		uniqueIDs = append(uniqueIDs, id)
	}
	placeholders := strings.Repeat("?,", len(uniqueIDs))
	placeholders = placeholders[:len(placeholders)-1]

	// Only upgrade receipt status, never downgrade.
	rankExpr := `CASE receipt WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 WHEN 'played' THEN 4 ELSE 0 END`
	args := []any{status, chatID}
	for _, id := range uniqueIDs {
		args = append(args, id)
	}
	args = append(args, receiptStatusRank(status))

	result, err := a.db.Exec(fmt.Sprintf(`
		UPDATE messages SET receipt = ?
		WHERE chat_id = ? AND from_me = 1 AND id IN (%s)
		AND %s < ?
	`, placeholders, rankExpr), args...)
	if err != nil {
		log.Printf("updateReceiptStatus: %v", err)
		return false
	}
	n, _ := result.RowsAffected()
	return n > 0
}

func (a *App) startSession() error {
	a.mu.Lock()
	alreadyStarted := a.started
	if !a.started {
		a.started = true
	}
	a.mu.Unlock()

	if a.client.Store.ID == nil {
		qrChan, err := a.client.GetQRChannel(context.Background())
		if err != nil {
			a.mu.Lock()
			a.started = false
			a.mu.Unlock()
			return err
		}
		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					a.broadcast(EventEnvelope{Type: "qr", Payload: evt.Code})
				} else if evt.Event == "timeout" {
					a.broadcast(EventEnvelope{Type: "status", Payload: "QR timed out, retrying..."})
				}
			}
		}()
	}
	if alreadyStarted {
		a.mu.RLock()
		connected := a.connected
		a.mu.RUnlock()
		if connected && a.client.IsConnected() && a.client.IsLoggedIn() {
			return nil
		}
	}
	// Connect in a goroutine so /start returns immediately.
	// The TUI learns the session is ready via the WS "ready" event.
	go func() {
		if err := a.client.Connect(); err != nil {
			a.mu.Lock()
			a.started = false
			a.mu.Unlock()
			log.Printf("startSession: connect failed: %v", err)
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

func (a *App) recanonicalizeState() {
	// Copy state under lock, then do slow LID lookups outside the lock so
	// that the Connected event handler is never blocked on a.mu.
	a.mu.RLock()
	stateCopy := PersistedState{
		Chats:    make(map[string]Chat, len(a.state.Chats)),
		Contacts: make(map[string]Contact, len(a.state.Contacts)),
	}
	for k, v := range a.state.Chats {
		stateCopy.Chats[k] = v
	}
	for k, v := range a.state.Contacts {
		stateCopy.Contacts[k] = v
	}
	a.mu.RUnlock()

	a.migrateStateCanonicalIDs(&stateCopy)

	a.mu.Lock()
	a.state = stateCopy
	a.mu.Unlock()

	a.persistState()
	a.broadcast(EventEnvelope{Type: "contacts:updated"})
	a.broadcast(EventEnvelope{Type: "chats:loaded"})
}

func (a *App) refreshGroupMetadata() int {
	if a == nil || a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		return 0
	}
	a.mu.RLock()
	db := a.db
	shuttingDown := a.shuttingDown
	a.mu.RUnlock()
	if db == nil || shuttingDown {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	groups, err := a.client.GetJoinedGroups(ctx)
	if err != nil {
		return 0
	}

	changed := 0
	a.mu.Lock()
	if a.shuttingDown || a.db == nil {
		a.mu.Unlock()
		return 0
	}
	for _, g := range groups {
		if g == nil || g.JID.IsEmpty() {
			continue
		}
		cid := a.canonicalizeChatID(g.JID.String())
		if cid == "" {
			continue
		}
		ch := a.state.Chats[cid]
		ch.ID = cid
		name := strings.TrimSpace(g.Name)
		if name != "" && ch.Name != name {
			ch.Name = name
			changed++
		}
		if topic := strings.TrimSpace(g.Topic); topic != "" && ch.Subject != topic {
			ch.Subject = topic
			changed++
		}
		if ch.ConversationTimestamp == 0 {
			var maxTS int64
			_ = a.db.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM messages WHERE chat_id = ? AND ts > 0`, cid).Scan(&maxTS)
			ch.ConversationTimestamp = maxTS
		}
		a.state.Chats[cid] = ch
		delete(a.state.Contacts, cid)
	}
	a.mu.Unlock()

	if changed > 0 {
		a.persistState()
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
	}
	return changed
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
	}

	go func() {
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleChats(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	rawChats := make([]Chat, 0, len(a.state.Chats))
	for _, c := range a.state.Chats {
		rawChats = append(rawChats, c)
	}
	contacts := make(map[string]Contact, len(a.state.Contacts))
	for k, v := range a.state.Contacts {
		contacts[k] = v
	}
	a.mu.RUnlock()

	nameByID := map[string]string{}
	for _, c := range rawChats {
		if n := strings.TrimSpace(c.Name); n != "" {
			nameByID[c.ID] = n
		}
	}
	for id, ct := range contacts {
		if strings.HasSuffix(id, "@g.us") {
			continue
		}
		if n := strings.TrimSpace(ct.Notify); n != "" {
			nameByID[id] = n
		} else if n := strings.TrimSpace(ct.Name); n != "" {
			nameByID[id] = n
		}
	}

	mergedByID := map[string]Chat{}
	for _, c := range rawChats {
		resolvedID := a.canonicalizeChatID(c.ID)
		if resolvedID != "" {
			c.ID = resolvedID
		}
		if c.Name == "" && !strings.HasSuffix(c.ID, "@g.us") {
			if ct, ok := contacts[c.ID]; ok {
				if n := strings.TrimSpace(ct.Notify); n != "" {
					c.Name = n
				} else if n := strings.TrimSpace(ct.Name); n != "" {
					c.Name = n
				}
			}
		}
		if c.Name == "" && a.client != nil && a.client.Store != nil && a.client.Store.LIDs != nil {
			if jid, err := types.ParseJID(c.ID); err == nil && jid.Server == types.DefaultUserServer {
				if lid, err := types.ParseJID(jid.User + "@lid"); err == nil {
					pn, err := a.getPNForLID(lid)
					if err == nil && pn.User != "" {
						pnID := canonicalChatID(pn.String())
						if ct, ok := contacts[pnID]; ok {
							if n := strings.TrimSpace(ct.Notify); n != "" {
								c.Name = n
							} else if n := strings.TrimSpace(ct.Name); n != "" {
								c.Name = n
							}
						}
						if c.Name == "" {
							if n := strings.TrimSpace(nameByID[pnID]); n != "" {
								c.Name = n
							}
						}
					}
				}
			}
		}
		mergedByID[c.ID] = mergeChat(mergedByID[c.ID], c)
	}

	chats := make([]Chat, 0, len(mergedByID))
	for _, c := range mergedByID {
		chats = append(chats, c)
	}
	sort.Slice(chats, func(i, j int) bool {
		return chats[i].ConversationTimestamp > chats[j].ConversationTimestamp
	})
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (a *App) handleContacts(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	contacts := make([]Contact, 0, len(a.state.Contacts))
	for _, c := range a.state.Contacts {
		contacts = append(contacts, c)
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts})
}

func (a *App) handleResolveLIDPN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || a.client.Store == nil || a.client.Store.LIDs == nil {
		writeErr(w, http.StatusInternalServerError, "lid mapping store unavailable")
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("id"))
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	jid, err := types.ParseJID(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	out := map[string]any{
		"input":  raw,
		"server": jid.Server,
	}

	switch jid.Server {
	case types.DefaultUserServer:
		lid, err := a.client.Store.LIDs.GetLIDForPN(ctx, jid)
		out["lookup"] = "pn_to_lid"
		out["lid"] = lid.String()
		if err != nil {
			out["error"] = err.Error()
		}
	case types.HiddenUserServer:
		pn, err := a.client.Store.LIDs.GetPNForLID(ctx, jid)
		out["lookup"] = "lid_to_pn"
		out["pn"] = pn.String()
		if err != nil {
			out["error"] = err.Error()
		}
	default:
		writeErr(w, http.StatusBadRequest, "id must be @s.whatsapp.net or @lid")
		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSyncContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return
	}

	if a.client.Store != nil && a.client.Store.AppState != nil {
		for _, patch := range []appstate.WAPatchName{
			appstate.WAPatchCriticalBlock,
			appstate.WAPatchRegularLow,
			appstate.WAPatchRegularHigh,
			appstate.WAPatchRegular,
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			a.safeFetchAppState(ctx, patch)
			cancel()
		}
	}
	if a.client.Store == nil || a.client.Store.Contacts == nil {
		writeErr(w, http.StatusInternalServerError, "contacts store unavailable")
		return
	}

	storeCtx, storeCancel := context.WithTimeout(context.Background(), 12*time.Second)
	allContacts, err := a.client.Store.Contacts.GetAllContacts(storeCtx)
	storeCancel()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated := 0
	total := 0
	a.mu.Lock()
	for jid, info := range allContacts {
		raw := strings.TrimSpace(jid.String())
		if raw == "" {
			continue
		}
		total++
		cid := a.canonicalizeChatID(raw)

		name := strings.TrimSpace(info.PushName)
		if name == "" {
			name = strings.TrimSpace(info.FullName)
		}
		if name == "" {
			name = strings.TrimSpace(info.FirstName)
		}
		if name == "" {
			name = strings.TrimSpace(info.BusinessName)
		}

		oldContact := a.state.Contacts[cid]
		contact := oldContact
		contact.ID = cid
		if name != "" {
			contact.Name = name
			contact.Notify = name
		}
		a.state.Contacts[cid] = contact

		oldChat := a.state.Chats[cid]
		chat := oldChat
		chat.ID = cid
		if name != "" {
			chat.Name = name
		}
		a.state.Chats[cid] = chat

		if oldContact != contact || oldChat != chat {
			updated++
		}
	}
	a.mu.Unlock()

	// Second pass: ask WA server for missing profile data for unresolved direct chats.
	unresolved := []types.JID{}
	a.mu.RLock()
	for id, ch := range a.state.Chats {
		if strings.HasSuffix(id, "@g.us") || id == "status@broadcast" {
			continue
		}
		if strings.TrimSpace(ch.Name) != "" {
			continue
		}
		ct := a.state.Contacts[id]
		if strings.TrimSpace(ct.Notify) != "" || strings.TrimSpace(ct.Name) != "" {
			continue
		}
		if jid, err := types.ParseJID(id); err == nil && jid.Server == types.DefaultUserServer {
			unresolved = append(unresolved, jid.ToNonAD())
		}
	}
	a.mu.RUnlock()

	enriched := 0
	queried := 0
	lookupErrors := 0
	if len(unresolved) > 0 {
		seen := map[string]struct{}{}
		unique := make([]types.JID, 0, len(unresolved))
		for _, j := range unresolved {
			key := j.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			unique = append(unique, j)
		}
		queried = len(unique)

		const batchSize = 100
		for i := 0; i < len(unique); i += batchSize {
			end := i + batchSize
			if end > len(unique) {
				end = len(unique)
			}
			batch := unique[i:end]
			lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 8*time.Second)
			infoMap, err := a.client.GetUserInfo(lookupCtx, batch)
			lookupCancel()
			if err != nil {
				lookupErrors++
				continue
			}
			a.mu.Lock()
			for pnJID, info := range infoMap {
				cid := a.canonicalizeChatID(pnJID.String())
				name := ""
				if info.VerifiedName != nil && info.VerifiedName.Details != nil {
					name = strings.TrimSpace(info.VerifiedName.Details.GetVerifiedName())
				}
				if name == "" {
					continue
				}

				ct := a.state.Contacts[cid]
				if ct.ID == "" {
					ct.ID = cid
				}
				changed := false
				if strings.TrimSpace(name) != "" && ct.Notify != name {
					ct.Notify = name
					changed = true
				}
				if strings.TrimSpace(name) != "" && ct.Name != name {
					ct.Name = name
					changed = true
				}
				a.state.Contacts[cid] = ct

				ch := a.state.Chats[cid]
				if ch.ID == "" {
					ch.ID = cid
				}
				if strings.TrimSpace(name) != "" && ch.Name != name {
					ch.Name = name
					changed = true
				}
				a.state.Chats[cid] = ch

				if changed {
					enriched++
				}
			}
			a.mu.Unlock()
		}
	}

	if updated > 0 || enriched > 0 {
		a.recanonicalizeState()
	}
	unresolvedAfter := 0
	a.mu.RLock()
	for id, ch := range a.state.Chats {
		if strings.HasSuffix(id, "@g.us") || id == "status@broadcast" {
			continue
		}
		if strings.TrimSpace(ch.Name) != "" {
			continue
		}
		ct := a.state.Contacts[id]
		if strings.TrimSpace(ct.Notify) != "" || strings.TrimSpace(ct.Name) != "" {
			continue
		}
		unresolvedAfter++
	}
	a.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"total":        total,
		"updated":      updated,
		"enriched":     enriched,
		"queried":      queried,
		"lookupErrors": lookupErrors,
		"unresolved":   unresolvedAfter,
	})
}

func (a *App) handleSyncGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	groups, err := a.client.GetJoinedGroups(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated := a.refreshGroupMetadata()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"total":   len(groups),
		"updated": updated,
	})
}

func (a *App) handleMessages(w http.ResponseWriter, r *http.Request) {
	chatID := a.canonicalizeChatID(strings.TrimSpace(r.URL.Query().Get("chatId")))
	if chatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, maxMessagesResponseLimit)
	}
	if around := strings.TrimSpace(r.URL.Query().Get("around")); around != "" {
		a.handleMessagesAround(w, chatID, around, limit)
		return
	}

	const q = `SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		FROM messages WHERE chat_id = ? %s ORDER BY ts DESC LIMIT ?`
	// Fetch limit+1 so we can detect whether more older messages exist.
	fetch := limit + 1
	var (
		rows *sql.Rows
		err  error
	)
	if beforeStr := strings.TrimSpace(r.URL.Query().Get("before")); beforeStr != "" {
		beforeTS, _ := strconv.ParseInt(beforeStr, 10, 64)
		rows, err = a.db.Query(fmt.Sprintf(q, "AND ts < ?"), chatID, beforeTS, fetch)
	} else {
		rows, err = a.db.Query(fmt.Sprintf(q, ""), chatID, fetch)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	msgs := make([]WireMessage, 0, fetch)
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		log.Printf("handleMessages rows err: %v", err)
	}
	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}
	// Results came DESC; reverse to chronological order for the TUI.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "hasMore": hasMore})
}

// handleMessagesAround returns a window of messages centred on a specific
// message ID: up to limit/2 older messages, the anchor itself, and up to
// limit/2 newer messages. The response includes anchorIndex so the TUI can
// scroll to position the anchor in view.
func (a *App) handleMessagesAround(w http.ResponseWriter, chatID, msgID string, limit int) {
	// Look up the anchor message's timestamp.
	var anchorTS int64
	var anchorFromMe int
	err := a.db.QueryRow(
		`SELECT ts, from_me FROM messages WHERE chat_id = ? AND id = ? LIMIT 1`,
		chatID, msgID,
	).Scan(&anchorTS, &anchorFromMe)
	if err != nil {
		writeErr(w, http.StatusNotFound, "message not found")
		return
	}

	half := limit / 2

	// Older messages (before anchor, exclusive).
	olderRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND ts < ?
		 ORDER BY ts DESC LIMIT ?`,
		chatID, anchorTS, half,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var older []WireMessage
	for olderRows.Next() {
		if msg, err := scanMessageRow(olderRows); err == nil {
			older = append(older, msg)
		}
	}
	if err := olderRows.Err(); err != nil {
		log.Printf("handleMessagesAround olderRows err: %v", err)
	}
	_ = olderRows.Close()
	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}

	anchorRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND id = ? AND from_me = ?`,
		chatID, msgID, anchorFromMe,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var anchor []WireMessage
	for anchorRows.Next() {
		if msg, err := scanMessageRow(anchorRows); err == nil {
			anchor = append(anchor, msg)
		}
	}
	if err := anchorRows.Err(); err != nil {
		log.Printf("handleMessagesAround anchorRows err: %v", err)
	}
	_ = anchorRows.Close()

	newerRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND ts > ?
		 ORDER BY ts ASC LIMIT ?`,
		chatID, anchorTS, half,
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var newer []WireMessage
	for newerRows.Next() {
		if msg, err := scanMessageRow(newerRows); err == nil {
			newer = append(newer, msg)
		}
	}
	if err := newerRows.Err(); err != nil {
		log.Printf("handleMessagesAround newerRows err: %v", err)
	}
	_ = newerRows.Close()

	// Combine: older + anchor + newer (all chronological).
	msgs := make([]WireMessage, 0, len(older)+len(anchor)+len(newer))
	msgs = append(msgs, older...)
	msgs = append(msgs, anchor...)
	msgs = append(msgs, newer...)

	anchorIndex := len(older) // position of anchor in msgs
	writeJSON(w, http.StatusOK, map[string]any{
		"messages":    msgs,
		"anchorIndex": anchorIndex,
		"hasMore":     false,
	})
}

type searchHit struct {
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	FromMe    bool   `json:"fromMe"`
	Timestamp int64  `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	chatID := strings.TrimSpace(r.URL.Query().Get("chatId"))
	if chatID != "" {
		chatID = a.canonicalizeChatID(chatID)
	}

	// Wrap each token in double quotes so FTS5 treats them as phrase tokens (avoids
	// users hitting FTS5 special syntax accidentally; multi-word still ANDs).
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []searchHit{}})
		return
	}
	for i, t := range tokens {
		tokens[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	matchExpr := strings.Join(tokens, " ")

	const baseQ = `
		SELECT f.chat_id, f.msg_id, f.from_me,
		       snippet(messages_fts, 3, '<b>', '</b>', '...', 12) AS snip,
		       COALESCE(m.ts, 0) AS ts
		FROM messages_fts f
		LEFT JOIN messages m ON m.chat_id = f.chat_id AND m.id = f.msg_id AND m.from_me = f.from_me
		WHERE f.body MATCH ? %s
		ORDER BY ts DESC
		LIMIT ?`
	var (
		rows *sql.Rows
		err  error
	)
	if chatID != "" {
		rows, err = a.db.Query(fmt.Sprintf(baseQ, "AND f.chat_id = ?"), matchExpr, chatID, limit)
	} else {
		rows, err = a.db.Query(fmt.Sprintf(baseQ, ""), matchExpr, limit)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	results := make([]searchHit, 0, limit)
	for rows.Next() {
		var (
			cID, mID, snip string
			fromMe         int
			ts             int64
		)
		if err := rows.Scan(&cID, &mID, &fromMe, &snip, &ts); err != nil {
			continue
		}
		results = append(results, searchHit{
			ChatID:    cID,
			MessageID: mID,
			FromMe:    fromMe == 1,
			Timestamp: ts,
			Snippet:   snip,
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("handleSearch rows err: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *App) requireConnectedClient(w http.ResponseWriter) bool {
	if a == nil || a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return false
	}
	return true
}

func (a *App) isChatAllowed(chatID string) (bool, error) {
	phone := phoneFromJID(chatID)
	var allowed int
	err := a.withPermissionDB(func(db *sql.DB) error {
		return db.QueryRow(`SELECT allowed FROM chat_permissions WHERE phone = ?`, phone).Scan(&allowed)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return allowed == 1, nil
}

func (a *App) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID             string `json:"chatId"`
		Text               string `json:"text"`
		ReplyToMsgID       string `json:"replyToMsgId,omitempty"`
		ReplyToText        string `json:"replyToText,omitempty"`
		ReplyToParticipant string `json:"replyToParticipant,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	req.Text = sanitizeOutgoingText(req.Text)
	if req.ChatID == "" || !hasVisibleText(req.Text) {
		writeErr(w, http.StatusBadRequest, "chatId and text are required")
		return
	}

	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}

	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}

	var msg *waE2E.Message
	if req.ReplyToMsgID != "" {
		quotedMsg := &waE2E.Message{Conversation: proto.String(req.ReplyToText)}
		participant := req.ReplyToParticipant
		if participant == "" {
			participant = req.ChatID
		}
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String(req.Text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:      proto.String(req.ReplyToMsgID),
					Participant:   proto.String(participant),
					QuotedMessage: quotedMsg,
				},
			},
		}
	} else {
		msg = &waE2E.Message{Conversation: proto.String(req.Text)}
	}

	resp, err := a.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().Unix()
	wireMsg := map[string]any{"conversation": req.Text}
	if req.ReplyToMsgID != "" {
		wireMsg = map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              req.Text,
				"quotedText":        req.ReplyToText,
				"quotedParticipant": req.ReplyToParticipant,
			},
		}
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: req.ChatID,
			FromMe:    true,
		},
		Message:          wireMsg,
		MessageTimestamp: now,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(req.ChatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}

func (a *App) handleSendFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID  string `json:"chatId"`
		Path    string `json:"path"`
		Caption string `json:"caption"`
		Kind    string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = a.canonicalizeChatID(strings.TrimSpace(req.ChatID))
	req.Path = filepath.Clean(strings.TrimSpace(req.Path))
	req.Caption = strings.TrimSpace(sanitizeOutgoingText(req.Caption))
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.ChatID == "" || req.Path == "" {
		writeErr(w, http.StatusBadRequest, "chatId and path are required")
		return
	}

	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}

	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}

	info, err := os.Stat(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file not found")
		return
	}
	if info.IsDir() {
		writeErr(w, http.StatusBadRequest, "path must be a file")
		return
	}
	if !info.Mode().IsRegular() {
		writeErr(w, http.StatusBadRequest, "path must be a regular file")
		return
	}
	if info.Size() > maxUploadBytes {
		writeErr(w, http.StatusBadRequest, "file too large: max 100 MB")
		return
	}
	if err := validateSendFileInput(req.Path, req.Kind); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	mediaType, err := mediaTypeForSendKind(req.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	f, err := os.Open(req.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(f, data); err != nil {
		_ = f.Close()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = f.Close()
	upload, err := a.client.Upload(context.Background(), data, mediaType)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	msg, wireBody, err := buildOutgoingMediaMessage(req.Kind, req.Path, req.Caption, upload, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := a.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var mediaProto string
	if b, err := proto.Marshal(msg); err == nil {
		mediaProto = base64.StdEncoding.EncodeToString(b)
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: req.ChatID,
			FromMe:    true,
		},
		Message:          wireBody,
		MessageTimestamp: time.Now().Unix(),
		MediaProto:       mediaProto,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(req.ChatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}

func (a *App) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}

	a.mu.Lock()
	chat := a.state.Chats[req.ChatID]
	chat.ID = req.ChatID
	unreadToMark := chat.UnreadCount
	chat.UnreadCount = 0
	a.state.Chats[req.ChatID] = chat
	a.mu.Unlock()

	senderToIDs := make(map[string][]types.MessageID)
	{
		limit := 100
		if unreadToMark > 0 && unreadToMark < limit {
			limit = unreadToMark
		}
		rows, err := a.db.Query(`
			SELECT id, participant, chat_id FROM messages
			WHERE chat_id = ? AND from_me = 0 AND id != ''
			ORDER BY ts DESC LIMIT ?
		`, req.ChatID, limit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, participant, cid string
				if rows.Scan(&id, &participant, &cid) != nil {
					continue
				}
				sender := participant
				if sender == "" {
					sender = cid
				}
				senderToIDs[sender] = append(senderToIDs[sender], types.MessageID(id))
			}
			if err := rows.Err(); err != nil {
				log.Printf("handleMarkRead rows err: %v", err)
			}
		}
	}

	// Inform WhatsApp server in the background so the HTTP response returns
	// immediately — MarkRead can be slow and would otherwise trigger the TUI's
	// 12s client timeout. persistState is called after the API calls complete
	// so we don't flush the zeroed unread count before WhatsApp confirms it.
	go func() {
		chatJID, _ := types.ParseJID(req.ChatID)
		for senderStr, ids := range senderToIDs {
			senderJID, _ := types.ParseJID(senderStr)
			_ = a.client.MarkRead(context.Background(), ids, time.Now(), chatJID, senderJID)
		}
		a.persistState()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleTyping(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
		State  string `json:"state"` // "composing" or "paused"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	state := types.ChatPresenceComposing
	if req.State == "paused" {
		state = types.ChatPresencePaused
	}
	_ = a.client.SendChatPresence(context.Background(), jid, state, types.ChatPresenceMediaText)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleReact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
		Sender    string `json:"sender"`
		Reaction  string `json:"reaction"` // emoji string, empty to remove
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" || req.MessageID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and messageId are required")
		return
	}
	chatJID, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	senderJID := types.EmptyJID
	if req.Sender != "" {
		senderJID, _ = types.ParseJID(a.canonicalizeChatID(req.Sender))
	}
	msg := a.client.BuildReaction(chatJID, senderJID, types.MessageID(req.MessageID), req.Reaction)
	resp, err := a.client.SendMessage(context.Background(), chatJID, msg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().Unix()
	wireMsg := map[string]any{
		"reactionMessage": map[string]any{
			"emoji":       req.Reaction,
			"targetMsgID": req.MessageID,
		},
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: req.ChatID,
			FromMe:    true,
		},
		Message:          wireMsg,
		MessageTimestamp: now,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(req.ChatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" || req.MessageID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and messageId are required")
		return
	}
	chatJID, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	_, err = a.client.RevokeMessage(context.Background(), chatJID, types.MessageID(req.MessageID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	if a.db != nil {
		tx, err := a.db.Begin()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`DELETE FROM messages WHERE chat_id = ? AND id = ?`, req.ChatID, req.MessageID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ?`, req.ChatID, req.MessageID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	a.persistState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleProfilePicture(w http.ResponseWriter, r *http.Request) {
	if !a.requireConnectedClient(w) {
		return
	}
	jidRaw := strings.TrimSpace(r.URL.Query().Get("jid"))
	if jidRaw == "" {
		writeErr(w, http.StatusBadRequest, "jid is required")
		return
	}
	jid, err := types.ParseJID(jidRaw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}
	info, err := a.client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{})
	if err != nil || info == nil {
		writeJSON(w, http.StatusOK, map[string]any{"url": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": info.URL})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.mu.Lock()
	a.shuttingDown = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.shuttingDown = false
		a.mu.Unlock()
	}()
	hadRuntimeResources := a.client != nil || a.storeContainer != nil
	var errs []string
	// Remote logout is best-effort. Even if the server is unreachable, we want
	// to tear down local state.
	if a.client != nil {
		if a.client.Store != nil && a.client.Store.ID != nil {
			if err := a.client.Logout(context.Background()); err != nil {
				errs = append(errs, fmt.Sprintf("remote logout failed: %v", err))
			}
		}
		a.client.Disconnect()
		if a.client.Store != nil && a.client.Store.ID != nil {
			if err := a.client.Store.Delete(context.Background()); err != nil {
				errs = append(errs, fmt.Sprintf("store delete failed: %v", err))
			} else {
				a.client.Store.ID = nil
			}
		}
	}
	a.mu.Lock()
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
			errs = append(errs, fmt.Sprintf("store container close failed: %v", err))
		}
		a.storeContainer = nil
	}
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("db close failed: %v", err))
		}
		a.db = nil
	}
	a.mu.Unlock()
	if strings.TrimSpace(a.cacheDir) != "" {
		if hadRuntimeResources {
			if err := a.resetPersistentStorage(); err != nil {
				errs = append(errs, fmt.Sprintf("state cleanup failed: %v", err))
			}
		} else {
			if err := os.RemoveAll(a.cacheDir); err != nil {
				errs = append(errs, fmt.Sprintf("state cleanup failed: %v", err))
			} else if err := os.MkdirAll(a.cacheDir, 0o755); err != nil {
				errs = append(errs, fmt.Sprintf("state cleanup failed: %v", err))
			}
		}
		if err := a.persistStateWithErr(); err != nil {
			errs = append(errs, fmt.Sprintf("state cleanup failed: %v", err))
		}
	} else if err := a.persistStateWithErr(); err != nil {
		errs = append(errs, fmt.Sprintf("state cleanup failed: %v", err))
	}
	if len(errs) > 0 {
		writeErr(w, http.StatusInternalServerError, strings.Join(errs, "; "))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Logged out successfully"})
}

func (a *App) toWireMessage(evt *events.Message) WireMessage {
	info := evt.Info
	chatID := info.Chat.String()
	effective := effectiveMessage(evt.Message)
	msg, mediaProto := a.wireMessagePayload(evt.Message, effective, chatID, info.IsGroup)

	key := WireKey{
		ID:        info.ID,
		RemoteJID: chatID,
		FromMe:    info.IsFromMe,
	}
	if info.IsGroup {
		key.Participant = info.Sender.String()
	}

	return WireMessage{
		Key:              key,
		Message:          msg,
		MessageTimestamp: info.Timestamp.Unix(),
		PushName:         evt.Info.PushName,
		MediaProto:       mediaProto,
	}
}

func (a *App) toWireMessageFromHistory(webMsg *waWeb.WebMessageInfo) WireMessage {
	if webMsg == nil {
		return WireMessage{}
	}
	key := webMsg.GetKey()
	m := effectiveMessage(webMsg.GetMessage())
	chatID := key.GetRemoteJID()
	out, mediaProto := a.wireMessagePayload(webMsg.GetMessage(), m, chatID, strings.HasSuffix(chatID, "@g.us"))
	wireKey := WireKey{
		ID:          key.GetID(),
		RemoteJID:   key.GetRemoteJID(),
		FromMe:      key.GetFromMe(),
		Participant: key.GetParticipant(),
	}

	return WireMessage{
		Key:              wireKey,
		Message:          out,
		MessageTimestamp: int64(webMsg.GetMessageTimestamp()),
		PushName:         webMsg.GetPushName(),
		MediaProto:       mediaProto,
	}
}

func normalizeQuotedParticipant(participant, selfID string, canonicalize func(string) string) string {
	phoneIdentity := func(jid string) string {
		local := phoneFromJID(jid)
		local = strings.SplitN(local, ":", 2)[0]
		return strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, local)
	}
	participant = strings.TrimSpace(participant)
	if participant == "" {
		return ""
	}
	if canonicalize != nil {
		participant = canonicalize(participant)
	}
	if strings.TrimSpace(selfID) != "" {
		selfCanonical := selfID
		if canonicalize != nil {
			selfCanonical = canonicalize(selfID)
		}
		if participant == selfCanonical || phoneIdentity(participant) == phoneIdentity(selfCanonical) {
			return ""
		}
	}
	return participant
}

func phoneIdentity(jid string) string {
	local := phoneFromJID(jid)
	local = strings.SplitN(local, ":", 2)[0]
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, local)
}

func quotedFromMeForChat(chatID, quotedParticipant string, isGroup bool) bool {
	quotedParticipant = strings.TrimSpace(quotedParticipant)
	if quotedParticipant == "" {
		return true
	}
	if isGroup {
		return false
	}
	return phoneIdentity(quotedParticipant) != "" && phoneIdentity(quotedParticipant) != phoneIdentity(chatID)
}

func (a *App) wireMessagePayload(raw, effective *waE2E.Message, chatID string, isGroup bool) (map[string]any, string) {
	msg := map[string]any{}
	var mediaProto string
	if txt := effective.GetConversation(); txt != "" {
		msg["conversation"] = txt
	}
	if ext := effective.GetExtendedTextMessage(); ext != nil {
		entry := map[string]any{"text": ext.GetText()}
		if ctx := ext.GetContextInfo(); ctx != nil && ctx.GetQuotedMessage() != nil {
			entry["quotedText"] = quotedText(ctx.GetQuotedMessage())
			selfID := ""
			if a != nil && a.client != nil && a.client.Store != nil && a.client.Store.ID != nil {
				selfID = a.client.Store.ID.String()
			}
			entry["quotedParticipant"] = normalizeQuotedParticipant(ctx.GetParticipant(), selfID, func(id string) string {
				if a == nil {
					return strings.TrimSpace(id)
				}
				return a.canonicalizeChatID(id)
			})
			entry["quotedFromMe"] = quotedFromMeForChat(chatID, fmt.Sprint(entry["quotedParticipant"]), isGroup)
		}
		msg["extendedTextMessage"] = entry
	}
	if img := effective.GetImageMessage(); img != nil {
		msg["imageMessage"] = map[string]any{"caption": img.GetCaption(), "mimetype": img.GetMimetype()}
	}
	if vid := effective.GetVideoMessage(); vid != nil {
		msg["videoMessage"] = map[string]any{"caption": vid.GetCaption(), "mimetype": vid.GetMimetype()}
	}
	if doc := effective.GetDocumentMessage(); doc != nil {
		msg["documentMessage"] = map[string]any{"caption": doc.GetCaption(), "fileName": doc.GetFileName(), "mimetype": doc.GetMimetype()}
	}
	if aud := effective.GetAudioMessage(); aud != nil {
		msg["audioMessage"] = map[string]any{"ptt": aud.GetPTT(), "mimetype": aud.GetMimetype()}
	}
	if stk := effective.GetStickerMessage(); stk != nil {
		msg["stickerMessage"] = map[string]any{"mimetype": stk.GetMimetype()}
	}
	if rxn := effective.GetReactionMessage(); rxn != nil {
		msg["reactionMessage"] = map[string]any{
			"emoji":       rxn.GetText(),
			"targetMsgID": rxn.GetKey().GetID(),
		}
	}
	if protocol := protocolMessagePayload(raw, effective); protocol != nil {
		msg["protocolMessage"] = protocol
	}
	if len(msg) == 0 {
		msg["unknown"] = map[string]any{
			"rawFields":       messageFieldNames(raw),
			"effectiveFields": messageFieldNames(effective),
		}
	}
	isMedia := effective.GetImageMessage() != nil || effective.GetVideoMessage() != nil ||
		effective.GetDocumentMessage() != nil || effective.GetAudioMessage() != nil ||
		effective.GetStickerMessage() != nil
	if isMedia {
		if b, err := proto.Marshal(effective); err == nil {
			mediaProto = base64.StdEncoding.EncodeToString(b)
		}
	}
	return msg, mediaProto
}

func protocolMessagePayload(raw, effective *waE2E.Message) map[string]any {
	var protocol *waE2E.ProtocolMessage
	switch {
	case effective != nil && effective.GetProtocolMessage() != nil:
		protocol = effective.GetProtocolMessage()
	case raw != nil && raw.GetProtocolMessage() != nil:
		protocol = raw.GetProtocolMessage()
	default:
		return nil
	}
	out := map[string]any{
		"type": protocol.GetType().String(),
	}
	if key := protocol.GetKey(); key != nil {
		if id := key.GetID(); id != "" {
			out["targetMsgID"] = id
		}
	}
	if timer := protocol.GetEphemeralExpiration(); timer > 0 {
		out["ephemeralExpiration"] = timer
	}
	if edited := protocol.GetEditedMessage(); edited != nil {
		if text := quotedText(edited); text != "" {
			out["editedText"] = text
		}
	}
	return out
}

func effectiveMessage(msg *waE2E.Message) *waE2E.Message {
	for msg != nil {
		switch {
		case msg.GetDeviceSentMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		case msg.GetCommentMessage() != nil:
			msg = msg.GetCommentMessage().GetMessage()
		case msg.GetEphemeralMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDocumentWithCaptionMessage() != nil:
			msg = msg.GetDocumentWithCaptionMessage().GetMessage()
		case msg.GetEditedMessage() != nil:
			msg = msg.GetEditedMessage().GetMessage()
		default:
			return msg
		}
	}
	return nil
}

func messageFieldNames(msg *waE2E.Message) []string {
	if msg == nil {
		return nil
	}
	fields := make([]string, 0, 4)
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		if name != "messageContextInfo" {
			fields = append(fields, name)
		}
		return true
	})
	sort.Strings(fields)
	return fields
}

var dedupeSeq uint64

func dedupeKey(m WireMessage) string {
	if m.Key.ID != "" {
		return "id:" + m.Key.ID
	}
	seq := atomic.AddUint64(&dedupeSeq, 1)
	return "noid:" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(seq, 10)
}

// backfillFTS populates the messages_fts index from the messages table on
// first run after the FTS feature was added. No-op if FTS already has rows or
// if there are no messages.
func (a *App) backfillFTS() {
	if a.db == nil {
		return
	}
	var ftsCount, msgCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM messages_fts`).Scan(&ftsCount); err != nil {
		log.Printf("backfillFTS count fts: %v", err)
		return
	}
	if ftsCount > 0 {
		return
	}
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&msgCount); err != nil || msgCount == 0 {
		return
	}

	type ftsRow struct {
		chatID, msgID, body string
		fromMe              int
	}

	limit := 1000
	offset := 0
	for {
		rows, err := a.db.Query(`SELECT id, chat_id, from_me, message_json FROM messages LIMIT ? OFFSET ?`, limit, offset)
		if err != nil {
			log.Printf("backfillFTS scan query: %v", err)
			break
		}
		var pending []ftsRow
		for rows.Next() {
			var id, chatID, msgJSON string
			var fromMe int
			if err := rows.Scan(&id, &chatID, &fromMe, &msgJSON); err != nil {
				continue
			}
			var m map[string]any
			_ = json.Unmarshal([]byte(msgJSON), &m)
			body := extractSearchableText(m)
			if body == "" {
				continue
			}
			pending = append(pending, ftsRow{chatID: chatID, msgID: id, fromMe: fromMe, body: body})
		}
		if err := rows.Err(); err != nil {
			log.Printf("backfillFTS rows err: %v", err)
		}
		_ = rows.Close()

		if len(pending) == 0 {
			break
		}

		tx, err := a.db.Begin()
		if err != nil {
			log.Printf("backfillFTS tx: %v", err)
			break
		}
		for _, r := range pending {
			_, _ = tx.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`, r.chatID, r.msgID, r.fromMe, r.body)
		}
		if err := tx.Commit(); err != nil {
			log.Printf("backfillFTS commit: %v", err)
			break
		}

		if len(pending) < limit {
			break
		}
		offset += limit
	}
}

// vacuumDB runs VACUUM asynchronously to compact the database and reclaim space
// freed by deletions and FTS churn. Runs in a goroutine so startup is not blocked.
func (a *App) vacuumDB() {
	if a.db == nil {
		return
	}
	go func() {
		time.Sleep(30 * time.Second)
		a.mu.RLock()
		db := a.db
		shutting := a.shuttingDown
		a.mu.RUnlock()
		if db == nil || shutting {
			return
		}
		if _, err := db.Exec(`PRAGMA incremental_vacuum(100)`); err != nil {
			log.Printf("vacuum: %v", err)
		}
	}()
}

// purgeContactsWithName clears chat_permissions.name rows that match the given
// name. Returns the number of rows affected.
func (a *App) purgeContactsWithName(name string) (int64, error) {
	if a.db == nil {
		return 0, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, nil
	}
	res, err := a.db.Exec(`UPDATE chat_permissions SET name = '' WHERE name = ?`, name)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// purgeOwnPushNameFromContacts clears chat_permissions.name rows that match
// the local user's own WhatsApp push name. Legacy bug: outgoing messages used
// to write the sender's push name (i.e. our own) into the recipient's contact
// row via INSERT OR IGNORE, so once-bad rows persisted forever. Run once per
// startup as a defensive sweep.
func (a *App) purgeOwnPushNameFromContacts() {
	if a.client == nil || a.client.Store == nil {
		return
	}
	pushName := a.client.Store.PushName
	n, err := a.purgeContactsWithName(pushName)
	if err != nil {
		log.Printf("purgeOwnPushNameFromContacts: %v", err)
		return
	}
	if n > 0 {
		log.Printf("purgeOwnPushNameFromContacts: cleared %d row(s) where name = %q", n, strings.TrimSpace(pushName))
	}
}

// backfillReceipt upgrades any FromMe message row that has no receipt state
// to "delivered". History sync (and pre-fix inserts) leave FromMe rows with
// an empty receipt, which the TUI renders as a single tick — looking the
// same as a live message that's been sent but not yet delivered. Any FromMe
// row that made it into the store is, at minimum, delivered, so this is a
// safe default. Idempotent: a second run is a no-op.
func (a *App) backfillReceipt() {
	if a.db == nil {
		return
	}
	res, err := a.db.Exec(
		`UPDATE messages SET receipt = 'delivered' WHERE from_me = 1 AND (receipt = '' OR receipt IS NULL)`,
	)
	if err != nil {
		log.Printf("backfillReceipt: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("backfillReceipt: upgraded %d FromMe row(s) to delivered", n)
	}
}

func (a *App) loadState() {
	// Defensive: clear any chat_permissions rows that have the local user's own
	// push name as the contact name (legacy bug — see purgeOwnPushNameFromContacts).
	a.purgeOwnPushNameFromContacts()
	// Upgrade FromMe rows that have no receipt state (legacy: pre-fix history
	// sync inserted them as empty receipt, which the TUI renders as a single
	// tick). Idempotent.
	a.backfillReceipt()
	// Compact the DB in the background to reclaim space freed by message deletes.
	a.vacuumDB()

	// Primary source: SQLite chats + contacts tables.
	if a.db == nil {
		a.mu.Lock()
		a.needsBootstrapSync = true
		a.mu.Unlock()
		return
	}
	chats, err1 := a.loadChatsFromDB()
	contacts, err2 := a.loadContactsFromDB()
	if err1 != nil || err2 != nil {
		log.Printf("loadState: %v %v", err1, err2)
	}
	a.mu.Lock()
	if chats != nil {
		a.state.Chats = chats
	}
	if contacts != nil {
		a.state.Contacts = contacts
	}
	a.needsBootstrapSync = len(a.state.Chats) == 0
	a.mu.Unlock()
	a.reconcileChatTimestampsFromDB()
}

// seeding chats from store so UI isn't empty on first start
func (a *App) bootstrapFromStore() {
	a.mu.Lock()
	if !a.needsBootstrapSync {
		a.mu.Unlock()
		return
	}
	a.needsBootstrapSync = false
	a.mu.Unlock()

	a.broadcast(EventEnvelope{Type: "status", Payload: "Bootstrapping chat state..."})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if a.client == nil || a.client.Store == nil {
		return
	}
	if a.client.Store.AppState == nil {
		log.Printf("bootstrap: app state store unavailable, skipping app state sync")
	} else {
		for _, patch := range []appstate.WAPatchName{
			appstate.WAPatchCriticalBlock,
			appstate.WAPatchRegularLow,
			appstate.WAPatchRegularHigh,
			appstate.WAPatchRegular,
		} {
			patchCtx, patchCancel := context.WithTimeout(context.Background(), 30*time.Second)
			a.safeFetchAppState(patchCtx, patch)
			patchCancel()
		}
	}

	if a.client.Store.Contacts == nil {
		return
	}
	allContacts, err := a.client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return
	}

	a.mu.Lock()
	seeded := 0
	for jid, info := range allContacts {
		raw := strings.TrimSpace(jid.String())
		if raw == "" {
			continue
		}
		cid := a.canonicalizeChatID(raw)
		name := strings.TrimSpace(info.PushName)
		if name == "" {
			name = strings.TrimSpace(info.FullName)
		}
		if name == "" {
			name = strings.TrimSpace(info.FirstName)
		}
		if name == "" {
			name = strings.TrimSpace(info.BusinessName)
		}

		ct := a.state.Contacts[cid]
		ct.ID = cid
		if name != "" && ct.Notify == "" {
			ct.Notify = name
		}
		a.state.Contacts[cid] = ct

		ch := a.state.Chats[cid]
		ch.ID = cid
		if name != "" && ch.Name == "" {
			ch.Name = name
		}
		a.state.Chats[cid] = ch
		seeded++
	}
	a.mu.Unlock()

	if seeded > 0 {
		a.persistState()
		a.broadcast(EventEnvelope{Type: "contacts:updated"})
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
		a.broadcast(EventEnvelope{Type: "status", Payload: "Bootstrap complete"})
	}
}

func (a *App) safeFetchAppState(ctx context.Context, name appstate.WAPatchName) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("bootstrap: recovered panic during app state fetch %s: %v", name, r)
		}
	}()
	if err := a.client.FetchAppState(ctx, name, true, false); err != nil {
		log.Printf("bootstrap: failed to fetch app state %s: %v", name, err)
	}
}

func (a *App) startPersistWorker() {
	ticker := time.NewTicker(500 * time.Millisecond)
	go func() {
		for range ticker.C {
			if atomic.CompareAndSwapUint32(&a.persistDirty, 1, 0) {
				a.persistState()
			}
		}
	}()
}

func (a *App) persistState() {
	if a == nil {
		return
	}
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	a.mu.RUnlock()
	if shuttingDown {
		return
	}
	_ = a.persistStateWithErr()
}

func (a *App) persistStateWithErr() error {
	a.mu.RLock()
	if a.shuttingDown || a.db == nil {
		a.mu.RUnlock()
		return nil
	}
	chats := make([]Chat, 0, len(a.state.Chats))
	for _, c := range a.state.Chats {
		chats = append(chats, c)
	}
	contacts := make([]Contact, 0, len(a.state.Contacts))
	for _, c := range a.state.Contacts {
		contacts = append(contacts, c)
	}
	db := a.db
	a.mu.RUnlock()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, chat := range chats {
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO chats (id, name, subject, conv_ts, unread_count)
			VALUES (?, ?, ?, ?, ?)
		`, chat.ID, chat.Name, chat.Subject, chat.ConversationTimestamp, chat.UnreadCount); err != nil {
			return err
		}
	}
	for _, contact := range contacts {
		if _, err := tx.Exec(`
			INSERT OR REPLACE INTO contacts (id, name, notify)
			VALUES (?, ?, ?)
		`, contact.ID, contact.Name, contact.Notify); err != nil {
			return err
		}
	}
	return tx.Commit()
}


// reconcileChatTimestampsFromDB queries the DB for the max message timestamp per
// chat and bumps a.state.Chats entries that are behind.
func (a *App) reconcileChatTimestampsFromDB() {
	if a.db == nil {
		return
	}
	rows, err := a.db.Query(`SELECT chat_id, MAX(ts) FROM messages WHERE ts > 0 GROUP BY chat_id`)
	if err != nil {
		return
	}
	defer rows.Close()

	changed := false
	a.mu.Lock()
	for rows.Next() {
		var chatID string
		var maxTS int64
		if rows.Scan(&chatID, &maxTS) != nil {
			continue
		}
		chat := a.state.Chats[chatID]
		if maxTS > chat.ConversationTimestamp {
			chat.ID = chatID
			chat.ConversationTimestamp = maxTS
			a.state.Chats[chatID] = chat
			changed = true
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("reconcileChatTimestampsFromDB rows err: %v", err)
	}
	a.mu.Unlock()

	if changed {
		a.persistState()
	}
}

func reconcileChatTimestamps(state *PersistedState) bool {
	// Kept for tests that work with in-memory state snapshots only.
	return false
}

func reconcileChatTimestampsFromMessages(state *PersistedState, msgs map[string][]WireMessage) bool {
	if state == nil {
		return false
	}
	changed := false
	for chatID, list := range msgs {
		var latest int64
		for _, msg := range list {
			if msg.MessageTimestamp > latest {
				latest = msg.MessageTimestamp
			}
		}
		chat := state.Chats[chatID]
		if chat.ID == "" {
			chat.ID = chatID
		}
		if latest > chat.ConversationTimestamp {
			chat.ConversationTimestamp = latest
			changed = true
		}
		state.Chats[chatID] = chat
	}
	return changed
}

func phoneFromJID(jid string) string {
	p := strings.Split(jid, "@")
	return p[0]
}

func canonicalChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ""
	}
	if strings.HasSuffix(chatID, "@g.us") || chatID == "status@broadcast" {
		return chatID
	}
	local := phoneFromJID(chatID)
	local = strings.SplitN(local, ":", 2)[0]
	local = strings.SplitN(local, ".", 2)[0]
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, local)
	if len(digits) >= 6 {
		return digits + "@s.whatsapp.net"
	}
	return chatID
}

func (a *App) getPNForLID(lid types.JID) (types.JID, error) {
	if a == nil || a.client == nil || a.client.Store == nil || a.client.Store.LIDs == nil {
		return types.JID{}, fmt.Errorf("store unavailable")
	}
	key := lid.String()
	a.lidCacheMu.RLock()
	cachedVal, ok := a.lidCache[key]
	a.lidCacheMu.RUnlock()
	if ok {
		if cachedVal == "" {
			return types.JID{}, fmt.Errorf("not found (cached)")
		}
		return types.ParseJID(cachedVal)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	pn, err := a.client.Store.LIDs.GetPNForLID(ctx, lid)
	cancel()

	a.lidCacheMu.Lock()
	if err == nil && pn.User != "" {
		a.lidCache[key] = pn.String()
		a.lidCache[pn.String()] = pn.String()
	} else {
		a.lidCache[key] = ""
	}
	a.lidCacheMu.Unlock()

	return pn, err
}

func (a *App) canonicalizeChatID(chatID string) string {
	base := canonicalChatID(chatID)
	if base == "" || strings.HasSuffix(base, "@g.us") || base == "status@broadcast" {
		return base
	}
	if a == nil || a.client == nil || a.client.Store == nil || a.client.Store.LIDs == nil {
		return base
	}

	jid, err := types.ParseJID(strings.TrimSpace(chatID))
	if err != nil {
		jid, err = types.ParseJID(base)
		if err != nil {
			return base
		}
	}
	jid = jid.ToNonAD()

	switch jid.Server {
	case types.HiddenUserServer:
		if pn, err := a.getPNForLID(jid); err == nil && pn.User != "" {
			return canonicalChatID(pn.String())
		}
	case types.DefaultUserServer:
		if possibleLID, err := types.ParseJID(jid.User + "@lid"); err == nil {
			if pn, err := a.getPNForLID(possibleLID); err == nil && pn.User != "" {
				return canonicalChatID(pn.String())
			}
		}
	}

	return base
}

func (a *App) historyConversationChatID(conv *waHistorySync.Conversation) string {
	if conv == nil {
		return ""
	}
	candidates := []string{
		conv.GetPnJID(),
		conv.GetID(),
		conv.GetLidJID(),
		conv.GetNewJID(),
		conv.GetOldJID(),
	}
	for _, raw := range candidates {
		cid := a.canonicalizeChatID(strings.TrimSpace(raw))
		if cid != "" {
			return cid
		}
	}
	return ""
}

func mergeChat(dst Chat, src Chat) Chat {
	if dst.ID == "" {
		dst.ID = src.ID
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Subject == "" {
		dst.Subject = src.Subject
	}
	if src.ConversationTimestamp > dst.ConversationTimestamp {
		dst.ConversationTimestamp = src.ConversationTimestamp
	}
	if src.UnreadCount > dst.UnreadCount {
		dst.UnreadCount = src.UnreadCount
	}
	return dst
}

func mergeContact(dst Contact, src Contact) Contact {
	if dst.ID == "" {
		dst.ID = src.ID
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if dst.Notify == "" {
		dst.Notify = src.Notify
	}
	return dst
}

func (a *App) migrateStateCanonicalIDs(state *PersistedState) {
	newChats := map[string]Chat{}
	for rawID, ch := range state.Chats {
		cid := a.canonicalizeChatID(rawID)
		ch.ID = cid
		merged := mergeChat(newChats[cid], ch)
		newChats[cid] = merged
	}

	newContacts := map[string]Contact{}
	for rawID, ct := range state.Contacts {
		cid := a.canonicalizeChatID(rawID)
		ct.ID = cid
		merged := mergeContact(newContacts[cid], ct)
		newContacts[cid] = merged
	}

	state.Chats = newChats
	state.Contacts = newContacts
}

func (a *App) upsertPermission(phone, name string) {
	if phone == "" {
		return
	}
	if a == nil {
		return
	}
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	db := a.db
	a.mu.RUnlock()
	if shuttingDown || db == nil {
		return
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO chat_permissions (phone, name, allowed) VALUES (?, ?, 0)`,
		phone, name,
	); err != nil {
		log.Printf("upsertPermission %s: %v", phone, err)
	}
}

// withPermissionDB holds a.mu.RLock() for the entire duration of fn so that
// logout cannot close a.db while the caller is still using it.
func (a *App) withPermissionDB(fn func(*sql.DB) error) error {
	if a == nil {
		return fmt.Errorf("permission store unavailable")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.shuttingDown || a.db == nil {
		return fmt.Errorf("permission store unavailable")
	}
	return fn(a.db)
}

func (a *App) handleGetWhitelist(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Phone   string `json:"phone"`
		Name    string `json:"name"`
		Allowed int    `json:"allowed"`
	}
	var result []entry
	err := a.withPermissionDB(func(db *sql.DB) error {
		rows, err := db.Query(`SELECT phone, name, allowed FROM chat_permissions ORDER BY phone`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.Phone, &e.Name, &e.Allowed); err == nil {
				result = append(result, e)
			}
		}
		return rows.Err()
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	if result == nil {
		result = []entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": result})
}

func (a *App) handleSetWhitelist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Phone   string `json:"phone"`
		Name    string `json:"name"`
		Allowed int    `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Phone == "" {
		writeErr(w, http.StatusBadRequest, "phone is required")
		return
	}
	err := a.withPermissionDB(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT OR REPLACE INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)`,
			req.Phone, req.Name, req.Allowed,
		)
		return err
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleSetName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Phone == "" {
		writeErr(w, http.StatusBadRequest, "phone is required")
		return
	}
	// create row if not exists (allowed stays 0), then only update name
	err := a.withPermissionDB(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, 0)
			 ON CONFLICT(phone) DO UPDATE SET name=excluded.name`,
			req.Phone, req.Name,
		)
		return err
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleMediaDownload(w http.ResponseWriter, r *http.Request) {
	if !a.requireConnectedClient(w) {
		return
	}
	chatID := r.URL.Query().Get("chatId")
	msgID := r.URL.Query().Get("msgId")
	if chatID == "" || msgID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and msgId required")
		return
	}
	var mediaProto string
	if err := a.db.QueryRow(
		`SELECT media_proto FROM messages WHERE chat_id = ? AND id = ? AND media_proto != ''`,
		chatID, msgID,
	).Scan(&mediaProto); err != nil || mediaProto == "" {
		writeErr(w, http.StatusNotFound, "media not found")
		return
	}
	b, err := base64.StdEncoding.DecodeString(mediaProto)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decode error")
		return
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(b, &msg); err != nil {
		writeErr(w, http.StatusInternalServerError, "unmarshal error")
		return
	}
	data, err := a.client.DownloadAny(context.Background(), &msg)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ext := mediaExtension(&msg)
	tmp, err := os.CreateTemp("", "whatzap-*"+ext)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp file error")
		return
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, "temp file write error")
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, "temp file close error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": tmp.Name()})
}

func mediaExtension(msg *waE2E.Message) string {
	msg = effectiveMessage(msg)
	if msg == nil {
		return ".bin"
	}
	if img := msg.GetImageMessage(); img != nil {
		if strings.Contains(img.GetMimetype(), "jpeg") {
			return ".jpg"
		}
		return ".png"
	}
	if msg.GetVideoMessage() != nil {
		return ".mp4"
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		if ext := filepath.Ext(doc.GetFileName()); ext != "" {
			return ext
		}
		return ".bin"
	}
	if msg.GetAudioMessage() != nil {
		return ".ogg"
	}
	if msg.GetStickerMessage() != nil {
		return ".webp"
	}
	return ".bin"
}

func mediaTypeForSendKind(kind string) (whatsmeow.MediaType, error) {
	switch kind {
	case "image":
		return whatsmeow.MediaImage, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	default:
		return "", fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func validateSendFileInput(path, kind string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if kind == "" {
		return fmt.Errorf("kind is required")
	}
	if kind == "document" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file")
	}
	defer f.Close()

	header := make([]byte, 512)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to inspect file")
	}

	sniffedType := http.DetectContentType(header[:n])
	extType := ""
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		extType = mime.TypeByExtension(ext)
	}

	switch kind {
	case "image":
		if isExpectedMediaType(sniffedType, "image/") || isExpectedMediaType(extType, "image/") {
			return nil
		}
		return fmt.Errorf("kind=image but file does not appear to be an image")
	case "video":
		if isExpectedMediaType(sniffedType, "video/") || isExpectedMediaType(extType, "video/") {
			return nil
		}
		return fmt.Errorf("kind=video but file does not appear to be a video")
	default:
		return fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func isExpectedMediaType(actual, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actual)), prefix)
}

func detectMIMEType(path string, data []byte, fallback string) string {
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return fallback
}

func buildOutgoingMediaMessage(kind, path, caption string, upload whatsmeow.UploadResponse, data []byte) (*waE2E.Message, map[string]any, error) {
	mimeType := detectMIMEType(path, data, "application/octet-stream")
	fileName := filepath.Base(path)
	switch kind {
	case "image":
		msg := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"imageMessage": map[string]any{
				"fileName": fileName,
				"caption":  caption,
				"mimetype": mimeType,
			},
		}, nil
	case "video":
		msg := &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"videoMessage": map[string]any{
				"fileName": fileName,
				"caption":  caption,
				"mimetype": mimeType,
			},
		}, nil
	case "document":
		msg := &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				Caption:       proto.String(caption),
				Title:         proto.String(fileName),
				FileName:      proto.String(fileName),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"documentMessage": map[string]any{
				"caption":  caption,
				"fileName": fileName,
				"mimetype": mimeType,
			},
		}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func quotedText(m *waE2E.Message) string {
	m = effectiveMessage(m)
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if m.GetImageMessage() != nil {
		return "[image]"
	}
	if m.GetVideoMessage() != nil {
		return "[video]"
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		if doc.GetFileName() != "" {
			return "[file: " + doc.GetFileName() + "]"
		}
		return "[document]"
	}
	if aud := m.GetAudioMessage(); aud != nil {
		if aud.GetPTT() {
			return "[voice]"
		}
		return "[audio]"
	}
	if m.GetStickerMessage() != nil {
		return "[sticker]"
	}
	return "[message]"
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

func (a *App) handleBlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rawChatID := strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(rawChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	if strings.HasSuffix(req.ChatID, "@g.us") || req.ChatID == "status@broadcast" {
		writeErr(w, http.StatusBadRequest, "cannot block group or status chats")
		return
	}
	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	jid = jid.ToNonAD()
	log.Printf("handleBlock: raw=%s canonical=%s parsed=%s server=%s", rawChatID, req.ChatID, jid.String(), jid.Server)
	var altJID types.JID
	_, err = a.client.UpdateBlocklist(context.Background(), jid, events.BlocklistChangeActionBlock)
	if err != nil {
		if jid.Server == types.DefaultUserServer && a.client.Store != nil && a.client.Store.LIDs != nil {
			if l, errAlt := a.client.Store.LIDs.GetLIDForPN(context.Background(), jid); errAlt == nil && l.User != "" {
				altJID = l
			}
		} else if jid.Server == types.HiddenUserServer && a.client.Store != nil && a.client.Store.LIDs != nil {
			if p, errAlt := a.client.Store.LIDs.GetPNForLID(context.Background(), jid); errAlt == nil && p.User != "" {
				altJID = p
			}
		}
		if altJID.User != "" {
			_, err = a.client.UpdateBlocklist(context.Background(), altJID, events.BlocklistChangeActionBlock)
		}
	}
	if err != nil {
		errMsg := fmt.Sprintf("failed to block %s (alt: %s): %s", jid.String(), altJID.String(), err.Error())
		writeErr(w, http.StatusInternalServerError, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jidStr := r.URL.Query().Get("jid")
	if !strings.HasSuffix(jidStr, "@g.us") {
		writeErr(w, http.StatusBadRequest, "jid must be a group JID ending in @g.us")
		return
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}
	info, err := a.client.GetGroupInfo(r.Context(), jid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to get group info: "+err.Error())
		return
	}

	a.mu.RLock()
	contacts := make(map[string]Contact, len(a.state.Contacts))
	for k, v := range a.state.Contacts {
		contacts[k] = v
	}
	a.mu.RUnlock()

	resolveName := func(p types.GroupParticipant) (name string, saved bool) {
		phoneJID := p.PhoneNumber
		if phoneJID.IsEmpty() {
			phoneJID = p.JID
		}
		key := phoneJID.User + "@" + phoneJID.Server
		if ct, ok := contacts[key]; ok {
			if n := strings.TrimSpace(ct.Notify); n != "" {
				return n, true
			}
			if n := strings.TrimSpace(ct.Name); n != "" {
				return n, true
			}
		}
		return phoneJID.User, false
	}

	var savedNames, unknownNames []string
	for _, p := range info.Participants {
		name, isSaved := resolveName(p)
		if isSaved {
			savedNames = append(savedNames, name)
		} else {
			unknownNames = append(unknownNames, name)
		}
	}

	// Up to 4: saved contacts first, pad with unknown numbers if needed.
	members := make([]string, 0, 4)
	members = append(members, savedNames...)
	if len(members) > 4 {
		members = members[:4]
	}
	if len(members) < 4 && len(unknownNames) > 0 {
		need := 4 - len(members)
		if need > len(unknownNames) {
			need = len(unknownNames)
		}
		members = append(members, unknownNames[:need]...)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"members": members,
		"total":   len(info.Participants),
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
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
