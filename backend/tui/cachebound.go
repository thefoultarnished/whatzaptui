package main

import "os"

// Bounds for the TUI in-memory caches. Everything evicted here stays
// safely on disk: messages in SQLite, media re-downloadable on demand.
const (
	maxCachedMessagesPerChat = 150
	maxCachedChats           = 30
	maxDownloadedMedia       = 100
)

// boundCaches trims unbounded per-session maps. Called once per Update
// (the single choke point all mutations flow through), so memory stays
// bounded no matter which handler appended data.
func (x *m) boundCaches() {
	for chatID, msgs := range x.msgs {
		if len(msgs) > maxCachedMessagesPerChat {
			x.msgs[chatID] = msgs[len(msgs)-maxCachedMessagesPerChat:]
		}
	}
	for len(x.msgs) > maxCachedChats {
		evicted := false
		for chatID := range x.msgs {
			if chatID == x.active {
				continue
			}
			delete(x.msgs, chatID)
			evicted = true
			break
		}
		if !evicted {
			break
		}
	}
	for len(x.mediaOrder) > maxDownloadedMedia {
		oldest := x.mediaOrder[0]
		x.mediaOrder = x.mediaOrder[1:]
		if path, ok := x.downloadedMedia[oldest]; ok {
			delete(x.downloadedMedia, oldest)
			_ = os.Remove(path)
		}
	}
	// Rebuild order list if it drifted (e.g. map reset elsewhere).
	if len(x.mediaOrder) != len(x.downloadedMedia) {
		seen := map[string]bool{}
		kept := x.mediaOrder[:0]
		for _, id := range x.mediaOrder {
			if _, ok := x.downloadedMedia[id]; ok && !seen[id] {
				seen[id] = true
				kept = append(kept, id)
			}
		}
		for id := range x.downloadedMedia {
			if !seen[id] {
				kept = append(kept, id)
			}
		}
		x.mediaOrder = kept
	}
}

// rememberMedia records a downloaded file, evicting the oldest first
// when over cap. Must be called instead of direct map assignment.
func (x *m) rememberMedia(msgID, path string) {
	if x.downloadedMedia == nil {
		x.downloadedMedia = make(map[string]string)
	}
	if _, ok := x.downloadedMedia[msgID]; !ok {
		x.mediaOrder = append(x.mediaOrder, msgID)
	}
	x.downloadedMedia[msgID] = path
}

// invalidate bumps the render revision, discarding the cached main pane.
// Called automatically on every Update; call it directly for mutations
// that happen outside Update.
func (x *m) invalidate() {
	x.revision++
}
