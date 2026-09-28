package backend

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialTestWS connects to app's /ws endpoint and reads past the two initial
// sync messages ("ready", "chats:loaded") that handleWS sends to a client
// that connects while app.connected is true, so callers see only the
// broadcast events they actually care about.
func dialTestWS(t *testing.T, app *App, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	header := map[string][]string{
		authHeaderName: {"Bearer " + app.apiToken},
		"Origin":       {allowedBackendOrigin},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 2; i++ {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatalf("read initial sync message %d: %v", i, err)
		}
	}
	return conn
}

// --- Unit-level tests on wsClient.enqueue/stop (no network) ---

// Positive: enqueue succeeds while the queue has room, and runWriter-style
// draining sees events in the exact order they were enqueued.
func TestWSClientEnqueuePreservesOrder(t *testing.T) {
	c := newWSClient(nil)
	for i := 0; i < 5; i++ {
		if !c.enqueue([]byte(fmt.Sprintf("%d", i))) {
			t.Fatalf("enqueue %d: expected success, queue should have room", i)
		}
	}
	for i := 0; i < 5; i++ {
		select {
		case got := <-c.sendCh:
			if string(got) != fmt.Sprintf("%d", i) {
				t.Fatalf("drained order = %q, want %q", got, fmt.Sprintf("%d", i))
			}
		default:
			t.Fatalf("expected a queued message at position %d", i)
		}
	}
}

// Edge: once the queue is full, further enqueues are dropped (return
// false) instead of blocking the caller - this is the core of the fix,
// since the old code spawned an unbounded goroutine per event instead of
// ever exerting backpressure.
func TestWSClientEnqueueDropsWhenQueueFull(t *testing.T) {
	c := newWSClient(nil)
	for i := 0; i < wsSendQueueSize; i++ {
		if !c.enqueue([]byte("x")) {
			t.Fatalf("enqueue %d: expected success while under capacity", i)
		}
	}
	done := make(chan bool, 1)
	go func() { done <- c.enqueue([]byte("overflow")) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("enqueue on a full queue should return false (dropped), not succeed")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("enqueue on a full queue must not block the caller")
	}
}

// Negative: enqueueing after the client has been stopped must not panic
// and must report the event as dropped.
func TestWSClientEnqueueAfterStopIsSafe(t *testing.T) {
	c := newWSClient(nil)
	c.stop()
	if ok := c.enqueue([]byte("late")); ok {
		t.Fatal("enqueue after stop() should return false")
	}
	// Calling stop() again must not panic (sync.Once guards it).
	c.stop()
}

// --- Integration tests through the real /ws handler ---

// Positive: a broadcast event reaches a connected client, and multiple
// events arrive in the order they were broadcast (each client is drained
// by exactly one writer goroutine, so ordering is preserved).
func TestBroadcastDeliversEventsInOrder(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	conn := dialTestWS(t, app, srv)
	defer conn.Close()

	for i := 0; i < 10; i++ {
		app.broadcast(EventEnvelope{Type: "test", Payload: i})
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 10; i++ {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		var evt EventEnvelope
		if err := json.Unmarshal(data, &evt); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}
		got, ok := evt.Payload.(float64)
		if !ok || int(got) != i {
			t.Fatalf("event %d payload = %v, want %d", i, evt.Payload, i)
		}
	}
}

// Positive: a broadcast reaches every connected client, not just one.
func TestBroadcastDeliversToAllClients(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	connA := dialTestWS(t, app, srv)
	defer connA.Close()
	connB := dialTestWS(t, app, srv)
	defer connB.Close()

	app.broadcast(EventEnvelope{Type: "test", Payload: "hello"})

	for name, c := range map[string]*websocket.Conn{"A": connA, "B": connB} {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := c.ReadMessage(); err != nil {
			t.Fatalf("client %s did not receive broadcast: %v", name, err)
		}
	}
}

// Edge/regression: broadcasting many events no longer spawns a goroutine
// per client per event (the old, unbounded behavior). A slow client that
// never reads must not make the goroutine count grow with the number of
// events broadcast.
func TestBroadcastDoesNotLeakGoroutinesPerEvent(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	// This client never reads after the initial handshake, so its queue
	// will fill and further events for it are dropped, not queued as
	// goroutines.
	conn := dialTestWS(t, app, srv)
	defer conn.Close()

	// Let the writer goroutine settle, then take a baseline.
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	const events = 500
	for i := 0; i < events; i++ {
		app.broadcast(EventEnvelope{Type: "test", Payload: i})
	}
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	// The old per-event-per-client goroutine spawn would grow this by
	// roughly `events`. The fixed version uses one long-lived writer
	// goroutine per client, so growth should be tiny regardless of how
	// many events were broadcast.
	if grew := after - before; grew > 20 {
		t.Fatalf("goroutine count grew by %d after %d broadcasts, want a small bounded amount (no per-event goroutines)", grew, events)
	}
}

