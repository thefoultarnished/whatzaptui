package backend

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"whatzap/internal/whatsapp"

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
	if a.store != nil {
		mediaProto, _ = a.store.GetMediaProto(chatID, msgID)
	} else if a.db != nil {
		_ = a.db.QueryRow(
			`SELECT media_proto FROM messages WHERE chat_id = ? AND id = ? AND media_proto != ''`,
			chatID, msgID,
		).Scan(&mediaProto)
	}
	if mediaProto == "" {
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
	return whatsapp.MediaTypeForSendKind(kind)
}

func validateSendFileInput(filename string, data []byte, kind string) error {
	return whatsapp.ValidateSendFileInput(filename, data, kind)
}

func isExpectedMediaType(actual, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actual)), prefix)
}

func detectMIMEType(filename string, data []byte, fallback string) string {
	return whatsapp.DetectMIMEType(filename, data, fallback)
}

func buildOutgoingMediaMessage(kind, filename, caption string, upload whatsmeow.UploadResponse, data []byte) (*waE2E.Message, map[string]any, error) {
	return whatsapp.BuildOutgoingMediaMessage(kind, filename, caption, upload, data)
}
