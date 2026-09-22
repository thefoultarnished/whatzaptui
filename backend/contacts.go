package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func (a *App) handleContacts(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	contacts := make([]Contact, 0, len(a.state.Contacts))
	for _, c := range a.state.Contacts {
		contacts = append(contacts, c)
	}
	a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts})
}

func (a *App) handleResolveLIDPN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || a.client.Store == nil || a.client.Store.LIDs == nil {
		writeErr(w, http.StatusInternalServerError, "lid mapping store unavailable")
		return
	}

	raw := strings.TrimSpace(r.URL.Query().Get("id"))
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	jid, err := types.ParseJID(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	out := map[string]any{
		"input":  raw,
		"server": jid.Server,
	}

	switch jid.Server {
	case types.DefaultUserServer:
		lid, err := a.client.Store.LIDs.GetLIDForPN(ctx, jid)
		out["lookup"] = "pn_to_lid"
		out["lid"] = lid.String()
		if err != nil {
			out["error"] = err.Error()
		}
	case types.HiddenUserServer:
		pn, err := a.client.Store.LIDs.GetPNForLID(ctx, jid)
		out["lookup"] = "lid_to_pn"
		out["pn"] = pn.String()
		if err != nil {
			out["error"] = err.Error()
		}
	default:
		writeErr(w, http.StatusBadRequest, "id must be @s.whatsapp.net or @lid")
		return
	}

	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSyncContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return
	}

	if a.client.Store != nil && a.client.Store.AppState != nil {
		for _, patch := range []appstate.WAPatchName{
			appstate.WAPatchCriticalBlock,
			appstate.WAPatchRegularLow,
			appstate.WAPatchRegularHigh,
			appstate.WAPatchRegular,
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			a.safeFetchAppState(ctx, patch)
			cancel()
		}
	}
	if a.client.Store == nil || a.client.Store.Contacts == nil {
		writeErr(w, http.StatusInternalServerError, "contacts store unavailable")
		return
	}

	storeCtx, storeCancel := context.WithTimeout(context.Background(), 12*time.Second)
	allContacts, err := a.client.Store.Contacts.GetAllContacts(storeCtx)
	storeCancel()
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	updated := 0
	total := 0
	a.mu.Lock()
	a.state.Contacts = make(map[string]Contact)
	if err := a.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM contacts`); err != nil {
			return fmt.Errorf("delete contacts: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM chats WHERE conv_ts = 0 AND id NOT LIKE '%@g.us'`); err != nil {
			return fmt.Errorf("delete empty chats: %w", err)
		}
		return nil
	}); err != nil {
		log.Printf("syncContacts wipe: %v", err)
	}
	for id, ch := range a.state.Chats {
		if ch.ConversationTimestamp == 0 && !strings.HasSuffix(id, "@g.us") {
			delete(a.state.Chats, id)
		}
	}
	for jid, info := range allContacts {
		raw := strings.TrimSpace(jid.String())
		if raw == "" {
			continue
		}
		total++
		cid := a.canonicalizeChatID(raw)

		fullName := strings.TrimSpace(info.FullName)
		firstName := strings.TrimSpace(info.FirstName)
		businessName := strings.TrimSpace(info.BusinessName)
		pushName := strings.TrimSpace(info.PushName)

		ch, hasChat := a.state.Chats[cid]
		if hasChat && ch.ConversationTimestamp == 0 {
			hasChat = false
		}
		if fullName == "" && firstName == "" && businessName == "" && !hasChat {
			continue
		}

		name := fullName
		if name == "" {
			name = firstName
		}
		if name == "" {
			name = businessName
		}
		if name == "" {
			name = pushName
		}

		oldContact := a.state.Contacts[cid]
		contact := oldContact
		contact.ID = cid
		if name != "" {
			contact.Name = name
			contact.Notify = name
		}
		if fullName != "" || firstName != "" || businessName != "" {
			contact.Stored = true
		}
		a.state.Contacts[cid] = contact

		oldChat := a.state.Chats[cid]
		chat := oldChat
		chat.ID = cid
		if name != "" {
			chat.Name = name
		}
		a.state.Chats[cid] = chat

		if oldContact != contact || oldChat != chat {
			updated++
		}
	}
	a.mu.Unlock()

	// Second pass: ask WA server for missing profile data for unresolved direct chats.
	unresolved := []types.JID{}
	a.mu.RLock()
	for id, ch := range a.state.Chats {
		if strings.HasSuffix(id, "@g.us") || id == "status@broadcast" {
			continue
		}
		if strings.TrimSpace(ch.Name) != "" {
			continue
		}
		ct := a.state.Contacts[id]
		if strings.TrimSpace(ct.Notify) != "" || strings.TrimSpace(ct.Name) != "" {
			continue
		}
		if jid, err := types.ParseJID(id); err == nil && jid.Server == types.DefaultUserServer {
			unresolved = append(unresolved, jid.ToNonAD())
		}
	}
	a.mu.RUnlock()

	enriched := 0
	queried := 0
	lookupErrors := 0
	if len(unresolved) > 0 {
		seen := map[string]struct{}{}
		unique := make([]types.JID, 0, len(unresolved))
		for _, j := range unresolved {
			key := j.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			unique = append(unique, j)
		}
		queried = len(unique)

		const batchSize = 100
		for i := 0; i < len(unique); i += batchSize {
			end := i + batchSize
			if end > len(unique) {
				end = len(unique)
			}
			batch := unique[i:end]
			lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 8*time.Second)
			infoMap, err := a.client.GetUserInfo(lookupCtx, batch)
			lookupCancel()
			if err != nil {
				lookupErrors++
				continue
			}
			a.mu.Lock()
			for pnJID, info := range infoMap {
				cid := a.canonicalizeChatID(pnJID.String())
				name := ""
				if info.VerifiedName != nil && info.VerifiedName.Details != nil {
					name = strings.TrimSpace(info.VerifiedName.Details.GetVerifiedName())
				}
				if name == "" {
					continue
				}

				ct := a.state.Contacts[cid]
				if ct.ID == "" {
					ct.ID = cid
				}
				changed := false
				if strings.TrimSpace(name) != "" && ct.Notify != name {
					ct.Notify = name
					changed = true
				}
				if strings.TrimSpace(name) != "" && ct.Name != name {
					ct.Name = name
					changed = true
				}
				a.state.Contacts[cid] = ct

				ch := a.state.Chats[cid]
				if ch.ID == "" {
					ch.ID = cid
				}
				if strings.TrimSpace(name) != "" && ch.Name != name {
					ch.Name = name
					changed = true
				}
				a.state.Chats[cid] = ch

				if changed {
					enriched++
				}
			}
			a.mu.Unlock()
		}
	}

	if updated > 0 || enriched > 0 {
		a.recanonicalizeState()
	}
	unresolvedAfter := 0
	a.mu.RLock()
	for id, ch := range a.state.Chats {
		if strings.HasSuffix(id, "@g.us") || id == "status@broadcast" {
			continue
		}
		if strings.TrimSpace(ch.Name) != "" {
			continue
		}
		ct := a.state.Contacts[id]
		if strings.TrimSpace(ct.Notify) != "" || strings.TrimSpace(ct.Name) != "" {
			continue
		}
		unresolvedAfter++
	}
	a.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"total":        total,
		"updated":      updated,
		"enriched":     enriched,
		"queried":      queried,
		"lookupErrors": lookupErrors,
		"unresolved":   unresolvedAfter,
	})
}

