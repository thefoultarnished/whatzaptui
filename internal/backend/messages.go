package backend

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"whatzap/internal/whatsapp"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waWeb "go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var extractSearchableText = whatsapp.ExtractSearchableText

func (a *App) upsertMessageFTS(chatID, msgID string, fromMe int, body string) {
	if a.store != nil {
		_ = a.store.UpsertMessageFTS(chatID, msgID, fromMe == 1, body)
		return
	}
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return
	}
	a.upsertMessageFTSTx(db, chatID, msgID, fromMe, body)
}

func (a *App) upsertMessageFTSTx(exec dbExecutor, chatID, msgID string, fromMe int, body string) {
	if exec == nil {
		return
	}
	if _, err := exec.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, chatID, msgID, fromMe); err != nil {
		log.Printf("upsertMessageFTS delete: %v", err)
		return
	}
	if body == "" {
		return
	}
	if _, err := exec.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`, chatID, msgID, fromMe, body); err != nil {
		log.Printf("upsertMessageFTS insert: %v", err)
	}
}

func (a *App) insertMessageToDB(chatID string, msg WireMessage) error {
	_, err := a.insertMessageToDBTx(a.db, chatID, msg)
	return err
}

// insertMessageToDBTx writes msg to the DB and returns (isNew, error).
// isNew is true only when the row did not previously exist (INSERT OR IGNORE affected a row).
func (a *App) insertMessageToDBTx(exec dbExecutor, chatID string, msg WireMessage) (bool, error) {
	if exec == nil {
		return false, fmt.Errorf("no db executor")
	}
	id := msg.Key.ID
	if id == "" {
		id = dedupeKey(msg)
	}
	fromMe := 0
	if msg.Key.FromMe {
		fromMe = 1
	}
	msgJSON, err := json.Marshal(msg.Message)
	if err != nil {
		return false, err
	}
	res, err := exec.Exec(`
		INSERT OR IGNORE INTO messages (id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, chatID, fromMe, msg.Key.Participant, msg.MessageTimestamp, msg.PushName, msg.ReceiptStatus, string(msgJSON), msg.MediaProto)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	isNew := n > 0
	if _, err := exec.Exec(`
		UPDATE messages SET push_name = ?, message_json = ?, media_proto = ?
		WHERE id = ? AND chat_id = ? AND from_me = ?
	`, msg.PushName, string(msgJSON), msg.MediaProto, id, chatID, fromMe); err != nil {
		return false, err
	}
	a.upsertMessageFTSTx(exec, chatID, id, fromMe, extractSearchableText(msg.Message))
	return isNew, nil
}

func scanMessageRow(rows *sql.Rows) (WireMessage, error) {
	var (
		id, chatID, participant, pushName, receipt, messageJSON, mediaProto string
		fromMe                                                              int
		ts                                                                  int64
	)
	if err := rows.Scan(&id, &chatID, &fromMe, &participant, &ts, &pushName, &receipt, &messageJSON, &mediaProto); err != nil {
		return WireMessage{}, err
	}
	var message map[string]any
	_ = json.Unmarshal([]byte(messageJSON), &message)
	return WireMessage{
		Key:              WireKey{ID: id, RemoteJID: chatID, FromMe: fromMe == 1, Participant: participant},
		MessageTimestamp: ts,
		PushName:         pushName,
		ReceiptStatus:    receipt,
		Message:          message,
		MediaProto:       mediaProto,
	}, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *App) upsertMessage(chatID string, msg WireMessage) {
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return
	}
	tx, err := db.Begin()
	if err != nil {
		log.Printf("upsertMessage: begin tx: %v", err)
		a.upsertMessageTx(db, chatID, msg)
		return
	}
	a.upsertMessageTx(tx, chatID, msg)
	if err := tx.Commit(); err != nil {
		log.Printf("upsertMessage: commit: %v", err)
		_ = tx.Rollback()
	}
}

