package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func (a *App) handleProfilePicture(w http.ResponseWriter, r *http.Request) {
	if !a.requireConnectedClient(w) {
		return
	}
	jidRaw := strings.TrimSpace(r.URL.Query().Get("jid"))
	if jidRaw == "" {
		writeErr(w, http.StatusBadRequest, "jid is required")
		return
	}
	jid, err := types.ParseJID(jidRaw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid jid")
		return
	}
	info, err := a.client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{})
	if err != nil || info == nil {
		writeJSON(w, http.StatusOK, map[string]any{"url": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": info.URL})
}

func (a *App) handleMediaDownload(w http.ResponseWriter, r *http.Request) {
	if !a.requireConnectedClient(w) {
		return
	}
	chatID := r.URL.Query().Get("chatId")
	msgID := r.URL.Query().Get("msgId")
	if chatID == "" || msgID == "" {
		writeErr(w, http.StatusBadRequest, "chatId and msgId required")
		return
	}
	var mediaProto string
	if err := a.db.QueryRow(
		`SELECT media_proto FROM messages WHERE chat_id = ? AND id = ? AND media_proto != ''`,
		chatID, msgID,
	).Scan(&mediaProto); err != nil || mediaProto == "" {
		writeErr(w, http.StatusNotFound, "media not found")
		return
	}
	b, err := base64.StdEncoding.DecodeString(mediaProto)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "decode error")
		return
	}
	var msg waE2E.Message
	if err := proto.Unmarshal(b, &msg); err != nil {
		writeErr(w, http.StatusInternalServerError, "unmarshal error")
		return
	}
	data, err := a.client.DownloadAny(context.Background(), &msg)
	if err != nil {
		writeInternalErr(w, err)
		return
	}
	ext := mediaExtension(&msg)
	tmp, err := os.CreateTemp("", "whatzap-*"+ext)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "temp file error")
		return
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, "temp file write error")
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		writeErr(w, http.StatusInternalServerError, "temp file close error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": tmp.Name()})
}

func mediaExtension(msg *waE2E.Message) string {
	msg = effectiveMessage(msg)
	if msg == nil {
		return ".bin"
	}
	if img := msg.GetImageMessage(); img != nil {
		if strings.Contains(img.GetMimetype(), "jpeg") {
			return ".jpg"
		}
		return ".png"
	}
	if msg.GetVideoMessage() != nil {
		return ".mp4"
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		if ext := filepath.Ext(doc.GetFileName()); ext != "" {
			return ext
		}
		return ".bin"
	}
	if msg.GetAudioMessage() != nil {
		return ".ogg"
	}
	if msg.GetStickerMessage() != nil {
		return ".webp"
	}
	return ".bin"
}

func mediaTypeForSendKind(kind string) (whatsmeow.MediaType, error) {
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

func validateSendFileInput(filename string, data []byte, kind string) error {
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

func detectMIMEType(filename string, data []byte, fallback string) string {
	if ext := strings.ToLower(filepath.Ext(filename)); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	if len(data) > 0 {
		return http.DetectContentType(data)
	}
	return fallback
}

func buildOutgoingMediaMessage(kind, filename, caption string, upload whatsmeow.UploadResponse, data []byte) (*waE2E.Message, map[string]any, error) {
	mimeType := detectMIMEType(filename, data, "application/octet-stream")
	fileName := filepath.Base(filename)
	switch kind {
	case "image":
		msg := &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"imageMessage": map[string]any{
				"fileName": fileName,
				"caption":  caption,
				"mimetype": mimeType,
			},
		}, nil
	case "video":
		msg := &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				Caption:       proto.String(caption),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"videoMessage": map[string]any{
				"fileName": fileName,
				"caption":  caption,
				"mimetype": mimeType,
			},
		}, nil
	case "document":
		msg := &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				Caption:       proto.String(caption),
				Title:         proto.String(fileName),
				FileName:      proto.String(fileName),
				Mimetype:      proto.String(mimeType),
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(upload.FileLength),
			},
		}
		return msg, map[string]any{
			"documentMessage": map[string]any{
				"caption":  caption,
				"fileName": fileName,
				"mimetype": mimeType,
			},
		}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported media kind: %s", kind)
	}
}
