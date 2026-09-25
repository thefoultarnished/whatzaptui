package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

// renderCache stores the last-rendered main pane. It is validated by a
// revision counter: every Update bumps m.revision, so any state mutation
// invalidates the cache by construction. No manual field tracking.
type renderCache struct {
	mu       sync.RWMutex
	revision uint64
	w, h     int
	result   string
}

func (c *renderCache) get(revision uint64, w, h int) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.result != "" && c.revision == revision && c.w == w && c.h == h {
		return c.result, true
	}
	return "", false
}

func (c *renderCache) set(revision uint64, w, h int, result string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revision = revision
	c.w = w
	c.h = h
	c.result = result
}

type sidebarCache struct {
	contacts      []chat
	contactsValid bool
}

type Theme struct {
	Brand, Accent, Purple, Amber, Red, Muted, Text     string
	ImageTag, VideoTag, AudioTag, FileTag              string
	StickerTag                                         string
	ContactTag, PollTag, LocationTag, AnomalyTag       string
	SentText, ReceivedText, SentName, ReceivedName     string
	QuotedSentText, QuotedReceivedText                 string
	BadgeInk, ButtonInk, TagInk, Cursor                string
	QRLight, QRDark                                    string
	ShortcutActive                                     string
	SidebarActiveBg, SidebarActiveUnreadBg             string
	SidebarWhitelistActiveBg, SidebarBlacklistActiveBg string
	ReplyPreviewBg, MessageSelectedBg                  string
	MediaTokenBg, MediaTokenPulseBg                    string
	Background                                         string
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
	TypingAnimationStyle string `json:"typing_animation_style,omitempty"`
	MediaIconStyle       string `json:"media_icon_style,omitempty"`
	TimestampNewLine     bool   `json:"timestamp_new_line,omitempty"`
	MediaViewStyle       string `json:"media_view_style,omitempty"`
	Borderless           bool   `json:"borderless,omitempty"`
	UserlistIconStyle    string `json:"userlist_icon_style,omitempty"`
	HidePhoneNumber      bool   `json:"hide_phone_number,omitempty"`
	SplashStageSpeed     string `json:"splash_stage_speed,omitempty"`
	// ShowAllContacts disables the stored-only People filter. Default
	// false: the People tab shows address-book contacts plus renamed
	// and whitelisted chats, hiding push-name-only strangers.
	ShowAllContacts bool `json:"show_all_contacts,omitempty"`
}

var currentConfig Config

type groupPreview struct {
	members []string
	total   int
}
type groupPreviewMsg struct {
	jid     string
	preview groupPreview
	err     error
}

type chat struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Subject               string `json:"subject"`
	ConversationTimestamp int64  `json:"conversationTimestamp"`
	UnreadCount           int    `json:"unreadCount"`
}

// UnmarshalJSON sanitizes wire-derived display text (Name, Subject) to strip
// terminal control characters before they ever reach the TUI renderer.
func (c *chat) UnmarshalJSON(data []byte) error {
	type alias chat
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	a.Name = sanitizeIncomingText(a.Name)
	a.Subject = sanitizeIncomingText(a.Subject)
	*c = chat(a)
	return nil
}

type contact struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Notify string `json:"notify"`
	Stored bool   `json:"stored"`
}

// UnmarshalJSON sanitizes wire-derived display text (Name, Notify) to strip
// terminal control characters before they ever reach the TUI renderer.
func (c *contact) UnmarshalJSON(data []byte) error {
	type alias contact
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	a.Name = sanitizeIncomingText(a.Name)
	a.Notify = sanitizeIncomingText(a.Notify)
	*c = contact(a)
	return nil
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
	PushName         string         `json:"pushName,omitempty"`
}

