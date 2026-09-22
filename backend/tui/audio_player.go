package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// In-terminal audio playback for WhatsApp voice notes and audio files.
//
// Terminals cannot play sound, so playback is delegated to a headless
// CLI player resolved from PATH (ffplay or mpv). If neither exists, the
// caller falls back to openFile (system default player). No player is
// bundled: ogg/opus decoding in pure Go is unavailable and CGO audio
// bindings would complicate Windows builds.
//
// Pause is implemented as stop-and-remember: the process is killed and
// the elapsed position kept; resume restarts the player with -ss/--start.
// Single instance only — starting a new playback supersedes the old one
// via audioGen.

type audioPlayerKind int

const (
	audioPlayerNone audioPlayerKind = iota
	audioPlayerFFplay
	audioPlayerMPV
)

const audioTickInterval = 200 * time.Millisecond

type audioTickMsg struct{}

type audioDoneMsg struct {
	gen       int
	cancelled bool
	err       error
}

// resolveAudioPlayer finds a headless player on PATH. Empty PATH entries
// are fine — LookPath just finds nothing and we fall back.
func resolveAudioPlayer() (audioPlayerKind, string) {
	if p, err := exec.LookPath("ffplay"); err == nil {
		return audioPlayerFFplay, p
	}
	if p, err := exec.LookPath("mpv"); err == nil {
		return audioPlayerMPV, p
	}
	// Winget installs Gyan.FFmpeg without touching PATH, so probe its
	// install folder directly (LOCALAPPDATA is empty off Windows, in
	// which case the glob matches nothing).
	if p := wingetFFplay(); p != "" {
		return audioPlayerFFplay, p
	}
	return audioPlayerNone, ""
}

// wingetFFplay locates ffplay.exe inside a Gyan.FFmpeg winget install,
// e.g. %LOCALAPPDATA%\Microsoft\WinGet\Packages\Gyan.FFmpeg_…\ffmpeg-9.0.2-full_build\bin\ffplay.exe.
func wingetFFplay() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(
		base, "Microsoft", "WinGet", "Packages",
		"Gyan.FFmpeg_*", "ffmpeg-*", "bin", "ffplay.exe",
	))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func audioSeekArg(kind audioPlayerKind, startAt time.Duration) []string {
	secs := fmt.Sprintf("%d", int(startAt/time.Second))
	switch kind {
	case audioPlayerFFplay:
		if startAt > 0 {
			return []string{"-ss", secs}
		}
		return nil
	case audioPlayerMPV:
		if startAt > 0 {
			return []string{"--start=" + secs}
		}
		return nil
	}
	return nil
}

// audioInstallCmd is shown in the popup when no headless player exists.
// The user runs it manually in a new terminal; WhatZap stays open.
const audioInstallCmd = "winget install Gyan.FFmpeg"

// openAudioInstallPopup shows the install command and offers the default
// player as fallback. The message ID stays highlighted so the bubble
// keeps its progress line while the popup is up.
func (x *m) openAudioInstallPopup(chatID, msgID, path string, duration time.Duration) {
	x.stopAudio(false)
	x.audioMsgID = msgID
	x.audioChatID = chatID
	x.audioPath = path
	x.audioDuration = duration
	x.audioFallbackPath = path
	x.confirmDialog.Open(
		"No audio player found",
		"Please use the following command to install FFmpeg to play audio:\n\n"+audioInstallCmd+"\n\nAfter FFmpeg is installed restart WhatZap.\nYes = open this file in the default player instead.",
		"audioplayer",
	)
	x.invalidate()
}
// audioScope returns a cancellable context tied to the app lifecycle.
// apiCtx is nil in unit tests, so fall back to Background there.
func (x m) audioScope() (context.Context, context.CancelFunc) {
	if x.apiCtx == nil {
		return context.WithCancel(context.Background())
	}
	return context.WithCancel(x.apiCtx)
}
// playAudioCmd runs the player to completion. Cancellation (pause, stop,
// chat switch, quit) kills the process and reports cancelled.
func playAudioCmd(ctx context.Context, gen int, kind audioPlayerKind, bin, path string, startAt time.Duration) tea.Cmd {
	return func() tea.Msg {
		args := audioSeekArg(kind, startAt)
		switch kind {
		case audioPlayerFFplay:
			args = append(args, "-nodisp", "-autoexit", "-loglevel", "quiet", path)
		case audioPlayerMPV:
			args = append(args, "--no-video", "--really-quiet", path)
		default:
			return audioDoneMsg{gen: gen, err: fmt.Errorf("no audio player found (install ffplay or mpv)")}
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return audioDoneMsg{gen: gen, cancelled: true}
			}
			return audioDoneMsg{gen: gen, err: err}
		}
		return audioDoneMsg{gen: gen}
	}
}

