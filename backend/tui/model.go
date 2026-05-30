package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

type mainCacheKey struct {
	active       string
	themeName    string
	msgCount     int
	lastMsgID    string
	scroll       int
	w, h         int
	atInput      string
	replyToID    string
	selectedMsg  string
	contactCount int
	identityVer  int
	spinnerFrame int
	inputH       int
	pulseOn      bool
}
type renderCache struct {
	key    mainCacheKey
	result string
}

type sidebarCache struct {
	contacts      []chat
	contactsValid bool
}

type Theme struct {
	Brand, Accent, Purple, Amber, Red, Muted, Text string
	ImageTag, VideoTag, AudioTag, FileTag          string
	StickerTag                                     string
	ContactTag, PollTag, LocationTag, AnomalyTag   string
	SentText, ReceivedText, SentName, ReceivedName string
	QuotedSentText, QuotedReceivedText             string
	BadgeInk, ButtonInk, TagInk, Cursor            string
	QRLight, QRDark                                string
	ShortcutActive                                 string
	SidebarActiveBg, SidebarActiveUnreadBg         string
	ReplyPreviewBg, MessageSelectedBg              string
	MediaTokenBg, MediaTokenPulseBg                string
	Background                                     string
}

var currentTheme Theme

type Config struct {
	ThemeName            string `json:"theme_name"`
	MouseEnabled         bool   `json:"mouse_enabled"`
	SoundEnabled         bool   `json:"sound_enabled"`
	SoundProfile         int    `json:"sound_profile"`
	PointerIcon          string `json:"pointer_icon,omitempty"`
	SendTypingIndicator  bool   `json:"send_typing_indicator"`
	FlashTaskbar         bool   `json:"flash_taskbar"`
	NotificationsEnabled bool   `json:"notifications_enabled"`
}

var currentConfig Config

type chat struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Subject               string `json:"subject"`
	ConversationTimestamp int64  `json:"conversationTimestamp"`
	UnreadCount           int    `json:"unreadCount"`
}
type contact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Notify string `json:"notify"`
}
type wireMsg struct {
	Key struct {
		ID          string `json:"id"`
		RemoteJID   string `json:"remoteJid"`
		FromMe      bool   `json:"fromMe"`
		Participant string `json:"participant,omitempty"`
	} `json:"key"`
	Message          map[string]any `json:"message"`
	MessageTimestamp int64          `json:"messageTimestamp"`
	MediaProto       string         `json:"mediaProto,omitempty"`
	ReceiptStatus    string         `json:"receiptStatus,omitempty"`
}
type env struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type m struct {
	baseURL, wsURL, backendDir, apiToken     string
	client                                   *http.Client
	demoMode                                 bool
	w, h                                     int
	status, err                              string
	qrRaw                                    string
	topBarMsg                                string
	topBarShown                              int
	topBarVer                                int
	cursorOn                                 bool
	pulseOn                                  bool
	spinnerFrame                             int
	msgActivityUntil                         time.Time
	msgActivityType                          string // "sent" or "received"
	flashUntil                               map[string]time.Time
	sidebarTab                               string
	chats                                    []chat
	contacts                                 map[string]contact
	contactsByNumber                         map[string]contact
	msgs                                     map[string][]wireMsg
	loadingOlder                             map[string]bool // chatID → fetch in flight
	noMoreOlder                              map[string]bool // chatID → backend exhausted
	msgSearchInput                           string
	msgSearchResults                         []searchHit
	msgSearchSel                             int
	msgSearchLoading                         bool
	msgSearchErr                             string
	active, mode, search, searchInput, input string
	syncingContacts, syncingGroups           bool
	shineFrame                               int
	sel, scroll, sideScroll                  int
	sidebarFocused                           bool
	ws                    *websocket.Conn
	wsCh                  <-chan env
	wsReconnectDelay      time.Duration // current backoff; 0 = not disconnected
	wsDisconnected        bool
	backend                                  *exec.Cmd
	startedBackend                           bool
	whitelist                                map[string]string // phone -> name, allowed=1 only (send gating)
	names                                    map[string]string // phone -> custom display name (all contacts)
	replyTo                                  *wireMsg          // message being replied to, nil if none
	selectedMsgID                            string            // message ID highlighted via mouse click or reply pick
	replyPickMode                            bool              // Alt+R reply pick mode active
	replyPickIndex                           int               // index into visible messages during reply pick
	lastClickY                               int
	lastClickTime                            time.Time
	mouseEnabled                             bool
	inputAllSelected                         bool // true when Ctrl+A was pressed - whole input is "selected"
	leftInput                                string
	leftInputFocused                         bool
	emojiPickerOpen                          bool
	themePicker                              picker
	pointerPicker                            picker
	helpPicker                               picker
	settingsPicker                           picker
	fileBrowserOpen                          bool
	fileBrowserDir                           string
	fileBrowserEntries                       []fileBrowserEntry
	fileBrowserIndex                         int
	fileBrowserScroll                        int
	pendingAttachmentPath                    string
	pendingAttachmentKind                    string
	pendingAttachmentName                    string
	emojiQuery                               string
	emojiSel                                 int
	emojiScroll                              int
	emojiResultsCache                        []emojiItem
	emojiResultsDirty                        bool
	reactPickMode                            bool                 // emoji picker opened for reaction (not input insert)
	reactPickMsgID                           string               // message ID to react to
	reactPickChatID                          string               // chat ID for the reaction
	reactPickSender                          string               // sender JID for the reaction target
	typingChats                              map[string]time.Time // chatID -> when typing started (auto-expires)
	lastComposingChat                        string               // chatID we last sent "composing" to
	restartRequested                         bool
	soundEnabled                             bool
	soundProfile                             int
	lastNotifyAt                             map[string]time.Time
	lastNotifyGlobal                         time.Time
	lastTypeTime                             time.Time
	lastPasteLikeAt                          time.Time
	pendingSendSeq                           int
	pendingSendArmed                         bool
	identityVersion                          int
	sidebarMarqueeOffset                     int
	sidebarMarqueePause                      int
	sidebarMarqueeDir                        int
	sidebarMarqueeKey                        string
	sidebarMarqueeTick                       int
	sidebarHighlightKey                      string
	sidebarHighlightInset                    int
	windowTitle                              string
	sidebarCache                             *sidebarCache
	mainCache                                *renderCache
	inputBuf                                 string
	inputFlushScheduled                      bool
}

