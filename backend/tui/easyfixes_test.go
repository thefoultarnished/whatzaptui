package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDetectDirsHasNoHardcodedPath(t *testing.T) {
	got := detectDirs()
	if strings.Contains(strings.ToLower(got), "users/nav") {
		t.Fatalf("detectDirs = %q, still contains hardcoded dev path", got)
	}
	if !strings.HasSuffix(got, "backend") {
		t.Fatalf("detectDirs = %q, want suffix backend", got)
	}
}

func TestOpenWSChannelIsBuffered(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(env{Type: "hello"})
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	msg := openWS(context.Background(), url, "tok")()
	open, ok := msg.(wsOpenMsg)
	if !ok {
		t.Fatalf("openWS returned %T, want wsOpenMsg", msg)
	}
	if open.err != nil {
		t.Fatalf("openWS err = %v", open.err)
	}
	if open.ch == nil {
		t.Fatal("openWS ch is nil")
	}
	if got := cap(open.ch); got != 64 {
		t.Fatalf("cap(ch) = %d, want 64", got)
	}
	select {
	case e := <-open.ch:
		raw, _ := json.Marshal(e)
		if !strings.Contains(string(raw), "hello") {
			t.Fatalf("event = %s, want hello", raw)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WS event")
	}
	if open.conn != nil {
		_ = open.conn.Close()
	}
}