func (a *App) upsertMessageTx(exec dbExecutor, chatID string, msg WireMessage) {
	if exec == nil {
		return
	}
	chatID = a.canonicalizeChatID(chatID)
	if chatID == "" {
		return
	}
	msg.Key.RemoteJID = chatID
	if msg.Key.Participant != "" {
		msg.Key.Participant = a.canonicalizeChatID(msg.Key.Participant)
	}

	isNew, err := a.insertMessageToDBTx(exec, chatID, msg)
	if err != nil {
		log.Printf("upsertMessage: db write: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	chat := a.state.Chats[chatID]
	chat.ID = chatID
	if msg.MessageTimestamp > chat.ConversationTimestamp {
		chat.ConversationTimestamp = msg.MessageTimestamp
	}
	if isNew && !msg.Key.FromMe && !a.historySyncing {
		chat.UnreadCount++
	}
	if chat.Name == "" && msg.PushName != "" && !msg.Key.FromMe && !strings.HasSuffix(chatID, "@g.us") {
		chat.Name = msg.PushName
	}
	a.state.Chats[chatID] = chat

	prevNotify := a.state.Contacts[chatID].Notify
	if msg.PushName != "" && !msg.Key.FromMe && !strings.HasSuffix(chatID, "@g.us") {
		ct := a.state.Contacts[chatID]
		ct.ID = chatID
		ct.Notify = msg.PushName
		a.state.Contacts[chatID] = ct
	}
	if !a.historySyncing {
		atomic.StoreUint32(&a.persistDirty, 1)
	}
	permName := msg.PushName
	if msg.Key.FromMe {
		permName = ""
	}
	// prevNotify is the push name the permission row was last written with,
	// so upsertPermission can tell its own auto-captured name apart from a
	// user-set /rename name (which never matches a push name).
	go a.upsertPermission(phoneFromJID(chatID), permName, prevNotify)
}

var receiptStatusFromType = whatsapp.ReceiptStatusFromType

func receiptStatusRank(status string) int {
	switch status {
	case "sent":
		return 1
	case "delivered":
		return 2
	case "read":
		return 3
	case "played":
		return 4
	default:
		return 0
	}
}

func (a *App) updateReceiptStatus(chatID string, ids []string, status string) bool {
	if a.store != nil {
		updated, _ := a.store.UpdateReceiptStatus(chatID, ids, status)
		return updated
	}
	if receiptStatusRank(status) == 0 {
		return false
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return false
	}

	uniqueIDs := make([]string, 0, len(idSet))
	for id := range idSet {
		uniqueIDs = append(uniqueIDs, id)
	}
	placeholders := strings.Repeat("?,", len(uniqueIDs))
	placeholders = placeholders[:len(placeholders)-1]

	// Only upgrade receipt status, never downgrade.
	rankExpr := `CASE receipt WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 WHEN 'played' THEN 4 ELSE 0 END`
	args := []any{status, chatID}
	for _, id := range uniqueIDs {
		args = append(args, id)
	}
	args = append(args, receiptStatusRank(status))

	result, err := a.db.Exec(fmt.Sprintf(`
		UPDATE messages SET receipt = ?
		WHERE chat_id = ? AND from_me = 1 AND id IN (%s)
		AND %s < ?
	`, placeholders, rankExpr), args...)
	if err != nil {
		log.Printf("updateReceiptStatus: %v", err)
		return false
	}
	n, _ := result.RowsAffected()
	return n > 0
}

func (a *App) handleMessages(w http.ResponseWriter, r *http.Request) {
	chatID := a.canonicalizeChatID(strings.TrimSpace(r.URL.Query().Get("chatId")))
	if chatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, maxMessagesResponseLimit)
	}
	if around := strings.TrimSpace(r.URL.Query().Get("around")); around != "" {
		a.handleMessagesAround(w, chatID, around, limit)
		return
	}

	const qAll = `SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		FROM messages WHERE chat_id = ? ORDER BY ts DESC LIMIT ?`
	const qBefore = `SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		FROM messages WHERE chat_id = ? AND ts < ? ORDER BY ts DESC LIMIT ?`
	// Fetch limit+1 so we can detect whether more older messages exist.
	fetch := limit + 1
	var (
		rows *sql.Rows
		err  error
	)
	if beforeStr := strings.TrimSpace(r.URL.Query().Get("before")); beforeStr != "" {
		beforeTS, _ := strconv.ParseInt(beforeStr, 10, 64)
		rows, err = a.db.Query(qBefore, chatID, beforeTS, fetch)
	} else {
		rows, err = a.db.Query(qAll, chatID, fetch)
	}
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	defer rows.Close()

	msgs := make([]WireMessage, 0, fetch)
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			continue
		}
		msgs = append(msgs, msg)
	}
	if err := rows.Err(); err != nil {
		log.Printf("handleMessages rows err: %v", err)
	}
	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}
	// Results came DESC; reverse to chronological order for the TUI.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "hasMore": hasMore})
}

