package tokenlock

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRestrictFileToCurrentUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.token")
	if err := os.WriteFile(path, []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write temp token: %v", err)
	}
	if err := RestrictFileToCurrentUser(path); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if runtime.GOOS != "windows" {
		return
	}
	assertWindowsLockedDown(t, path)
}