func (a *App) handleGetWhitelist(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Phone   string `json:"phone"`
		Name    string `json:"name"`
		Allowed int    `json:"allowed"`
	}
	var result []entry
	err := a.withPermissionDB(func(db *sql.DB) error {
		rows, err := db.Query(`SELECT phone, name, allowed FROM chat_permissions ORDER BY phone`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.Phone, &e.Name, &e.Allowed); err == nil {
				result = append(result, e)
			}
		}
		return rows.Err()
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeInternalErr(w, err)
		}
		return
	}
	if result == nil {
		result = []entry{}
	}
	defaultAllowed, err := a.loadDefaultAllowed()
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": result, "defaultAllowed": defaultAllowed})
}

// loadDefaultAllowed reports the global whitelist default. Missing table
// row (fresh DBs predate it, it is only ever INSERTed) means deny. A DB
// error is returned so callers can 500 instead of silently failing closed.
func (a *App) loadDefaultAllowed() (bool, error) {
	var allowed int
	err := a.withPermissionDB(func(db *sql.DB) error {
		err := db.QueryRow(`SELECT allowed FROM whitelist_default WHERE id = 1`).Scan(&allowed)
		if errors.Is(err, sql.ErrNoRows) {
			allowed = 0
			return nil
		}
		return err
	})
	if err != nil {
		return false, err
	}
	return allowed == 1, nil
}

