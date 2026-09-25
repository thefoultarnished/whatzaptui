package buildid

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

var (
	cachedID string
	once     sync.Once
)

// Current returns the SHA-256 hash of the running executable.
// The result is computed once on first call and cached.
// If the executable cannot be resolved or read, an empty string is returned.
func Current() string {
	once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		id, err := OfFile(exe)
		if err == nil {
			cachedID = id
		}
	})
	return cachedID
}

// OfFile returns the SHA-256 hex digest of the file at path.
func OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
