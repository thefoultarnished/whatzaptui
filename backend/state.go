package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
)

// A-13: in-memory map caps for unbounded-growth maps. Trimmed on
// a 30s ticker in main(). Set to 0 to disable cap.
var maxChatCaps = 1000
var maxContactCaps = 5000
var maxLIDCacheCaps = 10000

// cappedEvict is a generic helper that trims a map to at most max
// entries by deleting random keys. Pass max <= 0 to disable. The
// random-order iteration of Go maps provides a uniform distribution
// for eviction — at a 1k/5k/10k cap the probability of hitting a
// recently-used entry in any single tick is negligible.
func cappedEvict[K comparable, V any](m map[K]V, max int) {
	if max <= 0 {
		return
	}
	for len(m) > max {
		for k := range m {
			delete(m, k)
			break
		}
	}
}

func (a *App) initPersistentResources() error {
	cacheDir := strings.TrimSpace(a.cacheDir)
	if cacheDir == "" {
		return fmt.Errorf("cache directory is not configured")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	a.cacheDir = cacheDir

	dbPath := filepath.Join(cacheDir, "store.db")

	rawDB, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	// Single-writer: SQLite/WAL allows one writer; cap pool to 1 so
	// concurrent writers serialize in-process instead of racing.
	rawDB.SetMaxOpenConns(1)
	rawDB.SetMaxIdleConns(1)
	rawDB.SetConnMaxLifetime(0)
	if _, err := rawDB.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = rawDB.Close()
		return err
	}
	if _, err := rawDB.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		_ = rawDB.Close()
		return err
	}

	// S-11: restrict the database file to owner-only. The PRAGMAs
	// above guarantee the file exists (sql.Open creates it lazily
	// on the first query). Ignore error — this is meaningful only
	// on Unix; on Windows os.Chmod is a no-op and the error is
	// always nil.
	_ = os.Chmod(dbPath, 0o600)

	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", waLog.Stdout("db", whatsmeowLogLevel, true))
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
			notify TEXT NOT NULL DEFAULT '',
			stored INTEGER NOT NULL DEFAULT 0
		);
	`); err != nil {
		_ = rawDB.Close()
		return err
	}
	// Migration for DBs created before the stored column existed.
	_, _ = rawDB.Exec(`ALTER TABLE contacts ADD COLUMN stored INTEGER NOT NULL DEFAULT 0`)
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
	// Async so a large first-run FTS build doesn't delay /health and TUI
	// startup. backfillFTS only touches rows older than its start cutoff
	// and search dedupes, so racing live inserts can't create dupes.
	go a.backfillFTS()
	a.client = whatsmeow.NewClient(device, waLog.Stdout("client", whatsmeowLogLevel, true))
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

func (a *App) upsertChatToDB(chat Chat) error {
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO chats (id, name, subject, conv_ts, unread_count)
		VALUES (?, ?, ?, ?, ?)
	`, chat.ID, chat.Name, chat.Subject, chat.ConversationTimestamp, chat.UnreadCount)
	return err
}

