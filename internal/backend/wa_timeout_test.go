package backend

import (
	"context"
	"errors"
	"os"
	"regexp"
	"testing"
	"time"
)

// Positive: normal WhatsApp calls get a deadline of about waCallTimeout.
func TestWACallCtxHasDeadline(t *testing.T) {
	ctx, cancel := waCallCtx()
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("waCallCtx has no deadline; a hung WhatsApp call would block forever")
	}
	if left := time.Until(dl); left <= 0 || left > waCallTimeout {
		t.Fatalf("deadline in %v, want within (0, %v]", left, waCallTimeout)
	}
}

// Positive: uploads get a deadline too, and a longer one than normal calls
// so big files on slow links aren't cut off.
func TestWAUploadCtxHasLongerDeadline(t *testing.T) {
	ctx, cancel := waUploadCtx()
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("waUploadCtx has no deadline")
	}
	if waUploadTimeout <= waCallTimeout {
		t.Fatalf("upload timeout %v must exceed call timeout %v", waUploadTimeout, waCallTimeout)
	}
}

// Positive: media downloads get a deadline, the most generous of all, since
// big videos can take a long time on slow links.
func TestWADownloadCtxHasMostGenerousDeadline(t *testing.T) {
	ctx, cancel := waDownloadCtx()
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("waDownloadCtx has no deadline")
	}
	if waDownloadTimeout < waUploadTimeout || waDownloadTimeout <= waCallTimeout {
		t.Fatalf("download timeout %v should be >= upload %v and > call %v", waDownloadTimeout, waUploadTimeout, waCallTimeout)
	}
}

// Edge: a download context also expires, so a stuck download gives up.
func TestWADownloadCtxExpires(t *testing.T) {
	orig := waDownloadTimeout
	waDownloadTimeout = 20 * time.Millisecond
	t.Cleanup(func() { waDownloadTimeout = orig })

	ctx, cancel := waDownloadCtx()
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download context never expired")
	}
}

// Edge: once the limit passes, the context is done with DeadlineExceeded,
// which is what makes a stuck call give up and return an error.
func TestWACallCtxExpires(t *testing.T) {
	orig := waCallTimeout
	waCallTimeout = 20 * time.Millisecond
	t.Cleanup(func() { waCallTimeout = orig })

	ctx, cancel := waCallCtx()
	defer cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("ctx.Err() = %v, want DeadlineExceeded", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context never expired")
	}
}

// Negative: cancel releases the context immediately (no lingering timer),
// and each call gets its own independent context.
func TestWACallCtxCancelAndIndependence(t *testing.T) {
	a, cancelA := waCallCtx()
	b, cancelB := waCallCtx()
	defer cancelB()
	cancelA()
	if !errors.Is(a.Err(), context.Canceled) {
		t.Fatalf("after cancel, a.Err() = %v, want Canceled", a.Err())
	}
	if b.Err() != nil {
		t.Fatalf("cancelling one call affected another: b.Err() = %v", b.Err())
	}
}

// Regression: the user-triggered WhatsApp network calls must not go back to
// an unlimited context.Background(). Local-only calls are exempt:
// DecryptPollVote (decrypts in memory) and Store.* lookups (local DB).
func TestNoUnboundedWhatsAppCalls(t *testing.T) {
	re := regexp.MustCompile(`client\.(\w+)\(context\.Background\(\)`)
	for _, file := range []string{"messages.go", "media.go", "contacts.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if m[1] == "DecryptPollVote" {
				continue
			}
			t.Errorf("%s: client.%s is called with context.Background() (no time limit)", file, m[1])
		}
	}
}
