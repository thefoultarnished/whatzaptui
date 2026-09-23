package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClipboardPasteRejectsMissingFile(t *testing.T) {
	model := m{}
	next, _ := model.Update(clipboardPasteMsg{path: filepath.Join(t.TempDir(), "gone.png")})
	got := next.(m)
	if got.pendingAttachmentPath != "" {
		t.Fatalf("pendingAttachmentPath = %q, want empty for missing file", got.pendingAttachmentPath)
	}
}

func TestClipboardPasteRejectsDirectory(t *testing.T) {
	model := m{}
	next, _ := model.Update(clipboardPasteMsg{path: t.TempDir()})
	got := next.(m)
	if got.pendingAttachmentPath != "" {
		t.Fatalf("pendingAttachmentPath = %q, want empty for directory", got.pendingAttachmentPath)
	}
}

func TestClipboardPasteAttachesExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	model := m{}
	next, _ := model.Update(clipboardPasteMsg{path: p})
	got := next.(m)
	if got.pendingAttachmentPath != p {
		t.Fatalf("pendingAttachmentPath = %q, want %q", got.pendingAttachmentPath, p)
	}
}