// handleSetWhitelistDefault flips the global default in one call and aligns
// every per-chat row with it in a single UPDATE, so /whitelistall and
// /blacklistall are O(1) network-wise. Row names are preserved; rows that
// already match keep their values, so subsequent per-chat /whitelist and
// /blacklist commands work as opt-in/opt-out overrides of the new default.
func (a *App) handleSetWhitelistDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Allowed int `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Allowed != 0 && req.Allowed != 1 {
		writeErr(w, http.StatusBadRequest, "allowed must be 0 or 1")
		return
	}
	err := a.withPermissionDB(func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`INSERT INTO whitelist_default (id, allowed) VALUES (1, ?)
			ON CONFLICT(id) DO UPDATE SET allowed=excluded.allowed`, req.Allowed); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE chat_permissions SET allowed = ?`, req.Allowed); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeInternalErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// normalizeWhitelistPhone validates and normalizes a phone string
// for chat_permissions. Accepts digits only (e.g. "15551230001")
// or digits + @s.whatsapp.net suffix (e.g. "15551230001@s.whatsapp.net")
// — the suffix is stripped to the local part that phoneFromJID would
// extract from a chat ID. Returns an error if the input is empty,
// contains non-digit characters, is a bare @server with no local
// part, or (A-9) is a group JID (@g.us) — the whitelist is a
// phone-number allowlist, not a group allowlist.
func normalizeWhitelistPhone(phone string) (string, error) {
	if strings.HasSuffix(phone, "@g.us") {
		return "", fmt.Errorf("group chats cannot be whitelisted")
	}
	local := phone
	if idx := strings.IndexByte(phone, '@'); idx >= 0 {
		local = phone[:idx]
	}
	if local == "" {
		return "", fmt.Errorf("phone is required")
	}
	for _, r := range local {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("phone must be digits only")
		}
	}
	return local, nil
}

func (a *App) handleSetWhitelist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Phone   string `json:"phone"`
		Name    string `json:"name"`
		Allowed int    `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	phone, err := normalizeWhitelistPhone(req.Phone)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err = a.withPermissionDB(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, ?)
			 ON CONFLICT(phone) DO UPDATE SET allowed=excluded.allowed,
			                                  name=CASE WHEN excluded.name != '' THEN excluded.name ELSE chat_permissions.name END`,
			phone, req.Name, req.Allowed,
		)
		return err
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeInternalErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleSetName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Phone == "" {
		writeErr(w, http.StatusBadRequest, "phone is required")
		return
	}
	// create row if not exists (allowed stays 0), then only update name
	err := a.withPermissionDB(func(db *sql.DB) error {
		_, err := db.Exec(
			`INSERT INTO chat_permissions (phone, name, allowed) VALUES (?, ?, 0)
			 ON CONFLICT(phone) DO UPDATE SET name=excluded.name`,
			req.Phone, req.Name,
		)
		return err
	})
	if err != nil {
		if err.Error() == "permission store unavailable" {
			writeErr(w, http.StatusConflict, err.Error())
		} else {
			writeInternalErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleBlock(w http.ResponseWriter, r *http.Request) {
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
	rawChatID := strings.TrimSpace(req.ChatID)
	req.ChatID = a.canonicalizeChatID(rawChatID)
	if req.ChatID == "" {
		writeErr(w, http.StatusBadRequest, "chatId is required")
		return
	}
	if strings.HasSuffix(req.ChatID, "@g.us") || req.ChatID == "status@broadcast" {
		writeErr(w, http.StatusBadRequest, "cannot block group or status chats")
		return
	}
	jid, err := types.ParseJID(req.ChatID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chatId")
		return
	}
	jid = jid.ToNonAD()
	log.Printf("handleBlock: canonical=%s parsed=%s server=%s", req.ChatID, jid.String(), jid.Server)
	var altJID types.JID
	_, err = a.client.UpdateBlocklist(context.Background(), jid, events.BlocklistChangeActionBlock)
	if err != nil {
		if jid.Server == types.DefaultUserServer && a.client.Store != nil && a.client.Store.LIDs != nil {
			if l, errAlt := a.client.Store.LIDs.GetLIDForPN(context.Background(), jid); errAlt == nil && l.User != "" {
				altJID = l
			}
		} else if jid.Server == types.HiddenUserServer && a.client.Store != nil && a.client.Store.LIDs != nil {
			if p, errAlt := a.client.Store.LIDs.GetPNForLID(context.Background(), jid); errAlt == nil && p.User != "" {
				altJID = p
			}
		}
		if altJID.User != "" {
			_, err = a.client.UpdateBlocklist(context.Background(), altJID, events.BlocklistChangeActionBlock)
		}
	}
	if err != nil {
		writeInternalErr(w, fmt.Errorf("failed to block %s (alt: %s): %w", jid.String(), altJID.String(), err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
