package tui

import (
	"errors"
	"testing"
)

func TestBackgroundMediaDownloadsGateOnReady(t *testing.T) {
	currentConfig.MediaViewStyle = "glyph"
	x := m{
		status: "Connecting...",
		active: "111@s.whatsapp.net",
		msgs: map[string][]wireMsg{
			"111@s.whatsapp.net": {
				{
					Message: map[string]any{"imageMessage": map[string]any{}},
				},
			},
		},
	}
	cmd := x.triggerBackgroundDownloads()
	if cmd != nil {
		t.Fatalf("triggerBackgroundDownloads should return nil when status is not ready")
	}

	// When status is ready, background download should be scheduled
	x.status = "ready"
	cmd = x.triggerBackgroundDownloads()
	if cmd == nil {
		t.Fatalf("triggerBackgroundDownloads should return cmd when status is ready")
	}
}

func TestPreviewMediaDownloadErrorNeverFlashesTopBar(t *testing.T) {
	x := m{
		status: "ready",
	}

	// Preview download error should be silently dropped
	mdl, _ := x.updateInner(mediaDownloadMsg{
		msgID:     "m1",
		isPreview: true,
		err:       errors.New("409 Conflict: not connected"),
	})
	got := mdl.(m)
	if got.topBarMsg != "" {
		t.Fatalf("preview media download error should not appear in topBar: got %q", got.topBarMsg)
	}

	// Explicit user download error (not a preview) still surfaces error to user
	mdl, _ = x.updateInner(mediaDownloadMsg{
		msgID:     "m1",
		isPreview: false,
		err:       errors.New("network failure"),
	})
	got = mdl.(m)
	if got.topBarMsg != "network failure" {
		t.Fatalf("manual download error should appear in topBar: got %q", got.topBarMsg)
	}
}
