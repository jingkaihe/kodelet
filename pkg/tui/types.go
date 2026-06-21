package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

// Config configures the native chat TUI.
type Config struct {
	ConversationID string
	Profile        string
	ProfileOptions []string
	CWD            string
	Theme          string
	Runner         chat.ChatRunner
}

type entryKind int

const (
	entryUser entryKind = iota
	entryAssistant
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
	content string
	blocks  []assistantBlock
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

type model struct {
	ctx    context.Context
	cancel context.CancelFunc
	runner chat.ChatRunner

	conversationID string
	profile        string
	profileOptions []string
	profileIndex   int

	profilePickerOpen   bool
	profilePickerIndex  int
	cwd                 string
	requestedCWD        string
	theme               tuiTheme
	slashCommands       []slashcommands.Command
	slashCommandIndex   int
	slashCommandErr     error
	slashDismissedDraft string

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model

	width      int
	height     int
	autoFollow bool

	pendingRefresh       bool
	pendingRefreshBottom bool

	entries []chatEntry
	usage   llmtypes.Usage

	assistantMarkdownRenderer      *glamour.TermRenderer
	assistantMarkdownRendererWidth int
	thoughtMarkdownRenderer        *glamour.TermRenderer
	thoughtMarkdownRendererWidth   int

	running      bool
	activeRunID  int
	nextRunID    int
	workingFrame int
	runCh        chan tea.Msg
	cancelRun    context.CancelFunc

	detailRegions  []detailRegion
	queuedSteering []string
	steerError     string
	status         string
	err            error
}

type chatEventMsg struct {
	runID int
	event chat.ChatEvent
}

type chatDoneMsg struct {
	runID          int
	conversationID string
	err            error
}

type initialHistoryMsg struct {
	loaded  bool
	entries []chatEntry
	usage   llmtypes.Usage
	cwd     string
	profile string
	err     error
}

type transcriptRefreshMsg struct{}

type slashCommandsMsg struct {
	cwd            string
	commands       []slashcommands.Command
	extensionsOnly bool
	err            error
}

type tuiSink struct {
	ch    chan<- tea.Msg
	runID int
}

func (s tuiSink) Send(event chat.ChatEvent) error {
	s.ch <- chatEventMsg{runID: s.runID, event: event}
	return nil
}
