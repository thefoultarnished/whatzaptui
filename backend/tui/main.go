package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	loadConfig()
	apiToken, err := resolveSessionToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	currentAPIToken = apiToken

	applyThemeByName(currentConfig.ThemeName)

	backendDir := detectDirs()
	demoMode := demoEnabled()
	for {
		model := m{
			baseURL:          "http://127.0.0.1:8787",
			wsURL:            "ws://127.0.0.1:8787/ws",
			backendDir:       backendDir,
			apiToken:         apiToken,
			client:           &http.Client{Timeout: 12 * time.Second},
			demoMode:         demoMode,
			status:           "Starting backend...",
			mode:             "nav",
			sidebarTab:       "chats",
			contacts:         map[string]contact{},
			contactsByNumber: map[string]contact{},
			msgs:             map[string][]wireMsg{},
			loadingOlder:     map[string]bool{},
			noMoreOlder:      map[string]bool{},
			uploadProgress:   map[string]int{},
			whitelist:        map[string]string{},
			names:            map[string]string{},
			groupPreviews:    map[string]groupPreview{},
			sel:              0,
			scroll:           0,
			sideScroll:       0,
			active:           "",
			search:           "",
			input:            "",
			err:              "",
			topBarMsg:        "",
			topBarShown:      0,
			topBarVer:        0,
			cursorOn:         true,
			pulseOn:          false,
			flashUntil:       map[string]time.Time{},
			typingChats:      map[string]time.Time{},
			lastNotifyAt:     map[string]time.Time{},
			soundEnabled:     currentConfig.SoundEnabled,
			soundProfile:     normalizeSoundProfile(currentConfig.SoundProfile),
			sidebarFocused:   false,
			startedBackend:   false,
			mouseEnabled:     currentConfig.MouseEnabled,
			sidebarCache:     &sidebarCache{},
			mainCache:        &renderCache{},
			themePicker:      picker{title: "Select Theme", items: buildThemePickerItems()},
			pointerPicker:    picker{title: "Select Pointer Icon", items: buildPointerPickerItems()},
			helpPicker:       picker{title: "Commands", items: buildHelpPickerItems()},
			settingsPicker:   picker{title: "Settings", items: buildSettingsPickerItems()},
			typingAnimationPicker: picker{title: "Typing Animation", items: buildTypingAnimationPickerItems()},
		}
		if demoMode {
			model.status = "Starting demo..."
		}

		opts := []tea.ProgramOption{tea.WithAltScreen()}
		if currentConfig.MouseEnabled {
			opts = append(opts, tea.WithMouseCellMotion())
		}

		p := tea.NewProgram(model, opts...)
		out, err := p.Run()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if fm, ok := out.(m); ok {
			fm.cleanup()
			if fm.restartRequested {
				continue
			}
		}
		break
	}
}

// sessionTokenPath returns <data-root>/backend/session.token — the same
// path the backend resolves independently (backend/main.go's
// sessionTokenPath). S-1: this is the shared file the token lives in,
// replacing the WHATZAP_API_TOKEN env var.
func sessionTokenPath() (string, error) {
	dir := filepath.Join(whatzapDataRoot(), "backend")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.token"), nil
}

// resolveSessionToken returns the token to use for talking to the backend.
// If a token file already exists (e.g. from a previous TUI run, or written
// by the backend during A-1 rotation), it's reused — this lets a restarted
// TUI reconnect to an already-running backend without a token mismatch.
// Otherwise a fresh token is generated and written to the file (0600).
func resolveSessionToken() (string, error) {
	path, err := sessionTokenPath()
	if err != nil {
		return "", fmt.Errorf("resolve session token path: %w", err)
	}
	if b, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(b)); token != "" {
			return token, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write session token: %w", err)
	}
	return token, nil
}

func detectDirs() string {
	cwd, _ := os.Getwd()
	cands := []string{
		cwd,
		filepath.Join(cwd, ".."),
		filepath.Join(cwd, "..", ".."),
		"c:/Users/Nav/Downloads/personal/whatzap",
	}
	for _, c := range cands {
		abs, _ := filepath.Abs(c)
		if exists(filepath.Join(abs, "backend", "main.go")) {
			return filepath.Join(abs, "backend")
		}
	}
	abs, _ := filepath.Abs(cwd)
	return filepath.Join(abs, "backend")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
