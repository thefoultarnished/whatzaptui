package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func insertFTSFixture(t *testing.T, app *App, chatID, msgID string, ts int64, body string) {
	t.Helper()
	msgJSON := "{}"
	if body != "" {
		msgJSON = fmt.Sprintf(`{"conversation":%q}`, body)
	}
	if _, err := app.db.Exec(`INSERT OR IGNORE INTO messages (id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto)
		VALUES (?, ?, 0, '', ?, '', '', ?, '')`, msgID, chatID, ts, msgJSON); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
}

func ftsTripleCount(t *testing.T, app *App, chatID, msgID string) int {
	t.Helper()
	var n int
	if err := app.db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE chat_id = ? AND msg_id = ?`, chatID, msgID).Scan(&n); err != nil {
		t.Fatalf("count fts: %v", err)
	}
	return n
}

func TestBackfillIndexesTextSkipsEmptyAndFuture(t *testing.T) {
	app := newTestApp(t)
	old := time.Now().Add(-time.Hour).Unix()
	insertFTSFixture(t, app, "c1", "m1", old, "hello backfill world")
	insertFTSFixture(t, app, "c1", "m2", old, "")
	insertFTSFixture(t, app, "c1", "m3", time.Now().Add(time.Hour).Unix(), "future message excluded")

	app.backfillFTS()

	if n := ftsTripleCount(t, app, "c1", "m1"); n != 1 {
		t.Fatalf("text row fts count = %d, want 1", n)
	}
	if n := ftsTripleCount(t, app, "c1", "m2"); n != 0 {
		t.Fatalf("empty row fts count = %d, want 0", n)
	}
	if n := ftsTripleCount(t, app, "c1", "m3"); n != 0 {
		t.Fatalf("future row fts count = %d, want 0", n)
	}
}

func TestBackfillResumesFromWatermark(t *testing.T) {
	app := newTestApp(t)
	old := time.Now().Add(-time.Hour).Unix()
	insertFTSFixture(t, app, "c1", "m1", old, "first batch message")
	app.backfillFTS()

	insertFTSFixture(t, app, "c1", "m2", old, "second batch message")
	app.backfillFTS()

	if n := ftsTripleCount(t, app, "c1", "m1"); n != 1 {
		t.Fatalf("m1 fts count = %d, want 1", n)
	}
	if n := ftsTripleCount(t, app, "c1", "m2"); n != 1 {
		t.Fatalf("m2 fts count = %d, want 1", n)
	}
}

func TestBackfillConcurrentLiveWrites(t *testing.T) {
	app := newTestApp(t)
	old := time.Now().Add(-time.Hour).Unix()
	for i := 0; i < 20; i++ {
		insertFTSFixture(t, app, "c1", fmt.Sprintf("m%d", i), old, fmt.Sprintf("history message number %d", i))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		app.backfillFTS()
	}()
	// Live writes racing the backfill must not error and must converge:
	// upsert deletes-then-inserts, so each triple ends at exactly 1 row.
	for i := 20; i < 30; i++ {
		insertFTSFixture(t, app, "c1", fmt.Sprintf("m%d", i), time.Now().Unix(), fmt.Sprintf("live message number %d", i))
		app.upsertMessageFTS("c1", fmt.Sprintf("m%d", i), 0, fmt.Sprintf("live message number %d", i))
	}
	wg.Wait()

	for i := 0; i < 20; i++ {
		if n := ftsTripleCount(t, app, "c1", fmt.Sprintf("m%d", i)); n != 1 {
			t.Fatalf("history row m%d fts count = %d, want 1", i, n)
		}
	}
}
