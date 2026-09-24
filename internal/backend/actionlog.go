package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// actionLog is a per-session structured event log. One file per backend
// startup, written to <data-root>/backend/logs/backend-<start-ts>.log.
//
// The log is for triage ("did HistorySync fire? how long did bootstrap
// take? how many messages were dropped?"), not for debugging payload
// shape. Two redaction rules keep it safe to share:
//   - the bearer token is replaced with [REDACTED] (S-16, same secretFn
//     the default logger uses)
//   - phone numbers / pushnames are replaced with a stable 6-char blake2b-
//     inspired prefix so the same contact across multiple events is still
//     correlatable, but the actual phone/name never leaves the process.
//
// Bodies, captions, file names, file paths outside WHATZAP_DATA_DIR, and
// any message_json content are never written. Counts and durations only.
type actionLog struct {
	mu     sync.Mutex
	w      io.WriteCloser // the file; closed on Shutdown
	file   string         // absolute path of the log file
	start  time.Time      // backend start time, used in the file name
	secret func() string  // returns the current bearer token (S-16 redactor)
}

const actionLogDir = "logs"
const actionLogPrefix = "backend-"
const actionLogExt = ".log"
const actionLogFlag = os.O_CREATE | os.O_WRONLY | os.O_APPEND

// openActionLog creates logs/ under the backend cache dir, opens a new
// per-session file named backend-<utc>.log (owner-only, 0o600), and
// returns the logger. secret is the same function the default logger
// uses to look up the *current* bearer token for S-16 redaction.
func openActionLog(cacheDir string, start time.Time, secret func() string) (*actionLog, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return nil, fmt.Errorf("action log: empty cache dir")
	}
	dir := filepath.Join(cacheDir, actionLogDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("action log: mkdir: %w", err)
	}
	name := actionLogPrefix + start.UTC().Format("20060102T150405Z") + actionLogExt
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, actionLogFlag, 0o600)
	if err != nil {
		return nil, fmt.Errorf("action log: open %s: %w", path, err)
	}
	a := &actionLog{
		w:      f,
		file:   path,
		start:  start,
		secret: secret,
	}
	a.header()
	return a, nil
}

// header writes a single comment line at file creation so the file is
// self-describing even when an external tool opens it. Always unredacted
// path is fine to write (it identifies the install).
func (a *actionLog) header() {
	a.write(time.Now(), "session.start", map[string]string{
		"logFile": a.file,
		"pid":     fmt.Sprintf("%d", os.Getpid()),
	})
}

func (a *actionLog) write(t time.Time, action string, kv map[string]string) {
	if a == nil || a.w == nil {
		return
	}
	var sb strings.Builder
	sb.WriteString(t.UTC().Format(time.RFC3339Nano))
	sb.WriteByte(' ')
	sb.WriteString(action)
	if len(kv) > 0 {
		sb.WriteString("  ")
		// Deterministic order: sorted by key so grep diffs across runs are stable.
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		stringSort(keys)
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(k)
			sb.WriteByte('=')
			sb.WriteString(quoteAttr(kv[k]))
		}
	}
	sb.WriteByte('\n')

	line := []byte(sb.String())
	if a.secret != nil {
		if tok := strings.TrimSpace(a.secret()); tok != "" {
			line = scrubToken(line, tok)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.w.Write(line); err != nil {
		log.Printf("action log write: %v", err)
	}
}

// Event is the public entry point. action is one of the canonical names
// listed below; kv pairs must not contain message bodies, captions, file
// names, or full phone numbers.
func (a *actionLog) Event(action string, kv map[string]string) {
	a.write(time.Now(), action, kv)
}

// Timed is sugar for Event("action.start", kv) returning a func the
// caller defers to record the matching "action.end" with elapsed_ms.
//
//	start := a.actionLog.Timed("bootstrap", map[string]string{})
//	defer start("ok", nil)
func (a *actionLog) Timed(action string, kv map[string]string) func(status string, extra map[string]string) {
	if a == nil {
		return func(string, map[string]string) {}
	}
	t0 := time.Now()
	a.write(t0, action+".start", kv)
	return func(status string, extra map[string]string) {
		kv2 := make(map[string]string, len(kv)+2)
		for k, v := range kv {
			kv2[k] = v
		}
		kv2["status"] = status
		kv2["elapsedMs"] = fmt.Sprintf("%d", time.Since(t0).Milliseconds())
		for k, v := range extra {
			if _, dup := kv2[k]; !dup {
				kv2[k] = v
			}
		}
		a.write(time.Now(), action+".end", kv2)
	}
}

// Close flushes the log file. Idempotent and safe on a nil receiver.
func (a *actionLog) Close() error {
	if a == nil || a.w == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.w.Close()
	a.w = nil
	return err
}

// Path returns the absolute path of the log file (for inclusion in
// /health or shutdown messages).
func (a *actionLog) Path() string {
	if a == nil {
		return ""
	}
	return a.file
}

// StartedAt returns the backend session start time. Used by
// shutdown logs to print total uptime.
func (a *actionLog) StartedAt() time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.start
}

