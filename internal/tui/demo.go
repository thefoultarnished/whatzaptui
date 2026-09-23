package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func demoEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("WHATZAP_DEMO")))
	return v == "1" || v == "true" || v == "yes"
}

func initDemo() tea.Cmd {
	return func() tea.Msg {
		return initMsg{demo: true}
	}
}

func (x *m) loadDemoState() {
	now := time.Now()
	x.status = "ready"
	x.mode = "chat"
	x.sidebarFocused = true
	x.chats = []chat{
		{ID: "15551230001@s.whatsapp.net", Name: "Alex Rivera", ConversationTimestamp: now.Add(-3 * time.Minute).Unix(), UnreadCount: 2},
		{ID: "120363040000001234@g.us", Name: "Launch Crew", Subject: "Launch Crew", ConversationTimestamp: now.Add(-28 * time.Minute).Unix(), UnreadCount: 5},
		{ID: "15551230002@s.whatsapp.net", Name: "Mom", ConversationTimestamp: now.Add(-2 * time.Hour).Unix()},
		{ID: "15551230003@s.whatsapp.net", Name: "Nina Patel", ConversationTimestamp: now.Add(-26 * time.Hour).Unix(), UnreadCount: 1},
		{ID: "15551230004@s.whatsapp.net", Name: "Studio Ops", ConversationTimestamp: now.Add(-3 * 24 * time.Hour).Unix()},
	}
	x.contacts = map[string]contact{
		"15551230001@s.whatsapp.net": {ID: "15551230001@s.whatsapp.net", Notify: "Alex Rivera"},
		"15551230002@s.whatsapp.net": {ID: "15551230002@s.whatsapp.net", Notify: "Mom"},
		"15551230003@s.whatsapp.net": {ID: "15551230003@s.whatsapp.net", Notify: "Nina Patel"},
		"15551230004@s.whatsapp.net": {ID: "15551230004@s.whatsapp.net", Notify: "Studio Ops"},
		"15551239991@s.whatsapp.net": {ID: "15551239991@s.whatsapp.net", Notify: "Ivy"},
		"15551239992@s.whatsapp.net": {ID: "15551239992@s.whatsapp.net", Notify: "Marco"},
	}
	x.whitelist = map[string]string{
		"15551230001": "Alex Rivera",
		"15551230002": "Mom",
		"15551230003": "Product QA",
	}
	x.names = map[string]string{
		"15551230003": "Product QA",
	}
	x.msgs = map[string][]wireMsg{
		"15551230001@s.whatsapp.net": {
			demoTextMsg("a1", "15551230001@s.whatsapp.net", false, "", now.Add(-22*time.Minute), "Can you send me the Windows build after dinner?"),
			demoTextMsg("a2", "15551230001@s.whatsapp.net", true, "", now.Add(-20*time.Minute), "Yep. I cleaned up the status bar and the compose box."),
			demoQuotedMsg("a3", "15551230001@s.whatsapp.net", false, "", now.Add(-11*time.Minute), "Perfect. The Aurora theme looks sick in the terminal.", "Yep. I cleaned up the status bar and the compose box.", "me"),
			demoMediaMsg("a4", "15551230001@s.whatsapp.net", true, "", now.Add(-6*time.Minute), "image", "aurora-preview.png", "Screenshot-safe demo mode is next."),
			demoTextMsg("a5", "15551230001@s.whatsapp.net", false, "", now.Add(-3*time.Minute), "Ship it."),
		},
		"120363040000001234@g.us": {
			demoTextMsg("g1", "120363040000001234@g.us", false, "15551239991@s.whatsapp.net", now.Add(-90*time.Minute), "Pushed the release notes draft."),
			demoTextMsg("g2", "120363040000001234@g.us", false, "15551239992@s.whatsapp.net", now.Add(-65*time.Minute), "Need one more screenshot with unread badges."),
			demoQuotedMsg("g3", "120363040000001234@g.us", true, "", now.Add(-52*time.Minute), "I can fake that in demo mode.", "Need one more screenshot with unread badges.", "15551239992@s.whatsapp.net"),
			demoMediaMsg("g4", "120363040000001234@g.us", false, "15551239991@s.whatsapp.net", now.Add(-35*time.Minute), "document", "release-checklist.pdf", "Final pass before GitHub upload."),
			demoReactionMsg("g5", "120363040000001234@g.us", false, "15551239992@s.whatsapp.net", now.Add(-30*time.Minute), "g4", "fire"),
			demoTextMsg("g6", "120363040000001234@g.us", false, "15551239992@s.whatsapp.net", now.Add(-28*time.Minute), "Also add the missed call banner to the README."),
		},
		"15551230002@s.whatsapp.net": {
			demoTextMsg("m1", "15551230002@s.whatsapp.net", false, "", now.Add(-5*time.Hour), "Did you eat?"),
			demoTextMsg("m2", "15551230002@s.whatsapp.net", true, "", now.Add(-4*time.Hour+12*time.Minute), "Yes. Also I turned WhatsApp into a terminal app."),
			demoTextMsg("m3", "15551230002@s.whatsapp.net", false, "", now.Add(-2*time.Hour), "That sounds extremely on-brand."),
		},
		"15551230003@s.whatsapp.net": {
			demoTextMsg("n1", "15551230003@s.whatsapp.net", false, "", now.Add(-30*time.Hour), "Need a safe screenshot build."),
			demoTextMsg("n2", "15551230003@s.whatsapp.net", true, "", now.Add(-29*time.Hour), "Done. Fake chats only, no QR, no backend."),
		},
		"15551230004@s.whatsapp.net": {
			demoMediaMsg("s1", "15551230004@s.whatsapp.net", false, "", now.Add(-76*time.Hour), "video", "terminal-tour.mp4", "Draft teaser clip"),
			demoTextMsg("s2", "15551230004@s.whatsapp.net", true, "", now.Add(-74*time.Hour), "Need a cleaner intro frame and fewer real identifiers."),
		},
	}
	x.active = x.chats[0].ID
	x.sel = 0
	x.scroll = 0
	x.rebuildContactIndex()
	x.markIdentityChanged()
	x.invalidate()
}

