//go:build windows

package tokenlock

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Layout constants for the on-disk ACL/ACE structures.
const (
	aclRevision   = 2 // ACL_REVISION
	aclHeaderSize = 8 // revision + sbz1 + size + ace count + sbz2
	aceHeaderSize = 4 // type + flags + size
	aceMaskSize   = 4
)

// RestrictFileToCurrentUser replaces path's DACL with a protected (no
// inheritance) DACL granting full control to the current user only.
// Administrators can still take ownership — this stops other standard
// local accounts, which is the shared-workstation threat.
func RestrictFileToCurrentUser(path string) error {
	userSID, err := currentUserSID()
	if err != nil {
		return err
	}
	dacl, buf := buildSingleAceDACL(userSID, uint32(windows.GENERIC_ALL))
	_ = buf // kept alive for the syscall below via normal scoping
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		return fmt.Errorf("set security info: %w", err)
	}
	return nil
}

// buildSingleAceDACL builds a one-ACE DACL granting mask to sid with no
// inheritance. It returns the ACL pointer and the backing buffer, which
// the caller must keep reachable until the ACL is consumed.
func buildSingleAceDACL(sid *windows.SID, mask uint32) (*windows.ACL, []byte) {
	sidLen := sid.Len()
	aceSize := aceHeaderSize + aceMaskSize + sidLen
	buf := make([]byte, aclHeaderSize+aceSize)
	buf[0] = aclRevision
	binary.LittleEndian.PutUint16(buf[2:], uint16(len(buf)))
	binary.LittleEndian.PutUint16(buf[4:], 1) // AceCount
	off := aclHeaderSize
	buf[off] = windows.ACCESS_ALLOWED_ACE_TYPE
	buf[off+1] = 0 // no inheritance flags
	binary.LittleEndian.PutUint16(buf[off+2:], uint16(aceSize))
	binary.LittleEndian.PutUint32(buf[off+4:], mask)
	sidBytes := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sidLen)
	copy(buf[off+aceHeaderSize+aceMaskSize:], sidBytes)
	return (*windows.ACL)(unsafe.Pointer(&buf[0])), buf
}

func currentUserSID() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get token user: %w", err)
	}
	return tu.User.Sid, nil
}
