package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Startup check.
func TestBackendProcessStartsAndServesHealth(t *testing.T) {
	guard, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		t.Skip("127.0.0.1:8787 is already in use")
	}
	_ = guard.Close()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	tmpDir := t.TempDir()
	exeName := "whatzap-backend-test"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exePath := filepath.Join(tmpDir, exeName)

	buildCmd := exec.Command("go", "build", "-o", exePath, ".")
	buildCmd.Dir = wd
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build backend: %v: %s", err, strings.TrimSpace(string(buildOut)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	apiToken := "process-test-token"

	// S-1: the backend reads its token from <data-root>/backend/session.token
	// rather than an env var, so write it there before starting the process.
	dataDir := filepath.Join(tmpDir, "appdata")
	tokenDir := filepath.Join(dataDir, "backend")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatalf("mkdir token dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tokenDir, "session.token"), []byte(apiToken), 0o600); err != nil {
		t.Fatalf("write session token: %v", err)
	}

	runCmd := exec.CommandContext(ctx, exePath)
	runCmd.Dir = tmpDir
	runCmd.Env = append(os.Environ(),
		"WHATZAP_DATA_DIR="+dataDir,
	)
	logPath := filepath.Join(tmpDir, "backend.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log file: %v", err)
	}
	defer func() {
		_ = logFile.Close()
	}()
	runCmd.Stdout = logFile
	runCmd.Stderr = logFile

	if err := runCmd.Start(); err != nil {
		t.Fatalf("start backend: %v", err)
	}
	defer func() {
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
			_, _ = runCmd.Process.Wait()
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	healthURL := "http://127.0.0.1:8787/health"
	var lastErr error
	ready := false
	timedOut := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !timedOut {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				ready = true
				break
			}
			lastErr = fmt.Errorf("health status %s", res.Status)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			timedOut = true
		case <-time.After(250 * time.Millisecond):
		}
	}

	if !ready {
		_, _ = logFile.Seek(0, 0)
		rawLog, _ := io.ReadAll(logFile)
		t.Fatalf("backend health did not become ready: %v: %s", lastErr, strings.TrimSpace(string(rawLog)))
	}

	unauthReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8787/contacts", nil)
	unauthRes, err := client.Do(unauthReq)
	if err != nil {
		t.Fatalf("unauthorized request failed: %v", err)
	}
	_ = unauthRes.Body.Close()
	if unauthRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized contacts status = %d, want %d", unauthRes.StatusCode, http.StatusUnauthorized)
	}

	authReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8787/contacts", nil)
	authReq.Header.Set(authHeaderName, "Bearer "+apiToken)
	authRes, err := client.Do(authReq)
	if err != nil {
		t.Fatalf("authorized request failed: %v", err)
	}
	_ = authRes.Body.Close()
	if authRes.StatusCode != http.StatusOK {
		t.Fatalf("authorized contacts status = %d, want %d", authRes.StatusCode, http.StatusOK)
	}
}
