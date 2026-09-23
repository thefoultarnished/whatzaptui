package whatsapp

import "testing"

func TestValidateSendFileInput(t *testing.T) {
	if err := ValidateSendFileInput("photo.png", []byte("not really a png"), "image"); err != nil {
		t.Fatalf("extension should allow image input: %v", err)
	}
	if err := ValidateSendFileInput("notes.txt", []byte("text"), "image"); err == nil {
		t.Fatal("text input should not pass image validation")
	}
	if err := ValidateSendFileInput("notes.txt", []byte("text"), "document"); err != nil {
		t.Fatalf("documents should accept arbitrary content: %v", err)
	}
}

func TestDetectMIMEType(t *testing.T) {
	if got := DetectMIMEType("photo.png", nil, "application/octet-stream"); got != "image/png" {
		t.Fatalf("DetectMIMEType = %q, want image/png", got)
	}
	if got := DetectMIMEType("unknown", nil, "application/octet-stream"); got != "application/octet-stream" {
		t.Fatalf("DetectMIMEType fallback = %q, want application/octet-stream", got)
	}
}
