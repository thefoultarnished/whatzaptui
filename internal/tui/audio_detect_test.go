package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMediaSendKindRecognizesAudio(t *testing.T) {
	tmpDir := t.TempDir()

	audioExts := []string{".mp3", ".m4a", ".ogg", ".wav", ".aac", ".flac", ".opus"}
	for _, ext := range audioExts {
		filePath := filepath.Join(tmpDir, "test"+ext)
		if err := os.WriteFile(filePath, []byte("fake-audio-content"), 0o600); err != nil {
			t.Fatalf("write file %s: %v", ext, err)
		}
		kind, err := detectMediaSendKind(filePath)
		if err != nil {
			t.Fatalf("detectMediaSendKind(%s) error = %v", ext, err)
		}
		if kind != "audio" {
			t.Errorf("detectMediaSendKind(%s) = %q, want %q", ext, kind, "audio")
		}
	}
}
