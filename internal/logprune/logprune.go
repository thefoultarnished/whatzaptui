// Package logprune removes old per-session log files so they don't pile up
// forever. Both the backend action log and the TUI trace log write one file
// per session named <prefix><utc-timestamp><ext>.
package logprune

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultKeep is how many session logs each log folder keeps.
const DefaultKeep = 10

// KeepNewest deletes all but the newest keep files in dir whose names start
// with prefix and end with ext. Names carry a fixed-width UTC timestamp, so
// name order is age order. Other files are never touched. Deletion is best
// effort: a file that can't be removed (e.g. still open) is skipped.
func KeepNewest(dir, prefix, ext string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ext) {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(dir, n))
	}
	return nil
}
