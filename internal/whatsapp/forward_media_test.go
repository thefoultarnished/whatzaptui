package whatsapp

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func storedImage() *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:       proto.String("sunset"),
		Mimetype:      proto.String("image/jpeg"),
		URL:           proto.String("https://mmg.whatsapp.net/old"),
		DirectPath:    proto.String("/v/old"),
		MediaKey:      []byte{1, 2, 3},
		FileEncSHA256: []byte{4, 5},
		FileSHA256:    []byte{6, 7},
		FileLength:    proto.Uint64(1234),
		JPEGThumbnail: []byte{9, 9},
	}}
}

func TestForwardedImageKeepsLinkKeyAndCaption(t *testing.T) {
	out, kind, err := BuildForwardedMediaMessage(storedImage())
	if err != nil || kind != "image" {
		t.Fatalf("kind=%q err=%v, want image and no error", kind, err)
	}
	img := out.GetImageMessage()
	if img == nil {
		t.Fatalf("expected an image message, got %+v", out)
	}
	if img.GetURL() != "https://mmg.whatsapp.net/old" || img.GetDirectPath() != "/v/old" || string(img.GetMediaKey()) != "\x01\x02\x03" {
		t.Fatalf("the link and key must be copied unchanged: %+v", img)
	}
	if img.GetCaption() != "sunset" || img.GetMimetype() != "image/jpeg" || img.GetFileLength() != 1234 || len(img.GetJPEGThumbnail()) != 2 {
		t.Fatalf("caption and file details must be kept: %+v", img)
	}
	ctx := img.GetContextInfo()
	if !ctx.GetIsForwarded() || ctx.GetForwardingScore() != 1 {
		t.Fatalf("must be marked forwarded with score 1: %+v", ctx)
	}
}

func TestForwardedMediaKeepsPerKindDetails(t *testing.T) {
	video := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("clip"), Mimetype: proto.String("video/mp4"), Seconds: proto.Uint32(12), URL: proto.String("u")}}
	doc := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("plan.pdf"), Title: proto.String("plan"), Caption: proto.String("read me"), URL: proto.String("u")}}
	voice := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Seconds: proto.Uint32(7), Mimetype: proto.String("audio/ogg; codecs=opus"), URL: proto.String("u")}}

	out, kind, err := BuildForwardedMediaMessage(video)
	if err != nil || kind != "video" || out.GetVideoMessage().GetCaption() != "clip" || out.GetVideoMessage().GetSeconds() != 12 || !out.GetVideoMessage().GetContextInfo().GetIsForwarded() {
		t.Fatalf("video: kind=%q err=%v out=%+v", kind, err, out)
	}
	out, kind, err = BuildForwardedMediaMessage(doc)
	if err != nil || kind != "document" || out.GetDocumentMessage().GetFileName() != "plan.pdf" || out.GetDocumentMessage().GetCaption() != "read me" || !out.GetDocumentMessage().GetContextInfo().GetIsForwarded() {
		t.Fatalf("document: kind=%q err=%v out=%+v", kind, err, out)
	}
	out, kind, err = BuildForwardedMediaMessage(voice)
	if err != nil || kind != "audio" || !out.GetAudioMessage().GetPTT() || out.GetAudioMessage().GetSeconds() != 7 || !out.GetAudioMessage().GetContextInfo().GetIsForwarded() {
		t.Fatalf("voice note: kind=%q err=%v out=%+v", kind, err, out)
	}
}

func TestForwardedMediaDropsTheQuoteAndMentionsOfTheOriginal(t *testing.T) {
	msg := storedImage()
	msg.ImageMessage.ContextInfo = &waE2E.ContextInfo{
		StanzaID:      proto.String("orig"),
		Participant:   proto.String("1555@s.whatsapp.net"),
		QuotedMessage: &waE2E.Message{Conversation: proto.String("secret")},
		MentionedJID:  []string{"1555@s.whatsapp.net"},
	}
	out, _, err := BuildForwardedMediaMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := out.GetImageMessage().GetContextInfo()
	if ctx.GetQuotedMessage() != nil || ctx.GetStanzaID() != "" || ctx.GetParticipant() != "" || len(ctx.GetMentionedJID()) != 0 {
		t.Fatalf("a forward must not carry the quote or mentions: %+v", ctx)
	}
}

