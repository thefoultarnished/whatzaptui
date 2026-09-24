package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
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

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

const backendStartupLogLimit = 8192
const authHeaderName = "Authorization"

// currentAPIToken is the session token (S-1) resolved once at startup by
// resolveSessionToken() in main.go. Replaces the old WHATZAP_API_TOKEN env
// var lookup in apiTokenFromURL.
var currentAPIToken string

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

// backendBinPath returns the backend executable for dir.
func backendBinPath(dir string) string {
	name := "backend"
	if runtime.GOOS == "windows" {
		name = "backend.exe"
	}
	if isProjectRoot(dir) {
		return filepath.Join(dir, "dist", name)
	}
	return filepath.Join(dir, name)
}

func isProjectRoot(dir string) bool {
	return exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "cmd", "backend", "main.go"))
}

func backendBinStale(dir string) bool {
	st, err := os.Stat(backendBinPath(dir))
	if err != nil {
		return true
	}
	if isProjectRoot(dir) {
		if mod, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && mod.ModTime().After(st.ModTime()) {
			return true
		}
		stale := false
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || stale {
				return walkErr
			}
			if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "dist") {
				return filepath.SkipDir
			}
			if !entry.IsDir() && filepath.Ext(path) == ".go" {
				if source, err := entry.Info(); err == nil && source.ModTime().After(st.ModTime()) {
					stale = true
				}
			}
			return nil
		})
		return stale
	}
	srcs, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, source := range srcs {
		if info, err := os.Stat(source); err == nil && info.ModTime().After(st.ModTime()) {
			return true
		}
	}
	return false
}

