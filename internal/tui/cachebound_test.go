package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundCachesTrimsPerChatKeepNewest(t *testing.T) {
	msgs := make([]wireMsg, 0, maxCachedMessagesPerChat+50)
	for i := 0; i < maxCachedMessagesPerChat+50; i++ {
		msgs = append(msgs, mkMsg(fmt.Sprintf("m%d", i), int64(i)))
	}
	model := m{active: "c1", msgs: map[string][]wireMsg{"c1": msgs}}
	model.boundCaches()
	got := model.msgs["c1"]
	if len(got) != maxCachedMessagesPerChat {
		t.Fatalf("len = %d, want %d", len(got), maxCachedMessagesPerChat)
	}
	if got[0].Key.ID != "m50" || got[len(got)-1].Key.ID != fmt.Sprintf("m%d", maxCachedMessagesPerChat+49) {
		t.Fatalf("kept wrong window: first=%s last=%s", got[0].Key.ID, got[len(got)-1].Key.ID)
	}
}

func TestBoundCachesEvictsNonActiveChats(t *testing.T) {
	msgs := map[string][]wireMsg{}
	for i := 0; i < maxCachedChats+5; i++ {
		id := fmt.Sprintf("chat%d@s.whatsapp.net", i)
		msgs[id] = []wireMsg{mkMsg("m1", 1)}
	}
	model := m{active: "chat0@s.whatsapp.net", msgs: msgs}
	model.boundCaches()
	if len(model.msgs) != maxCachedChats {
		t.Fatalf("chats = %d, want %d", len(model.msgs), maxCachedChats)
	}
	if _, ok := model.msgs["chat0@s.whatsapp.net"]; !ok {
		t.Fatal("active chat was evicted")
	}
}

func TestBoundCachesViaUpdate(t *testing.T) {
	big := make([]wireMsg, 0, maxCachedMessagesPerChat+10)
	for i := 0; i < maxCachedMessagesPerChat+10; i++ {
		big = append(big, mkMsg(fmt.Sprintf("m%d", i), int64(i)))
	}
	model := m{status: "ready", active: "c1", msgs: map[string][]wireMsg{}}
	next, _ := model.Update(msgsMsg{chatID: "c1", msgs: big})
	got := next.(m).msgs["c1"]
	if len(got) != maxCachedMessagesPerChat {
		t.Fatalf("len = %d, want %d", len(got), maxCachedMessagesPerChat)
	}
	if got[len(got)-1].Key.ID != fmt.Sprintf("m%d", maxCachedMessagesPerChat+9) {
		t.Fatalf("newest dropped: last=%s", got[len(got)-1].Key.ID)
	}
}

func TestRememberMediaEvictsOldestFile(t *testing.T) {
	dir := t.TempDir()
	model := m{}
	for i := 0; i < maxDownloadedMedia+5; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%d.tmp", i))
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		model.rememberMedia(fmt.Sprintf("id%d", i), p)
	}
	model.boundCaches()
	if len(model.downloadedMedia) != maxDownloadedMedia {
		t.Fatalf("media = %d, want %d", len(model.downloadedMedia), maxDownloadedMedia)
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id%d", i)
		if _, ok := model.downloadedMedia[id]; ok {
			t.Fatalf("%s should have been evicted", id)
		}
		if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("f%d.tmp", i))); !os.IsNotExist(err) {
			t.Fatalf("oldest file f%d.tmp not removed", i)
		}
	}
	if _, ok := model.downloadedMedia[fmt.Sprintf("id%d", maxDownloadedMedia+4)]; !ok {
		t.Fatal("newest media missing")
	}
}
