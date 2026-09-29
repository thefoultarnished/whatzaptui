package tui

import (
	"errors"
	"testing"
)

func lookFound(string) (string, error)   { return "/bin/player", nil }
func lookMissing(string) (string, error) { return "", errors.New("not found") }
func fileYes(string) bool                { return true }
func fileNo(string) bool                 { return false }

func TestSoundCommandMacUsesAfplay(t *testing.T) {
	name, args, ok := soundCommand("darwin", 3, lookFound, fileYes)
	if !ok || name != "afplay" || len(args) != 1 || args[0] != "/System/Library/Sounds/Glass.aiff" {
		t.Fatalf("got %q %v ok=%v", name, args, ok)
	}
}

func TestSoundCommandLinuxUsesPaplay(t *testing.T) {
	name, args, ok := soundCommand("linux", 1, lookFound, fileYes)
	if !ok || name != "paplay" || len(args) != 1 || args[0] != linuxSoundDir+"message.oga" {
		t.Fatalf("got %q %v ok=%v", name, args, ok)
	}
}

func TestSoundCommandEveryProfileHasAFile(t *testing.T) {
	seenMac := map[string]bool{}
	seenLinux := map[string]bool{}
	for p := 1; p <= 5; p++ {
		_, a, ok := soundCommand("darwin", p, lookFound, fileYes)
		if !ok {
			t.Fatalf("darwin profile %d not ok", p)
		}
		seenMac[a[0]] = true
		_, a, ok = soundCommand("linux", p, lookFound, fileYes)
		if !ok {
			t.Fatalf("linux profile %d not ok", p)
		}
		seenLinux[a[0]] = true
	}
	if len(seenMac) != 5 || len(seenLinux) != 5 {
		t.Fatalf("profiles should map to distinct sounds: mac=%d linux=%d", len(seenMac), len(seenLinux))
	}
}

func TestSoundCommandOutOfRangeProfileUsesDefault(t *testing.T) {
	_, want, _ := soundCommand("linux", 2, lookFound, fileYes)
	_, got, ok := soundCommand("linux", 99, lookFound, fileYes)
	if !ok || got[0] != want[0] {
		t.Fatalf("profile 99 = %v, want default %v", got, want)
	}
}

func TestSoundCommandFallsBackWhenPlayerMissing(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		if _, _, ok := soundCommand(goos, 2, lookMissing, fileYes); ok {
			t.Fatalf("%s: missing player should fall back to bell", goos)
		}
	}
}

func TestSoundCommandFallsBackWhenSoundFileMissing(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		if _, _, ok := soundCommand(goos, 2, lookFound, fileNo); ok {
			t.Fatalf("%s: missing sound file should fall back to bell", goos)
		}
	}
}

func TestSoundCommandOtherOSFallsBack(t *testing.T) {
	if _, _, ok := soundCommand("freebsd", 2, lookFound, fileYes); ok {
		t.Fatal("unknown OS should fall back to bell")
	}
}
