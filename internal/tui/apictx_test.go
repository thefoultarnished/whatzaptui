package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCancelledCtxAbortsRequest(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := getChats(ctx, slow.Client(), slow.URL)()
	errMsg, ok := msg.(chatsMsg)
	if !ok {
		t.Fatalf("got %T, want chatsMsg", msg)
	}
	if errMsg.err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestReqCtxFallsBackToBackground(t *testing.T) {
	var x m // no apiCtx assigned (as in unit tests)
	if x.reqCtx() == nil {
		t.Fatal("reqCtx must never be nil")
	}
}

func TestModelCtxWiresThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"chats":[]}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x := m{client: srv.Client(), baseURL: srv.URL, apiCtx: ctx, apiCancel: cancel}
	msg := getChats(x.reqCtx(), x.client, x.baseURL)()
	if errMsg := msg.(chatsMsg); errMsg.err != nil {
		t.Fatalf("getChats err = %v", errMsg.err)
	}
}

func TestCancelRequestsIsNilSafe(t *testing.T) {
	var x m
	x.cancelRequests() // must not panic
}

func TestOpenWSCancelledCtxFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := openWS(ctx, "ws://127.0.0.1:9/ws", "tok")()
	open, ok := msg.(wsOpenMsg)
	if !ok {
		t.Fatalf("got %T, want wsOpenMsg", msg)
	}
	if open.err == nil {
		t.Fatal("expected dial error from cancelled context")
	}
	if !strings.Contains(open.err.Error(), "canceled") && !strings.Contains(open.err.Error(), "cancelled") {
		t.Logf("dial err (acceptable): %v", open.err)
	}
}
