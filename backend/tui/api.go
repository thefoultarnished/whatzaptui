package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const backendStartupLogLimit = 8192
const apiTokenEnvVar = "WHATZAP_API_TOKEN"
const authHeaderName = "Authorization"

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = b.buf[len(b.buf)-b.limit:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

func formatBackendStartupError(prefix string, raw string) error {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return fmt.Errorf("%s", prefix)
	}
	return fmt.Errorf("%s: %s", prefix, msg)
}

func apiErrorFromResponse(res *http.Response, fallback string) error {
	if res == nil {
		return fmt.Errorf("%s", fallback)
	}
	raw, _ := io.ReadAll(res.Body)
	msg := strings.TrimSpace(string(raw))
	if len(raw) > 0 {
		var payload struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			if strings.TrimSpace(payload.Error) != "" {
				msg = strings.TrimSpace(payload.Error)
			} else if strings.TrimSpace(payload.Message) != "" {
				msg = strings.TrimSpace(payload.Message)
			}
		}
	}
	if msg == "" {
		msg = fallback
	}
	return fmt.Errorf("%s: %s", res.Status, msg)
}

func ensureBackend(c *http.Client, base, dir, apiToken string) tea.Cmd {
	return func() tea.Msg {
		if health(c, base) == nil {
			if err := probeAuth(c, base, apiToken); err != nil {
				return initMsg{err: err}
			}
			return initMsg{}
		}
		var cmd *exec.Cmd
		binName := "backend"
		if runtime.GOOS == "windows" {
			binName = "backend.exe"
		}
		binPath := filepath.Join(dir, binName)
		if exists(binPath) {
			cmd = exec.Command(binPath)
		} else {
			goBin := "go"
			if runtime.GOOS == "windows" {
				goBin = "go.exe"
			}
			cmd = exec.Command(goBin, "run", ".")
		}
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), apiTokenEnvVar+"="+apiToken)
		logBuf := &limitedBuffer{limit: backendStartupLogLimit}
		cmd.Stdout = logBuf
		cmd.Stderr = logBuf
		if err := cmd.Start(); err != nil {
			return initMsg{err: err}
		}
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- cmd.Wait()
		}()
		deadline := time.Now().Add(35 * time.Second)
		for time.Now().Before(deadline) {
			if health(c, base) == nil {
				if err := probeAuth(c, base, apiToken); err != nil {
					return initMsg{err: err}
				}
				return initMsg{started: true, cmd: cmd}
			}
			select {
			case err := <-waitCh:
				if err == nil {
					return initMsg{err: formatBackendStartupError("backend exited before becoming ready", logBuf.String())}
				}
				return initMsg{err: formatBackendStartupError(fmt.Sprintf("backend failed to start (%v)", err), logBuf.String())}
			default:
			}
			time.Sleep(400 * time.Millisecond)
		}
		return initMsg{err: formatBackendStartupError("backend did not become ready", logBuf.String())}
	}
}

func health(c *http.Client, base string) error {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/health", nil)
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("health status %s", res.Status)
	}
	return nil
}

func probeAuth(c *http.Client, base, apiToken string) error {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/contacts", nil)
	attachAuthHeader(req, apiToken)
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("backend is running with a different API token; stop the stale backend and retry")
	}
	if res.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("backend auth probe failed: %s %s", res.Status, strings.TrimSpace(string(raw)))
	}
	return nil
}

func openWS(url, apiToken string) tea.Cmd {
	return func() tea.Msg {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+apiToken)
		conn, _, err := websocket.DefaultDialer.Dial(url, header)
		if err != nil {
			return wsOpenMsg{err: err}
		}
		ch := make(chan env)
		go func() {
			defer close(ch)
			for {
				_, b, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var e env
				if json.Unmarshal(b, &e) == nil {
					ch <- e
				}
			}
		}()
		return wsOpenMsg{conn: conn, ch: ch}
	}
}
func readWS(ch <-chan env) tea.Cmd {
	return func() tea.Msg { e, ok := <-ch; return wsEvtMsg{evt: e, ok: ok} }
}
func postEmpty(c *http.Client, url string, ok func([]byte) tea.Msg) tea.Cmd {
	return postJSON(c, url, map[string]string{}, ok)
}

func postJSON(c *http.Client, url string, body any, ok func([]byte) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(url))
		res, err := c.Do(req)
		if err != nil {
			return dataErr{err: err}
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if res.StatusCode/100 != 2 {
			return dataErr{err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		if ok != nil {
			return ok(raw)
		}
		return dataErr{}
	}
}

func logout(c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]string{})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/logout", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return logoutMsg{err: err}
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &out)
		if res.StatusCode/100 != 2 {
			msg := strings.TrimSpace(out.Error)
			if msg == "" {
				msg = strings.TrimSpace(string(raw))
			}
			if msg == "" {
				msg = res.Status
			}
			return logoutMsg{err: fmt.Errorf("%s", msg)}
		}
		if strings.TrimSpace(out.Message) == "" {
			out.Message = "Logged out successfully"
		}
		return logoutMsg{msg: out.Message}
	}
}

