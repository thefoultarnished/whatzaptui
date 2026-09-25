package backend

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

	"go.mau.fi/whatsmeow/appstate"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	_ "modernc.org/sqlite"
	"whatzap/internal/store"
	"whatzap/internal/whatsapp"
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

	s, err := store.Open(dbPath)
	if err != nil {
		return err
	}

	container, err := sqlstore.New(context.Background(), "sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", waLog.Stdout("db", whatsmeowLogLevel, true))
	if err != nil {
		_ = s.Close()
		return err
	}
	device, err := container.GetFirstDevice(context.Background())
	if err != nil {
		_ = s.Close()
		return err
	}

	a.store = s
	a.db = s.DB()
	a.storeContainer = container
	// Async so a large first-run FTS build doesn't delay /health and TUI
	// startup. backfillFTS only touches rows older than its start cutoff
	// and search dedupes, so racing live inserts can't create dupes.
	go a.backfillFTS()
	a.client = whatsapp.NewClient(device, whatsmeowLogLevel)
	a.bindEvents()
	return nil
}

func (a *App) resetPersistentStorage() error {
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			return err
		}
		a.store = nil
		a.db = nil
	} else if a.db != nil {
		if err := a.db.Close(); err != nil {
			return err
		}
		a.db = nil
	}

	if strings.TrimSpace(a.cacheDir) != "" {
		if err := wipeCacheDirExceptLogs(a.cacheDir); err != nil {
			return err
		}
		if err := os.MkdirAll(a.cacheDir, 0o700); err != nil {
			return err
		}
		return a.initPersistentResources()
	}
	return nil
}

// wipeCacheDirExceptLogs removes every entry directly under dir except the
// actionLog's own "logs" subdirectory. Logout used to os.RemoveAll(dir)
// wholesale, which includes the actionLog's currently-open log file —
// Windows refuses to delete a file another handle still has open, so that
// RemoveAll failed with a generic "used by another process" error the
// moment a session had generated enough log lines to still be flushing at
// wipe time. Every /logout then came back "backend cleanup failed", and
// whatever had already been removed (session.token, store.db) stayed gone
// while the still-open logs/ directory blocked the rest, leaving cacheDir
// half-wiped. Logs are meant to survive a logout anyway — they are the
// diagnostic trail for exactly this kind of failure.
func wipeCacheDirExceptLogs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.Name() == actionLogDir {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
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
	if a.store != nil {
		return a.store.UpsertChat(store.ChatRecord{
			ID:                    chat.ID,
			Name:                  chat.Name,
			Subject:               chat.Subject,
			ConversationTimestamp: chat.ConversationTimestamp,
			UnreadCount:           chat.UnreadCount,
		})
	}
	if a.db == nil {
		return nil
	}
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO chats (id, name, subject, conv_ts, unread_count)
		VALUES (?, ?, ?, ?, ?)
	`, chat.ID, chat.Name, chat.Subject, chat.ConversationTimestamp, chat.UnreadCount)
	return err
}

func (a *App) upsertContactToDB(contact Contact) error {
	if a.store != nil {
		return a.store.UpsertContact(store.ContactRecord{
			ID:     contact.ID,
			Name:   contact.Name,
			Notify: contact.Notify,
			Stored: contact.Stored,
		})
	}
	if a.db == nil {
		return nil
	}
	_, err := a.db.Exec(`
		INSERT OR REPLACE INTO contacts (id, name, notify, stored)
		VALUES (?, ?, ?, ?)
	`, contact.ID, contact.Name, contact.Notify, boolToInt(contact.Stored))
	return err
}

func (a *App) loadChatsFromDB() (map[string]Chat, error) {
	if a.store != nil {
		records, err := a.store.LoadChats()
		if err != nil {
			return nil, err
		}
		chats := make(map[string]Chat, len(records))
		for k, r := range records {
			chats[k] = Chat{
				ID:                    r.ID,
				Name:                  r.Name,
				Subject:               r.Subject,
				ConversationTimestamp: r.ConversationTimestamp,
				UnreadCount:           r.UnreadCount,
			}
		}
		return chats, nil
	}
	if a.db == nil {
		return map[string]Chat{}, nil
	}
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
	if a.store != nil {
		records, err := a.store.LoadContacts()
		if err != nil {
			return nil, err
		}
		contacts := make(map[string]Contact, len(records))
		for k, r := range records {
			contacts[k] = Contact{
				ID:     r.ID,
				Name:   r.Name,
				Notify: r.Notify,
				Stored: r.Stored,
			}
		}
		return contacts, nil
	}
	if a.db == nil {
		return map[string]Contact{}, nil
	}
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
	// Slow work (LID resolution hits the store DB, MAX(ts) hits messages)
	// must stay outside the write lock: holding a.mu across per-group
	// lookups freezes every other request (even /health) for the whole
	// run. So collect everything first, then apply under one brief lock.
	type groupUpdate struct {
		cid        string
		name       string
		topic      string
		maxTS      int64
		needsMaxTS bool
	}
	updates := make([]groupUpdate, 0, len(groups))
	for _, g := range groups {
		if g == nil || g.JID.IsEmpty() {
			continue
		}
		cid := a.canonicalizeChatID(g.JID.String())
		if cid == "" {
			continue
		}
		u := groupUpdate{cid: cid, name: strings.TrimSpace(g.Name), topic: strings.TrimSpace(g.Topic)}
		a.mu.RLock()
		cur, ok := a.state.Chats[cid]
		a.mu.RUnlock()
		if !ok || cur.ConversationTimestamp == 0 {
			u.needsMaxTS = true
			_ = db.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM messages WHERE chat_id = ? AND ts > 0`, cid).Scan(&u.maxTS)
		}
		updates = append(updates, u)
	}
	a.mu.Lock()
	if a.shuttingDown || a.db == nil {
		a.mu.Unlock()
		return 0
	}
	for _, u := range updates {
		ch := a.state.Chats[u.cid]
		ch.ID = u.cid
		if u.name != "" && ch.Name != u.name {
			ch.Name = u.name
			changed++
		}
		if u.topic != "" && ch.Subject != u.topic {
			ch.Subject = u.topic
			changed++
		}
		if ch.ConversationTimestamp == 0 && u.needsMaxTS && u.maxTS > 0 {
			ch.ConversationTimestamp = u.maxTS
		}
		a.state.Chats[u.cid] = ch
		delete(a.state.Contacts, u.cid)
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
	if a.store != nil {
		_ = a.store.BackfillFTS(func(msgJSON string) string {
			var m map[string]any
			if err := json.Unmarshal([]byte(msgJSON), &m); err == nil {
				return extractSearchableText(m)
			}
			return ""
		})
		return
	}
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
	if a.store == nil && a.db == nil {
		return
	}
	go func() {
		time.Sleep(30 * time.Second)
		a.mu.RLock()
		s := a.store
		db := a.db
		shutting := a.shuttingDown
		a.mu.RUnlock()
		if (s == nil && db == nil) || shutting {
			return
		}
		if s != nil {
			_ = s.Vacuum()
		} else if db != nil {
			_, _ = db.Exec(`PRAGMA incremental_vacuum(100)`)
		}
	}()
}