func audioTickCmd() tea.Cmd {
	return tea.Tick(audioTickInterval, func(time.Time) tea.Msg { return audioTickMsg{} })
}

// audioDuration extracts the voice-note length. The backend stores Seconds
// as a number that may arrive as uint32 (live), float64 (JSON round-trip),
// int, or json.Number — all are accepted. 0 means unknown.
func audioDuration(msg wireMsg) time.Duration {
	v, ok := msg.Message["audioMessage"].(map[string]any)
	if !ok {
		return 0
	}
	var secs float64
	switch n := v["seconds"].(type) {
	case float64:
		secs = n
	case float32:
		secs = float64(n)
	case int:
		secs = float64(n)
	case int64:
		secs = float64(n)
	case uint32:
		secs = float64(n)
	case uint64:
		secs = float64(n)
	case json.Number:
		secs, _ = n.Float64()
	}
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

func isAudioWire(msg wireMsg) bool {
	_, ok := msg.Message["audioMessage"].(map[string]any)
	return ok
}

// latestAudioMsg prefers the selected message when it is audio in this
// chat, otherwise the most recent audio message. Nil when there is none.
func latestAudioMsg(msgs []wireMsg, selectedID string) *wireMsg {
	if selectedID != "" {
		for i := range msgs {
			if msgs[i].Key.ID == selectedID && isAudioWire(msgs[i]) {
				return &msgs[i]
			}
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if isAudioWire(msgs[i]) {
			return &msgs[i]
		}
	}
	return nil
}

func formatAudioTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// renderAudioBar draws a fixed-width bar so bubble line counts stay
// stable on every tick (the click map in msgIDAtLine depends on it).
func renderAudioBar(elapsed, total time.Duration, width int) string {
	if width < 1 {
		return ""
	}
	filled := 0
	if total > 0 && elapsed > 0 {
		filled = int(elapsed * time.Duration(width) / total)
		if filled > width {
			filled = width
		}
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// audioBlockSeconds is how many seconds of audio one bar block covers.
// Each block fills in two visible steps per tick: left half, then full.
const audioBlockSeconds = 2

// audioMaxBarWidth caps the bar so long voice notes still fit the bubble;
// beyond it the bar scales proportionally instead of 1 block per step.
const audioMaxBarWidth = 40

// renderAudioProgress draws the bar with one block per audioBlockSeconds
// of audio, advancing a half block (▌) then a full block (█) per step.
// Output width depends only on the total, never on elapsed, so bubble
// line counts stay stable on every tick.
func renderAudioProgress(elapsed, total time.Duration) string {
	if total <= 0 {
		return ""
	}
	totalSteps := int(total / (audioBlockSeconds * time.Second))
	if total%(audioBlockSeconds*time.Second) != 0 {
		totalSteps++
	}
	if totalSteps < 1 {
		totalSteps = 1
	}
	width := totalSteps
	scale := 1.0
	if width > audioMaxBarWidth {
		scale = float64(audioMaxBarWidth) / float64(width)
		width = audioMaxBarWidth
	}
	pos := float64(elapsed) / float64(audioBlockSeconds*time.Second) * scale
	if pos < 0 {
		pos = 0
	}
	full := int(pos)
	half := pos-float64(full) >= 0.5
	if full > width {
		full = width
		half = false
	}
	bar := strings.Repeat("█", full)
	if half && full < width {
		bar += "▌"
		return bar + strings.Repeat("░", width-full-1)
	}
	return bar + strings.Repeat("░", width-full)
}

// audioProgressLine is the single extra bubble line for the active audio
// message. Both the main render pass (view.go) and the click map
// (msgIDAtLine) must call it so line heights agree.
func (x m) audioProgressLine(msg wireMsg) string {
	if msg.Key.ID == "" || msg.Key.ID != x.audioMsgID || x.active == "" || x.audioChatID != x.active {
		return ""
	}
	icon := "▶"
	if x.audioPlaying {
		icon = "⏸"
	}
	if x.audioDuration > 0 {
		return "\n" + icon + " " + formatAudioTime(x.audioElapsed) + " / " + formatAudioTime(x.audioDuration) +
			" " + renderAudioProgress(x.audioElapsed, x.audioDuration)
	}
	// Unknown total (messages stored before durations were kept): an
	// indeterminate marker that still advances on every tick.
	const unknownWidth = 16
	pos := int(x.audioElapsed/audioTickInterval) % unknownWidth
	return "\n" + icon + " " + formatAudioTime(x.audioElapsed) +
		" " + strings.Repeat("░", pos) + "█" + strings.Repeat("░", unknownWidth-1-pos)
}

// stopAudio halts playback. If clear is true the bubble highlight goes
// away too (chat switch, quit); otherwise the paused position is kept
// for resume.
func (x *m) stopAudio(clear bool) {
	x.audioGen++
	if x.audioCancel != nil {
		x.audioCancel()
		x.audioCancel = nil
	}
	x.audioPlaying = false
	x.audioPending = ""
	x.audioFallbackPath = ""
	if clear {
		x.audioMsgID = ""
		x.audioChatID = ""
		x.audioPath = ""
		x.audioElapsed = 0
		x.audioDuration = 0
	}
	x.invalidate()
}

// startAudioPlayback spawns the player for an already-downloaded file.
func (x *m) startAudioPlayback(chatID, msgID, path string, duration time.Duration) tea.Cmd {
	kind, bin := resolveAudioPlayer()
	if kind == audioPlayerNone || x.demoMode {
		if x.demoMode {
			x.audioMsgID = ""
			x.audioChatID = ""
			x.audioPending = ""
			x.invalidate()
			return x.setTopBar("Demo mode: audio disabled")
		}
		x.openAudioInstallPopup(chatID, msgID, path, duration)
		return nil
	}
	x.stopAudio(false)
	x.audioGen++
	x.audioMsgID = msgID
	x.audioChatID = chatID
	x.audioPath = path
	x.audioDuration = duration
	x.audioElapsed = 0
	ctx, cancel := x.audioScope()
	x.audioCancel = cancel
	x.audioPlaying = true
	x.audioStartedAt = time.Now()
	x.invalidate()
	return tea.Batch(
		playAudioCmd(ctx, x.audioGen, kind, bin, path, 0),
		audioTickCmd(),
		x.setTopBar("Playing audio (Space pauses)"),
	)
}

// toggleAudioPlayback pauses/resumes the current audio or starts the
// most recent audio message in the active chat.
func (x *m) toggleAudioPlayback() tea.Cmd {
	if x.status != "ready" || x.active == "" {
		return x.setTopBar("No active chat")
	}
	msgs := x.msgs[x.active]
	target := latestAudioMsg(msgs, x.selectedMsgID)
	if target == nil {
		return x.setTopBar("No audio message in this chat")
	}
	// Pause the currently playing message.
	if x.audioPlaying && x.audioMsgID == target.Key.ID {
		x.audioElapsed = x.audioBase + time.Since(x.audioStartedAt)
		if x.audioDuration > 0 && x.audioElapsed > x.audioDuration {
			x.audioElapsed = x.audioDuration
		}
		x.stopAudio(false)
		return x.setTopBar("Paused")
	}
	// Resume the paused message.
	if !x.audioPlaying && x.audioMsgID == target.Key.ID && x.audioPath != "" {
		kind, bin := resolveAudioPlayer()
		if kind == audioPlayerNone {
			x.openAudioInstallPopup(x.audioChatID, x.audioMsgID, x.audioPath, x.audioDuration)
			return nil
		}
		x.audioGen++
		ctx, cancel := x.audioScope()
		x.audioCancel = cancel
		x.audioPlaying = true
		x.audioBase = x.audioElapsed
		x.audioStartedAt = time.Now()
		x.invalidate()
		return tea.Batch(
			playAudioCmd(ctx, x.audioGen, kind, bin, x.audioPath, x.audioElapsed),
			audioTickCmd(),
			x.setTopBar("Playing audio (Space pauses)"),
		)
	}
	// New message: stop anything current, then play from cache or download.
	duration := audioDuration(*target)
	if path := x.downloadedMedia[target.Key.ID]; path != "" {
		return x.startAudioPlayback(x.active, target.Key.ID, path, duration)
	}
	if x.demoMode {
		return x.setTopBar("Demo mode: audio disabled")
	}
	x.stopAudio(false)
	x.audioMsgID = target.Key.ID
	x.audioChatID = x.active
	x.audioDuration = duration
	x.audioElapsed = 0
	x.audioPending = target.Key.ID
	x.invalidate()
	return tea.Batch(
		x.setTopBar("Downloading audio…"),
		downloadMedia(x.reqCtx(), x.client, x.baseURL, x.active, target.Key.ID, true),
	)
}