type initMsg struct {
	started bool
	demo    bool
	cmd     *exec.Cmd
	err     error
}
type wsOpenMsg struct {
	conn *websocket.Conn
	ch   <-chan env
	err  error
}
type wsEvtMsg struct {
	evt env
	ok  bool
}
type dataErr struct{ err error }
type chatsMsg struct {
	chats []chat
	err   error
}
type contactsMsg struct {
	contacts []contact
	err      error
}
type msgsMsg struct {
	chatID  string
	msgs    []wireMsg
	hasMore bool
	err     error
}

// olderMsgsMsg is delivered when a lazy-load page (older messages) returns.
// hasMore comes from the backend's pagination metadata (true when more older
// pages exist). `requested` is kept for tests written before hasMore landed.
type olderMsgsMsg struct {
	chatID    string
	msgs      []wireMsg
	hasMore   bool
	requested int
	err       error
}

// aroundMsgsMsg is returned after a search-jump fetch that centres on a
// specific message. anchorIndex is the position of the target in msgs.
type aroundMsgsMsg struct {
	chatID      string
	msgs        []wireMsg
	anchorIndex int
	err         error
}

type searchHit struct {
	ChatID    string `json:"chatId"`
	MessageID string `json:"messageId"`
	FromMe    bool   `json:"fromMe"`
	Timestamp int64  `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

type searchResultsMsg struct {
	query   string
	results []searchHit
	err     error
}
type sentMsg struct {
	chatID    string
	pendingID string
	msg       wireMsg
	err       error
}
type logoutMsg struct {
	msg string
	err error
}
type reconnectMsg struct{}
type whitelistLoadMsg struct {
	whitelist map[string]string // allowed=1 only
	names     map[string]string // all custom names
	err       error
}
type whitelistSetMsg struct{ err error }
type topBarClearMsg struct{ ver int }
type topBarTypeMsg struct{ ver int }
type topBarSetMsg struct{ msg string }
type cursorBlinkMsg struct{}
type spinnerTickMsg struct{}
type flushInputMsg struct{}
type composerSendMsg struct{ seq int }
type mediaDownloadMsg struct {
	path string
	err  error
}
type fileOpenMsg struct {
	path string
	err  error
}
type receiptMsg struct {
	ChatID        string   `json:"chatId"`
	MessageIDs    []string `json:"messageIds"`
	ReceiptStatus string   `json:"receiptStatus"`
}
type callMsg struct {
	Status   string `json:"status"`
	CallerID string `json:"callerId"`
	GroupID  string `json:"groupId"`
	CallID   string `json:"callId"`
	Media    string `json:"media"`
	Reason   string `json:"reason"`
}
type syncContactsDoneMsg struct {
	msg string
}
type syncGroupsDoneMsg struct {
	msg string
}

func (x m) Init() tea.Cmd {
	bgCmd := setTerminalBgCmd(currentTheme.Background)
	if x.demoMode {
		return tea.Batch(initDemo(), nextCursorBlink(), nextSpinnerTick(), setTerminalTitleCmd("WhatZap"), bgCmd)
	}
	return tea.Batch(ensureBackend(x.client, x.baseURL, x.backendDir, x.apiToken), nextCursorBlink(), nextSpinnerTick(), setTerminalTitleCmd("WhatZap"), bgCmd)
}
