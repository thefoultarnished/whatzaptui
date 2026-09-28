package whatsapp

import (
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func MediaTypeForSendKind(kind string) (whatsmeow.MediaType, error) {
	switch kind {
	case "image":
		return whatsmeow.MediaImage, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	case "audio":
		return whatsmeow.MediaAudio, nil
	default:
		return "", fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func ValidateSendFileInput(filename string, data []byte, kind string) error {
	if filename == "" {
		return fmt.Errorf("filename is required")
	}
	if kind == "" {
		return fmt.Errorf("kind is required")
	}
	if kind == "document" {
		return nil
	}

	// The file's content decides, not its name: a renamed .exe must not go
	// out as "photo.png".
	switch kind {
	case "image":
		if mediaMIMEType("image/", filename, data) != "" {
			return nil
		}
		return fmt.Errorf("kind=image but file does not appear to be an image")
	case "video":
		if mediaMIMEType("video/", filename, data) != "" {
			return nil
		}
		return fmt.Errorf("kind=video but file does not appear to be a video")
	case "audio":
		if audioMIMEType(filename, data) != "" {
			return nil
		}
		return fmt.Errorf("kind=audio but file does not appear to be audio")
	default:
		return fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func isExpectedMediaType(actual, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actual)), prefix)
}

// isISOBMFF reports whether data starts with an ISO base media "ftyp" box,
// the container used by HEIC/AVIF photos and MOV/3GP/M4V videos.
func isISOBMFF(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp"
}

// isoBMFFTypes maps the extensions of ISO-BMFF media that Go's sniffer
// can't identify to their MIME types. A fixed table, not
// mime.TypeByExtension, because the OS registry doesn't reliably know them.
var isoBMFFTypes = map[string]string{
	".heic": "image/heic",
	".heif": "image/heif",
	".avif": "image/avif",
	".mp4":  "video/mp4",
	".m4v":  "video/x-m4v",
	".mov":  "video/quicktime",
	".3gp":  "video/3gpp",
	".3g2":  "video/3gpp2",
}

// audioMIMEType returns the MIME type for an audio upload, judged by
// content, or "" when the content isn't a format WhatsApp plays (MP3, AAC,
// M4A, Ogg/Opus). Go's sniffer only knows some of these, so MPEG/ADTS frame
// headers and M4A containers are checked by hand, with the extension used
// only to tell look-alike formats apart.
func audioMIMEType(filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch http.DetectContentType(data) {
	case "audio/mpeg":
		return "audio/mpeg"
	case "application/ogg":
		return "audio/ogg; codecs=opus"
	}
	if isISOBMFF(data) && (ext == ".m4a" || ext == ".aac" || ext == ".mp4") {
		return "audio/mp4"
	}
	// MPEG audio / ADTS AAC frame sync: 11 set bits.
	if len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0 {
		switch ext {
		case ".mp3":
			return "audio/mpeg"
		case ".aac":
			return "audio/aac"
		}
	}
	return ""
}

// mediaMIMEType returns the MIME type for an image ("image/") or video
// ("video/") upload, judged by content: the sniffed type when it matches,
// otherwise the extension's type only for ISO-BMFF files, which Go's sniffer
// can't identify (HEIC, AVIF, MOV, 3GP). Returns "" when the content isn't
// that kind of media.
func mediaMIMEType(prefix, filename string, data []byte) string {
	if sniffed := http.DetectContentType(data); isExpectedMediaType(sniffed, prefix) {
		return sniffed
	}
	if !isISOBMFF(data) {
		return ""
	}
	if t := isoBMFFTypes[strings.ToLower(filepath.Ext(filename))]; isExpectedMediaType(t, prefix) {
		return t
	}
	return ""
}

func DetectMIMEType(filename string, data []byte, fallback string) string {
	if ext := strings.ToLower(filepath.Ext(filename)); ext != "" {
		if mediaType := mime.TypeByExtension(ext); mediaType != "" {
			return mediaType
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return fallback
}

func BuildOutgoingMediaMessage(kind, filename, caption string, upload whatsmeow.UploadResponse, data []byte) (*waE2E.Message, map[string]any, error) {
	mimeType := DetectMIMEType(filename, data, "application/octet-stream")
	switch kind {
	case "image":
		if t := mediaMIMEType("image/", filename, data); t != "" {
			mimeType = t
		}
	case "video":
		if t := mediaMIMEType("video/", filename, data); t != "" {
			mimeType = t
		}
	case "audio":
		if t := audioMIMEType(filename, data); t != "" {
			mimeType = t
		}
	}
	fileName := filepath.Base(filename)
	switch kind {
	case "image":
		msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType), URL: proto.String(upload.URL),
			DirectPath: proto.String(upload.DirectPath), MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256,
			FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
		}}
		return msg, map[string]any{"imageMessage": map[string]any{"fileName": fileName, "caption": caption, "mimetype": mimeType}}, nil
	case "video":
		msg := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType), URL: proto.String(upload.URL),
			DirectPath: proto.String(upload.DirectPath), MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256,
			FileSHA256: upload.FileSHA256, FileLength: proto.Uint64(upload.FileLength),
		}}
		return msg, map[string]any{"videoMessage": map[string]any{"fileName": fileName, "caption": caption, "mimetype": mimeType}}, nil
	case "document":
		msg := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Caption: proto.String(caption), Title: proto.String(fileName), FileName: proto.String(fileName),
			Mimetype: proto.String(mimeType), URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength),
		}}
		return msg, map[string]any{"documentMessage": map[string]any{"caption": caption, "fileName": fileName, "mimetype": mimeType}}, nil
	case "audio":
		// WhatsApp audio messages have no caption field; any caption is dropped.
		msg := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String(mimeType), URL: proto.String(upload.URL), DirectPath: proto.String(upload.DirectPath),
			MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256,
			FileLength: proto.Uint64(upload.FileLength),
		}}
		return msg, map[string]any{"audioMessage": map[string]any{"fileName": fileName, "mimetype": mimeType}}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported media kind: %s", kind)
	}
}
