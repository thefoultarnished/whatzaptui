package backend

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func (a *App) handleChats(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	rawChats := make([]Chat, 0, len(a.state.Chats))
	for _, c := range a.state.Chats {
		rawChats = append(rawChats, c)
	}
	contacts := make(map[string]Contact, len(a.state.Contacts))
	for k, v := range a.state.Contacts {
		contacts[k] = v
	}
	a.mu.RUnlock()

	nameByID := map[string]string{}
	for _, c := range rawChats {
		if n := strings.TrimSpace(c.Name); n != "" {
			nameByID[c.ID] = n
		}
	}
	for id, ct := range contacts {
		if strings.HasSuffix(id, "@g.us") {
			continue
		}
		if n := strings.TrimSpace(ct.Notify); n != "" {
			nameByID[id] = n
		} else if n := strings.TrimSpace(ct.Name); n != "" {
			nameByID[id] = n
		}
	}

	mergedByID := map[string]Chat{}
	for _, c := range rawChats {
		resolvedID := a.canonicalizeChatID(c.ID)
		if resolvedID != "" {
			c.ID = resolvedID
		}
		if c.Name == "" && !strings.HasSuffix(c.ID, "@g.us") {
			if ct, ok := contacts[c.ID]; ok {
				if n := strings.TrimSpace(ct.Notify); n != "" {
					c.Name = n
				} else if n := strings.TrimSpace(ct.Name); n != "" {
					c.Name = n
				}
			}
		}
		if c.Name == "" && a.client != nil && a.client.Store != nil && a.client.Store.LIDs != nil {
			if jid, err := types.ParseJID(c.ID); err == nil && jid.Server == types.DefaultUserServer {
				if lid, err := types.ParseJID(jid.User + "@lid"); err == nil {
					pn, err := a.getPNForLID(lid)
					if err == nil && pn.User != "" {
						pnID := canonicalChatID(pn.String())
						if ct, ok := contacts[pnID]; ok {
							if n := strings.TrimSpace(ct.Notify); n != "" {
								c.Name = n
							} else if n := strings.TrimSpace(ct.Name); n != "" {
								c.Name = n
							}
						}
						if c.Name == "" {
							if n := strings.TrimSpace(nameByID[pnID]); n != "" {
								c.Name = n
							}
						}
					}
				}
			}
		}
		mergedByID[c.ID] = mergeChat(mergedByID[c.ID], c)
	}

	chats := make([]Chat, 0, len(mergedByID))
	for _, c := range mergedByID {
		chats = append(chats, c)
	}
	sort.Slice(chats, func(i, j int) bool {
		return chats[i].ConversationTimestamp > chats[j].ConversationTimestamp
	})
	named := 0
	for _, c := range chats {
		if strings.TrimSpace(c.Name) != "" || strings.TrimSpace(c.Subject) != "" {
			named++
		}
	}
	a.actionLog.Event("chats.served", map[string]string{
		"count": intStr(len(chats)),
		"named": intStr(named),
	})
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (a *App) handleSyncGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		writeErr(w, http.StatusConflict, "not connected")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	groups, err := a.client.GetJoinedGroups(ctx)
	if err != nil {
		writeInternalErr(w, err)
		return
	}

	updated := a.refreshGroupMetadata()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"total":   len(groups),
		"updated": updated,
	})
}

func (a *App) handleGroupMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !a.requireConnectedClient(w) {
		return
	}
	jidStr := r.URL.Query().Get("jid")
	if !strings.HasSuffix(jidStr, "@g.us") {
		writeErr(w, http.StatusBadRequest, "jid must be a group JID ending in @g.us")
		return
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}
	info, err := a.client.GetGroupInfo(r.Context(), jid)
	if err != nil {
		writeInternalErr(w, fmt.Errorf("get group info: %w", err))
		return
	}

	a.mu.RLock()
	contacts := make(map[string]Contact, len(a.state.Contacts))
	for k, v := range a.state.Contacts {
		contacts[k] = v
	}
	a.mu.RUnlock()

	resolveName := func(p types.GroupParticipant) (name string, saved bool) {
		phoneJID := p.PhoneNumber
		if phoneJID.IsEmpty() {
			phoneJID = p.JID
		}
		key := phoneJID.User + "@" + phoneJID.Server
		if ct, ok := contacts[key]; ok {
			if n := strings.TrimSpace(ct.Notify); n != "" {
				return n, true
			}
			if n := strings.TrimSpace(ct.Name); n != "" {
				return n, true
			}
		}
		return phoneJID.User, false
	}

	var savedNames, unknownNames []string
	for _, p := range info.Participants {
		name, isSaved := resolveName(p)
		if isSaved {
			savedNames = append(savedNames, name)
		} else {
			unknownNames = append(unknownNames, name)
		}
	}

	// Up to 4: saved contacts first, pad with unknown numbers if needed.
	members := make([]string, 0, 4)
	members = append(members, savedNames...)
	if len(members) > 4 {
		members = members[:4]
	}
	if len(members) < 4 && len(unknownNames) > 0 {
		need := 4 - len(members)
		if need > len(unknownNames) {
			need = len(unknownNames)
		}
		members = append(members, unknownNames[:need]...)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"members": members,
		"total":   len(info.Participants),
	})
}
