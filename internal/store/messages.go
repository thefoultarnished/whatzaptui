package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MessageRecord represents a row in the messages table.
type MessageRecord struct {
	ID          string `json:"id"`
	ChatID      string `json:"chatId"`
	FromMe      bool   `json:"fromMe"`
	Participant string `json:"participant"`
	Timestamp   int64  `json:"timestamp"`
	PushName    string `json:"pushName"`
	Receipt     string `json:"receipt"`
	MessageJSON string `json:"messageJson"`
	MediaProto  string `json:"mediaProto"`
}

// SearchHit represents a full-text search result.
type SearchHit struct {
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	FromMe    bool   `json:"fromMe"`
	Timestamp int64  `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InsertMessage inserts a message record and updates full-text search.
// Returns true if the row was inserted, false if it already existed (deduped).
func (s *Store) InsertMessage(msg MessageRecord, searchableText string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("store: begin tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	res, err := tx.Exec(`
		INSERT OR IGNORE INTO messages (
			id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, msg.ID, msg.ChatID, boolToInt(msg.FromMe), msg.Participant, msg.Timestamp, msg.PushName, msg.Receipt, msg.MessageJSON, msg.MediaProto)
	if err != nil {
		return false, fmt.Errorf("store: insert message: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}

	if searchableText != "" {
		_, _ = tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`,
			msg.ChatID, msg.ID, boolToInt(msg.FromMe))
		if _, err := tx.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`,
			msg.ChatID, msg.ID, boolToInt(msg.FromMe), searchableText); err != nil {
			return false, fmt.Errorf("store: insert fts: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: commit insert message: %w", err)
	}
	tx = nil

	return true, nil
}

// GetMessages queries messages for a chat, optionally before a given timestamp.
func (s *Store) GetMessages(chatID string, beforeTS int64, limit int) ([]MessageRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	const cols = `id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto`

	if beforeTS > 0 {
		rows, err = s.db.Query(`
			SELECT `+cols+` FROM messages
			WHERE chat_id = ? AND ts < ?
			ORDER BY ts DESC LIMIT ?
		`, chatID, beforeTS, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT `+cols+` FROM messages
			WHERE chat_id = ?
			ORDER BY ts DESC LIMIT ?
		`, chatID, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("store: query messages: %w", err)
	}
	defer rows.Close()

	var out []MessageRecord
	for rows.Next() {
		var rec MessageRecord
		var fromMe int
		if err := rows.Scan(
			&rec.ID, &rec.ChatID, &fromMe, &rec.Participant,
			&rec.Timestamp, &rec.PushName, &rec.Receipt,
			&rec.MessageJSON, &rec.MediaProto,
		); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		rec.FromMe = (fromMe == 1)
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GetMessagesAround returns anchorTS and older, anchor, and newer message slices around an anchor message ID.
func (s *Store) GetMessagesAround(chatID, msgID string, limit int) (anchorTS int64, older, anchor, newer []MessageRecord, err error) {
	if s == nil || s.db == nil {
		return 0, nil, nil, nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	var anchorFromMe int
	if err := s.db.QueryRow(
		`SELECT ts, from_me FROM messages WHERE chat_id = ? AND id = ? LIMIT 1`,
		chatID, msgID,
	).Scan(&anchorTS, &anchorFromMe); err != nil {
		return 0, nil, nil, nil, err
	}

	const cols = `id, chat_id, from_me, participant, ts, push_name, receipt, message_json, media_proto`

	scanRows := func(q string, args ...any) ([]MessageRecord, error) {
		rows, err := s.db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var res []MessageRecord
		for rows.Next() {
			var rec MessageRecord
			var fromMe int
			if err := rows.Scan(
				&rec.ID, &rec.ChatID, &fromMe, &rec.Participant,
				&rec.Timestamp, &rec.PushName, &rec.Receipt,
				&rec.MessageJSON, &rec.MediaProto,
			); err != nil {
				return nil, err
			}
			rec.FromMe = (fromMe == 1)
			res = append(res, rec)
		}
		return res, rows.Err()
	}

	older, err = scanRows(`
		SELECT `+cols+` FROM messages
		WHERE chat_id = ? AND ts < ?
		ORDER BY ts DESC LIMIT ?
	`, chatID, anchorTS, limit)
	if err != nil {
		return anchorTS, nil, nil, nil, err
	}

	anchor, err = scanRows(`
		SELECT `+cols+` FROM messages
		WHERE chat_id = ? AND id = ? AND from_me = ?
	`, chatID, msgID, anchorFromMe)
	if err != nil {
		return anchorTS, nil, nil, nil, err
	}

	newer, err = scanRows(`
		SELECT `+cols+` FROM messages
		WHERE chat_id = ? AND ts > ?
		ORDER BY ts ASC LIMIT ?
	`, chatID, anchorTS, limit)
	if err != nil {
		return anchorTS, nil, nil, nil, err
	}

	return anchorTS, older, anchor, newer, nil
}

// DeleteMessage deletes a message from both the messages table and full-text index.
func (s *Store) DeleteMessage(chatID, msgID string, fromMe bool) error {
	if s == nil || s.db == nil {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	fromMeInt := boolToInt(fromMe)
	if _, err := tx.Exec(`DELETE FROM messages WHERE chat_id = ? AND id = ? AND from_me = ?`, chatID, msgID, fromMeInt); err != nil {
		return err
	}
	_, _ = tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, chatID, msgID, fromMeInt)

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

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

// UpdateReceiptStatus updates receipt statuses on matching outgoing messages.
// Returns true if any row was updated.
func (s *Store) UpdateReceiptStatus(chatID string, ids []string, status string) (bool, error) {
	if s == nil || s.db == nil || len(ids) == 0 || status == "" {
		return false, nil
	}

	newRank := receiptStatusRank(status)
	if newRank == 0 {
		return false, nil
	}

	rankExpr := `CASE receipt WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 WHEN 'played' THEN 4 ELSE 0 END`

	placeholders := make([]string, len(ids))
	args := make([]any, 0, 3+len(ids))
	args = append(args, status, chatID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	args = append(args, newRank)

	query := fmt.Sprintf(`
		UPDATE messages SET receipt = ?
		WHERE chat_id = ? AND from_me = 1 AND id IN (%s)
		AND %s < ?
	`, strings.Join(placeholders, ","), rankExpr)

	res, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// EditMessage updates message_json and FTS for an edited message.
func (s *Store) EditMessage(chatID, targetID string, fromMe bool, updateJSON func(oldJSON string) (newJSON string, newText string, err error)) (MessageRecord, bool, error) {
	if s == nil || s.db == nil {
		return MessageRecord{}, false, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return MessageRecord{}, false, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	fromMeInt := boolToInt(fromMe)
	var rec MessageRecord
	rec.ID = targetID
	rec.ChatID = chatID
	rec.FromMe = fromMe

	var oldJSON string
	if err := tx.QueryRow(`
		SELECT ts, participant, push_name, receipt, message_json, media_proto
		FROM messages WHERE chat_id = ? AND id = ? AND from_me = ? LIMIT 1
	`, chatID, targetID, fromMeInt).Scan(
		&rec.Timestamp, &rec.Participant, &rec.PushName, &rec.Receipt, &oldJSON, &rec.MediaProto,
	); err != nil {
		if err == sql.ErrNoRows {
			return MessageRecord{}, false, nil
		}
		return MessageRecord{}, false, err
	}

	newJSON, newText, err := updateJSON(oldJSON)
	if err != nil {
		return MessageRecord{}, false, err
	}
	rec.MessageJSON = newJSON

	if _, err := tx.Exec(`
		UPDATE messages SET message_json = ?
		WHERE chat_id = ? AND id = ? AND from_me = ?
	`, newJSON, chatID, targetID, fromMeInt); err != nil {
		return MessageRecord{}, false, err
	}

	if newText != "" {
		_, _ = tx.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`,
			chatID, targetID, fromMeInt)
		if _, err := tx.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`,
			chatID, targetID, fromMeInt, newText); err != nil {
			return MessageRecord{}, false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return MessageRecord{}, false, err
	}
	tx = nil

	return rec, true, nil
}

// GetMediaProto retrieves the media_proto base64 protobuf string for a message.
func (s *Store) GetMediaProto(chatID, msgID string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var mediaProto string
	err := s.db.QueryRow(
		`SELECT media_proto FROM messages WHERE chat_id = ? AND id = ? AND media_proto != ''`,
		chatID, msgID,
	).Scan(&mediaProto)
	return mediaProto, err
}

// GetMessageJSON retrieves the message_json payload for a message.
func (s *Store) GetMessageJSON(chatID, msgID string) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var msgJSON string
	err := s.db.QueryRow(
		`SELECT message_json FROM messages WHERE chat_id = ? AND id = ? LIMIT 1`,
		chatID, msgID,
	).Scan(&msgJSON)
	return msgJSON, err
}

// GetRecentIncomingMessageIDs returns IDs of recent incoming messages in a chat.
func (s *Store) GetRecentIncomingMessageIDs(chatID string, limit int) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id FROM messages
		WHERE chat_id = ? AND from_me = 0 AND id != ''
		ORDER BY ts DESC LIMIT ?
	`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && id != "" {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// UpsertMessageFTS inserts or replaces an entry in the FTS5 search index.
func (s *Store) UpsertMessageFTS(chatID, msgID string, fromMe bool, body string) error {
	if s == nil || s.db == nil || body == "" {
		return nil
	}
	fromMeInt := boolToInt(fromMe)
	_, _ = s.db.Exec(`DELETE FROM messages_fts WHERE chat_id = ? AND msg_id = ? AND from_me = ?`, chatID, msgID, fromMeInt)
	_, err := s.db.Exec(`INSERT INTO messages_fts (chat_id, msg_id, from_me, body) VALUES (?, ?, ?, ?)`, chatID, msgID, fromMeInt, body)
	return err
}

// SearchMessages performs full-text search against messages_fts.
func (s *Store) SearchMessages(query, chatID string, limit int) ([]SearchHit, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	escaped := make([]string, len(tokens))
	for i, t := range tokens {
		escaped[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	matchExpr := strings.Join(escaped, " ")

	const baseCols = `
		messages_fts.chat_id,
		messages_fts.msg_id,
		messages_fts.from_me,
		COALESCE(messages.ts, 0),
		snippet(messages_fts, 3, '<b>', '</b>', '...', 12)
	`
	const baseFrom = `
		FROM messages_fts
		LEFT JOIN messages ON messages.chat_id = messages_fts.chat_id
			AND messages.id = messages_fts.msg_id
			AND messages.from_me = messages_fts.from_me
	`

	var rows *sql.Rows
	var err error

	if chatID != "" {
		rows, err = s.db.Query(`
			SELECT `+baseCols+baseFrom+`
			WHERE messages_fts MATCH ? AND messages_fts.chat_id = ?
			ORDER BY rank LIMIT ?
		`, matchExpr, chatID, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT `+baseCols+baseFrom+`
			WHERE messages_fts MATCH ?
			ORDER BY rank LIMIT ?
		`, matchExpr, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("store: search messages: %w", err)
	}
	defer rows.Close()

	var hits []SearchHit
	for rows.Next() {
		var hit SearchHit
		var fromMe int
		if err := rows.Scan(&hit.ChatID, &hit.MessageID, &fromMe, &hit.Timestamp, &hit.Snippet); err != nil {
			return nil, fmt.Errorf("store: scan search hit: %w", err)
		}
		hit.FromMe = (fromMe == 1)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// BackfillFTS indexes legacy messages missing from messages_fts using a resumable watermark.
func (s *Store) BackfillFTS(extractText func(msgJSON string) string) error {
	if s == nil || s.db == nil || extractText == nil {
		return nil
	}

	var watermark int64
	_ = s.db.QueryRow(`SELECT val FROM fts_backfill_meta WHERE key = 'rowid'`).Scan(&watermark)

	type ftsRow struct {
		chatID, msgID string
		fromMe        int
		body          string
	}

	const batchSize = 500
	cutoff := time.Now().Unix()
	lastRowID := watermark

	for {
		rows, err := s.db.Query(`
			SELECT rowid, id, chat_id, from_me, message_json FROM messages
			WHERE rowid > ? AND ts <= ?
			ORDER BY rowid LIMIT ?
		`, lastRowID, cutoff, batchSize)
		if err != nil {
			return err
		}

		var pending []ftsRow
		var scanned int
		for rows.Next() {
			scanned++
			var rowid int64
			var id, chatID, msgJSON string
			var fromMe int
			if err := rows.Scan(&rowid, &id, &chatID, &fromMe, &msgJSON); err != nil {
				rows.Close()
				return err
			}
			lastRowID = rowid
			text := extractText(msgJSON)
			if text != "" {
				pending = append(pending, ftsRow{chatID, id, fromMe, text})
			}
		}
		rows.Close()

		if scanned == 0 {
			break
		}

		if len(pending) > 0 {
			tx, err := s.db.Begin()
			if err != nil {
				return err
			}
			stmt, err := tx.Prepare(`INSERT OR IGNORE INTO messages_fts(chat_id, msg_id, from_me, body) VALUES(?, ?, ?, ?)`)
			if err != nil {
				_ = tx.Rollback()
				return err
			}
			for _, r := range pending {
				_, _ = stmt.Exec(r.chatID, r.msgID, r.fromMe, r.body)
			}
			_ = stmt.Close()
			if err := tx.Commit(); err != nil {
				return err
			}
		}

		_, _ = s.db.Exec(`
			INSERT INTO fts_backfill_meta(key, val) VALUES ('rowid', ?)
			ON CONFLICT(key) DO UPDATE SET val=excluded.val
		`, lastRowID)
	}

	return nil
}
