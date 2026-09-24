package backend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newLoggedTestApp(t *testing.T) (*App, func() string) {
	t.Helper()
	dir := t.TempDir()
	al, err := openActionLog(dir, time.Now(), nil)
	if err != nil {
		t.Fatalf("openActionLog: %v", err)
	}
	t.Cleanup(func() { _ = al.Close() })
	app := &App{actionLog: al}
	read := func() string {
		b, err := os.ReadFile(al.Path())
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(b)
	}
	return app, read
}

func shrinkStallTimers(t *testing.T, after time.Duration) {
	t.Helper()
	oldAfter, oldGap := stallAfter, stackDumpMinGap
	stallAfter, stackDumpMinGap = after, 0
	lastStackDump.Store(0)
	t.Cleanup(func() {
		stallAfter, stackDumpMinGap = oldAfter, oldGap
		lastStackDump.Store(0)
	})
}

// A stuck operation must name its phase in the log and leave a goroutine
// dump next to it, so a hang can be pinpointed without a debugger.
func TestStallWatchLogsPhaseAndDumpsStacks(t *testing.T) {
	shrinkStallTimers(t, 30*time.Millisecond)
	app, read := newLoggedTestApp(t)

	w := app.startStallWatch("historysync")
	w.Phase("insert")
	time.Sleep(120 * time.Millisecond)
	w.Stop()
	w.Stop() // idempotent

	logText := read()
	if !strings.Contains(logText, "historysync.stall") || !strings.Contains(logText, "phase=insert") {
		t.Fatalf("stall event with phase missing from log:\n%s", logText)
	}
	if strings.Count(logText, "debug.stackdump ") != 1 {
		t.Fatalf("want exactly one stack dump per watch, log:\n%s", logText)
	}
	dumps, _ := filepath.Glob(filepath.Join(filepath.Dir(app.actionLog.Path()), "stack-*.txt"))
	if len(dumps) != 1 {
		t.Fatalf("stack dump files = %d, want 1", len(dumps))
	}
	b, _ := os.ReadFile(dumps[0])
	if !strings.Contains(string(b), "goroutine ") {
		t.Fatalf("dump does not look like a goroutine stack")
	}
}

// A watch stopped before stallAfter must stay silent.
func TestStallWatchQuietWhenFast(t *testing.T) {
	shrinkStallTimers(t, time.Second)
	app, read := newLoggedTestApp(t)
	w := app.startStallWatch("historysync")
	w.Stop()
	time.Sleep(20 * time.Millisecond)
	if strings.Contains(read(), ".stall") {
		t.Fatal("fast operation logged a stall")
	}
}

// Load-path requests are always logged (with a redacted chat), other fast
// requests are not, and a stuck request logs http.stall.
func TestRequestLogCoversLoadPath(t *testing.T) {
	shrinkStallTimers(t, 30*time.Millisecond)
	app, read := newLoggedTestApp(t)
	h := app.withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slowthing":
			time.Sleep(80 * time.Millisecond)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	for _, p := range []string{"/messages?chatId=15551230001@s.whatsapp.net", "/typing", "/broken", "/slowthing"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	logText := read()
	if !strings.Contains(logText, "http.req  chat=chat:phone:") || !strings.Contains(logText, "path=/messages") {
		t.Fatalf("/messages not logged with redacted chat:\n%s", logText)
	}
	if strings.Contains(logText, "15551230001") {
		t.Fatal("raw phone number leaked into the log")
	}
	if strings.Contains(logText, "path=/typing") {
		t.Fatal("fast non-load-path request should not be logged")
	}
	if !strings.Contains(logText, "path=/broken") || !strings.Contains(logText, "status=500") {
		t.Fatalf("5xx request not logged:\n%s", logText)
	}
	if !strings.Contains(logText, "http.stall") || !strings.Contains(logText, "path=/slowthing") {
		t.Fatalf("stuck request did not log http.stall:\n%s", logText)
	}
}
