package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMediaSendKindRecognizesAudio(t *testing.T) {
	tmpDir := t.TempDir()

	audioExts := []string{".mp3", ".m4a", ".ogg", ".aac", ".opus"}
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

// WAV and FLAC go out as files: phones don't reliably play them as audio.
func TestDetectMediaSendKindSendsWavFlacAsDocument(t *testing.T) {
	tmpDir := t.TempDir()
	for _, ext := range []string{".wav", ".flac"} {
		filePath := filepath.Join(tmpDir, "test"+ext)
		if err := os.WriteFile(filePath, []byte("fake-audio-content"), 0o600); err != nil {
			t.Fatalf("write file %s: %v", ext, err)
		}
		if kind, err := detectMediaSendKind(filePath); err != nil || kind != "document" {
			t.Errorf("detectMediaSendKind(%s) = %q, %v; want document", ext, kind, err)
		}
	}
}

func TestOptimisticAudioPlaceholderIsAudioMessage(t *testing.T) {
	msg := optimisticOutgoingMediaMessage("1555@s.whatsapp.net", "audio", "song.mp3", "", "P1")
	if _, ok := msg.Message["audioMessage"]; !ok {
		t.Fatalf("audio placeholder should be an audioMessage, got %v", msg.Message)
	}
}