// handleMessagesAround returns a window of messages centred on a specific
// message ID: up to limit/2 older messages, the anchor itself, and up to
// limit/2 newer messages. The response includes anchorIndex so the TUI can
// scroll to position the anchor in view.
func (a *App) handleMessagesAround(w http.ResponseWriter, chatID, msgID string, limit int) {
	// Look up the anchor message's timestamp.
	var anchorTS int64
	var anchorFromMe int
	err := a.db.QueryRow(
		`SELECT ts, from_me FROM messages WHERE chat_id = ? AND id = ? LIMIT 1`,
		chatID, msgID,
	).Scan(&anchorTS, &anchorFromMe)
	if err != nil {
		writeErr(w, http.StatusNotFound, "message not found")
		return
	}

	half := limit / 2

	// Older messages (before anchor, exclusive).
	olderRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND ts < ?
		 ORDER BY ts DESC LIMIT ?`,
		chatID, anchorTS, half,
	)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	var older []WireMessage
	for olderRows.Next() {
		if msg, err := scanMessageRow(olderRows); err == nil {
			older = append(older, msg)
		}
	}
	if err := olderRows.Err(); err != nil {
		log.Printf("handleMessagesAround olderRows err: %v", err)
	}
	_ = olderRows.Close()
	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}

	anchorRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND id = ? AND from_me = ?`,
		chatID, msgID, anchorFromMe,
	)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	var anchor []WireMessage
	for anchorRows.Next() {
		if msg, err := scanMessageRow(anchorRows); err == nil {
			anchor = append(anchor, msg)
		}
	}
	if err := anchorRows.Err(); err != nil {
		log.Printf("handleMessagesAround anchorRows err: %v", err)
	}
	_ = anchorRows.Close()

	newerRows, err := a.db.Query(
		`SELECT id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		 FROM messages WHERE chat_id = ? AND ts > ?
		 ORDER BY ts ASC LIMIT ?`,
		chatID, anchorTS, half,
	)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	var newer []WireMessage
	for newerRows.Next() {
		if msg, err := scanMessageRow(newerRows); err == nil {
			newer = append(newer, msg)
		}
	}
	if err := newerRows.Err(); err != nil {
		log.Printf("handleMessagesAround newerRows err: %v", err)
	}
	_ = newerRows.Close()

	// Combine: older + anchor + newer (all chronological).
	msgs := make([]WireMessage, 0, len(older)+len(anchor)+len(newer))
	msgs = append(msgs, older...)
	msgs = append(msgs, anchor...)
	msgs = append(msgs, newer...)

	anchorIndex := len(older) // position of anchor in msgs
	writeJSON(w, http.StatusOK, map[string]any{
		"messages":    msgs,
		"anchorIndex": anchorIndex,
		"hasMore":     false,
	})
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, maxMessagesResponseLimit)
	}
	chatID := strings.TrimSpace(r.URL.Query().Get("chatId"))
	if chatID != "" {
		chatID = a.canonicalizeChatID(chatID)
	}

	// Wrap each token in double quotes so FTS5 treats them as phrase tokens (avoids
	// users hitting FTS5 special syntax accidentally; multi-word still ANDs).
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []searchHit{}})
		return
	}
	for i, t := range tokens {
		tokens[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	matchExpr := strings.Join(tokens, " ")

	const baseQAll = `
		SELECT DISTINCT f.chat_id, f.msg_id, f.from_me,
		       snippet(messages_fts, 3, '<b>', '</b>', '...', 12) AS snip,
		       COALESCE(m.ts, 0) AS ts
		FROM messages_fts f
		LEFT JOIN messages m ON m.chat_id = f.chat_id AND m.id = f.msg_id AND m.from_me = f.from_me
		WHERE f.body MATCH ?
		ORDER BY ts DESC
		LIMIT ?`
	const baseQChat = `
		SELECT DISTINCT f.chat_id, f.msg_id, f.from_me,
		       snippet(messages_fts, 3, '<b>', '</b>', '...', 12) AS snip,
		       COALESCE(m.ts, 0) AS ts
		FROM messages_fts f
		LEFT JOIN messages m ON m.chat_id = f.chat_id AND m.id = f.msg_id AND m.from_me = f.from_me
		WHERE f.body MATCH ? AND f.chat_id = ?
		ORDER BY ts DESC
		LIMIT ?`
	var (
		rows *sql.Rows
		err  error
	)
	if chatID != "" {
		rows, err = a.db.Query(baseQChat, matchExpr, chatID, limit)
	} else {
		rows, err = a.db.Query(baseQAll, matchExpr, limit)
	}
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	defer rows.Close()

	results := make([]searchHit, 0, limit)
	for rows.Next() {
		var (
			cID, mID, snip string
			fromMe         int
			ts             int64
		)
		if err := rows.Scan(&cID, &mID, &fromMe, &snip, &ts); err != nil {
			continue
		}
		results = append(results, searchHit{
			ChatID:    cID,
			MessageID: mID,
			FromMe:    fromMe == 1,
			Timestamp: ts,
			Snippet:   snip,
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("handleSearch rows err: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *App) requireConnectedClient(w http.ResponseWriter) bool {
	if a == nil || a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return false
	}
	return true
}

func (a *App) isChatAllowed(chatID string) (bool, error) {
	phone := phoneFromJID(chatID)
	var allowed int
	err := a.withPermissionDB(func(db *sql.DB) error {
		return db.QueryRow(`SELECT allowed FROM chat_permissions WHERE phone = ?`, phone).Scan(&allowed)
	})
	if errors.Is(err, sql.ErrNoRows) {
		// No per-chat row: fall back to the global default.
		return a.loadDefaultAllowed()
	}
	if err != nil {
		return false, err
	}
	return allowed == 1, nil
}

func (a *App) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID             string `json:"chatId"`
		Text               string `json:"text"`
		ReplyToMsgID       string `json:"replyToMsgId,omitempty"`
		ReplyToText        string `json:"replyToText,omitempty"`
		ReplyToParticipant string `json:"replyToParticipant,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	req.Text = sanitizeOutgoingText(req.Text)
	if req.ChatID == "" || !hasVisibleText(req.Text) {
		writeErr(w, http.StatusBadRequest, "chatId and text are required")
		return
	}

	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}

	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}

	participant := req.ReplyToParticipant
	if participant == "" {
		participant = req.ChatID
	}
	msg := whatsapp.BuildTextMessage(req.Text, req.ReplyToMsgID, participant, req.ReplyToText)

	resp, err := a.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	now := time.Now().Unix()
	wireMsg := map[string]any{"conversation": req.Text}
	if req.ReplyToMsgID != "" {
		wireMsg = map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              req.Text,
				"quotedText":        req.ReplyToText,
				"quotedParticipant": req.ReplyToParticipant,
			},
		}
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: req.ChatID,
			FromMe:    true,
		},
		Message:          wireMsg,
		MessageTimestamp: now,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(req.ChatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}

func (a *App) handleSendFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}

	// Reject overly large request bodies early. Allow some overhead for the
	// multipart framing and form fields above the file size cap.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(64<<10))

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
		return
	}

	chatID := a.canonicalizeChatID(strings.TrimSpace(r.FormValue("chatId")))
	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	caption := strings.TrimSpace(sanitizeOutgoingText(r.FormValue("caption")))

	if chatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}

	jid, err := types.ParseJID(chatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}

	allowed, err := a.isChatAllowed(chatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}

	f, fileHeader, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file part is required")
		return
	}
	defer f.Close()
	if fileHeader.Size > maxUploadBytes {
		writeErr(w, http.StatusBadRequest, "file too large: max 150 MB")
		return
	}

	filename := filepath.Base(fileHeader.Filename)
	if filename == "" || filename == "." || filename == "/" {
		writeErr(w, http.StatusBadRequest, "file must have a valid filename")
		return
	}

	// LimitReader caps the read at maxUploadBytes+1 so we can detect overflow.
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		writeInternalErr(w, fmt.Errorf("read file: %w", err))
		return
	}
	if int64(len(data)) > maxUploadBytes {
		writeErr(w, http.StatusBadRequest, "file too large: max 150 MB")
		return
	}

	if err := validateSendFileInput(filename, data, kind); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	mediaType, err := mediaTypeForSendKind(kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	upload, err := a.client.Upload(context.Background(), data, mediaType)
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	msg, wireBody, err := buildOutgoingMediaMessage(kind, filename, caption, upload, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := a.client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	var mediaProto string
	if b, err := proto.Marshal(msg); err == nil {
		mediaProto = base64.StdEncoding.EncodeToString(b)
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: chatID,
			FromMe:    true,
		},
		Message:          wireBody,
		MessageTimestamp: time.Now().Unix(),
		MediaProto:       mediaProto,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(chatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"message": wire})
}

func (a *App) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}

	a.mu.Lock()
	chat := a.state.Chats[req.ChatID]
	chat.ID = req.ChatID
	unreadToMark := chat.UnreadCount
	chat.UnreadCount = 0
	a.state.Chats[req.ChatID] = chat
	a.mu.Unlock()

	senderToIDs := make(map[string][]types.MessageID)
	{
		limit := 100
		if unreadToMark > 0 && unreadToMark < limit {
			limit = unreadToMark
		}
		rows, err := a.db.Query(`
			SELECT id, participant, chat_id FROM messages
			WHERE chat_id = ? AND from_me = 0 AND id != ''
			ORDER BY ts DESC LIMIT ?
		`, req.ChatID, limit)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, participant, cid string
				if rows.Scan(&id, &participant, &cid) != nil {
					continue
				}
				sender := participant
				if sender == "" {
					sender = cid
				}
				senderToIDs[sender] = append(senderToIDs[sender], types.MessageID(id))
			}
			if err := rows.Err(); err != nil {
				log.Printf("handleMarkRead rows err: %v", err)
			}
		}
	}

	// Inform WhatsApp server in the background so the HTTP response returns
	// immediately — MarkRead can be slow and would otherwise trigger the TUI's
	// 12s client timeout. persistState is called after the API calls complete
	// so we don't flush the zeroed unread count before WhatsApp confirms it.
	go func() {
		chatJID, _ := types.ParseJID(req.ChatID)
		for senderStr, ids := range senderToIDs {
			senderJID, _ := types.ParseJID(senderStr)
			_ = a.client.MarkRead(context.Background(), ids, time.Now(), chatJID, senderJID)
		}
		a.persistState()
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleTyping(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID string `json:"chatId"`
		State  string `json:"state"` // "composing" or "paused"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}
	state := types.ChatPresenceComposing
	if req.State == "paused" {
		state = types.ChatPresencePaused
	}
	_ = a.client.SendChatPresence(context.Background(), jid, state, types.ChatPresenceMediaText)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleReact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	var req struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
		Sender    string `json:"sender"`
		Reaction  string `json:"reaction"` // emoji string, empty to remove
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" || req.MessageID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and messageId are required")
		return
	}
	chatJID, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}
	senderJID := types.EmptyJID
	if req.Sender != "" {
		senderJID, _ = types.ParseJID(a.canonicalizeChatID(req.Sender))
	}
	msg := a.client.BuildReaction(chatJID, senderJID, types.MessageID(req.MessageID), req.Reaction)
	resp, err := a.client.SendMessage(context.Background(), chatJID, msg)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	now := time.Now().Unix()
	wireMsg := map[string]any{
		"reactionMessage": map[string]any{
			"emoji":       req.Reaction,
			"targetMsgID": req.MessageID,
		},
	}
	wire := WireMessage{
		Key: WireKey{
			ID:        resp.ID,
			RemoteJID: req.ChatID,
			FromMe:    true,
		},
		Message:          wireMsg,
		MessageTimestamp: now,
		ReceiptStatus:    "sent",
	}
	a.upsertMessage(req.ChatID, wire)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
		// FromMe is a *bool so we can distinguish "field absent"
		// (nil → 400) from "field present, value false". The
		// messages table's PRIMARY KEY is (chat_id, id, from_me);
		// a delete using only (chat_id, id) would wipe both the
		// incoming and outgoing row if the same chat ever has two
		// messages with the same id (A-16). Old TUI binaries that
		// send no fromMe field get a 400 — the TUI is rebuilt in
		// lockstep with the backend, so this is a hard fail-fast
		// rather than a silent regression.
		FromMe *bool `json:"fromMe"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Input validation (bad request shape) runs before any
	// state-dependent check (not connected), so a malformed
	// request always gets 400 — never 409.
	if req.FromMe == nil {
		writeErr(w, http.StatusBadRequest, "fromMe is required")
		return
	}
	req.ChatID = strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(req.ChatID)
	if req.ChatID == "" || req.MessageID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and messageId are required")
		return
	}
	chatJID, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	// Whitelist is checked before the connection state so a non-whitelisted
	// chat is always rejected with 403, even when disconnected (mirrors
	// handleEditMessage).
	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	// RevokeMessage only works for the user's own messages; an
	// incoming message can't be revoked.
	if *req.FromMe {
		_, err = a.client.RevokeMessage(context.Background(), chatJID, types.MessageID(req.MessageID))
		if err != nil {
			writeInternalErr(w, err)
			return
		}
	}
	if err := a.deleteMessageFromDB(req.ChatID, req.MessageID, *req.FromMe); err != nil {
		writeInternalErr(w, err)
		return
	}
	a.persistState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deleteMessageFromDB removes a single message row (and its FTS
// index entry) keyed on the full primary key (chat_id, id, from_me).
// A-16: a delete using only (chat_id, id) would wipe both the
// incoming and outgoing row when the same chat had two messages with
// the same id but different from_me. Returns nil if a.db is nil
// (no DB to delete from) so callers can use it unconditionally.
func (a *App) deleteMessageFromDB(chatID, messageID string, fromMe bool) error {
	if a.store != nil {
		return a.store.DeleteMessage(chatID, messageID, fromMe)
	}
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fromMeInt := 0
	if fromMe {
		fromMeInt = 1
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE chat_id = ? AND id = ? AND from_me = ?`, chatID, messageID, fromMeInt); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, chatID, messageID, fromMeInt); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		ChatID    string `json:"chatId"`
		MessageID string `json:"messageId"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.ChatID = a.canonicalizeChatID(strings.TrimSpace(req.ChatID))
	req.MessageID = strings.TrimSpace(req.MessageID)
	req.Text = sanitizeOutgoingText(req.Text)
	if req.ChatID == "" || req.MessageID == "" || !hasVisibleText(req.Text) {
		writeErr(w, http.StatusBadRequest, "chatId, messageId and text are required")
		return
	}
	chatJID, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	// Whitelist is checked before the connection state so a non-whitelisted
	// chat is always rejected with 403, even when disconnected.
	allowed, err := a.isChatAllowed(req.ChatID)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if !allowed {
		writeErr(w, http.StatusForbidden, "chat not whitelisted")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		writeErr(w, http.StatusInternalServerError, "no database")
		return
	}
	var ts int64
	err = db.QueryRow(`SELECT ts FROM messages WHERE chat_id = ? AND id = ? AND from_me = 1`, req.ChatID, req.MessageID).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "message not found")
		return
	}
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if time.Since(time.Unix(ts, 0)) > whatsmeow.EditWindow {
		writeErr(w, http.StatusConflict, "edit window expired")
		return
	}
	newContent := &waE2E.Message{Conversation: proto.String(req.Text)}
	editMsg := a.client.BuildEdit(chatJID, types.MessageID(req.MessageID), newContent)
	if _, err := a.client.SendMessage(context.Background(), chatJID, editMsg); err != nil {
		writeInternalErr(w, err)
		return
	}
	wire, ok, err := a.editMessageInDB(req.ChatID, req.MessageID, true, func(m map[string]any) {
		if ext, ok := m["extendedTextMessage"].(map[string]any); ok {
			ext["text"] = req.Text
			m["extendedTextMessage"] = ext
		} else {
			m["conversation"] = req.Text
		}
	})
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	if ok {
		a.broadcast(EventEnvelope{Type: "message:edited", Payload: wire})
	}
	a.persistState()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": wire})
}

// editMessageInDB applies mutate to the stored message_json for (chatID, msgID, fromMe),
// marks it edited, persists the change and FTS index, and returns the updated WireMessage.
// ok is false if no matching row exists.
func (a *App) editMessageInDB(chatID, msgID string, fromMe bool, mutate func(map[string]any)) (WireMessage, bool, error) {
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return WireMessage{}, false, nil
	}
	fromMeInt := 0
	if fromMe {
		fromMeInt = 1
	}
	var participant, pushName, receipt, messageJSON string
	var ts int64
	err := db.QueryRow(
		`SELECT participant, ts, push_name, receipt, message_json FROM messages WHERE chat_id = ? AND id = ? AND from_me = ?`,
		chatID, msgID, fromMeInt,
	).Scan(&participant, &ts, &pushName, &receipt, &messageJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return WireMessage{}, false, nil
	}
	if err != nil {
		return WireMessage{}, false, err
	}
	var msgMap map[string]any
	if err := json.Unmarshal([]byte(messageJSON), &msgMap); err != nil || msgMap == nil {
		msgMap = map[string]any{}
	}
	mutate(msgMap)
	msgMap["edited"] = true
	newJSON, err := json.Marshal(msgMap)
	if err != nil {
		return WireMessage{}, false, err
	}
	if _, err := db.Exec(`UPDATE messages SET message_json = ? WHERE chat_id = ? AND id = ? AND from_me = ?`, string(newJSON), chatID, msgID, fromMeInt); err != nil {
		return WireMessage{}, false, err
	}
	a.upsertMessageFTS(chatID, msgID, fromMeInt, extractSearchableText(msgMap))
	wire := WireMessage{
		Key:              WireKey{ID: msgID, RemoteJID: chatID, FromMe: fromMe, Participant: participant},
		Message:          msgMap,
		MessageTimestamp: ts,
		PushName:         pushName,
		ReceiptStatus:    receipt,
	}
	return wire, true, nil
}

func (a *App) toWireMessage(evt *events.Message) WireMessage {
	info := evt.Info
	chatID := info.Chat.String()
	effective := effectiveMessage(evt.Message)
	msg, mediaProto := a.wireMessagePayload(evt.Message, effective, chatID, info.IsGroup)

	// Decrypt poll votes so the TUI can show which option(s) were selected.
	if pu, ok := msg["pollUpdateMessage"].(map[string]any); ok {
		if names := a.decryptPollVoteOptions(evt); names != nil {
			pu["selectedOptionNames"] = names
		}
		msg["pollUpdateMessage"] = pu
	}

	key := WireKey{
		ID:        info.ID,
		RemoteJID: chatID,
		FromMe:    info.IsFromMe,
	}
	if info.IsGroup {
		key.Participant = info.Sender.String()
	}

	return WireMessage{
		Key:              key,
		Message:          msg,
		MessageTimestamp: info.Timestamp.Unix(),
		PushName:         evt.Info.PushName,
		MediaProto:       mediaProto,
	}
}

// decryptPollVoteOptions decrypts an incoming poll vote and returns the
// selected option name(s) by matching SHA-256 hashes against the original
// poll's option list (fetched from our message DB).
func (a *App) decryptPollVoteOptions(evt *events.Message) []string {
	vote, err := a.client.DecryptPollVote(context.Background(), evt)
	if err != nil || vote == nil {
		return nil
	}
	if len(vote.GetSelectedOptions()) == 0 {
		return []string{} // empty slice = removed vote
	}
	pu := evt.Message.GetPollUpdateMessage()
	if pu == nil {
		return nil
	}
	pollMsgID := pu.GetPollCreationMessageKey().GetID()
	pollChatID := pu.GetPollCreationMessageKey().GetRemoteJID()
	if pollChatID == "" {
		pollChatID = evt.Info.Chat.String()
	}
	pollChatID = a.canonicalizeChatID(pollChatID)
	options := a.pollOptionNames(pollChatID, pollMsgID)
	if options == nil {
		return nil
	}
	var selected []string
	for _, optHash := range vote.GetSelectedOptions() {
		for _, name := range options {
			h := sha256OfString(name)
			if len(h) == len(optHash) {
				match := true
				for i := range h {
					if h[i] != optHash[i] {
						match = false
						break
					}
				}
				if match {
					selected = append(selected, name)
				}
			}
		}
	}
	return selected
}

func sha256OfString(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// pollOptionNames looks up a poll creation message in the DB and returns its
// option names, or nil if the message isn't found or has no options stored.
func (a *App) pollOptionNames(chatID, msgID string) []string {
	if chatID == "" || msgID == "" {
		return nil
	}
	row := a.db.QueryRow(
		`SELECT message_json FROM messages WHERE chat_id = ? AND id = ? LIMIT 1`,
		chatID, msgID,
	)
	var msgJSON string
	if err := row.Scan(&msgJSON); err != nil {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(msgJSON), &payload); err != nil {
		return nil
	}
	pc, _ := payload["pollCreationMessage"].(map[string]any)
	if pc == nil {
		return nil
	}
	raw, _ := pc["options"].([]any)
	names := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			names = append(names, s)
		}
	}
	return names
}

func (a *App) toWireMessageFromHistory(webMsg *waWeb.WebMessageInfo) WireMessage {
	if webMsg == nil {
		return WireMessage{}
	}
	key := webMsg.GetKey()
	m := effectiveMessage(webMsg.GetMessage())
	chatID := key.GetRemoteJID()
	out, mediaProto := a.wireMessagePayload(webMsg.GetMessage(), m, chatID, strings.HasSuffix(chatID, "@g.us"))
	wireKey := WireKey{
		ID:          key.GetID(),
		RemoteJID:   key.GetRemoteJID(),
		FromMe:      key.GetFromMe(),
		Participant: key.GetParticipant(),
	}

	return WireMessage{
		Key:              wireKey,
		Message:          out,
		MessageTimestamp: int64(webMsg.GetMessageTimestamp()),
		PushName:         webMsg.GetPushName(),
		MediaProto:       mediaProto,
	}
}

func normalizeQuotedParticipant(participant, selfID string, canonicalize func(string) string) string {
	participant = strings.TrimSpace(participant)
	if participant == "" {
		return ""
	}
	if canonicalize != nil {
		participant = canonicalize(participant)
	}
	if strings.TrimSpace(selfID) != "" {
		selfCanonical := selfID
		if canonicalize != nil {
			selfCanonical = canonicalize(selfID)
		}
		if participant == selfCanonical || phoneIdentity(participant) == phoneIdentity(selfCanonical) {
			return ""
		}
	}
	return participant
}

func phoneIdentity(jid string) string {
	local := phoneFromJID(jid)
	local = strings.SplitN(local, ":", 2)[0]
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, local)
}