// purgeContactsWithName clears chat_permissions.name rows that match the given
// name. Returns the number of rows affected.
func (a *App) purgeContactsWithName(name string) (int64, error) {
	if a.store != nil {
		return a.store.PurgePermissionName(name)
	}
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

// purgeGroupSenderNames clears chat_permissions.name on group rows. Legacy
// bug: group messages wrote the sender's push name into the group's row, and
// the TUI prefers that name over the group subject. Runs once (marker in
// fts_backfill_meta) so a later /rename of a group isn't wiped on every start.
func (a *App) purgeGroupSenderNames() {
	if a.db == nil {
		return
	}
	if _, err := a.db.Exec(`CREATE TABLE IF NOT EXISTS fts_backfill_meta(key TEXT PRIMARY KEY, val INTEGER NOT NULL DEFAULT 0)`); err != nil {
		log.Printf("purgeGroupSenderNames: %v", err)
		return
	}
	var done int
	_ = a.db.QueryRow(`SELECT val FROM fts_backfill_meta WHERE key = 'group_name_purge'`).Scan(&done)
	if done == 1 {
		return
	}
	res, err := a.db.Exec(`UPDATE chat_permissions SET name = ''
		WHERE name != '' AND phone IN (
			SELECT substr(id, 1, length(id) - 5) FROM chats WHERE id LIKE '%@g.us')`)
	if err != nil {
		log.Printf("purgeGroupSenderNames: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("purgeGroupSenderNames: cleared %d group row(s)", n)
	}
	if _, err := a.db.Exec(`INSERT INTO fts_backfill_meta(key, val) VALUES ('group_name_purge', 1)
		ON CONFLICT(key) DO UPDATE SET val = 1`); err != nil {
		log.Printf("purgeGroupSenderNames marker: %v", err)
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
	a.purgeGroupSenderNames()
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
	if a.client == nil || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return
	}
	a.needsBootstrapSync = false
	a.mu.Unlock()

	a.broadcast(EventEnvelope{Type: "status", Payload: "Syncing chats and contacts..."})
	finish := a.actionLog.Timed("bootstrap", map[string]string{})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if a.client == nil || a.client.Store == nil {
		finish("no-store", nil)
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
	seeded := a.seedContactsFromStore(ctx, a.client.Store.Contacts.GetAllContacts)

	if seeded > 0 {
		a.persistState()
		a.broadcast(EventEnvelope{Type: "contacts:updated"})
		a.broadcast(EventEnvelope{Type: "chats:loaded"})
		a.broadcast(EventEnvelope{Type: "status", Payload: "Sync complete"})
	}
	finish("ok", map[string]string{
		"seeded":   intStr(seeded),
		"contacts": intStr(len(a.state.Contacts)),
		"chats":    intStr(len(a.state.Chats)),
	})
}

// seedContactsFromStore additively merges getAllContacts (whatsmeow's own
// address-book table) into a.state.Contacts/Chats, marking a contact
// Stored when it carries a real saved name (not just a push name). Shared
// by bootstrapFromStore (the initial pass, run moments after connecting)
// and runContactReseed (re-run whenever WhatsApp finishes pushing more app
// state) because the address book often keeps arriving in the background
// for a while after the initial bootstrap already returned — see
// handleAppStateSyncComplete.
//
// getAllContacts is a parameter (not a.client.Store.Contacts.GetAllContacts
// read inline) so this is unit-testable without a live whatsmeow client.
// LID resolution hits the store DB per contact, so every entry is resolved
// before a.mu is taken: holding a.mu across that would freeze every other
// request (even /health) for the whole pass, the same class of bug fixed
// in the history-sync deadlock (see applyHistorySync).
func (a *App) seedContactsFromStore(ctx context.Context, getAllContacts func(context.Context) (map[types.JID]types.ContactInfo, error)) int {
	if getAllContacts == nil {
		return 0
	}
	allContacts, err := getAllContacts(ctx)
	if err != nil {
		return 0
	}

	type seedEntry struct {
		cid                                         string
		fullName, firstName, businessName, pushName string
	}
	entries := make([]seedEntry, 0, len(allContacts))
	for jid, info := range allContacts {
		raw := strings.TrimSpace(jid.String())
		if raw == "" {
			continue
		}
		entries = append(entries, seedEntry{
			cid:          a.canonicalizeChatID(raw),
			fullName:     strings.TrimSpace(info.FullName),
			firstName:    strings.TrimSpace(info.FirstName),
			businessName: strings.TrimSpace(info.BusinessName),
			pushName:     strings.TrimSpace(info.PushName),
		})
	}
	a.mu.Lock()
	seeded := 0
	for _, e := range entries {
		ch, hasChat := a.state.Chats[e.cid]
		if hasChat && ch.ConversationTimestamp == 0 {
			hasChat = false
		}
		if e.fullName == "" && e.firstName == "" && e.businessName == "" && !hasChat {
			continue
		}

		name := e.fullName
		if name == "" {
			name = e.firstName
		}
		if name == "" {
			name = e.businessName
		}
		if name == "" {
			name = e.pushName
		}

		ct := a.state.Contacts[e.cid]
		ct.ID = e.cid
		if name != "" && ct.Notify == "" {
			ct.Notify = name
		}
		if e.fullName != "" || e.firstName != "" || e.businessName != "" {
			ct.Stored = true
		}
		a.state.Contacts[e.cid] = ct

		// Do not write a Chats entry here: a saved-contact name alone
		// (no hasChat) must not synthesize a phantom zero-message chat
		// — same bug as the historysync pushname path (events.go). A
		// real chat's name is already filled in from Contacts at serve
		// time (handleChats), so writing it here again was redundant
		// for real chats and actively wrong for everyone else.
		seeded++
	}
	a.mu.Unlock()
	return seeded
}

// contactReseedDebounce coalesces a burst of AppStateSyncComplete events
// (WhatsApp fires one per app-state patch category, often several within
// a second) into a single reseed pass. Var, not const, so tests can
// shrink it.
var contactReseedDebounce = 2 * time.Second

// handleAppStateSyncComplete is called for every events.AppStateSyncComplete
// whatsmeow dispatches — both the ones bootstrapFromStore triggers directly
// and the ones whatsmeow triggers on its own later, when the server pushes
// a "server_sync" notification with a newer app-state version (this is how
// a large address book that didn't finish syncing within the first ~25s
// bootstrap window still reaches the People tab, without a manual
// /synccontacts). Debounced via scheduleContactReseed.
func (a *App) handleAppStateSyncComplete(name appstate.WAPatchName) {
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	a.mu.RUnlock()
	if shuttingDown {
		return
	}
	a.actionLog.Event("appstate.sync.complete", map[string]string{"patch": string(name)})
	a.scheduleContactReseed()
}

// scheduleContactReseed (re)arms a single debounce timer so a burst of
// events collapses into one reseed run. onContactReseed lets tests observe
// invocations without a live whatsmeow client; nil (the normal case) runs
// the real runContactReseed.
func (a *App) scheduleContactReseed() {
	a.contactReseedMu.Lock()
	defer a.contactReseedMu.Unlock()
	if a.contactReseedTimer != nil {
		a.contactReseedTimer.Stop()
	}
	run := a.runContactReseed
	if a.onContactReseed != nil {
		run = a.onContactReseed
	}
	a.contactReseedTimer = time.AfterFunc(contactReseedDebounce, run)
}

// runContactReseed is scheduleContactReseed's default action: re-run
// seedContactsFromStore and, if it found anything new, persist and tell
// the TUI to refetch. Reads a.client through a local snapshot rather than
// re-reading the field mid-call, so a concurrent logout nil-ing it out
// can't race this goroutine onto a nil pointer.
func (a *App) runContactReseed() {
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	client := a.client
	a.mu.RUnlock()
	if shuttingDown || client == nil || !client.IsLoggedIn() || client.Store == nil || client.Store.Contacts == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	seeded := a.seedContactsFromStore(ctx, client.Store.Contacts.GetAllContacts)
	a.actionLog.Event("contacts.reseed", map[string]string{"seeded": intStr(seeded)})
	if seeded == 0 {
		return
	}
	if err := a.persistStateWithErr(); err != nil {
		log.Printf("persistState: %v", err)
	}
	a.broadcast(EventEnvelope{Type: "contacts:updated"})
	a.broadcast(EventEnvelope{Type: "chats:loaded"})
}

func (a *App) safeFetchAppState(ctx context.Context, name appstate.WAPatchName) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("bootstrap: recovered panic during app state fetch %s: %v", name, r)
			a.actionLog.Event("bootstrap.appstate.panic", map[string]string{
				"patch": string(name),
				"err":   fmt.Sprintf("%v", r),
			})
		}
	}()
	if err := a.client.FetchAppState(ctx, name, true, false); err != nil {
		log.Printf("bootstrap: failed to fetch app state %s: %v", name, err)
		a.actionLog.Event("bootstrap.appstate.fail", map[string]string{
			"patch": string(name),
			"err":   truncateErr(err),
		})
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
	a.dbLifecycleMu.RLock()
	defer a.dbLifecycleMu.RUnlock()
	return a.persistStateWithErrUnlocked()
}

func (a *App) persistStateWithErrUnlocked() error {
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
	a.dbLifecycleMu.RLock()
	defer a.dbLifecycleMu.RUnlock()
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

// upsertPermission records that a sender exists (allowed stays 0) and keeps
// the auto-captured push name fresh. The row is only (re)written when its
// stored name is empty or still matches prevName — the push name last
// captured for this contact. Anything else was set by the user via /rename
// and is left alone, so a contact who changes their WhatsApp name picks up
// the new name on their next message instead of keeping the first one forever.
func (a *App) upsertPermission(phone, name, prevName string) {
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
		`INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, 0)
		 ON CONFLICT(phone) DO UPDATE SET name = excluded.name
		 WHERE excluded.name != ''
		   AND (chat_permissions.name = '' OR chat_permissions.name = ?)`,
		phone, name, prevName,
	); err != nil {
		log.Printf("upsertPermission %s: %v", phone, err)
	}
}

// withPermissionDB holds dbLifecycleMu.RLock() for the entire duration of fn
// so that logout cannot close a.db while the caller is still using it (logout
// takes dbLifecycleMu.Lock() to set shuttingDown before closing anything).
// a.mu is only held briefly to read the handle: holding it while fn waits for
// the single SQLite connection deadlocks against a history-sync transaction
// that owns the connection and needs a.mu.Lock() to update chat state.
func (a *App) withPermissionDB(fn func(*sql.DB) error) error {
	if a == nil {
		return fmt.Errorf("permission store unavailable")
	}
	a.dbLifecycleMu.RLock()
	defer a.dbLifecycleMu.RUnlock()
	a.mu.RLock()
	shuttingDown := a.shuttingDown
	db := a.db
	a.mu.RUnlock()
	if shuttingDown || db == nil {
		return fmt.Errorf("permission store unavailable")
	}
	return fn(db)
}
