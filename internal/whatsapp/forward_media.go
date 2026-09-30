package whatsapp

import (
	"errors"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// MediaLinkMaxAge is how long a stored media link is trusted for forwarding.
// WhatsApp keeps uploads for a limited time, so an older message is re-sent
// from a fresh download instead of pointing at the old link.
const MediaLinkMaxAge = 14 * 24 * time.Hour

// Errors returned when a message cannot be forwarded as media.
var (
	ErrNoForwardableMedia = errors.New("only text, image, video, file and audio messages can be forwarded")
	ErrViewOnceMedia      = errors.New("view once media cannot be forwarded")
)

// MediaLinkStale reports whether the media link of a message sent at ts (unix
// seconds) is too old to point at. An unknown time counts as stale, since a
// fresh upload is always safe.
func MediaLinkStale(ts int64, now time.Time) bool {
	if ts <= 0 {
		return true
	}
	return now.Sub(time.Unix(ts, 0)) > MediaLinkMaxAge
}

// forwardKind returns the send kind of the media in msg ("image", "video",
// "document" or "audio"), or "" when it has none of them.
func forwardKind(msg *waE2E.Message) string {
	switch {
	case msg.GetImageMessage() != nil:
		return "image"
	case msg.GetVideoMessage() != nil:
		return "video"
	case msg.GetDocumentMessage() != nil:
		return "document"
	case msg.GetAudioMessage() != nil:
		return "audio"
	}
	return ""
}

// ForwardMediaKind returns the kind of forwardable media in a stored message,
// or "" when there is none. Stickers and other message types are not included.
func ForwardMediaKind(msg *waE2E.Message) string {
	return forwardKind(EffectiveMessage(msg))
}

// forwardedContext builds the context of a forwarded media message from the
// original one. It carries nothing but the forwarded mark, so a quote or mention
// on the original never travels along. The forwarding score goes up by one.
func forwardedContext(old *waE2E.ContextInfo) *waE2E.ContextInfo {
	score := old.GetForwardingScore() + 1
	return &waE2E.ContextInfo{
		IsForwarded:     proto.Bool(true),
		ForwardingScore: proto.Uint32(score),
	}
}

// BuildForwardedMediaMessage copies a stored media message into a new message
// marked as forwarded. The copy keeps the same link and key, so nothing is
// downloaded or uploaded, and keeps the caption. It also returns the media kind.
func BuildForwardedMediaMessage(stored *waE2E.Message) (*waE2E.Message, string, error) {
	eff := EffectiveMessage(stored)
	if eff == nil {
		return nil, "", ErrNoForwardableMedia
	}
	kind := forwardKind(eff)
	if kind == "" {
		return nil, "", ErrNoForwardableMedia
	}
	if isViewOnce(stored, eff) {
		return nil, "", ErrViewOnceMedia
	}
	out := &waE2E.Message{}
	switch kind {
	case "image":
		img := proto.Clone(eff.GetImageMessage()).(*waE2E.ImageMessage)
		img.ContextInfo = forwardedContext(img.GetContextInfo())
		out.ImageMessage = img
	case "video":
		vid := proto.Clone(eff.GetVideoMessage()).(*waE2E.VideoMessage)
		vid.ContextInfo = forwardedContext(vid.GetContextInfo())
		out.VideoMessage = vid
	case "document":
		doc := proto.Clone(eff.GetDocumentMessage()).(*waE2E.DocumentMessage)
		doc.ContextInfo = forwardedContext(doc.GetContextInfo())
		out.DocumentMessage = doc
	case "audio":
		aud := proto.Clone(eff.GetAudioMessage()).(*waE2E.AudioMessage)
		aud.ContextInfo = forwardedContext(aud.GetContextInfo())
		out.AudioMessage = aud
	}
	return out, kind, nil
}

// isViewOnce reports whether a media message was sent as view once, either by
// a wrapper around it or by the flag on the media itself.
func isViewOnce(stored, eff *waE2E.Message) bool {
	for msg := stored; msg != nil; {
		switch {
		case msg.GetViewOnceMessage() != nil, msg.GetViewOnceMessageV2() != nil, msg.GetViewOnceMessageV2Extension() != nil:
			return true
		case msg.GetDeviceSentMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		case msg.GetEphemeralMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		default:
			msg = nil
		}
	}
	return eff.GetImageMessage().GetViewOnce() || eff.GetVideoMessage().GetViewOnce() || eff.GetAudioMessage().GetViewOnce()
}

// ReplaceMediaUpload points a forwarded media message at a fresh upload. It
// keeps everything else (caption, file name, type, voice note mark, duration).
// It is the fallback for a media link that is too old to reuse.
func ReplaceMediaUpload(msg *waE2E.Message, up whatsmeow.UploadResponse) error {
	now := proto.Int64(time.Now().Unix())
	switch {
	case msg.GetImageMessage() != nil:
		img := msg.GetImageMessage()
		img.URL, img.DirectPath, img.MediaKey = proto.String(up.URL), proto.String(up.DirectPath), up.MediaKey
		img.FileEncSHA256, img.FileSHA256, img.FileLength = up.FileEncSHA256, up.FileSHA256, proto.Uint64(up.FileLength)
		img.MediaKeyTimestamp = now
	case msg.GetVideoMessage() != nil:
		vid := msg.GetVideoMessage()
		vid.URL, vid.DirectPath, vid.MediaKey = proto.String(up.URL), proto.String(up.DirectPath), up.MediaKey
		vid.FileEncSHA256, vid.FileSHA256, vid.FileLength = up.FileEncSHA256, up.FileSHA256, proto.Uint64(up.FileLength)
		vid.MediaKeyTimestamp = now
	case msg.GetDocumentMessage() != nil:
		doc := msg.GetDocumentMessage()
		doc.URL, doc.DirectPath, doc.MediaKey = proto.String(up.URL), proto.String(up.DirectPath), up.MediaKey
		doc.FileEncSHA256, doc.FileSHA256, doc.FileLength = up.FileEncSHA256, up.FileSHA256, proto.Uint64(up.FileLength)
		doc.MediaKeyTimestamp = now
	case msg.GetAudioMessage() != nil:
		aud := msg.GetAudioMessage()
		aud.URL, aud.DirectPath, aud.MediaKey = proto.String(up.URL), proto.String(up.DirectPath), up.MediaKey
		aud.FileEncSHA256, aud.FileSHA256, aud.FileLength = up.FileEncSHA256, up.FileSHA256, proto.Uint64(up.FileLength)
		aud.MediaKeyTimestamp = now
	default:
		return ErrNoForwardableMedia
	}
	return nil
}
