//go:build !windows

package tokenlock

// RestrictFileToCurrentUser is a no-op outside Windows: POSIX 0600/0700
// modes already restrict the file to the current user there.
func RestrictFileToCurrentUser(_ string) error { return nil }