// quotedStanzaFromMe looks up a quoted message by its stanza (message) ID
// in our own DB and reports whether we authored it. The second return is
// false when the message isn't in our store (so the caller can fall back
// to a heuristic). This is the authoritative source for "is this quote
// mine" because it reads our recorded from_me flag, independent of whether
// WhatsApp populated the ContextInfo participant field.
func (a *App) quotedStanzaFromMe(chatID, stanzaID string) (bool, bool) {
	stanzaID = strings.TrimSpace(stanzaID)
	if a == nil || stanzaID == "" {
		return false, false
	}
	a.mu.RLock()
	db := a.db
	a.mu.RUnlock()
	if db == nil {
		return false, false
	}
	var fromMe int
	err := db.QueryRow(
		`SELECT from_me FROM messages WHERE chat_id = ? AND id = ? ORDER BY from_me DESC LIMIT 1`,
		chatID, stanzaID,
	).Scan(&fromMe)
	if err != nil {
		return false, false
	}
	return fromMe == 1, true
}

// quotedMessageFromMe decides whether a quoted message was authored by us,
// which the TUI uses to pick the quote's color (sent vs received palette).
// normalized is the output of normalizeQuotedParticipant, which returns ""
// precisely when the quoted author is self. So a raw participant that was
// present but normalized away is the unambiguous "this is my message"
// signal — and it works in groups too, where the elimination heuristic
// below can't. If it wasn't self, fall back to the 1-to-1 elimination check.
func quotedMessageFromMe(chatID, rawParticipant, normalized string, isGroup bool) bool {
	if strings.TrimSpace(rawParticipant) != "" && strings.TrimSpace(normalized) == "" {
		return true
	}
	return quotedFromMeForChat(chatID, normalized, isGroup)
}

