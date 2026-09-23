package whatsapp

import (
	"strings"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// ReceiptStatusFromType maps a whatsmeow ReceiptType to our canonical receipt status string.
func ReceiptStatusFromType(t types.ReceiptType) string {
	switch t {
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	case types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return "played"
	default:
		return ""
	}
}

// CallMeta models a normalized WhatsApp audio/video call event.
type CallMeta struct {
	Status   string `json:"status"`
	CallerID string `json:"callerId,omitempty"`
	GroupID  string `json:"groupId,omitempty"`
	CallID   string `json:"callId,omitempty"`
	Media    string `json:"media,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// BuildCallMeta normalizes call metadata from types.BasicCallMeta.
func BuildCallMeta(status string, meta types.BasicCallMeta, media, reason string, canonicalize func(string) string) CallMeta {
	if canonicalize == nil {
		canonicalize = func(s string) string { return strings.TrimSpace(s) }
	}
	callerID := canonicalize(meta.CallCreator.String())
	if callerID == "" {
		callerID = canonicalize(meta.CallCreatorAlt.String())
	}
	if callerID == "" {
		callerID = canonicalize(meta.From.String())
	}
	groupID := canonicalize(meta.GroupJID.String())
	return CallMeta{
		Status:   status,
		CallerID: callerID,
		GroupID:  groupID,
		CallID:   meta.CallID,
		Media:    strings.TrimSpace(media),
		Reason:   strings.TrimSpace(reason),
	}
}

// IsMessageEdit reports if the protocol message is a message edit.
func IsMessageEdit(pm *waE2E.ProtocolMessage) bool {
	return pm != nil && pm.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT && pm.GetEditedMessage() != nil
}

// IsMessageRevoke reports if the protocol message is a message revoke/delete.
func IsMessageRevoke(pm *waE2E.ProtocolMessage) bool {
	return pm != nil && pm.GetType() == waE2E.ProtocolMessage_REVOKE
}