func (a *App) upsertContactToDB(contact Contact) error {
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO contacts (id, name, notify, stored)
		VALUES (?, ?, ?, ?)
	`, contact.ID, contact.Name, contact.Notify, boolToInt(contact.Stored))
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
	rows, err := a.db.Query(`SELECT id, name, notify, stored FROM contacts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := map[string]Contact{}
	for rows.Next() {
		var c Contact
		var stored int
		if err := rows.Scan(&c.ID, &c.Name, &c.Notify, &stored); err != nil {
			return nil, err
		}
		c.Stored = stored != 0
		contacts[c.ID] = c
	}
	return contacts, rows.Err()
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

// backfillFTS populates the messages_fts index from the messages table on
// first run after the FTS feature was added. No-op if FTS already has rows or
// if there are no messages.
func (a *App) backfillFTS() {
	if a.db == nil {
		return
	}
	// Resume watermark: max messages.rowid already examined. Replaces the
	// old "skip when FTS non-empty" check, which raced with live inserts
	// once the backfill moved off the startup path.
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS fts_backfill_meta(key TEXT PRIMARY KEY, val INTEGER NOT NULL DEFAULT 0)`); err != nil {
		log.Printf("backfillFTS meta table: %v", err)
		return
	}
	var watermark int64
	_ = a.db.QueryRow(`SELECT val FROM fts_backfill_meta WHERE key = 'rowid'`).Scan(&watermark)

	type ftsRow struct {
		chatID, msgID, body string
		fromMe              int
	}

	// Only rows older than this cutoff: live inserts racing the backfill
	// are newer and write their own FTS rows, so the sets stay disjoint.
	cutoff := time.Now().Unix()
	limit := 1000
	lastRowID := watermark
	for {
		rows, err := a.db.Query(`SELECT rowid, id, chat_id, from_me, message_json FROM messages WHERE rowid > ? AND ts <= ? ORDER BY rowid LIMIT ?`, lastRowID, cutoff, limit)
		if err != nil {
			log.Printf("backfillFTS scan query: %v", err)
			break
		}
		var pending []ftsRow
		scanned := 0
		for rows.Next() {
			var rowID int64
			var id, chatID, msgJSON string
			var fromMe int
			if err := rows.Scan(&rowID, &id, &chatID, &fromMe, &msgJSON); err != nil {
				continue
			}
			lastRowID = rowID
			scanned++
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

		if len(pending) > 0 {
			tx, err := a.db.Begin()
			if err != nil {
				log.Printf("backfillFTS tx: %v", err)
				break
			}
			// Delete-then-insert per page (atomic): a live writer may
			// have indexed these rows first, and a crashed/resumed
			// backfill may revisit them — either way we converge to
			// exactly one FTS row per triple, never duplicates.
			for _, r := range pending {
				if _, err := tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, r.chatID, r.msgID, r.fromMe); err != nil {
					log.Printf("backfillFTS delete: %v", err)
				}
			}
			for _, r := range pending {
				if _, err := tx.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`, r.chatID, r.msgID, r.fromMe, r.body); err != nil {
					log.Printf("backfillFTS insert: %v", err)
				}
			}
			if err := tx.Commit(); err != nil {
				log.Printf("backfillFTS commit: %v", err)
				break
			}
		}
		// Advance the watermark past every examined row, even textless
		// ones, so the next boot resumes instead of rescanning.
		if _, err := a.db.Exec(`INSERT INTO fts_backfill_meta(key, val) VALUES ('rowid', ?)
			ON CONFLICT(key) DO UPDATE SET val=excluded.val`, lastRowID); err != nil {
			log.Printf("backfillFTS watermark: %v", err)
			break
		}

		if scanned < limit {
			break
		}
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

