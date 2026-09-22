# Plan: Terminal Voice & Audio Playback for WhatZap

This document outlines the architecture, implementation plan, and integration details for inline voice message and audio playback within the WhatZap TUI (specifically Windows Terminal, with cross-platform compatibility).

---

## 1. Overview & Goals

- **Objective:** Allow users to play WhatsApp voice messages (`ptt`) and audio attachments directly inside the TUI without launching external media player windows or breaking terminal layout.
- **Key UX:**
  - Selecting an audio message and pressing `Space` or `Enter` starts playback in-place.
  - An inline progress bar and elapsed/total time indicator update smoothly in the chat bubble.
  - Switching chats, opening another audio file, or pressing `Space` again pauses/stops playback cleanly.
  - Zero terminal corruption or audio overlap.

---

## 2. Audio Specifications & Constraints

- **WhatsApp Format:** Voice notes are Ogg containers containing Opus audio streams (`.ogg`, MIME type `audio/ogg; codecs=opus`). Standard audio files may also be `.mp3`, `.m4a`, or `.wav`.
- **Backend API:** `GET /media/download?chatId=...&msgId=...` in `backend/media.go` already downloads the raw audio file to disk and returns `{"path": "<temp_path>"}`. **No backend HTTP changes are required.**
- **Terminal Capabilities:** The TUI runs in Bubble Tea on Windows Terminal, supporting Unicode block characters (`█`, `░`), RGB colors, and sub-second redraw cycles.

---

## 3. Audio Engine Architecture

To avoid bloating the WhatZap distribution with a ~90 MB `ffplay.exe` static build, WhatZap will use a **tiered player cascade**:

```
                  ┌───────────────────────────────┐
                  │   User requests audio play    │
                  └───────────────┬───────────────┘
                                  │
                                  ▼
                    Is local helper / bundled
                     micro-player available?
                     ├── Yes ──► Spawn micro-player
                     └── No
                          │
                          ▼
                    Is ffplay or mpv on PATH?
                     ├── Yes ──► Spawn headless ffplay/mpv
                     └── No
                          │
                          ▼
                    Fallback: Launch system default
                    player via openFile() with notice
```

### Player Options Evaluated

1. **Option A: Headless CLI Player Detection (Recommended Primary)**
   - Check `exec.LookPath("ffplay")` or `exec.LookPath("mpv")`.
   - Command: `ffplay -nodisp -autoexit -loglevel quiet -ss <start> <path>`
   - Size impact: **0 MB** bundled (uses existing user tools if present).
2. **Option B: Dedicated Bundled Micro-Player (~1.5 MB – 2 MB)**
   - A stripped, audio-only CLI helper binary (`whatzap-player.exe`) compiled with `miniaudio` or `libopus` + WASAPI.
   - Placed in the same directory as `backend.exe` and `whatzap.exe`.
   - Guarantees 100% reliability even if the user has no media tools on their `PATH`.
3. **Option C: Graceful Fallback**
   - If no in-terminal player can be resolved, fall back to the existing `openFile` behavior with a top-bar notification: `"ffplay/mpv not found; opening in default media player"`.

---

## 4. TUI State Machine & Lifecycle

### 4.1 State Additions (`backend/tui/model.go`)

```go
type audioPlayerState struct {
    msgID       string             // Currently playing message ID
    path        string             // Path to local decrypted audio file
    playing     bool               // Active playback flag
    elapsed     time.Duration      // Current position
    duration    time.Duration      // Total length
    cancel      context.CancelFunc // Cancels playback worker
    volume      float64            // 0.0 - 1.0
}
```

Add `audio audioPlayerState` to the main model struct `m`.

### 4.2 New Message Types (`backend/tui/audio_player.go`)

- `audioStartMsg{msgID string, duration time.Duration}`: Signals playback has started.
- `audioTickMsg{msgID string, elapsed time.Duration}`: Dispatched on an interval (~100ms) to advance the UI progress bar.
- `audioPausedMsg{msgID string}`: Signals playback was paused.
- `audioStoppedMsg{msgID string, err error}`: Signals playback reached the end or was stopped.

### 4.3 Subprocess Supervisor (`backend/tui/audio_player.go`)

A dedicated controller manages the player process:
- Spawns the player with a `context.WithCancel`.
- Tracks duration via WhatsApp message metadata (`v["seconds"]` from `audioMessage`) or via header inspection.
- Drives a `time.Ticker(100 * time.Millisecond)` that yields `audioTickMsg` to Bubble Tea.
- Ensures single-instance playback: calling `play()` automatically cancels and drains any existing audio playback before starting the new stream.

---

## 5. UI / View Implementation

### 5.1 Chat Bubble Inline Representation (`backend/tui/view.go`)

Inside `renderStyledMessageText` / `renderChatMessages`:

When `audioMessage` is detected:
```text
Idle:
╭───────────────────────────────────────────────╮
│ 👤 Contact Name                         14:32 │
│ ▶ 0:00 / 0:38  ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░ │
╰───────────────────────────────────────────────╯

Playing:
╭───────────────────────────────────────────────╮
│ 👤 Contact Name                         14:32 │
│ ⏸ 0:14 / 0:38  ██████████░░░░░░░░░░░░░░░░░░░ │
╰───────────────────────────────────────────────╯
```

- Accent color: Theme primary accent for filled blocks (`█`), muted color for unfilled blocks (`░`).
- Duration formatted as `MM:SS`.
- PTT badge: If `v["ptt"] == true`, display `[Voice]` label; otherwise `[Audio]`.

---

## 6. Input & Keybinding Map (`backend/tui/input.go`)

When the cursor or selected message in the chat pane is an audio message:

| Key | Action |
|---|---|
| `Space` / `Enter` | Toggle Play / Pause on highlighted audio message |
| `s` | Stop playback and reset position to 0:00 |
| `Left` / `Right` | Seek backward / forward 5 seconds |
| `Esc` | Stop audio playback and unfocus |

*Note: If the user changes active chats in the sidebar, audio playback automatically stops.*

---

## 7. File Change Breakdown

| File | Status | Planned Changes |
|---|---|---|
| **`backend/tui/audio_player.go`** | **New** | Player process launcher, ticker loop, cancellation, and PATH resolution logic (`ffplay`/`mpv`/bundled helper). |
| **`backend/tui/model.go`** | **Modified** | Add `audioPlayerState` fields to `m`. |
| **`backend/tui/update.go`** | **Modified** | Handle `audioStartMsg`, `audioTickMsg`, `audioStoppedMsg`, and auto-cancel on chat switch/quit. |
| **`backend/tui/view.go`** | **Modified** | Render the play icon, time counters, and progress bar inside the message bubble. |
| **`backend/tui/input.go`** | **Modified** | Intercept `Space`/`Enter` on audio messages to route to `audio_player.go` instead of `openFile()`. |
| **`backend/tui/audio_player_test.go`** | **New** | Unit tests for state transitions, tick math, progress calculations, and process cancellation. |

---

## 8. Verification & Test Plan

1. **Unit Tests:**
   - Verify progress bar character calculation (`renderAudioBar(elapsed, total, width)`).
   - Test tick dispatch and completion handlers in `update.go`.
   - Test single-instance concurrency: playing message B must terminate message A without leaking goroutines or processes.
2. **Integration Verification:**
   - Test playback of `.ogg` voice notes sent from official mobile clients.
   - Verify smooth 100ms ticker updates in Windows Terminal without screen flickering or render cache misalignment.
   - Verify clean exit: quitting WhatZap (`Ctrl+C` or `q`) terminates any active audio subprocess immediately.
