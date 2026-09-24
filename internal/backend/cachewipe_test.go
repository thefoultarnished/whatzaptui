package backend

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// wipeCacheDirExceptLogs must remove everything under the cache dir except
// the actionLog's own "logs" subdirectory.
func TestWipeCacheDirExceptLogsPreservesLogsDir(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "store.db"), "db")
	mustWriteFile(t, filepath.Join(dir, "store.db-wal"), "wal")
	mustWriteFile(t, filepath.Join(dir, "session.token"), "tok")
	mustWriteFile(t, filepath.Join(dir, "logs", "backend-x.log"), "log line")

	if err := wipeCacheDirExceptLogs(dir); err != nil {
		t.Fatalf("wipeCacheDirExceptLogs: %v", err)
	}

	for _, gone := range []string{"store.db", "store.db-wal", "session.token"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been wiped, stat err = %v", gone, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "logs", "backend-x.log"))
	if err != nil || string(b) != "log line" {
		t.Fatalf("logs/backend-x.log should survive untouched: content=%q err=%v", b, err)
	}
}

// A missing cache dir (never initialized, or already gone) is a no-op, not
// an error — matches the old os.RemoveAll behavior callers relied on.
func TestWipeCacheDirExceptLogsMissingDirIsNoop(t *testing.T) {
	if err := wipeCacheDirExceptLogs(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("missing dir should be a no-op, got: %v", err)
	}
}

// The bug this fixes: Windows refuses to delete a file that's still open
// (no FILE_SHARE_DELETE), and the actionLog keeps its file open for the
// life of the backend process. A whole-directory RemoveAll that includes
// logs/ therefore fails the instant a real session has generated any log
// output — which is every session, since session.start is written
// immediately at boot. wipeCacheDirExceptLogs must succeed regardless of
// what's held open under logs/.
func TestWipeCacheDirExceptLogsSurvivesOpenLogFile(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "store.db"), "db")
	logPath := filepath.Join(dir, "logs", "backend-x.log")
	mustWriteFile(t, logPath, "session.start\n")

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer f.Close()

	if err := wipeCacheDirExceptLogs(dir); err != nil {
		t.Fatalf("wipeCacheDirExceptLogs with an open log file: %v", err)
	}
	if _, err := f.WriteString("still writable after wipe\n"); err != nil {
		t.Fatalf("log handle should still be writable after wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "store.db")); !os.IsNotExist(err) {
		t.Fatal("store.db should have been wiped")
	}
}

// End-to-end reproduction of the actual /logout failure: a live actionLog
// (the same one main() opens at boot, in the same cacheDir logout wipes)
// must survive resetPersistentStorage, and the DB must come back usable.
func TestResetPersistentStorageSurvivesLiveActionLog(t *testing.T) {
	dir := t.TempDir()
	al, err := openActionLog(dir, time.Now(), nil)
	if err != nil {
		t.Fatalf("openActionLog: %v", err)
	}
	defer al.Close()

	app := &App{cacheDir: dir, actionLog: al}
	if err := app.initPersistentResources(); err != nil {
		t.Fatalf("initPersistentResources: %v", err)
	}

	// Mirrors real traffic: the actionLog is still being written right up
	// to the moment logout wipes its own directory.
	al.Event("http.req", map[string]string{"path": "/logout"})
	// initPersistentResources starts backfillFTS in the background,
	// unsynchronized with Close (see store.TestCloseRacesBackfillFTS...
	// for that specific race). Give it a moment to finish against this
	// trivially small test DB so this test stays about the logs-open-file
	// wipe bug it's named for, not that separate, already-covered race.
	time.Sleep(200 * time.Millisecond)

	// Real /logout (resetRuntimeState, server.go) closes storeContainer and
	// store BEFORE calling resetPersistentStorage; mirror that ordering so
	// this test exercises the actual call path instead of the leftover
	// whatsmeow container connection tripping an unrelated open-handle error.
	if err := app.storeContainer.Close(); err != nil {
		t.Fatalf("close storeContainer: %v", err)
	}
	app.storeContainer = nil

	if err := app.resetPersistentStorage(); err != nil {
		t.Fatalf("resetPersistentStorage: %v (this is the exact /logout 500 the fix addresses)", err)
	}
	defer func() {
		if app.storeContainer != nil {
			_ = app.storeContainer.Close()
		}
		if app.store != nil {
			_ = app.store.Close()
		}
	}()

	if app.db == nil {
		t.Fatal("resetPersistentStorage must reinitialize a.db")
	}
	if err := app.db.Ping(); err != nil {
		t.Fatalf("reinitialized db not usable: %v", err)
	}
	al.Event("historysync.done", nil)
	if _, err := os.Stat(al.Path()); err != nil {
		t.Fatalf("actionLog file should still exist after the wipe: %v", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
