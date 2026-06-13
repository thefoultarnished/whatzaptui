package main

import (
	"os"
	"runtime"
	"syscall"
)

// processAlive reports whether a process with the given PID is currently
// running. Used by A-1 to detect when the TUI process that registered via
// /session/register has exited, so the session token can be rotated.
//
// On Unix, os.FindProcess never fails (it just wraps the PID), so liveness
// is checked by sending signal 0: the kernel still validates the PID exists
// and is owned by this user without actually delivering a signal.
//
// On Windows, os.FindProcess itself calls OpenProcess and returns an error
// if the PID doesn't exist, so a successful FindProcess is sufficient.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
