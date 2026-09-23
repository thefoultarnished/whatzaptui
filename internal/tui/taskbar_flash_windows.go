//go:build windows

package tui

import (
	"strings"
	"syscall"
	"unsafe"
)

type flashwinfo struct {
	cbSize    uint32
	hwnd      uintptr
	dwFlags   uint32
	uCount    uint32
	dwTimeout uint32
}

// PROCESSENTRY32W mirrors the Win32 struct layout exactly on 64-bit.
// th32DefaultHeapID (ULONG_PTR) causes 4 bytes of automatic padding after
// th32ProcessID, which matches the Windows struct offset.
type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr // 8 bytes on 64-bit; Go inserts 4-byte pad before this
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16
}

const (
	flashwAll         = 0x00000003
	flashwTimerNFG    = 0x0000000C
	th32csSnapProcess = 0x00000002
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextLen         = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText            = user32.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetConsoleWindow         = kernel32.NewProc("GetConsoleWindow")
	procFlashWindowEx            = user32.NewProc("FlashWindowEx")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First           = kernel32.NewProc("Process32FirstW")
	procProcess32Next            = kernel32.NewProc("Process32NextW")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

func flashTaskbarWindow() {
	// Strategy 1: GetConsoleWindow (works in classic conhost, no-op in ConPTY/WT).
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd != 0 {
		flashWindow(hwnd)
	}

	// Strategy 2: Search top-level windows by title "WhatZap".
	flashMatchingWindowTitles("WhatZap")

	// Strategy 3: Find Windows Terminal (or any terminal emulator) by tracing
	// the parent process and flashing its main visible window.
	flashParentProcessWindow()
}

// flashParentProcessWindow finds our parent process (e.g. wt.exe) and flashes
// its main visible window. This reliably covers Windows Terminal ConPTY mode.
func flashParentProcessWindow() {
	parentPID := parentProcessID()
	if parentPID == 0 {
		return
	}
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !isWindowVisible(hwnd) {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if uint32(pid) == parentPID {
			flashWindow(hwnd)
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
}

func parentProcessID() uint32 {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == ^uintptr(0) { // INVALID_HANDLE_VALUE
		return 0
	}
	defer procCloseHandle.Call(snap)

	ourPID := uint32(syscall.Getpid())

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))
	ret, _, _ := procProcess32First.Call(snap, uintptr(unsafe.Pointer(&entry)))
	for ret != 0 {
		if entry.th32ProcessID == ourPID {
			return entry.th32ParentProcessID
		}
		entry.dwSize = uint32(unsafe.Sizeof(entry))
		ret, _, _ = procProcess32Next.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return 0
}

func flashMatchingWindowTitles(substr string) {
	if strings.TrimSpace(substr) == "" {
		return
	}
	needle := strings.ToLower(substr)
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !isWindowVisible(hwnd) {
			return 1
		}
		title := strings.ToLower(windowText(hwnd))
		if title == "" || !strings.Contains(title, needle) {
			return 1
		}
		flashWindow(hwnd)
		return 1
	})
	procEnumWindows.Call(cb, 0)
}

func isWindowVisible(hwnd uintptr) bool {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	return visible != 0
}

func windowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLen.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func flashWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	info := flashwinfo{
		cbSize:    uint32(unsafe.Sizeof(flashwinfo{})),
		hwnd:      hwnd,
		dwFlags:   flashwAll | flashwTimerNFG,
		uCount:    3,
		dwTimeout: 0,
	}
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}
