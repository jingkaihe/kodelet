package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Config configures the native chat TUI.
type Config struct {
	ConversationID          string
	Profile                 string
	ProfileOptions          []string
	ReasoningEffort         string
	ReasoningEffortOptions  []string
	ReasoningEffortExplicit bool
	CWD                     string
	Theme                   string
	Runner                  chat.ChatRunner
}

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
	entryInfo
)

type detailKind int

const (
	detailThoughts detailKind = iota
	detailTools
)

type thoughtBlock struct {
	text string
	done bool
}

type toolCall struct {
	id              string
	name            string
	input           string
	result          string
	startedAt       time.Time
	done            bool
	failed          bool
	structured      *tooltypes.StructuredToolResult
	expanded        bool
	expandedChanges map[int]bool
}

type assistantBlockKind int

const (
	blockText assistantBlockKind = iota
	blockThoughts
	blockTools
)

type markdownKind int

const (
	markdownAssistant markdownKind = iota
	markdownThought
)

type assistantBlock struct {
	kind     assistantBlockKind
	text     string
	thoughts []thoughtBlock
	tools    []toolCall
	expanded bool
}

type chatEntry struct {
	kind    entryKind
	title   string
	content string
	blocks  []assistantBlock
}

type historySearchState struct {
	originalDraft string
	query         string
	matches       []string
	selected      int
}

type detailRegion struct {
	entryIndex  int
	blockIndex  int
	kind        detailKind
	line        int
	toolStart   int
	toolEnd     int
	changeIndex int
}

// conversationState contains the presentation and execution state owned by one
// conversation. The active state is embedded into model so existing rendering
// helpers can continue to access fields such as m.entries and m.status while
// background conversations remain independently addressable.
type conversationState struct {
	key                    string
	conversationID         string
	conversationWasResumed bool
	loaded                 bool
	title                  string
	updatedAt              time.Time
	unread                 bool

	profile                 string
	profileOptions          []string
	profileIndex            int
	reasoningEffort         string
	reasoningEffortOptions  []string
	reasoningEffortIndex    int
	reasoningEffortExplicit bool

	profilePickerOpen    bool
	profilePickerIndex   int
	reasoningPickerOpen  bool
	reasoningPickerIndex int
	cwd                  string
	requestedCWD         string
	slashCommands        []slashcommands.Command
	slashCommandIndex    int
	slashCommandErr      error
	slashDismissedDraft  string

	messageHistoryScopeCWD        string
	initialHistoryPending         bool
	deferSubmitUntilHistory       bool
	submitAfterHistoryLoad        string
	extensionDiscoveryBlocked     bool
	extensionLifecyclePending     bool
	submitAfterExtensionLifecycle string
	messageHistory                []string
	historySearch                 *historySearchState

	draft          string
	viewportOffset int
	autoFollow     bool

	entries                 []chatEntry
	activeAssistantEntry    int
	hasActiveAssistantEntry bool
	usage                   llmtypes.Usage

	running       bool
	runCancelling bool
	activeRunID   int
	workingFrame  int
	cancelRun     context.CancelFunc

	queuedSteering  []string
	queuedFollowUps []string
	steerError      string
	status          string
	err             error
	activeUIPrompt  *uiPromptState
}

type conversationDefaults struct {
	profile                 string
	profileOptions          []string
	reasoningEffort         string
	reasoningEffortOptions  []string
	reasoningEffortExplicit bool
	cwd                     string
	requestedCWD            string
}

type conversationRun struct {
	conversationKey string
	cancel          context.CancelFunc
}

type transcriptElapsedClock struct {
	markers []rune
	width   int
	render  func(time.Time) string
}

type model struct {
	*conversationState

	ctx               context.Context
	cancel            context.CancelFunc
	runner            chat.ChatRunner
	extensionRuntimes *extensions.RuntimeManager
	extensionUI       *tuiExtensionUIHost
	extensionWidgets  map[extensionUIKey]tuiExtensionWidget
	widgetOrder       []extensionUIKey
	collapsedWidgets  map[extensionUIKey]bool
	widgetOffsets     map[extensionWidgetOffsetKey]int
	extensionSurfaces map[extensionUIKey]tuiExtensionSurface
	// extensionSurfaceOrder is both the overlay z-order and the focus stack.
	extensionSurfaceOrder []extensionUIKey

	conversations                 map[string]*conversationState
	activeConversationKey         string
	nextConversationKey           int
	nextConversationListRequestID int
	conversationDefaults          conversationDefaults
	conversationPicker            *conversationPickerState

	theme          tuiTheme
	themeSelection string

	messageHistoryStore *messagehistory.Store

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	width  int
	height int

	pendingRefresh           bool
	pendingRefreshBottom     bool
	captureTranscriptElapsed bool
	transcriptElapsedClocks  []transcriptElapsedClock

	assistantMarkdownRenderer      *glamour.TermRenderer
	assistantMarkdownRendererWidth int
	thoughtMarkdownRenderer        *glamour.TermRenderer
	thoughtMarkdownRendererWidth   int

	quitAfterRun bool
	nextRunID    int
	runs         map[int]*conversationRun
	runByState   map[string]int
	runCh        chan tea.Msg

	terminalTitleEpoch   time.Time
	lastTerminalTitle    string
	terminalTitleWritten bool

	detailRegions []detailRegion
	shortcutsOpen bool

	uiNotifications      []uiNotification
	nextUINotificationID int
}

type chatEventMsg struct {
	runID           int
	conversationKey string
	event           chat.ChatEvent
}

type chatDoneMsg struct {
	runID           int
	conversationKey string
	conversationID  string
	err             error
}

type initialHistoryMsg struct {
	conversationKey string
	conversationID  string
	loaded          bool
	entries         []chatEntry
	usage           llmtypes.Usage
	cwd             string
	title           string
	updatedAt       time.Time
	profile         string
	provider        string
	model           string
	reasoningEffort string
	err             error
}

type extensionLifecycleMsg struct {
	conversationKey string
	conversationID  string
	cwd             string
	err             error
}

type transcriptRefreshMsg struct{}

type slashCommandsMsg struct {
	conversationKey string
	cwd             string
	commands        []slashcommands.Command
	extensionsOnly  bool
	err             error
}

type messageHistoryMsg struct {
	conversationKey string
	scopeCWD        string
	messages        []string
	err             error
}

type editorFinishedMsg struct {
	path string
	err  error
}

type tuiSink struct {
	ch              chan<- tea.Msg
	runID           int
	conversationKey string
	done            <-chan struct{}
}

func (s tuiSink) Send(event chat.ChatEvent) error {
	select {
	case s.ch <- chatEventMsg{runID: s.runID, conversationKey: s.conversationKey, event: event}:
		return nil
	case <-s.done:
		return context.Canceled
	}
}
