package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveAudioPlayerEmptyPath(t *testing.T) {
	withTempAPIEnv(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	kind, bin := resolveAudioPlayer()
	if kind != audioPlayerNone || bin != "" {
		t.Fatalf("resolve = (%v, %q), want (none, \"\")", kind, bin)
	}
}

func TestResolveAudioPlayerWingetFallback(t *testing.T) {
	withTempAPIEnv(t)
	t.Setenv("PATH", t.TempDir())
	fakeBase := t.TempDir()
	t.Setenv("LOCALAPPDATA", fakeBase)
	ffplay := filepath.Join(fakeBase, "Microsoft", "WinGet", "Packages",
		"Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe",
		"ffmpeg-9.0.2-full_build", "bin", "ffplay.exe")
	if err := os.MkdirAll(filepath.Dir(ffplay), 0o755); err != nil {
		t.Fatalf("mkdir fake winget tree: %v", err)
	}
	if err := os.WriteFile(ffplay, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write fake ffplay: %v", err)
	}
	kind, bin := resolveAudioPlayer()
	if kind != audioPlayerFFplay || bin != ffplay {
		t.Fatalf("resolve = (%v, %q), want (ffplay, %q)", kind, bin, ffplay)
	}
}

func TestFormatAudioTime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                 "0:00",
		14 * time.Second:  "0:14",
		74 * time.Second:  "1:14",
		600 * time.Second: "10:00",
	}
	for d, want := range cases {
		if got := formatAudioTime(d); got != want {
			t.Fatalf("formatAudioTime(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRenderAudioProgress(t *testing.T) {
	// 38s at 2s per block = 19 wide; 19s elapsed = 9 full + half.
	bar := renderAudioProgress(19 * time.Second, 38*time.Second)
	if got := len([]rune(bar)); got != 19 {
		t.Fatalf("bar width = %d runes, want 19: %q", got, bar)
	}
	if got := strings.Count(bar, "█"); got != 9 {
		t.Fatalf("filled = %d, want 9: %q", got, bar)
	}
	if !strings.Contains(bar, "▌") {
		t.Fatalf("odd second should show half block: %q", bar)
	}
	if got := renderAudioProgress(18*time.Second, 38*time.Second); strings.Count(got, "█") != 9 || strings.Contains(got, "▌") {
		t.Fatalf("even second should be exact full blocks: %q", got)
	}
	if got := renderAudioProgress(0, 38*time.Second); strings.Contains(got, "█") || strings.Contains(got, "▌") {
		t.Fatalf("zero elapsed should be empty: %q", got)
	}
	if got := renderAudioProgress(99*time.Second, 38*time.Second); strings.Contains(got, "░") || strings.Contains(got, "▌") {
		t.Fatalf("overrun should clamp full: %q", got)
	}
	if got := len([]rune(renderAudioProgress(0, 300*time.Second))); got != audioMaxBarWidth {
		t.Fatalf("long audio width = %d, want cap %d", got, audioMaxBarWidth)
	}
}

func audioTestMsg(id string, audio bool, seconds float64) wireMsg {
	var wm wireMsg
	wm.Key.ID = id
	wm.Message = map[string]any{}
	if audio {
		wm.Message["audioMessage"] = map[string]any{"seconds": seconds, "ptt": true}
	} else {
		wm.Message["conversation"] = "hi"
	}
	return wm
}

func TestAudioDuration(t *testing.T) {
	if got := audioDuration(audioTestMsg("a", true, 38)); got != 38*time.Second {
		t.Fatalf("duration = %v, want 38s", got)
	}
	uintMsg := audioTestMsg("u", true, 0)
	uintMsg.Message["audioMessage"].(map[string]any)["seconds"] = uint32(21)
	if got := audioDuration(uintMsg); got != 21*time.Second {
		t.Fatalf("uint32 duration = %v, want 21s", got)
	}
	if got := audioDuration(audioTestMsg("b", true, 0)); got != 0 {
		t.Fatalf("missing seconds duration = %v, want 0", got)
	}
	if got := audioDuration(audioTestMsg("c", false, 0)); got != 0 {
		t.Fatalf("text msg duration = %v, want 0", got)
	}
}

func TestLatestAudioMsg(t *testing.T) {
	msgs := []wireMsg{
		audioTestMsg("a1", true, 10),
		audioTestMsg("t1", false, 0),
		audioTestMsg("a2", true, 20),
	}
	if got := latestAudioMsg(msgs, ""); got == nil || got.Key.ID != "a2" {
		t.Fatalf("default pick = %+v, want a2", got)
	}
	if got := latestAudioMsg(msgs, "a1"); got == nil || got.Key.ID != "a1" {
		t.Fatalf("selected pick = %+v, want a1", got)
	}
	if got := latestAudioMsg(msgs, "t1"); got == nil || got.Key.ID != "a2" {
		t.Fatalf("non-audio selection should fall back to latest, got %+v", got)
	}
	if got := latestAudioMsg([]wireMsg{audioTestMsg("t1", false, 0)}, ""); got != nil {
		t.Fatalf("no audio should be nil, got %+v", got)
	}
}

func TestAudioTickPinsAtFull(t *testing.T) {
	model := baseTestModel("", nil)
	model.audioPlaying = true
	model.audioGen = 3
	_, cancel := context.WithCancel(context.Background())
	model.audioCancel = cancel
	model.audioBase = 0
	model.audioStartedAt = time.Now().Add(-time.Hour)
	model.audioDuration = 38 * time.Second
	next, cmd := model.Update(audioTickMsg{})
	got := next.(m)
	if !got.audioPlaying {
		t.Fatal("playing = false at nominal end, want pinned-full until process exits")
	}
	if got.audioElapsed != 38*time.Second {
		t.Fatalf("elapsed = %v, want pinned at 38s", got.audioElapsed)
	}
	if cmd != nil {
		t.Fatal("no further ticks needed while pinned full")
	}
}

func TestAudioDoneWrongGenIgnored(t *testing.T) {
	model := baseTestModel("", nil)
	model.audioPlaying = true
	model.audioGen = 3
	model.audioMsgID = "a1"
	next, _ := model.Update(audioDoneMsg{gen: 2})
	got := next.(m)
	if !got.audioPlaying || got.audioMsgID != "a1" {
		t.Fatal("stale done msg mutated state")
	}
}

func TestAudioProgressLineStableWidth(t *testing.T) {	model := baseTestModel("", nil)
	model.active = "c@s.whatsapp.net"
	model.audioChatID = model.active
	model.audioMsgID = "a1"
	model.audioPlaying = true
	model.audioElapsed = 14 * time.Second
	model.audioDuration = 38 * time.Second
	msg := audioTestMsg("a1", true, 38)
	line1 := model.audioProgressLine(msg)
	model.audioElapsed = 15 * time.Second
	line2 := model.audioProgressLine(msg)
	if strings.Count(line1, "\n") != 1 || strings.Count(line2, "\n") != 1 {
		t.Fatalf("progress must stay one line: %q %q", line1, line2)
	}
	if model.audioProgressLine(audioTestMsg("zz", true, 5)) != "" {
		t.Fatal("other messages must get no progress line")
	}
}

func TestAudioProgressLineUnknownDuration(t *testing.T) {
	model := baseTestModel("", nil)
	model.active = "c@s.whatsapp.net"
	model.audioChatID = model.active
	model.audioMsgID = "a1"
	model.audioPlaying = true
	model.audioElapsed = time.Second
	model.audioDuration = 0
	line := model.audioProgressLine(audioTestMsg("a1", true, 0))
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("progress must stay one line: %q", line)
	}
	if !strings.Contains(line, "█") {
		t.Fatalf("unknown duration should show a moving marker: %q", line)
	}
}

func TestAudioPopupWhenNoPlayer(t *testing.T) {
	withTempAPIEnv(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	model := baseTestModel("http://127.0.0.1:1", nil)
	model.active = "c@s.whatsapp.net"
	model.status = "ready"
	model.msgs[model.active] = []wireMsg{audioTestMsg("a1", true, 38)}
	model.downloadedMedia = map[string]string{"a1": t.TempDir() + "/a.ogg"}

	_ = model.toggleAudioPlayback()
	if !model.confirmDialog.open {
		t.Fatal("dialog should open when no player is installed")
	}
	if model.confirmDialog.action != "audioplayer" {
		t.Fatalf("action = %q, want audioplayer", model.confirmDialog.action)
	}
	if !strings.Contains(model.confirmDialog.message, "winget install") {
		t.Fatalf("dialog should show the install command, got %q", model.confirmDialog.message)
	}
	if model.audioFallbackPath == "" {
		t.Fatal("fallback path should be kept for the default-player option")
	}
}
