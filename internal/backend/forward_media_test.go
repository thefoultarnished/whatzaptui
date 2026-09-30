package backend

import (
	"net/http"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"whatzap/internal/whatsapp"
)

const (
	fwdFrom = "15550000001@s.whatsapp.net"
	fwdBob  = "15550000002@s.whatsapp.net"
	fwdCat  = "15550000003@s.whatsapp.net"
)

// storeMedia saves a media message the way the backend does: the wire payload
// for the chat plus the base64 proto used to download or forward it.
func storeMedia(t *testing.T, app *App, id string, ts int64, wire map[string]any, msg *waE2E.Message) {
	t.Helper()
	mediaProto, err := whatsapp.MarshalMediaProto(msg)
	if err != nil {
		t.Fatal(err)
	}
	app.upsertMessage(fwdFrom, WireMessage{
		Key:              WireKey{ID: id, RemoteJID: fwdFrom},
		Message:          wire,
		MessageTimestamp: ts,
		MediaProto:       mediaProto,
	})
}

func imageProto(caption string) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:    proto.String(caption),
		Mimetype:   proto.String("image/jpeg"),
		URL:        proto.String("https://mmg.whatsapp.net/x"),
		DirectPath: proto.String("/v/x"),
		MediaKey:   []byte{1, 2, 3},
	}}
}

func TestResolveForwardMediaReusesTheStoredLink(t *testing.T) {
	app := forwardTestApp(t)
	storeMedia(t, app, "pic", time.Now().Unix(), map[string]any{"imageMessage": map[string]any{"caption": "sunset"}}, imageProto("sunset"))

	plan, status, msg := app.resolveForward(fwdFrom, "pic", fwdBob)
	if status != 0 || msg != "" {
		t.Fatalf("status=%d msg=%q, want success", status, msg)
	}
	img := plan.media.GetImageMessage()
	if plan.kind != "image" || img == nil || img.GetURL() != "https://mmg.whatsapp.net/x" || img.GetCaption() != "sunset" {
		t.Fatalf("expected the same link and caption, got kind=%q %+v", plan.kind, plan.media)
	}
	if !img.GetContextInfo().GetIsForwarded() {
		t.Fatalf("the copy must be marked forwarded: %+v", img.GetContextInfo())
	}
	if plan.stale {
		t.Fatal("a message sent just now must reuse its link, not re-upload")
	}
	if plan.text != "" || plan.to != fwdBob || plan.toJID.User != "15550000002" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestResolveForwardMediaGoesStaleWhenTheMessageIsOld(t *testing.T) {
	app := forwardTestApp(t)
	old := time.Now().Add(-whatsapp.MediaLinkMaxAge - time.Hour).Unix()
	storeMedia(t, app, "oldpic", old, map[string]any{"imageMessage": map[string]any{}}, imageProto(""))
	storeMedia(t, app, "edgepic", time.Now().Add(-whatsapp.MediaLinkMaxAge+time.Hour).Unix(), map[string]any{"imageMessage": map[string]any{}}, imageProto(""))
	storeMedia(t, app, "nots", 0, map[string]any{"imageMessage": map[string]any{}}, imageProto(""))

	for id, want := range map[string]bool{"oldpic": true, "edgepic": false, "nots": true} {
		plan, status, msg := app.resolveForward(fwdFrom, id, fwdBob)
		if status != 0 {
			t.Fatalf("%s: status=%d msg=%q", id, status, msg)
		}
		if plan.stale != want {
			t.Errorf("%s: stale = %v, want %v", id, plan.stale, want)
		}
		if plan.source == nil {
			t.Errorf("%s: the stored message is needed to download the file again", id)
		}
	}
}

func TestResolveForwardEveryMediaKind(t *testing.T) {
	app := forwardTestApp(t)
	now := time.Now().Unix()
	storeMedia(t, app, "vid", now, map[string]any{"videoMessage": map[string]any{}}, &waE2E.Message{VideoMessage: &waE2E.VideoMessage{Caption: proto.String("clip"), URL: proto.String("u")}})
	storeMedia(t, app, "doc", now, map[string]any{"documentMessage": map[string]any{}}, &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("plan.pdf"), URL: proto.String("u")}})
	storeMedia(t, app, "aud", now, map[string]any{"audioMessage": map[string]any{}}, &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: proto.Bool(true), URL: proto.String("u")}})

	for id, kind := range map[string]string{"vid": "video", "doc": "document", "aud": "audio"} {
		plan, status, msg := app.resolveForward(fwdFrom, id, fwdBob)
		if status != 0 || plan.kind != kind || plan.media == nil {
			t.Errorf("%s: status=%d msg=%q kind=%q", id, status, msg, plan.kind)
		}
	}
	plan, _, _ := app.resolveForward(fwdFrom, "doc", fwdBob)
	if plan.media.GetDocumentMessage().GetFileName() != "plan.pdf" {
		t.Errorf("the file name must be kept: %+v", plan.media)
	}
	plan, _, _ = app.resolveForward(fwdFrom, "aud", fwdBob)
	if !plan.media.GetAudioMessage().GetPTT() {
		t.Errorf("a voice note must stay a voice note: %+v", plan.media)
	}
}

