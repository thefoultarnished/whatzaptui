package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	loadConfig()
	apiToken, err := resolveClientAPIToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

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

func resolveClientAPIToken() (string, error) {
	if token := os.Getenv(apiTokenEnvVar); token != "" {
		_ = os.Setenv(apiTokenEnvVar, token)
		return token, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	_ = os.Setenv(apiTokenEnvVar, token)
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
