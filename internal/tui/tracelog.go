package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The TUI trace log is a per-session triage file at
// <data-root>/tui/logs/tui-<utc>.log that follows the load path from the
// TUI's side: backend start, WS connect, WS events, chat/contact/message
// fetches, and what the chat list / open chat hold when handed to View.
// Paired with the backend's action log it shows exactly where history
// stops on its way to the screen. Same redaction rules as the backend:
// counts, durations and hashed chat IDs only, never bodies or names.
var (
	traceMu   sync.Mutex
	traceFile *os.File

	// Last state reported by traceRenderState, so it logs on change only.
	traceLastChats  = -1
	traceLastActive string
	traceLastMsgs   = -1
)

// openTraceLog opens the session trace file under dir (created 0700) and
// routes the standard logger into it: stray log.Printf calls would
// otherwise write to stderr underneath the alt screen, where nobody sees
// them. Returns the file path, or "" when the file can't be opened.
func openTraceLog(dir string) string {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(dir, "tui-"+time.Now().UTC().Format("20060102T150405Z")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ""
	}
	traceMu.Lock()
	traceFile = f
	traceLastChats, traceLastActive, traceLastMsgs = -1, "", -1
	traceMu.Unlock()
	log.SetOutput(f)
	tlog("session.start", "pid", fmt.Sprintf("%d", os.Getpid()), "logFile", path)
	return path
}

func closeTraceLog() {
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceFile != nil {
		log.SetOutput(os.Stderr)
		_ = traceFile.Close()
		traceFile = nil
	}
}

// tlog writes one "<ts> <event>  k=v ..." line (backend action-log format).
// kv is key/value pairs. No-op when the trace log isn't open (tests).
func tlog(event string, kv ...string) {
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceFile == nil {
		return
	}
	var sb strings.Builder
	sb.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	sb.WriteByte(' ')
	sb.WriteString(event)
	for i := 0; i+1 < len(kv); i += 2 {
		if i == 0 {
			sb.WriteString("  ")
		} else {
			sb.WriteByte(' ')
		}
		v := kv[i+1]
		if v == "" || strings.ContainsAny(v, " \t\n\"=") {
			v = `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
		}
		sb.WriteString(kv[i])
		sb.WriteByte('=')
		sb.WriteString(v)
	}
	sb.WriteByte('\n')
	_, _ = traceFile.WriteString(sb.String())
}

// traceChat hashes a chat ID exactly like the backend's redactChatID so
// lines in the two logs can be joined on it.
func traceChat(id string) string {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return "chat:empty"
	case id == "status@broadcast":
		return "chat:status"
	case strings.HasSuffix(id, "@g.us"):
		return "chat:group"
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, id)
	if digits == "" {
		return "chat:phone:empty"
	}
	sum := sha256.Sum256([]byte(digits))
	return "chat:phone:" + hex.EncodeToString(sum[:3])
}

func traceErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// traceRenderState logs what the model hands to View after an update: the
// chat-list size and the open chat's message count. Logged on change only,
// so it marks the moment history actually becomes drawable.
func traceRenderState(x m) {
	traceMu.Lock()
	if traceFile == nil {
		traceMu.Unlock()
		return
	}
	chats := len(x.chats)
	msgs := -1
	if x.active != "" {
		msgs = len(x.msgs[x.active])
	}
	chatsChanged := chats != traceLastChats
	activeChanged := x.active != traceLastActive || msgs != traceLastMsgs
	traceLastChats, traceLastActive, traceLastMsgs = chats, x.active, msgs
	traceMu.Unlock()

	if chatsChanged {
		tlog("render.chatlist", "chats", fmt.Sprintf("%d", chats), "status", x.status)
	}
	if activeChanged && x.active != "" {
		tlog("render.chat", "chat", traceChat(x.active), "messages", fmt.Sprintf("%d", msgs))
	}
}
