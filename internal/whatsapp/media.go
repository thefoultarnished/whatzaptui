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

	sniffedType := http.DetectContentType(data)
	extType := ""
	if ext := strings.ToLower(filepath.Ext(filename)); ext != "" {
		extType = mime.TypeByExtension(ext)
	}

	switch kind {
	case "image":
		if isExpectedMediaType(sniffedType, "image/") || isExpectedMediaType(extType, "image/") {
			return nil
		}
		return fmt.Errorf("kind=image but file does not appear to be an image")
	case "video":
		if isExpectedMediaType(sniffedType, "video/") || isExpectedMediaType(extType, "video/") {
			return nil
		}
		return fmt.Errorf("kind=video but file does not appear to be a video")
	default:
		return fmt.Errorf("unsupported media kind: %s", kind)
	}
}

func isExpectedMediaType(actual, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actual)), prefix)
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
	default:
		return nil, nil, fmt.Errorf("unsupported media kind: %s", kind)
	}
}