func getChats(c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/chats", nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return chatsMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return chatsMsg{err: apiErrorFromResponse(res, "failed to load chats")}
		}
		var out struct {
			Chats []chat `json:"chats"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return chatsMsg{err: err}
		}
		return chatsMsg{chats: out.Chats}
	}
}
func getContacts(c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/contacts", nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return contactsMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return contactsMsg{err: apiErrorFromResponse(res, "failed to load contacts")}
		}
		var out struct {
			Contacts []contact `json:"contacts"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return contactsMsg{err: err}
		}
		return contactsMsg{contacts: out.Contacts}
	}
}
func getMsgs(c *http.Client, base, chatID string, limit int) tea.Cmd {
	return getMsgsBefore(c, base, chatID, limit, 0)
}

// getMsgsBefore fetches up to `limit` messages older than `before` (unix seconds).
// Pass before=0 for the initial fetch (returns the most recent messages).
func getMsgsBefore(c *http.Client, base, chatID string, limit int, before int64) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("limit", strconv.Itoa(limit))
		if before > 0 {
			q.Set("before", strconv.FormatInt(before, 10))
		}
		u := base + "/messages?" + q.Encode()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			if before > 0 {
				return olderMsgsMsg{chatID: chatID, requested: limit, err: err}
			}
			return msgsMsg{chatID: chatID, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			apiErr := apiErrorFromResponse(res, "failed to load messages")
			if before > 0 {
				return olderMsgsMsg{chatID: chatID, requested: limit, err: apiErr}
			}
			return msgsMsg{chatID: chatID, err: apiErr}
		}
		var out struct {
			Messages []wireMsg `json:"messages"`
			HasMore  bool      `json:"hasMore"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			if before > 0 {
				return olderMsgsMsg{chatID: chatID, requested: limit, err: err}
			}
			return msgsMsg{chatID: chatID, err: err}
		}
		if before > 0 {
			return olderMsgsMsg{chatID: chatID, requested: limit, msgs: out.Messages, hasMore: out.HasMore}
		}
		return msgsMsg{chatID: chatID, msgs: out.Messages, hasMore: out.HasMore}
	}
}

func getMsgsAround(c *http.Client, base, chatID, msgID string, limit int) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("around", msgID)
		q.Set("limit", strconv.Itoa(limit))
		u := base + "/messages?" + q.Encode()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return aroundMsgsMsg{chatID: chatID, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return aroundMsgsMsg{chatID: chatID, err: apiErrorFromResponse(res, "failed to load messages")}
		}
		var out struct {
			Messages    []wireMsg `json:"messages"`
			AnchorIndex int       `json:"anchorIndex"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return aroundMsgsMsg{chatID: chatID, err: err}
		}
		return aroundMsgsMsg{chatID: chatID, msgs: out.Messages, anchorIndex: out.AnchorIndex}
	}
}

func searchMsgs(c *http.Client, base, query string) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("q", query)
		q.Set("limit", "50")
		u := base + "/search?" + q.Encode()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return searchResultsMsg{query: query, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return searchResultsMsg{query: query, err: apiErrorFromResponse(res, "search failed")}
		}
		var out struct {
			Results []searchHit `json:"results"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return searchResultsMsg{query: query, err: err}
		}
		return searchResultsMsg{query: query, results: out.Results}
	}
}

func send(c *http.Client, base, chatID, text string, replyTo *wireMsg, pendingID string) tea.Cmd {
	return func() tea.Msg {
		payload := map[string]any{"chatId": chatID, "text": text}
		if replyTo != nil {
			payload["replyToMsgId"] = replyTo.Key.ID
			payload["replyToText"] = renderMessageBody(replyTo.Message)
			participant := replyTo.Key.RemoteJID
			if replyTo.Key.Participant != "" {
				participant = replyTo.Key.Participant
			}
			if replyTo.Key.FromMe {
				participant = ""
			}
			payload["replyToParticipant"] = participant
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/messages/send", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(res.Body)
			return sentMsg{chatID: chatID, pendingID: pendingID, err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		var out struct {
			Message wireMsg `json:"message"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		return sentMsg{chatID: chatID, pendingID: pendingID, msg: out.Message}
	}
}