// purgeInvisibleProtocolMessages deletes stored protocol control messages
// (history sync notifications, peer data op responses, key shares, ...) that
// should never appear as chat content, plus their orphaned FTS rows.
// Runs every startup — the DELETE is a no-op when there is nothing to clean.
func (a *App) purgeInvisibleProtocolMessages() {
	if a.db == nil {
		return
	}
	res, err := a.db.Exec(`DELETE FROM messages
		WHERE json_extract(message_json, '$.protocolMessage.type') IS NOT NULL
		AND json_extract(message_json, '$.protocolMessage.type') NOT IN ('REVOKE', 'MESSAGE_EDIT', 'EPHEMERAL_SETTING')`)
	if err != nil {
		log.Printf("purgeInvisibleProtocol: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("purgeInvisibleProtocol: removed %d control message(s)", n)
		if _, err := a.db.Exec(`DELETE FROM messages_fts WHERE NOT EXISTS (
			SELECT 1 FROM messages m WHERE m.chat_id = messages_fts.chat_id
			AND m.id = messages_fts.msg_id AND m.from_me = messages_fts.from_me)`); err != nil {
			log.Printf("purgeInvisibleProtocol fts cleanup: %v", err)
		}
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
	// Drop stored protocol plumbing (history sync notifications, key shares)
	// that pre-fix builds saved as visible chat messages. One-time.
	a.purgeInvisibleProtocolMessages()
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
		fetchAppStates([]appstate.WAPatchName{
			appstate.WAPatchCriticalBlock,
			appstate.WAPatchRegularLow,
			appstate.WAPatchRegularHigh,
			appstate.WAPatchRegular,
		}, a.safeFetchAppState)
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

		fullName := strings.TrimSpace(info.FullName)
		firstName := strings.TrimSpace(info.FirstName)
		businessName := strings.TrimSpace(info.BusinessName)
		pushName := strings.TrimSpace(info.PushName)

		ch, hasChat := a.state.Chats[cid]
		if hasChat && ch.ConversationTimestamp == 0 {
			hasChat = false
		}
		if fullName == "" && firstName == "" && businessName == "" && !hasChat {
			continue
		}

		name := fullName
		if name == "" {
			name = firstName
		}
		if name == "" {
			name = businessName
		}
		if name == "" {
			name = pushName
		}

		ct := a.state.Contacts[cid]
		ct.ID = cid
		if name != "" && ct.Notify == "" {
			ct.Notify = name
		}
		if fullName != "" || firstName != "" || businessName != "" {
			ct.Stored = true
		}
		a.state.Contacts[cid] = ct

		if ch.ID != "" {
			if name != "" && ch.Name == "" {
				ch.Name = name
				a.state.Chats[cid] = ch
			}
		}
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

// fetchAppStates fetches every patch concurrently so one slow patch stops
// delaying the others. All patches are attempted even if some fail; each
// fetch owns a 30s timeout. Extracted for unit testing the fan-out.
func fetchAppStates(patches []appstate.WAPatchName, fetch func(context.Context, appstate.WAPatchName)) {
	var wg sync.WaitGroup
	for _, patch := range patches {
		wg.Add(1)
		go func(p appstate.WAPatchName) {
			defer wg.Done()
			patchCtx, patchCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer patchCancel()
			fetch(patchCtx, p)
		}(patch)
	}
	wg.Wait()
}

func (a *App) startPersistWorker() {
	if a.stopPersist == nil {
		a.stopPersist = make(chan struct{})
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	go func(stop <-chan struct{}) {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if atomic.CompareAndSwapUint32(&a.persistDirty, 1, 0) {
					a.persistState()
				}
			case <-stop:
				return
			}
		}
	}(a.stopPersist)
}

func (a *App) stopPersistWorker() {
	a.stopPersistOnce.Do(func() {
		if a.stopPersist != nil {
			close(a.stopPersist)
		}
	})
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
	if err := a.persistStateWithErr(); err != nil {
		log.Printf("persistState: %v", err)
	}
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
			INSERT OR REPLACE INTO contacts (id, name, notify, stored)
			VALUES (?, ?, ?, ?)
		`, contact.ID, contact.Name, contact.Notify, boolToInt(contact.Stored)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// withTx runs fn inside a single SQLite transaction.
func (a *App) withTx(fn func(tx *sql.Tx) error) error {
	if a.db == nil {
		return fmt.Errorf("db not initialized")
	}
	tx, err := a.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
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
		a.enqueueLIDMigration(lid.User, pn.User)
	} else {
		a.lidCache[key] = ""
	}
	a.lidCacheMu.Unlock()

	return pn, err
}

func (a *App) enqueueLIDMigration(lidUser, pnUser string) {
	if a == nil || a.db == nil || lidUser == "" || pnUser == "" || lidUser == pnUser {
		return
	}
	a.lidMigrateOnce.Do(func() {
		a.lidMigrateJobs = make(chan lidMigrateJob, 256)
		go func() {
			for job := range a.lidMigrateJobs {
				a.migrateLIDPermissions(job.lidUser, job.pnUser)
			}
		}()
	})
	select {
	case a.lidMigrateJobs <- lidMigrateJob{lidUser: lidUser, pnUser: pnUser}:
	default:
	}
}

func (a *App) migrateLIDPermissions(lidUser, pnUser string) {
	if a == nil || a.db == nil || lidUser == "" || pnUser == "" || lidUser == pnUser {
		return
	}
	if err := a.withPermissionDB(func(db *sql.DB) error {
		var name string
		var allowed int
		err := db.QueryRow(`SELECT name, allowed FROM chat_permissions WHERE phone = ?`, lidUser).Scan(&name, &allowed)
		if err != nil {
			return nil
		}
		_, err = db.Exec(`
			INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)
			ON CONFLICT(phone) DO UPDATE SET name=excluded.name, allowed=excluded.allowed
		`, pnUser, name, allowed)
		if err != nil {
			log.Printf("migrateLIDPermissions db update: %v", err)
			return err
		}
		if _, err := db.Exec(`DELETE FROM chat_permissions WHERE phone = ?`, lidUser); err != nil {
			log.Printf("migrateLIDPermissions delete: %v", err)
		}
		return nil
	}); err != nil {
		log.Printf("migrateLIDPermissions: %v", err)
	}
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
	dst.Stored = dst.Stored || src.Stored
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
