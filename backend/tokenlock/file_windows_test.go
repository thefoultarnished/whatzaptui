//go:build windows

package tokenlock

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// assertWindowsLockedDown verifies the file DACL is protected from
// inheritance and holds exactly one ACE granting full control to the
// current user.
func assertWindowsLockedDown(t *testing.T, path string) {
	t.Helper()

	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("get security info: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("get dacl: %v", err)
	}

	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("get control: %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("dacl is not protected from inheritance")
	}
	if n := dacl.AceCount; n != 1 {
		t.Fatalf("dacl ace count = %d, want 1", n)
	}

	var aceRaw *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &aceRaw); err != nil {
		t.Fatalf("get ace: %v", err)
	}
	// Windows maps GENERIC_ALL to object-specific rights on write
	// (files end up with 0x1F01FF = full control), so check for the
	// full-control bits rather than the generic constant.
	const fileFullControl = 0x1F01FF
	if got := uint32(aceRaw.Mask); got&fileFullControl != fileFullControl {
		t.Fatalf("ace mask = %#x, want full-control bits %#x", got, fileFullControl)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&aceRaw.SidStart))
	want, err := currentUserSID()
	if err != nil {
		t.Fatalf("current user sid: %v", err)
	}
	if !windows.EqualSid(aceSID, want) {
		t.Fatal("ace sid is not the current user")
	}
}