func demoTextMsg(id, chatID string, fromMe bool, participant string, ts time.Time, text string) wireMsg {
	msg := wireMsg{
		Message:          map[string]any{"conversation": text},
		MessageTimestamp: ts.Unix(),
		ReceiptStatus:    "read",
	}
	msg.Key.ID = id
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = fromMe
	msg.Key.Participant = participant
	return msg
}

func demoQuotedMsg(id, chatID string, fromMe bool, participant string, ts time.Time, text, quotedText, quotedParticipant string) wireMsg {
	msg := wireMsg{
		Message: map[string]any{
			"extendedTextMessage": map[string]any{
				"text":              text,
				"quotedText":        quotedText,
				"quotedParticipant": quotedParticipant,
			},
		},
		MessageTimestamp: ts.Unix(),
		ReceiptStatus:    "read",
	}
	msg.Key.ID = id
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = fromMe
	msg.Key.Participant = participant
	return msg
}

func demoMediaMsg(id, chatID string, fromMe bool, participant string, ts time.Time, kind, fileName, caption string) wireMsg {
	payload := map[string]any{
		"fileName": fileName,
		"caption":  caption,
	}
	message := map[string]any{}
	switch kind {
	case "image":
		message["imageMessage"] = payload
	case "video":
		message["videoMessage"] = payload
	default:
		message["documentMessage"] = payload
	}
	msg := wireMsg{
		Message:          message,
		MessageTimestamp: ts.Unix(),
		MediaProto:       "demo",
		ReceiptStatus:    "delivered",
	}
	msg.Key.ID = id
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = fromMe
	msg.Key.Participant = participant
	return msg
}

func demoReactionMsg(id, chatID string, fromMe bool, participant string, ts time.Time, targetID, emoji string) wireMsg {
	msg := wireMsg{
		Message: map[string]any{
			"reactionMessage": map[string]any{
				"targetMsgID": targetID,
				"emoji":       emoji,
			},
		},
		MessageTimestamp: ts.Unix(),
	}
	msg.Key.ID = id
	msg.Key.RemoteJID = chatID
	msg.Key.FromMe = fromMe
	msg.Key.Participant = participant
	return msg
}

func demoSend(chatID, text string, replyTo *wireMsg) tea.Cmd {
	return func() tea.Msg {
		id := fmt.Sprintf("demo-%d", time.Now().UnixNano())
		msg := wireMsg{
			MessageTimestamp: time.Now().Unix(),
			ReceiptStatus:    "read",
		}
		if replyTo != nil {
			participant := replyTo.Key.RemoteJID
			if replyTo.Key.Participant != "" {
				participant = replyTo.Key.Participant
			}
			msg.Message = map[string]any{
				"extendedTextMessage": map[string]any{
					"text":              text,
					"quotedText":        renderMessageBody(replyTo.Message),
					"quotedParticipant": participant,
				},
			}
		} else {
			msg.Message = map[string]any{"conversation": text}
		}
		msg.Key.ID = id
		msg.Key.RemoteJID = chatID
		msg.Key.FromMe = true
		return sentMsg{chatID: chatID, msg: msg}
	}
}