func sendFile(c *http.Client, base, chatID, kind, path, caption string, pendingID string) tea.Cmd {
	return func() tea.Msg {
		payload := map[string]any{
			"chatId":  chatID,
			"kind":    kind,
			"path":    path,
			"caption": caption,
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/messages/send-file", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(res.Body)
			return sentMsg{chatID: chatID, pendingID: pendingID, err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		var out struct {
			Message wireMsg `json:"message"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		return sentMsg{chatID: chatID, pendingID: pendingID, msg: out.Message}
	}
}
func (x m) cleanup() {
	if x.demoMode {
		return
	}
	if x.ws != nil {
		_ = x.ws.Close()
	}
	if !x.startedBackend || x.backend == nil || x.backend.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(x.backend.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = x.backend.Process.Kill()
}

func getWhitelist(c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/whitelist", nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return whitelistLoadMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return whitelistLoadMsg{err: apiErrorFromResponse(res, "failed to load whitelist")}
		}
		var out struct {
			Contacts []struct {
				Phone   string `json:"phone"`
				Name    string `json:"name"`
				Allowed int    `json:"allowed"`
			} `json:"contacts"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return whitelistLoadMsg{err: err}
		}
		wl := map[string]string{}
		names := map[string]string{}
		for _, e := range out.Contacts {
			if e.Allowed == 1 {
				wl[e.Phone] = e.Name
			}
			if e.Name != "" {
				names[e.Phone] = e.Name
			}
		}
		return whitelistLoadMsg{whitelist: wl, names: names}
	}
}

func downloadMedia(c *http.Client, base, chatID, msgID string) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("msgId", msgID)
		u := base + "/media/download?" + q.Encode()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return mediaDownloadMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return mediaDownloadMsg{err: apiErrorFromResponse(res, "failed to download media")}
		}
		var out struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return mediaDownloadMsg{err: err}
		}
		if out.Error != "" {
			return mediaDownloadMsg{err: fmt.Errorf("%s", out.Error)}
		}
		return mediaDownloadMsg{path: out.Path}
	}
}

func openFile(path string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", "", path)
		case "darwin":
			cmd = exec.Command("open", path)
		default:
			cmd = exec.Command("xdg-open", path)
		}
		if err := cmd.Start(); err != nil {
			return fileOpenMsg{path: path, err: err}
		}
		return fileOpenMsg{path: path}
	}
}

func setName(c *http.Client, base, phone, name string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]any{"phone": phone, "name": name})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/names/set", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return whitelistSetMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return whitelistSetMsg{err: apiErrorFromResponse(res, "failed to rename contact")}
		}
		return whitelistSetMsg{}
	}
}

func setWhitelistEntry(c *http.Client, base, phone, name string, allowed int) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]any{"phone": phone, "name": name, "allowed": allowed})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/whitelist/set", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return whitelistSetMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(res.Body)
			return whitelistSetMsg{err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		return whitelistSetMsg{}
	}
}

func attachAuthHeader(req *http.Request, apiToken string) {
	if req == nil || apiToken == "" {
		return
	}
	req.Header.Set(authHeaderName, "Bearer "+apiToken)
}

func apiTokenFromURL(base string) string {
	return strings.TrimSpace(os.Getenv(apiTokenEnvVar))
}

func syncContacts(c *http.Client, base string) tea.Cmd {
	longClient := *c
	longClient.Timeout = 5 * time.Minute
	return postEmpty(&longClient, base+"/sync/contacts", func(raw []byte) tea.Msg {
		var out struct {
			Updated      int `json:"updated"`
			Enriched     int `json:"enriched"`
			Queried      int `json:"queried"`
			LookupErrors int `json:"lookupErrors"`
			Unresolved   int `json:"unresolved"`
			Total        int `json:"total"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return syncContactsDoneMsg{msg: "Contacts sync complete"}
		}
		msg := fmt.Sprintf("Sync: local %d, server %d/%d, unresolved %d", out.Updated, out.Enriched, out.Queried, out.Unresolved)
		if out.LookupErrors > 0 {
			msg += fmt.Sprintf(", server lookup timed out %d time(s)", out.LookupErrors)
		}
		return syncContactsDoneMsg{msg: msg}
	})
}

func syncGroups(c *http.Client, base string) tea.Cmd {
	longClient := *c
	longClient.Timeout = 5 * time.Minute
	return postEmpty(&longClient, base+"/sync/groups", func(raw []byte) tea.Msg {
		var out struct {
			Updated int `json:"updated"`
			Total   int `json:"total"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return syncGroupsDoneMsg{msg: "Groups sync complete"}
		}
		return syncGroupsDoneMsg{msg: fmt.Sprintf("Groups synced: %d/%d updated", out.Updated, out.Total)}
	})
}

type blockMsg struct {
	err error
}

func blockContact(c *http.Client, base, chatID string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]string{"chatId": chatID})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/block", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return blockMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(res.Body)
			return blockMsg{err: fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(raw)))}
		}
		return blockMsg{}
	}
}
