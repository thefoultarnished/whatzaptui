package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Store encapsulates SQLite database operations, schema migrations, and queries.
type Store struct {
	db *sql.DB
}

// Open initializes and configures a SQLite connection with WAL mode and runs migrations.
func Open(dbPath string) (*Store, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("store: database path is empty")
	}

	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	rawDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}

	// SQLite WAL mode with a single writer connection avoids in-process locking contention.
	rawDB.SetMaxOpenConns(1)
	rawDB.SetMaxIdleConns(1)
	rawDB.SetConnMaxLifetime(0)

	if _, err := rawDB.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("store: set wal: %w", err)
	}
	if _, err := rawDB.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("store: set auto_vacuum: %w", err)
	}

	// Ensure file permissions are restricted to owner (on Unix; no-op on Windows).
	_ = os.Chmod(dbPath, 0o600)

	s := &Store{db: rawDB}
	if err := s.migrate(); err != nil {
		_ = rawDB.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	return s, nil
}

// Wrap creates a Store from an existing sql.DB handle (useful for testing or wrappers).
func Wrap(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying *sql.DB handle.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// Close closes the underlying SQLite database. It intentionally does not
// nil out s.db afterward: initPersistentResources starts BackfillFTS in a
// background goroutine, unsynchronized with Close, so a Close racing ahead
// of it (e.g. an immediate shutdown, or /logout moments after a fresh
// login) used to nil s.db out from under that goroutine's next s.db.Query
// call — a nil *sql.DB dereference panics, whereas a closed-but-non-nil
// one just returns a "sql: database is closed" error that BackfillFTS
// already handles via its normal err-return path. sql.DB.Close is
// documented idempotent, so a second Close() (e.g. this store's App-level
// caller also nils its own a.db field independently) stays safe.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Vacuum triggers incremental or full vacuuming on the database.
func (s *Store) Vacuum() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`PRAGMA incremental_vacuum(500)`)
	return err
}

// migrate creates required tables and indexes if they do not exist.
func (s *Store) migrate() error {
	if s.db == nil {
		return fmt.Errorf("store: database not open")
	}

	schema := `
		CREATE TABLE IF NOT EXISTS chat_permissions (
			phone   TEXT PRIMARY KEY,
			name    TEXT NOT NULL DEFAULT '',
			allowed INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS whitelist_default (
			id      INTEGER PRIMARY KEY CHECK (id = 1),
			allowed INTEGER NOT NULL DEFAULT 0
		);

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

		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			chat_id UNINDEXED,
			msg_id  UNINDEXED,
			from_me UNINDEXED,
			body,
			tokenize = 'porter unicode61'
		);

		CREATE TABLE IF NOT EXISTS fts_backfill_meta(
			key TEXT PRIMARY KEY,
			val INTEGER NOT NULL DEFAULT 0
		);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Migration for DBs created before the stored column existed.
	_, _ = s.db.Exec(`ALTER TABLE contacts ADD COLUMN stored INTEGER NOT NULL DEFAULT 0`)

	return nil
}