func TestResolveForwardMediaRefusals(t *testing.T) {
	app := forwardTestApp(t)
	now := time.Now().Unix()
	storeMedia(t, app, "pic", now, map[string]any{"imageMessage": map[string]any{}}, imageProto("x"))
	viewOnce := imageProto("secret")
	viewOnce.ImageMessage.ViewOnce = proto.Bool(true)
	storeMedia(t, app, "once", now, map[string]any{"imageMessage": map[string]any{}}, viewOnce)
	storeMedia(t, app, "sticker", now, map[string]any{"stickerMessage": map[string]any{}}, &waE2E.Message{StickerMessage: &waE2E.StickerMessage{URL: proto.String("u")}})
	// The chat row says image but the stored proto is not media at all.
	storeMedia(t, app, "mismatch", now, map[string]any{"imageMessage": map[string]any{}}, &waE2E.Message{Conversation: proto.String("hi")})
	// Corrupt stored proto.
	app.upsertMessage(fwdFrom, WireMessage{
		Key:              WireKey{ID: "junk", RemoteJID: fwdFrom},
		Message:          map[string]any{"imageMessage": map[string]any{}},
		MessageTimestamp: now,
		MediaProto:       "!!!not base64!!!",
	})

	cases := []struct {
		name, id, to string
		wantStatus   int
	}{
		{"target not whitelisted", "pic", fwdCat, http.StatusForbidden},
		{"view once media", "once", fwdBob, http.StatusBadRequest},
		{"sticker", "sticker", fwdBob, http.StatusBadRequest},
		{"stored proto is not media", "mismatch", fwdBob, http.StatusBadRequest},
		{"corrupt stored proto", "junk", fwdBob, http.StatusInternalServerError},
		{"unknown message", "nope", fwdBob, http.StatusNotFound},
	}
	for _, c := range cases {
		plan, status, msg := app.resolveForward(fwdFrom, c.id, c.to)
		if status != c.wantStatus {
			t.Errorf("%s: status = %d (%q), want %d", c.name, status, msg, c.wantStatus)
		}
		if plan.media != nil || plan.text != "" || plan.source != nil {
			t.Errorf("%s: nothing must be forwarded on failure, got %+v", c.name, plan)
		}
	}
	if _, _, msg := app.resolveForward(fwdFrom, "once", fwdBob); msg != whatsapp.ErrViewOnceMedia.Error() {
		t.Errorf("view once refusal should say why, got %q", msg)
	}
	if _, _, msg := app.resolveForward(fwdFrom, "sticker", fwdBob); msg != whatsapp.ErrNoForwardableMedia.Error() {
		t.Errorf("sticker refusal should list what can be forwarded, got %q", msg)
	}
}

func TestResolveForwardTextStillForwardsAsText(t *testing.T) {
	app := forwardTestApp(t)
	plan, status, _ := app.resolveForward(fwdFrom, "text1", fwdBob)
	if status != 0 || plan.media != nil || plan.text == "" {
		t.Fatalf("a text message forwards as text: %+v", plan)
	}
}

func TestHasForwardableMedia(t *testing.T) {
	for _, k := range []string{"imageMessage", "videoMessage", "documentMessage", "audioMessage"} {
		if !hasForwardableMedia(map[string]any{k: map[string]any{}}) {
			t.Errorf("%s should be forwardable", k)
		}
	}
	for _, msg := range []map[string]any{
		{}, nil, {"stickerMessage": map[string]any{}}, {"conversation": "hi"},
		{"imageMessage": "not a map"}, {"reactionMessage": map[string]any{}},
	} {
		if hasForwardableMedia(msg) {
			t.Errorf("%v should not be forwardable media", msg)
		}
	}
}

func TestWirePayloadFlagsForwardedMedia(t *testing.T) {
	app := newTestApp(t)
	forwarded := &waE2E.ContextInfo{IsForwarded: proto.Bool(true)}

	img := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("a"), ContextInfo: forwarded}}
	wire, mediaProto := app.wireMessagePayload(img, img, fwdFrom, false)
	entry, _ := wire["imageMessage"].(map[string]any)
	if entry == nil || entry["forwarded"] != true || entry["caption"] != "a" {
		t.Fatalf("image wire = %v, want caption and forwarded=true", wire)
	}
	if mediaProto == "" {
		t.Fatal("a forwarded image keeps its media proto so it can be downloaded")
	}

	for name, msg := range map[string]*waE2E.Message{
		"videoMessage":    {VideoMessage: &waE2E.VideoMessage{ContextInfo: forwarded}},
		"documentMessage": {DocumentMessage: &waE2E.DocumentMessage{ContextInfo: forwarded}},
		"audioMessage":    {AudioMessage: &waE2E.AudioMessage{ContextInfo: forwarded}},
	} {
		w, _ := app.wireMessagePayload(msg, msg, fwdFrom, false)
		if e, _ := w[name].(map[string]any); e == nil || e["forwarded"] != true {
			t.Errorf("%s not flagged: %v", name, w)
		}
	}

	plain := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("b"), ContextInfo: &waE2E.ContextInfo{}}}
	w, _ := app.wireMessagePayload(plain, plain, fwdFrom, false)
	if e, _ := w["imageMessage"].(map[string]any); e == nil || e["forwarded"] != nil {
		t.Fatalf("a normal image must not be flagged forwarded: %v", w)
	}
	none := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("c")}}
	w, _ = app.wireMessagePayload(none, none, fwdFrom, false)
	if e, _ := w["imageMessage"].(map[string]any); e == nil || e["forwarded"] != nil {
		t.Fatalf("an image with no context must not be flagged forwarded: %v", w)
	}
}