func TestForwardingScoreGoesUpForAnAlreadyForwardedMessage(t *testing.T) {
	msg := storedImage()
	msg.ImageMessage.ContextInfo = &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(4)}
	out, _, err := BuildForwardedMediaMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.GetImageMessage().GetContextInfo().GetForwardingScore(); got != 5 {
		t.Fatalf("score = %d, want 5", got)
	}
}

func TestForwardedMediaDoesNotChangeTheOriginal(t *testing.T) {
	msg := storedImage()
	out, _, _ := BuildForwardedMediaMessage(msg)
	out.GetImageMessage().Caption = proto.String("changed")
	out.GetImageMessage().MediaKey[0] = 99
	if msg.GetImageMessage().GetCaption() != "sunset" || msg.GetImageMessage().GetMediaKey()[0] != 1 || msg.GetImageMessage().GetContextInfo() != nil {
		t.Fatalf("the stored message was modified: %+v", msg.GetImageMessage())
	}
}

func TestForwardedMediaUnwrapsWrappers(t *testing.T) {
	wrapped := &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: &waE2E.Message{
		EphemeralMessage: &waE2E.FutureProofMessage{Message: storedImage()},
	}}}
	out, kind, err := BuildForwardedMediaMessage(wrapped)
	if err != nil || kind != "image" || out.GetImageMessage().GetCaption() != "sunset" {
		t.Fatalf("wrapped image: kind=%q err=%v out=%+v", kind, err, out)
	}
	if out.GetDeviceSentMessage() != nil || out.GetEphemeralMessage() != nil {
		t.Fatalf("the copy must be a plain message, not wrapped: %+v", out)
	}
}

func TestForwardedMediaRefusesViewOnce(t *testing.T) {
	flagged := storedImage()
	flagged.ImageMessage.ViewOnce = proto.Bool(true)
	flaggedAudio := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{ViewOnce: proto.Bool(true), URL: proto.String("u")}}
	flaggedVideo := &waE2E.Message{VideoMessage: &waE2E.VideoMessage{ViewOnce: proto.Bool(true), URL: proto.String("u")}}
	wrappedV1 := &waE2E.Message{ViewOnceMessage: &waE2E.FutureProofMessage{Message: storedImage()}}
	wrappedV2 := &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: storedImage()}}
	wrappedExt := &waE2E.Message{ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: storedImage()}}
	nested := &waE2E.Message{DeviceSentMessage: &waE2E.DeviceSentMessage{Message: wrappedV2}}
	for name, msg := range map[string]*waE2E.Message{
		"flag on image": flagged, "flag on audio": flaggedAudio, "flag on video": flaggedVideo,
		"wrapper v1": wrappedV1, "wrapper v2": wrappedV2, "wrapper extension": wrappedExt, "wrapper inside device sent": nested,
	} {
		out, _, err := BuildForwardedMediaMessage(msg)
		if !errors.Is(err, ErrViewOnceMedia) || out != nil {
			t.Errorf("%s: want ErrViewOnceMedia and no message, got err=%v out=%+v", name, err, out)
		}
	}
}

func TestForwardedMediaRefusesEverythingElse(t *testing.T) {
	for name, msg := range map[string]*waE2E.Message{
		"nil":      nil,
		"empty":    {},
		"text":     {Conversation: proto.String("hi")},
		"sticker":  {StickerMessage: &waE2E.StickerMessage{URL: proto.String("u")}},
		"reaction": {ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("x")}},
		"poll":     {PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("q")}},
	} {
		out, kind, err := BuildForwardedMediaMessage(msg)
		if !errors.Is(err, ErrNoForwardableMedia) || out != nil || kind != "" {
			t.Errorf("%s: want ErrNoForwardableMedia, got kind=%q err=%v out=%+v", name, kind, err, out)
		}
	}
}

func TestForwardMediaKind(t *testing.T) {
	cases := map[string]*waE2E.Message{
		"image":    storedImage(),
		"video":    {VideoMessage: &waE2E.VideoMessage{}},
		"document": {DocumentMessage: &waE2E.DocumentMessage{}},
		"audio":    {AudioMessage: &waE2E.AudioMessage{}},
		"":         {StickerMessage: &waE2E.StickerMessage{}},
	}
	for want, msg := range cases {
		if got := ForwardMediaKind(msg); got != want {
			t.Errorf("ForwardMediaKind = %q, want %q", got, want)
		}
	}
	if ForwardMediaKind(nil) != "" {
		t.Error("nil has no media kind")
	}
}