func quotedFromMeForChat(chatID, quotedParticipant string, isGroup bool) bool {
	quotedParticipant = strings.TrimSpace(quotedParticipant)
	if quotedParticipant == "" {
		return false
	}
	if isGroup {
		return false
	}
	return phoneIdentity(quotedParticipant) != "" && phoneIdentity(quotedParticipant) != phoneIdentity(chatID)
}

func (a *App) wireMessagePayload(raw, effective *waE2E.Message, chatID string, isGroup bool) (map[string]any, string) {
	msg := map[string]any{}
	var mediaProto string
	if txt := effective.GetConversation(); txt != "" {
		msg["conversation"] = txt
	}
	if ext := effective.GetExtendedTextMessage(); ext != nil {
		entry := map[string]any{"text": ext.GetText()}
		if ctx := ext.GetContextInfo(); ctx != nil && ctx.GetQuotedMessage() != nil {
			entry["quotedText"] = quotedText(ctx.GetQuotedMessage())
			selfID := ""
			if a != nil && a.client != nil && a.client.Store != nil && a.client.Store.ID != nil {
				selfID = a.client.Store.ID.String()
			}
			rawParticipant := strings.TrimSpace(ctx.GetParticipant())
			normalized := normalizeQuotedParticipant(rawParticipant, selfID, func(id string) string {
				if a == nil {
					return strings.TrimSpace(id)
				}
				return a.canonicalizeChatID(id)
			})
			entry["quotedParticipant"] = normalized
			// Prefer the authoritative signal: if the quoted message is in
			// our own DB marked from_me=1, the quote is mine — regardless of
			// whether WhatsApp populated the participant field (it often
			// doesn't for 1-to-1 quotes). Fall back to the participant
			// heuristic when the quoted message isn't in our store yet.
			if fromMe, ok := a.quotedStanzaFromMe(chatID, ctx.GetStanzaID()); ok {
				entry["quotedFromMe"] = fromMe
			} else {
				entry["quotedFromMe"] = quotedMessageFromMe(chatID, rawParticipant, normalized, isGroup)
			}
		}
		msg["extendedTextMessage"] = entry
	}
	if img := effective.GetImageMessage(); img != nil {
		msg["imageMessage"] = map[string]any{"caption": img.GetCaption(), "mimetype": img.GetMimetype()}
	}
	if vid := effective.GetVideoMessage(); vid != nil {
		msg["videoMessage"] = map[string]any{"caption": vid.GetCaption(), "mimetype": vid.GetMimetype()}
	}
	if doc := effective.GetDocumentMessage(); doc != nil {
		msg["documentMessage"] = map[string]any{"caption": doc.GetCaption(), "fileName": doc.GetFileName(), "mimetype": doc.GetMimetype()}
	}
	if aud := effective.GetAudioMessage(); aud != nil {
		msg["audioMessage"] = map[string]any{"ptt": aud.GetPTT(), "mimetype": aud.GetMimetype(), "seconds": aud.GetSeconds()}
	}
	if stk := effective.GetStickerMessage(); stk != nil {
		msg["stickerMessage"] = map[string]any{"mimetype": stk.GetMimetype()}
	}
	if rxn := effective.GetReactionMessage(); rxn != nil {
		msg["reactionMessage"] = map[string]any{
			"emoji":       rxn.GetText(),
			"targetMsgID": rxn.GetKey().GetID(),
		}
	}
	if pc := extractPollCreationMessage(effective); pc != nil {
		opts := make([]string, 0, len(pc.GetOptions()))
		for _, o := range pc.GetOptions() {
			opts = append(opts, o.GetOptionName())
		}
		msg["pollCreationMessage"] = map[string]any{
			"name":    pc.GetName(),
			"options": opts,
		}
	}
	if pu := effective.GetPollUpdateMessage(); pu != nil {
		key := pu.GetPollCreationMessageKey()
		msg["pollUpdateMessage"] = map[string]any{
			"pollChatID": key.GetRemoteJID(),
			"pollMsgID":  key.GetID(),
		}
	} else if pu := raw.GetPollUpdateMessage(); pu != nil {
		key := pu.GetPollCreationMessageKey()
		msg["pollUpdateMessage"] = map[string]any{
			"pollChatID": key.GetRemoteJID(),
			"pollMsgID":  key.GetID(),
		}
	}
	if protocol := protocolMessagePayload(raw, effective); protocol != nil {
		msg["protocolMessage"] = protocol
	}
	if len(msg) == 0 {
		msg["unknown"] = map[string]any{
			"rawFields":       messageFieldNames(raw),
			"effectiveFields": messageFieldNames(effective),
		}
	}
	if whatsapp.IsMediaMessage(effective) {
		if b, err := whatsapp.MarshalMediaProto(effective); err == nil {
			mediaProto = b
		}
	}
	return msg, mediaProto
}

var (
	extractPollCreationMessage = whatsapp.ExtractPollCreationMessage
	visibleProtocolType        = whatsapp.VisibleProtocolType
	isInvisibleProtocolMessage = whatsapp.IsInvisibleProtocolMessage
	protocolMessagePayload     = whatsapp.ProtocolMessagePayload
	effectiveMessage           = whatsapp.EffectiveMessage
	messageFieldNames          = whatsapp.MessageFieldNames
)

var dedupeSeq uint64

func dedupeKey(m WireMessage) string {
	if m.Key.ID != "" {
		return "id:" + m.Key.ID
	}
	seq := atomic.AddUint64(&dedupeSeq, 1)
	return "noid:" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatUint(seq, 10)
}

var quotedText = whatsapp.QuotedText
