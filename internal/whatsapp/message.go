package whatsapp

import (
	"encoding/base64"
	"sort"
	"strings"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// BuildTextMessage constructs a standard or quoted waE2E.Message.
func BuildTextMessage(text string, replyToMsgID, replyToParticipant, replyToText string) *waE2E.Message {
	if replyToMsgID != "" {
		quotedMsg := &waE2E.Message{Conversation: proto.String(replyToText)}
		return &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String(text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID:      proto.String(replyToMsgID),
					Participant:   proto.String(replyToParticipant),
					QuotedMessage: quotedMsg,
				},
			},
		}
	}
	return &waE2E.Message{Conversation: proto.String(text)}
}

// EffectiveMessage unwraps wrapper layers (device sent, ephemeral, view once, edit, etc.)
// to find the innermost semantic message.
func EffectiveMessage(msg *waE2E.Message) *waE2E.Message {
	for msg != nil {
		switch {
		case msg.GetDeviceSentMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		case msg.GetCommentMessage() != nil:
			msg = msg.GetCommentMessage().GetMessage()
		case msg.GetEphemeralMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		case msg.GetViewOnceMessageV2Extension() != nil:
			msg = msg.GetViewOnceMessageV2Extension().GetMessage()
		case msg.GetDocumentWithCaptionMessage() != nil:
			msg = msg.GetDocumentWithCaptionMessage().GetMessage()
		case msg.GetEditedMessage() != nil:
			msg = msg.GetEditedMessage().GetMessage()
		default:
			return msg
		}
	}
	return nil
}

// ExtractPollCreationMessage unwraps various versions of poll creation messages.
func ExtractPollCreationMessage(msg *waE2E.Message) *waE2E.PollCreationMessage {
	if msg == nil {
		return nil
	}
	if pc := msg.GetPollCreationMessage(); pc != nil {
		return pc
	}
	if pc := msg.GetPollCreationMessageV2(); pc != nil {
		return pc
	}
	if pc := msg.GetPollCreationMessageV3(); pc != nil {
		return pc
	}
	if pc := msg.GetPollCreationMessageV5(); pc != nil {
		return pc
	}
	if pc := msg.GetPollCreationMessageV6(); pc != nil {
		return pc
	}
	if v4 := msg.GetPollCreationMessageV4(); v4 != nil {
		if inner := v4.GetMessage(); inner != nil {
			return ExtractPollCreationMessage(inner)
		}
	}
	return nil
}

// VisibleProtocolType reports whether a protocol message carries user-visible meaning.
func VisibleProtocolType(t waE2E.ProtocolMessage_Type) bool {
	switch t {
	case waE2E.ProtocolMessage_REVOKE,
		waE2E.ProtocolMessage_MESSAGE_EDIT,
		waE2E.ProtocolMessage_EPHEMERAL_SETTING:
		return true
	default:
		return false
	}
}

// IsInvisibleProtocolMessage reports whether msg is a bare protocol control message with no user-visible meaning.
// This includes SenderKeyDistributionMessage, which is a key-exchange handshake, not user content.
func IsInvisibleProtocolMessage(msg *waE2E.Message) bool {
	if msg == nil {
		return false
	}
	if msg.GetSenderKeyDistributionMessage() != nil {
		return true
	}
	pm := msg.GetProtocolMessage()
	if pm == nil {
		return false
	}
	return !VisibleProtocolType(pm.GetType())
}

// ProtocolMessagePayload extracts the structured map payload for a protocol message.
func ProtocolMessagePayload(raw, effective *waE2E.Message) map[string]any {
	var protocol *waE2E.ProtocolMessage
	switch {
	case effective != nil && effective.GetProtocolMessage() != nil:
		protocol = effective.GetProtocolMessage()
	case raw != nil && raw.GetProtocolMessage() != nil:
		protocol = raw.GetProtocolMessage()
	default:
		return nil
	}
	out := map[string]any{
		"type": protocol.GetType().String(),
	}
	if key := protocol.GetKey(); key != nil {
		if id := key.GetID(); id != "" {
			out["targetMsgID"] = id
		}
	}
	if timer := protocol.GetEphemeralExpiration(); timer > 0 {
		out["ephemeralExpiration"] = timer
	}
	if edited := protocol.GetEditedMessage(); edited != nil {
		if text := QuotedText(edited); text != "" {
			out["editedText"] = text
		}
	}
	return out
}

// MessageFieldNames returns sorted proto field names present in the message.
func MessageFieldNames(msg *waE2E.Message) []string {
	if msg == nil {
		return nil
	}
	fields := make([]string, 0, 4)
	msg.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		if name != "messageContextInfo" {
			fields = append(fields, name)
		}
		return true
	})
	sort.Strings(fields)
	return fields
}

// QuotedText extracts a summary string for a quoted message.
func QuotedText(m *waE2E.Message) string {
	m = EffectiveMessage(m)
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if m.GetImageMessage() != nil {
		return "[image]"
	}
	if m.GetVideoMessage() != nil {
		return "[video]"
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		if doc.GetFileName() != "" {
			return "[file: " + doc.GetFileName() + "]"
		}
		return "[document]"
	}
	if aud := m.GetAudioMessage(); aud != nil {
		if aud.GetPTT() {
			return "[voice]"
		}
		return "[audio]"
	}
	if m.GetStickerMessage() != nil {
		return "[sticker]"
	}
	return "[message]"
}

// IsMediaMessage reports if the message contains media (image, video, document, audio, sticker).
func IsMediaMessage(msg *waE2E.Message) bool {
	if msg == nil {
		return false
	}
	return msg.GetImageMessage() != nil || msg.GetVideoMessage() != nil ||
		msg.GetDocumentMessage() != nil || msg.GetAudioMessage() != nil ||
		msg.GetStickerMessage() != nil
}

// MarshalMediaProto serializes the media message into a base64 encoded protobuf string.
func MarshalMediaProto(msg *waE2E.Message) (string, error) {
	if msg == nil {
		return "", nil
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ExtractSearchableText pulls user-visible text from a WireMessage map payload for full-text indexing.
func ExtractSearchableText(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(s)
	}
	if s, ok := msg["conversation"].(string); ok {
		add(s)
	}
	if ext, ok := msg["extendedTextMessage"].(map[string]any); ok {
		if s, ok := ext["text"].(string); ok {
			add(s)
		}
		if s, ok := ext["quotedText"].(string); ok {
			add(s)
		}
	}
	for _, kind := range []string{"imageMessage", "videoMessage", "documentMessage"} {
		media, ok := msg[kind].(map[string]any)
		if !ok {
			continue
		}
		if s, ok := media["caption"].(string); ok {
			add(s)
		}
		if kind == "documentMessage" {
			if s, ok := media["fileName"].(string); ok {
				add(s)
			}
		}
	}
	return b.String()
}