func ensureBackend(ctx context.Context, c *http.Client, base, dir, apiToken string) tea.Cmd {
	return func() tea.Msg {
		if health(ctx, c, base) == nil {
			if err := probeAuth(ctx, c, base, apiToken); err != nil {
				return initMsg{err: err}
			}
			return initMsg{}
		}
		var cmd *exec.Cmd
		binPath := backendBinPath(dir)
		if backendBinStale(dir) && (isProjectRoot(dir) || hasGoSources(dir)) {
			goBin := "go"
			if runtime.GOOS == "windows" {
				goBin = "go.exe"
			}
			args := []string{"build", "-o", binPath, "."}
			if isProjectRoot(dir) {
				if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
					return initMsg{err: formatBackendStartupError("backend build failed", err.Error())}
				}
				args = []string{"build", "-o", binPath, "./cmd/backend"}
			}
			build := exec.Command(goBin, args...)
			build.Dir = dir
			if out, err := build.CombinedOutput(); err != nil {
				msg := strings.TrimSpace(string(out))
				if msg == "" {
					msg = err.Error()
				}
				return initMsg{err: formatBackendStartupError("backend build failed", msg)}
			}
		}
		if !exists(binPath) {
			if exe, err := os.Executable(); err == nil {
				cand := filepath.Join(filepath.Dir(exe), filepath.Base(binPath))
				if exists(cand) {
					binPath = cand
				}
			}
		}
		cmd = exec.Command(binPath)
		cmd.Dir = dir
		if !exists(dir) {
			if exe, err := os.Executable(); err == nil {
				cmd.Dir = filepath.Dir(exe)
			}
		}
		// <data-root>/backend/session.token (written by resolveSessionToken
		// before ensureBackend is called), so the child just inherits the
		// normal environment.
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
			if health(ctx, c, base) == nil {
				if err := probeAuth(ctx, c, base, apiToken); err != nil {
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

func hasGoSources(dir string) bool {
	srcs, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return len(srcs) > 0
}

func health(ctx context.Context, c *http.Client, base string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
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

func probeAuth(ctx context.Context, c *http.Client, base, apiToken string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/contacts", nil)
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

func openWS(ctx context.Context, url, apiToken string) tea.Cmd {
	return func() tea.Msg {
		header := http.Header{}
		header.Set(authHeaderName, "Bearer "+apiToken)
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, header)
		if err != nil {
			return wsOpenMsg{err: err}
		}
		ch := make(chan env, 64)
		go func() {
			defer close(ch)
			defer conn.Close()
			for {
				_, b, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var e env
				if json.Unmarshal(b, &e) == nil {
					select {
					case ch <- e:
					default:
						log.Printf("openWS: drop event, buffer full")
					}
				}
			}
		}()
		return wsOpenMsg{conn: conn, ch: ch}
	}
}
func readWS(ch <-chan env) tea.Cmd {
	return func() tea.Msg { e, ok := <-ch; return wsEvtMsg{evt: e, ok: ok} }
}
func postEmpty(ctx context.Context, c *http.Client, url string, ok func([]byte) tea.Msg) tea.Cmd {
	return postJSON(ctx, c, url, map[string]string{}, ok)
}

func postJSON(ctx context.Context, c *http.Client, url string, body any, ok func([]byte) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
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

func logout(ctx context.Context, c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		logoutClient := *c
		logoutClient.Timeout = 60 * time.Second
		b, _ := json.Marshal(map[string]string{})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/logout", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := logoutClient.Do(req)
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

func getChats(ctx context.Context, c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/chats", nil)
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
func getContacts(ctx context.Context, c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/contacts", nil)
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

func fetchGroupPreview(ctx context.Context, c *http.Client, base, jid string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/group/members?jid="+url.QueryEscape(jid), nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return groupPreviewMsg{jid: jid, err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return groupPreviewMsg{jid: jid, err: apiErrorFromResponse(res, "failed to get group members")}
		}
		var out struct {
			Members []string `json:"members"`
			Total   int      `json:"total"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return groupPreviewMsg{jid: jid, err: err}
		}
		return groupPreviewMsg{jid: jid, preview: groupPreview{members: out.Members, total: out.Total}}
	}
}
func getMsgs(ctx context.Context, c *http.Client, base, chatID string, limit int) tea.Cmd {
	return getMsgsBefore(ctx, c, base, chatID, limit, 0)
}

// getMsgsBefore fetches up to `limit` messages older than `before` (unix seconds).
// Pass before=0 for the initial fetch (returns the most recent messages).
func getMsgsBefore(ctx context.Context, c *http.Client, base, chatID string, limit int, before int64) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("limit", strconv.Itoa(limit))
		if before > 0 {
			q.Set("before", strconv.FormatInt(before, 10))
		}
		u := base + "/messages?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

func getMsgsAround(ctx context.Context, c *http.Client, base, chatID, msgID string, limit int) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("around", msgID)
		q.Set("limit", strconv.Itoa(limit))
		u := base + "/messages?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

func searchMsgs(ctx context.Context, c *http.Client, base, query string) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("q", query)
		q.Set("limit", "50")
		u := base + "/search?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

func send(ctx context.Context, c *http.Client, base, chatID, text string, replyTo *wireMsg, pendingID string) tea.Cmd {
	return func() tea.Msg {
		payload := map[string]any{"chatId": chatID, "text": text}
		if replyTo != nil {
			payload["replyToMsgId"] = replyTo.Key.ID
			rawText := renderMessageBody(replyTo.Message)
			payload["replyToText"] = ansiEscapeRegex.ReplaceAllString(rawText, "")
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
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/messages/send", bytes.NewReader(b))
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

type fileProgressMsg struct {
	chatID    string
	pendingID string
	pct       int // 0..100
}

// listenFileProgress returns a tea.Cmd that reads the next progress event
// from ch and returns it as a fileProgressMsg. When ch is closed, the cmd
// returns nil and the subscription dies naturally.
func listenFileProgress(ch <-chan fileProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// progressWriter wraps an io.Writer and emits throttled progress callbacks
// as bytes flow through. Used to drive the upload progress bar in the TUI.
type progressWriter struct {
	inner      io.Writer
	total      int64
	written    int64
	lastPct    int
	lastTime   time.Time
	onProgress func(uploaded, total int64, pct int)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.inner.Write(p)
	pw.written += int64(n)
	pct := 0
	if pw.total > 0 {
		pct = int(pw.written * 100 / pw.total)
		if pct > 100 {
			pct = 100
		}
	}
	now := time.Now()
	if pct != pw.lastPct && (pw.lastTime.IsZero() || now.Sub(pw.lastTime) > 80*time.Millisecond || pct == 100) {
		if pw.onProgress != nil {
			pw.onProgress(pw.written, pw.total, pct)
		}
		pw.lastPct = pct
		pw.lastTime = now
	}
	return n, err
}

func sendFile(ctx context.Context, c *http.Client, base, chatID, kind, path, caption string, pendingID string, progressCh chan fileProgressMsg) tea.Cmd {
	return func() tea.Msg {
		// The outer cmd owns closing the progress channel. The inner
		// writer goroutine is joined via doneCh before we return, so
		// closing the channel here is race-free.
		defer close(progressCh)

		f, err := os.Open(path)
		if err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		if info.IsDir() {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: fmt.Errorf("path must be a file")}
		}

		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			defer pw.Close()
			defer mw.Close()

			if err := mw.WriteField("chatId", chatID); err != nil {
				return
			}
			if err := mw.WriteField("kind", kind); err != nil {
				return
			}
			if err := mw.WriteField("caption", caption); err != nil {
				return
			}
			fw, err := mw.CreateFormFile("file", filepath.Base(path))
			if err != nil {
				return
			}
			pw2 := &progressWriter{
				inner: fw,
				total: info.Size(),
				onProgress: func(uploaded, total int64, pct int) {
					select {
					case progressCh <- fileProgressMsg{chatID: chatID, pendingID: pendingID, pct: pct}:
					default:
						// drop; the next tick will replace this one
					}
				},
			}
			if _, err := io.Copy(pw2, f); err != nil {
				return
			}
		}()

		// Per-call client with no timeout — 150MB uploads over slow links can
		// easily exceed the shared 12s default.
		uploadClient := &http.Client{Timeout: 0}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/messages/send-file", pr)
		if err != nil {
			return sentMsg{chatID: chatID, pendingID: pendingID, err: err}
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		attachAuthHeader(req, apiTokenFromURL(base))

		res, err := uploadClient.Do(req)
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
		<-doneCh // wait for the writer goroutine; safe to close progressCh after
		return sentMsg{chatID: chatID, pendingID: pendingID, msg: out.Message}
	}
}
func (x m) cleanup() {
	x.cancelRequests()
	x.stopAudio(true)
	if x.demoMode {
		return
	}
	for _, path := range x.downloadedMedia {
		_ = os.Remove(path)
	}
	if x.ws != nil {
		_ = x.ws.Close()
	}
	// Always ask the backend to shut down (best-effort): this covers the
	// reused-backend case where startedBackend is false and we hold no
	// child handle, so Ctrl+C never leaves an orphan on :8787 behind.
	shutdownBackend(x.client, x.baseURL, x.apiToken)
	if !x.startedBackend || x.backend == nil || x.backend.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(x.backend.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = x.backend.Process.Kill()
}

// shutdownBackend asks the backend to exit via POST /shutdown. Best-effort:
// failures (backend already gone, network closed) are ignored so quit never
// blocks on a dead server.
func shutdownBackend(c *http.Client, base, apiToken string) {
	if c == nil || strings.TrimSpace(base) == "" {
		return
	}
	call := *c
	call.Timeout = 2 * time.Second
	body, _ := json.Marshal(map[string]string{})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/shutdown", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	attachAuthHeader(req, apiToken)
	res, err := call.Do(req)
	if err != nil {
		return
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
}

func getWhitelist(ctx context.Context, c *http.Client, base string) tea.Cmd {
	return func() tea.Msg {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/whitelist", nil)
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
			DefaultAllowed bool `json:"defaultAllowed"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return whitelistLoadMsg{err: err}
		}
		wl := map[string]string{}
		denied := map[string]bool{}
		names := map[string]string{}
		for _, e := range out.Contacts {
			if e.Allowed == 1 {
				wl[e.Phone] = e.Name
			} else {
				denied[e.Phone] = true
			}
			if e.Name != "" {
				names[e.Phone] = e.Name
			}
		}
		return whitelistLoadMsg{whitelist: wl, denied: denied, defaultAllowed: out.DefaultAllowed, names: names}
	}
}

func downloadMedia(ctx context.Context, c *http.Client, base, chatID, msgID string, isPreview bool) tea.Cmd {
	return func() tea.Msg {
		q := url.Values{}
		q.Set("chatId", chatID)
		q.Set("msgId", msgID)
		u := base + "/media/download?" + q.Encode()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return mediaDownloadMsg{chatID: chatID, msgID: msgID, err: err, isPreview: isPreview}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return mediaDownloadMsg{chatID: chatID, msgID: msgID, err: apiErrorFromResponse(res, "failed to download media"), isPreview: isPreview}
		}
		var out struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			return mediaDownloadMsg{chatID: chatID, msgID: msgID, err: err, isPreview: isPreview}
		}
		if out.Error != "" {
			return mediaDownloadMsg{chatID: chatID, msgID: msgID, err: fmt.Errorf("%s", out.Error), isPreview: isPreview}
		}
		return mediaDownloadMsg{chatID: chatID, msgID: msgID, path: out.Path, isPreview: isPreview}
	}
}

func pasteFromClipboard() tea.Cmd {
	return func() tea.Msg {
		// PowerShell: try image first, then file drop list
		script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -ne $null) {
    $p = [System.IO.Path]::Combine([System.IO.Path]::GetTempPath(), 'whatzap_paste_' + [System.DateTime]::Now.Ticks.ToString() + '.png')
    $img.Save($p, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Output "IMAGE:$p"
    exit
}
$files = [System.Windows.Forms.Clipboard]::GetFileDropList()
if ($files.Count -gt 0) {
    Write-Output "FILE:$($files[0])"
    exit
}
Write-Output "NONE"
`
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err != nil {
			return clipboardPasteMsg{err: err}
		}
		result := strings.TrimSpace(string(out))
		switch {
		case strings.HasPrefix(result, "IMAGE:"):
			return clipboardPasteMsg{path: strings.TrimPrefix(result, "IMAGE:"), isImage: true}
		case strings.HasPrefix(result, "FILE:"):
			return clipboardPasteMsg{path: strings.TrimPrefix(result, "FILE:")}
		default:
			return clipboardPasteMsg{err: fmt.Errorf("no file or image in clipboard")}
		}
	}
}

func openFile(path string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
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

func setName(ctx context.Context, c *http.Client, base, phone, name string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]any{"phone": phone, "name": name})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/names/set", bytes.NewReader(b))
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

func setWhitelistEntry(ctx context.Context, c *http.Client, base, phone, name string, allowed int) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]any{"phone": phone, "name": name, "allowed": allowed})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/whitelist/set", bytes.NewReader(b))
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

// setWhitelistDefault flips the global whitelist default in one call
// (see POST /whitelist/default). Per-chat rows all align server-side, so
// no per-chat loop is needed.
func setWhitelistDefault(ctx context.Context, c *http.Client, base string, allowed int) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]any{"allowed": allowed})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/whitelist/default", bytes.NewReader(b))
		req.Header.Set("content-type", "application/json")
		attachAuthHeader(req, apiTokenFromURL(base))
		res, err := c.Do(req)
		if err != nil {
			return whitelistSetMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			raw, _ := io.ReadAll(res.Body)
			if res.StatusCode == http.StatusNotFound {
				return whitelistSetMsg{err: fmt.Errorf("%s on /whitelist/default: backend is out of date - stop backend.exe and restart WhatZap to pick up /whitelistall and /blacklistall", res.Status)}
			}
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
	return currentAPIToken
}

// registerSession tells the backend which TUI process (by PID) is the
// active session (A-1). Called once after ensureBackend succeeds; the
// backend uses this to rotate the session token if this process exits.
func registerSession(ctx context.Context, c *http.Client, base, apiToken string) tea.Cmd {
	return func() tea.Msg {
		body, err := json.Marshal(map[string]int{"pid": os.Getpid()})
		if err != nil {
			return sessionRegisterMsg{err: fmt.Errorf("encode session registration: %w", err)}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/session/register", bytes.NewReader(body))
		if err != nil {
			return sessionRegisterMsg{err: fmt.Errorf("create session registration request: %w", err)}
		}
		req.Header.Set("Content-Type", "application/json")
		attachAuthHeader(req, apiToken)
		res, err := c.Do(req)
		if err != nil {
			return sessionRegisterMsg{err: err}
		}
		defer res.Body.Close()
		if res.StatusCode/100 != 2 {
			return sessionRegisterMsg{err: fmt.Errorf("session registration failed: %s", res.Status)}
		}
		return sessionRegisterMsg{}
	}
}

type sessionRegisterMsg struct {
	err error
}

func syncContacts(ctx context.Context, c *http.Client, base string) tea.Cmd {
	longClient := *c
	longClient.Timeout = 5 * time.Minute
	return postEmpty(ctx, &longClient, base+"/sync/contacts", func(raw []byte) tea.Msg {
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

func syncGroups(ctx context.Context, c *http.Client, base string) tea.Cmd {
	longClient := *c
	longClient.Timeout = 5 * time.Minute
	return postEmpty(ctx, &longClient, base+"/sync/groups", func(raw []byte) tea.Msg {
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

func syncHistory(ctx context.Context, c *http.Client, base string) tea.Cmd {
	longClient := *c
	longClient.Timeout = 5 * time.Minute
	return postEmpty(ctx, &longClient, base+"/sync/history", func(raw []byte) tea.Msg {
		var out struct {
			Requested int `json:"requested"`
			Chats     int `json:"chats"`
			Skipped   int `json:"skipped"`
			Failed    int `json:"failed"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return syncHistoryDoneMsg{msg: "History sync requested"}
		}
		msg := fmt.Sprintf("History requested for %d/%d chats", out.Requested, out.Chats)
		if out.Failed > 0 {
			msg += fmt.Sprintf(", %d failed", out.Failed)
		}
		if out.Skipped > 0 {
			msg += fmt.Sprintf(", %d skipped (no messages yet)", out.Skipped)
		}
		return syncHistoryDoneMsg{msg: msg}
	})
}

type blockMsg struct {
	err error
}

func blockContact(ctx context.Context, c *http.Client, base, chatID string) tea.Cmd {
	return func() tea.Msg {
		b, _ := json.Marshal(map[string]string{"chatId": chatID})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/block", bytes.NewReader(b))
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
