package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow"
)

var (
	pngHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R'}
	// ISO-BMFF "ftyp" boxes: Go's sniffer can't identify these brands.
	heicHeader = []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c', 0, 0, 0, 0}
	movHeader  = []byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p', 'q', 't', ' ', ' ', 0, 0, 0, 0}
	mp4Header  = []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0, 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}
	exeHeader  = []byte{'M', 'Z', 0x90, 0, 3, 0, 0, 0, 4, 0, 0, 0, 0xff, 0xff, 0, 0}
)

func TestValidateSendFileInput(t *testing.T) {
	if err := ValidateSendFileInput("photo.png", pngHeader, "image"); err != nil {
		t.Fatalf("real PNG should pass: %v", err)
	}
	if err := ValidateSendFileInput("photo.png", []byte("not really a png"), "image"); err == nil {
		t.Fatal("a .png name alone must not make text pass as an image")
	}
	if err := ValidateSendFileInput("notes.txt", []byte("text"), "image"); err == nil {
		t.Fatal("text input should not pass image validation")
	}
	if err := ValidateSendFileInput("notes.txt", []byte("text"), "document"); err != nil {
		t.Fatalf("documents should accept arbitrary content: %v", err)
	}
}

func TestValidateSendFileInputRejectsRenamedExecutable(t *testing.T) {
	if err := ValidateSendFileInput("photo.png", exeHeader, "image"); err == nil {
		t.Fatal("renamed .exe must not pass as an image")
	}
	if err := ValidateSendFileInput("clip.mp4", exeHeader, "video"); err == nil {
		t.Fatal("renamed .exe must not pass as a video")
	}
	if err := ValidateSendFileInput("photo.heic", exeHeader, "image"); err == nil {
		t.Fatal("renamed .exe must not pass as HEIC")
	}
}

func TestValidateSendFileInputAcceptsUnsniffableContainers(t *testing.T) {
	if err := ValidateSendFileInput("IMG_0001.heic", heicHeader, "image"); err != nil {
		t.Fatalf("HEIC photo should pass: %v", err)
	}
	if err := ValidateSendFileInput("clip.mov", movHeader, "video"); err != nil {
		t.Fatalf("MOV video should pass: %v", err)
	}
	if err := ValidateSendFileInput("clip.mp4", mp4Header, "video"); err != nil {
		t.Fatalf("MP4 video should pass: %v", err)
	}
	// A container header with a non-image extension is still not an image.
	if err := ValidateSendFileInput("clip.mov", movHeader, "image"); err == nil {
		t.Fatal("MOV must not pass as an image")
	}
}

func TestBuildOutgoingMediaMessageUsesContentType(t *testing.T) {
	// Real PNG bytes named .jpg: the proto must say what the bytes are.
	msg, _, err := BuildOutgoingMediaMessage("image", "photo.jpg", "", whatsmeow.UploadResponse{}, pngHeader)
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.GetImageMessage().GetMimetype(); got != "image/png" {
		t.Fatalf("image mimetype = %q, want image/png", got)
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

var (
	id3MP3   = []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0, 0xFF, 0xFB, 0x90, 0x44}
	rawMP3   = []byte{0xFF, 0xFB, 0x90, 0x44, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	adtsAAC  = []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	oggOpus  = []byte{'O', 'g', 'g', 'S', 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 'O', 'p', 'u', 's', 'H', 'e', 'a', 'd'}
	m4aAudio = []byte{0, 0, 0, 0x1C, 'f', 't', 'y', 'p', 'M', '4', 'A', ' ', 0, 0, 0, 0, 'M', '4', 'A', ' ', 'm', 'p', '4', '2', 'i', 's', 'o', 'm'}
)

func TestMediaTypeForSendKindAudio(t *testing.T) {
	if mt, err := MediaTypeForSendKind("audio"); err != nil || mt != whatsmeow.MediaAudio {
		t.Fatalf("audio kind = %q, %v; want MediaAudio", mt, err)
	}
}

func TestValidateSendFileInputAudio(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"song.mp3", id3MP3, "audio/mpeg"},
		{"song.mp3", rawMP3, "audio/mpeg"},
		{"clip.aac", adtsAAC, "audio/aac"},
		{"note.ogg", oggOpus, "audio/ogg; codecs=opus"},
		{"note.opus", oggOpus, "audio/ogg; codecs=opus"},
		{"memo.m4a", m4aAudio, "audio/mp4"},
	}
	for _, c := range cases {
		if err := ValidateSendFileInput(c.name, c.data, "audio"); err != nil {
			t.Errorf("%s should pass as audio: %v", c.name, err)
		}
		msg, wire, err := BuildOutgoingMediaMessage("audio", c.name, "ignored caption", whatsmeow.UploadResponse{}, c.data)
		if err != nil {
			t.Fatalf("%s: build: %v", c.name, err)
		}
		if got := msg.GetAudioMessage().GetMimetype(); got != c.want {
			t.Errorf("%s mimetype = %q, want %q", c.name, got, c.want)
		}
		if _, ok := wire["audioMessage"]; !ok {
			t.Errorf("%s: wire body should be an audioMessage, got %v", c.name, wire)
		}
	}
}

func TestValidateSendFileInputRejectsFakeAudio(t *testing.T) {
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"song.mp3", []byte("just some text, not audio")},
		{"song.mp3", exeHeader},
		{"song.mp3", pngHeader},
		{"clip.m4a", heicHeader[:4]}, // too short to be a container
	} {
		if err := ValidateSendFileInput(c.name, c.data, "audio"); err == nil {
			t.Errorf("%s with non-audio content should be rejected", c.name)
		}
	}
}
