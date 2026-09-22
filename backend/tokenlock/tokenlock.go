// Package tokenlock restricts a file's Windows ACL to the current user.
//
// POSIX modes (0600/0700) are no-ops on Windows: files inherit the parent
// folder's DACL, so on shared workstations other local accounts can read
// session.token. RestrictFileToCurrentUser strips inherited ACEs and grants
// access only to the current user. On non-Windows platforms it is a no-op
// (0600/0700 already work there).
package tokenlock