func TestMediaLinkStale(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	cases := []struct {
		name string
		ts   int64
		want bool
	}{
		{"just sent", now.Unix(), false},
		{"a day old", now.Add(-24 * time.Hour).Unix(), false},
		{"exactly at the limit", now.Add(-MediaLinkMaxAge).Unix(), false},
		{"one second past the limit", now.Add(-MediaLinkMaxAge - time.Second).Unix(), true},
		{"very old", now.Add(-365 * 24 * time.Hour).Unix(), true},
		{"in the future (clock skew)", now.Add(time.Hour).Unix(), false},
		{"unknown time", 0, true},
		{"negative time", -5, true},
	}
	for _, c := range cases {
		if got := MediaLinkStale(c.ts, now); got != c.want {
			t.Errorf("%s: MediaLinkStale = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReplaceMediaUploadKeepsEverythingButTheFile(t *testing.T) {
	up := whatsmeow.UploadResponse{URL: "https://new", DirectPath: "/v/new", MediaKey: []byte{8}, FileEncSHA256: []byte{9}, FileSHA256: []byte{10}, FileLength: 555}
	build := func(msg *waE2E.Message) *waE2E.Message {
		out, _, err := BuildForwardedMediaMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := time.Now().Unix()

	img := build(storedImage())
	if err := ReplaceMediaUpload(img, up); err != nil {
		t.Fatal(err)
	}
	i := img.GetImageMessage()
	if i.GetURL() != "https://new" || i.GetDirectPath() != "/v/new" || i.GetMediaKey()[0] != 8 || i.GetFileEncSHA256()[0] != 9 || i.GetFileSHA256()[0] != 10 || i.GetFileLength() != 555 {
		t.Fatalf("image upload fields not replaced: %+v", i)
	}
	if i.GetCaption() != "sunset" || i.GetMimetype() != "image/jpeg" || !i.GetContextInfo().GetIsForwarded() || i.GetMediaKeyTimestamp() < before {
		t.Fatalf("image details must stay, the forwarded mark must stay and the key time must be fresh: %+v", i)
	}

	doc := build(&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("plan.pdf"), Caption: proto.String("c"), URL: proto.String("old")}})
	if err := ReplaceMediaUpload(doc, up); err != nil {
		t.Fatal(err)
	}
	if d := doc.GetDocumentMessage(); d.GetURL() != "https://new" || d.GetFileName() != "plan.pdf" || d.GetCaption() != "c" || d.GetFileLength() != 555 {
		t.Fatalf("document: %+v", d)
	}

	voice := build(&waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), Seconds: proto.Uint32(3), URL: proto.String("old")}})
	if err := ReplaceMediaUpload(voice, up); err != nil {
		t.Fatal(err)
	}
	if a := voice.GetAudioMessage(); a.GetURL() != "https://new" || !a.GetPTT() || a.GetSeconds() != 3 || a.GetFileLength() != 555 {
		t.Fatalf("voice note: %+v", a)
	}

	video := build(&waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("v"), URL: proto.String("old")}})
	if err := ReplaceMediaUpload(video, up); err != nil {
		t.Fatal(err)
	}
	if v := video.GetVideoMessage(); v.GetURL() != "https://new" || v.GetCaption() != "v" || v.GetFileLength() != 555 {
		t.Fatalf("video: %+v", v)
	}
}

func TestReplaceMediaUploadRefusesANonMediaMessage(t *testing.T) {
	err := ReplaceMediaUpload(&waE2E.Message{Conversation: proto.String("hi")}, whatsmeow.UploadResponse{})
	if !errors.Is(err, ErrNoForwardableMedia) {
		t.Fatalf("err = %v, want ErrNoForwardableMedia", err)
	}
	if err := ReplaceMediaUpload(&waE2E.Message{}, whatsmeow.UploadResponse{}); err == nil {
		t.Fatal("an empty message has no media to replace")
	}
}