// ---------- redaction helpers (do not leak to other files) ----------

// scrubToken replaces every occurrence of tok with [REDACTED] in line.
// Same behavior as redactingWriter so the on-disk file can never leak a
// token if some future code path logs a raw *http.Request.
func scrubToken(line []byte, tok string) []byte {
	if len(tok) == 0 {
		return line
	}
	return []byte(strings.ReplaceAll(string(line), tok, redactPlaceholder))
}

// quoteAttr wraps a value in double quotes when it contains space, "=",
// control chars, or starts with a double quote. Keeps log lines greppable
// without losing order.
func quoteAttr(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t\n\"=") {
		return `"` + strings.ReplaceAll(v, "\"", `\"`) + `"`
	}
	return v
}

// redactPhone returns "phone:<6-char-blake2b-style-prefix>" derived from
// the raw JID user. Same input → same prefix, so a contact is correlatable
// across events (entries for the same phone share a prefix) without ever
// recording the digits. 6 chars is plenty to disambiguate within one
// backend session and keeps the log readable.
func redactPhone(rawJID string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, rawJID)
	if digits == "" {
		return "phone:empty"
	}
	sum := sha256.Sum256([]byte(digits))
	return "phone:" + hex.EncodeToString(sum[:3])
}

// redactPushName hashes a free-form display name the same way. Returns
// "name:<prefix>" so two events with the same push name resolve to the
// same identifier for grep and diff purposes.
func redactPushName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(name))
	return "name:" + hex.EncodeToString(sum[:3])
}

// redactChatID normalizes a JID and returns "chat:<kind>:<phone-prefix>"
// or "chat:group" / "chat:status". Groups and status carry no phone and
// are returned without a digits suffix; individual chats get the same
// redacted phone prefix as the participant, so a per-message log entry can
// be joined to per-chat counts.
func redactChatID(rawJID string) string {
	rawJID = strings.TrimSpace(rawJID)
	if rawJID == "" {
		return "chat:empty"
	}
	if rawJID == "status@broadcast" {
		return "chat:status"
	}
	if strings.HasSuffix(rawJID, "@g.us") {
		return "chat:group"
	}
	return "chat:" + redactPhone(rawJID)
}

// stringSort is an inline insertion sort used at most once per write and
// never on hot paths. Avoids pulling sort.Strings' package-level import
// into the logger so future edits don't accidentally drag in unrelated
// surface area.
func stringSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// redactMessageKind maps an inbound message map to a stable kind token
// without leaking body/caption/filename. Returns one of "text", "image",
// "video", "audio", "voice", "document", "sticker", "contact", "location",
// "poll", "reaction", "protocol", "unknown".
func redactMessageKind(m map[string]any) string {
	if m == nil {
		return "unknown"
	}
	switch {
	case m["conversation"] != nil, m["extendedTextMessage"] != nil:
		return "text"
	case m["imageMessage"] != nil:
		return "image"
	case m["videoMessage"] != nil:
		return "video"
	case m["audioMessage"] != nil:
		// PTTSeparat eMessage flag has not been ported through here; if
		// a future caller needs to distinguish voice notes from audio
		// files, plumb an explicit kind from the caller rather than
		// re-inspecting the map here.
		return "audio"
	case m["documentMessage"] != nil, m["documentWithCaptionMessage"] != nil:
		return "document"
	case m["stickerMessage"] != nil:
		return "sticker"
	case m["contactMessage"] != nil:
		return "contact"
	case m["locationMessage"] != nil, m["liveLocationMessage"] != nil:
		return "location"
	case m["pollCreationMessage"] != nil, m["pollUpdateMessage"] != nil:
		return "poll"
	case m["reactionMessage"] != nil:
		return "reaction"
	case m["protocolMessage"] != nil:
		return "protocol"
	}
	return "unknown"
}

// truncateErr squeezes a possibly-verbose error string down to a class
// token plus the first relevant line. Used to keep SQLITE_BUSY stacks
// and protobuf details out of the action log; one human-readable line is
// enough for triage and most errors collapse to a stable class anyway.
//
//	"database is locked (5) (SQLITE_BUSY)"
//	"failed to decrypt prekey message: ..."
//	"connection refused"
func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.TrimSpace(err.Error())
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// intStr / boolStr stringify Go primitives the way the action log
// expects: bare integers (no padding, no quotes) for counts and
// durations, lowercase "true"/"false" for booleans. Kept here so
// log-call sites stay short.
func intStr(n int) string         { return fmt.Sprintf("%d", n) }
func int64Str(n int64) string     { return fmt.Sprintf("%d", n) }
func boolStr(b bool) string       { if b { return "true" }; return "false" }
func durMs(d time.Duration) string { return fmt.Sprintf("%d", d.Milliseconds()) }
