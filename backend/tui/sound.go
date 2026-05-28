package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func normalizeSoundProfile(v int) int {
	if v < 1 || v > 5 {
		return 2
	}
	return v
}

func soundName(v int) string {
	switch normalizeSoundProfile(v) {
	case 1:
		return "soft chime"
	case 2:
		return "warm double chime"
	case 3:
		return "glass ping"
	case 4:
		return "mellow rise"
	default:
		return "calm triad"
	}
}

// soundSlot is a single-slot semaphore that prevents concurrent playback.
var soundSlot = make(chan struct{}, 1)

func playSoundProfileCmd(profile int) tea.Cmd {
	return func() tea.Msg {
		select {
		case soundSlot <- struct{}{}:
			playSoundProfile(profile)
			<-soundSlot
		default:
			// playback already in progress — drop this notification
		}
		return nil
	}
}

func playSoundProfile(profile int) {
	profile = normalizeSoundProfile(profile)
	if runtime.GOOS == "windows" {
		script := windowsSoundScript(profile)
		ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
		defer cancel()
		_ = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script).Run()
		return
	}
	fmt.Print("\a")
}

func windowsSoundScript(profile int) string {
	switch normalizeSoundProfile(profile) {
	case 1:
		return "[console]::beep(780,90)"
	case 2:
		return "[console]::beep(660,90); Start-Sleep -Milliseconds 60; [console]::beep(780,90)"
	case 3:
		return "[console]::beep(980,110)"
	case 4:
		return "[console]::beep(520,85); Start-Sleep -Milliseconds 55; [console]::beep(660,100)"
	default:
		return "[console]::beep(523,70); Start-Sleep -Milliseconds 50; [console]::beep(659,70); Start-Sleep -Milliseconds 50; [console]::beep(784,90)"
	}
}