// Edge: a slow/non-reading client must not block delivery to other,
// actively-reading clients, and broadcast() itself must not block waiting
// on a stuck peer.
func TestBroadcastSlowClientDoesNotBlockOthers(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	slow := dialTestWS(t, app, srv)
	defer slow.Close()
	// slow never reads again from here on.

	active := dialTestWS(t, app, srv)
	defer active.Close()

	// Drain "active" concurrently with the broadcast loop below, the same
	// way a real, healthy TUI client would - otherwise "active" would fill
	// its own bounded queue just as "slow" does and this test would prove
	// nothing about the slow client's effect on it.
	const events = 200
	lastPayload := make(chan float64, 1)
	readErr := make(chan error, 1)
	go func() {
		_ = active.SetReadDeadline(time.Now().Add(5 * time.Second))
		var last float64 = -1
		for i := 0; i < events; i++ {
			_, data, err := active.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			var evt EventEnvelope
			if err := json.Unmarshal(data, &evt); err != nil {
				readErr <- err
				return
			}
			last, _ = evt.Payload.(float64)
		}
		lastPayload <- last
	}()
	// Give the reader goroutine a chance to start blocking on ReadMessage
	// before the loop below.
	time.Sleep(20 * time.Millisecond)

	// Pace the broadcasts like real events (message/receipt/typing), not
	// as one instant burst: a burst larger than wsSendQueueSize would
	// legitimately get dropped for every reader, healthy or not, since no
	// bounded queue can absorb an infinitely fast producer. That drop
	// behavior is correct and covered by TestWSClientEnqueueDropsWhenQueueFull;
	// this test is specifically about whether a *separate, stuck* client
	// can starve a normally-paced client, which pacing lets it show.
	start := time.Now()
	for i := 0; i < events; i++ {
		app.broadcast(EventEnvelope{Type: "test", Payload: i})
		time.Sleep(time.Millisecond)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("broadcast() took %v for %d events with a stuck client; it must not block on a slow peer", elapsed, events)
	}

	select {
	case err := <-readErr:
		t.Fatalf("active client read failed: %v", err)
	case last := <-lastPayload:
		if int(last) != events-1 {
			t.Fatalf("active client's last event = %v, want %d", last, events-1)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active client never received all events; the slow client must not starve it")
	}
}

// Negative: broadcasting to a client that disconnected must not panic,
// and must not double-close the underlying connection (mirrors Bug Audit
// #21, exercised here directly against removeWSClient/enqueue).
func TestBroadcastAfterClientGoneDoesNotPanic(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	conn := dialTestWS(t, app, srv)
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		app.wsMu.Lock()
		n := len(app.wsClients)
		app.wsMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never removed the closed client from wsClients")
		}
		time.Sleep(10 * time.Millisecond)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("broadcast panicked after client disconnected: %v", r)
		}
	}()
	app.broadcast(EventEnvelope{Type: "test"})
	app.broadcast(EventEnvelope{Type: "test"})
}

// Regression: on shutdown every WS client gets a "going away" close frame
// (not just a dropped socket) and is removed from wsClients.
func TestCloseAllWSClientsSendsCloseFrame(t *testing.T) {
	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	connA := dialTestWS(t, app, srv)
	defer connA.Close()
	connB := dialTestWS(t, app, srv)
	defer connB.Close()

	app.closeAllWSClients()

	for name, c := range map[string]*websocket.Conn{"A": connA, "B": connB} {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var err error
		for err == nil {
			_, _, err = c.ReadMessage() // skip the initial "ready" event
		}
		if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
			t.Fatalf("client %s: want going-away close, got %v", name, err)
		}
		if ce, ok := err.(*websocket.CloseError); !ok || ce.Text != wsShutdownReason {
			t.Fatalf("client %s: close reason = %v, want %q", name, err, wsShutdownReason)
		}
	}
	app.wsMu.Lock()
	left := len(app.wsClients)
	app.wsMu.Unlock()
	if left != 0 {
		t.Fatalf("wsClients has %d entries after close, want 0", left)
	}

	// Safe to call again with nothing connected.
	app.closeAllWSClients()
}

// Regression: connections above maxWSClients are refused with a
// "try again later" close, and a slot frees up once a client leaves.
func TestHandleWSCapsConnections(t *testing.T) {
	saved := maxWSClients
	maxWSClients = 2
	t.Cleanup(func() { maxWSClients = saved })

	app := newTestApp(t)
	app.connected = true
	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	connA := dialTestWS(t, app, srv)
	defer connA.Close()
	connB := dialTestWS(t, app, srv)
	defer connB.Close()

	// Dial the third directly: dialTestWS waits for the welcome messages,
	// which a refused connection never sends.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	connC, _, err := websocket.DefaultDialer.Dial(wsURL, map[string][]string{
		authHeaderName: {"Bearer " + app.apiToken},
		"Origin":       {allowedBackendOrigin},
	})
	if err != nil {
		t.Fatalf("dial third: %v", err)
	}
	defer connC.Close()
	_ = connC.SetReadDeadline(time.Now().Add(2 * time.Second))
	for err == nil {
		_, _, err = connC.ReadMessage()
	}
	if !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
		t.Fatalf("third connection: want try-again-later close, got %v", err)
	}
	if ce, ok := err.(*websocket.CloseError); !ok || ce.Text != wsTooManyReason {
		t.Fatalf("third connection: close reason = %v, want %q", err, wsTooManyReason)
	}
	if len(wsTooManyReason) > 123 {
		t.Fatalf("close reason is %d bytes; WebSocket allows at most 123", len(wsTooManyReason))
	}
	app.wsMu.Lock()
	n := len(app.wsClients)
	app.wsMu.Unlock()
	if n != 2 {
		t.Fatalf("wsClients = %d, want 2", n)
	}

	// Free a slot: a new connection is accepted again.
	_ = connA.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		app.wsMu.Lock()
		n = len(app.wsClients)
		app.wsMu.Unlock()
		if n < 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n >= 2 {
		t.Fatal("closed client was never removed")
	}
	connD := dialTestWS(t, app, srv)
	defer connD.Close()
	app.broadcast(EventEnvelope{Type: "test", Payload: "hi"})
	_ = connD.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := connD.ReadMessage(); err != nil {
		t.Fatalf("connection after freeing a slot should work: %v", err)
	}
}