// UnmarshalJSON sanitizes wire-derived display text (PushName and every
// string nested in Message — conversation, extendedTextMessage.text,
// quotedText, captions, poll text, reaction emoji, file names, etc.) to
// strip terminal control characters before they ever reach the TUI renderer.
func (w *wireMsg) UnmarshalJSON(data []byte) error {
	type alias wireMsg
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	a.PushName = sanitizeIncomingText(a.PushName)
	sanitizeWireValue(a.Message)
	*w = wireMsg(a)
	return nil
}

type env struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type m struct {
	// File Browser State
	fileBrowserOpen       bool
	fileBrowserDir        string
	fileBrowserEntries    []fileBrowserEntry
	fileBrowserFiltered   []fileBrowserEntry
	fileBrowserIndex      int
	fileBrowserScroll     int
	fileBrowserFilter     string
	fileBrowserSortRecent bool
	fileBrowserPathMode   bool
	fileBrowserPathBuf    string

	// Audio Player State
	audioMsgID        string             // playing/paused audio message ID (bubble highlight)
	audioChatID       string             // chat the audio belongs to
	audioPath         string             // local downloaded audio file
	audioPlaying      bool               // player process running
	audioElapsed      time.Duration      // position (frozen while paused)
	audioBase         time.Duration      // elapsed at last (re)start
	audioStartedAt    time.Time          // when the current run started
	audioDuration     time.Duration      // total length, 0 = unknown
	audioPending      string             // msgID awaiting download before play
	audioGen          int                // supersedes stale player/tick messages
	audioCancel       context.CancelFunc // kills the player process
	audioFallbackPath string             // audio file for default-player fallback from popup
	// API & Connection
	baseURL, wsURL, apiToken             string
	client                               *http.Client
	apiCtx                               context.Context
	apiCancel                            context.CancelFunc
	ws                                   *websocket.Conn
	wsCh                                 <-chan env
	wsReconnectDelay                     time.Duration // current backoff; 0 = not disconnected
	wsDisconnected                       bool
	backend                              *exec.Cmd
	startedBackend                       bool

	// Window & Layout
	w, h         int
	status, err  string
	qrRaw        string
	qrReceivedAt time.Time
	sessionReady bool
	demoMode     bool
	windowTitle  string
	mouseEnabled bool

	// Navigation & Input
	active, mode, search, searchInput, input string
	sidebarTab                               string
	sel, scroll, sideScroll                  int
	sidebarFocused                           bool
	leftInput                                string
	leftInputFocused                         bool
	inputAllSelected                         bool // true when Ctrl+A was pressed
	inputBuf                                 string
	inputFlushScheduled                      bool
	drafts                                   map[string]string // chatID -> unsent composer text

	// WhatsApp Entities
	chats                                          []chat
	chatsLoaded                                    bool
	contacts                                       map[string]contact
	contactsLoaded                                 bool
	contactsByNumber                               map[string]contact
	msgs                                           map[string][]wireMsg
	groupPreviews                                  map[string]groupPreview
	whitelist                                      map[string]string // phone -> name, allowed=1 only
	denied                                         map[string]bool   // phone -> true, allowed=0 overrides
	defaultAllowed                                 bool              // global default from backend
	names                                          map[string]string // phone -> custom display name
	syncingContacts, syncingGroups, syncingHistory bool

	// In-Flight Tasks & Downloads
	loadingOlder     map[string]bool                 // chatID → fetch in flight
	noMoreOlder      map[string]bool                 // chatID → backend exhausted
	uploadProgress   map[string]int                  // pendingID → 0..100 percent
	uploadChans      map[string]chan fileProgressMsg // pendingID → progress channel
	downloadedMedia  map[string]string
	mediaOrder       []string // FIFO insertion order for downloadedMedia eviction
	downloadingMedia map[string]bool

	// Pickers & Modals
	themePicker           picker
	pointerPicker         picker
	helpPicker            picker
	settingsPicker        picker
	typingAnimationPicker picker
	mediaIconPicker       picker
	mediaViewPicker       picker
	userlistIconPicker    picker
	splashSpeedPicker     picker
	confirmDialog         confirmDialog
	fontTestOpen          bool
	emojiPickerOpen       bool
	emojiQuery            string
	emojiSel, emojiScroll int
	emojiResultsCache     []emojiItem
	emojiResultsDirty     bool
	reactPickMode         bool   // emoji picker opened for reaction
	reactPickMsgID        string // message ID to react to
	reactPickChatID       string // chat ID for reaction
	reactPickSender       string // sender JID for reaction target

	// Message Operations (Reply/Edit/Attachment)
	replyTo               *wireMsg // message being replied to
	selectedMsgID         string   // message ID highlighted
	replyPickMode         bool     // Alt+R reply pick mode
	replyPickIndex        int      // index into visible messages
	editingMsgID          string   // message ID being edited
	editPickMode          bool     // Alt+A edit pick mode
	editPickIndex         int      // index into edit candidates
	pendingAttachmentPath string
	pendingAttachmentKind string
	pendingAttachmentName string

	// Activity, Ticks & Animation State
	topBarMsg                                                    string
	topBarShown, topBarVer                                       int
	cursorOn, pulseOn                                            bool
	spinnerFrame, shineFrame                                     int
	bootAt, msgActivityUntil                                     time.Time
	msgActivityType                                              string // "sent" or "received"
	flashUntil, typingChats, lastNotifyAt                        map[string]time.Time
	lastNotifyGlobal, lastTypeTime, lastPasteLikeAt              time.Time
	lastClickY                                                   int
	lastClickTime                                                time.Time
	pendingSendSeq                                               int
	pendingSendArmed                                             bool
	lastComposingChat                                            string
	restartRequested                                             bool
	soundEnabled                                                 bool
	soundProfile                                                 int
	identityVersion                                              int
	sidebarMarqueeOffset, sidebarMarqueePause, sidebarMarqueeDir int
	sidebarMarqueeKey                                            string
	sidebarMarqueeTick                                           int

	// Caches & Graphics
	sidebarCache *sidebarCache
	mainCache    *renderCache
	revision     uint64 // bumped on every Update
	gfx          *gfxState

	// Message Search
	msgSearchInput   string
	msgSearchResults []searchHit
	msgSearchSel     int
	msgSearchLoading bool
	msgSearchErr     string
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

// UnmarshalJSON sanitizes the wire-derived search snippet to strip terminal
// control characters before they ever reach the TUI renderer.
func (h *searchHit) UnmarshalJSON(data []byte) error {
	type alias searchHit
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	a.Snippet = sanitizeIncomingText(a.Snippet)
	*h = searchHit(a)
	return nil
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
type splashDoneMsg struct{}
type whitelistLoadMsg struct {
	whitelist      map[string]string // allowed=1 only
	denied         map[string]bool   // allowed=0 overrides
	defaultAllowed bool              // global default
	names          map[string]string // all custom names
	err            error
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
	chatID    string
	msgID     string
	path      string
	err       error
	isPreview bool
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
type syncHistoryDoneMsg struct {
	msg string
}
type clipboardPasteMsg struct {
	path    string
	isImage bool
	err     error
}

func (x m) Init() tea.Cmd {
	bgCmd := setTerminalBgCmd(currentTheme.Background)
	if x.demoMode {
		return tea.Batch(initDemo(), nextCursorBlink(), nextSpinnerTick(), setTerminalTitleCmd("WhatZap"), bgCmd)
	}
	return tea.Batch(ensureBackend(x.reqCtx(), x.client, x.baseURL, x.apiToken), nextCursorBlink(), nextSpinnerTick(), setTerminalTitleCmd("WhatZap"), bgCmd)
}

// reqCtx returns the model's request context, falling back to
// context.Background when none was assigned (e.g. in tests).
func (x m) reqCtx() context.Context {
	if x.apiCtx != nil {
		return x.apiCtx
	}
	return context.Background()
}

// cancelRequests aborts in-flight API requests. Called on quit.
func (x m) cancelRequests() {
	if x.apiCancel != nil {
		x.apiCancel()
	}
}
