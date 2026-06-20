package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/webui"
)

// Config configures the native chat TUI.
type Config struct {
	ConversationID string
	Profile        string
	CWD            string
	Theme          string
	Runner         webui.ChatRunner
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
	runner webui.ChatRunner

	conversationID string
	profile        string
	cwd            string
	theme          tuiTheme

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
	event webui.ChatEvent
}

type chatDoneMsg struct {
	runID          int
	conversationID string
	err            error
}

type initialHistoryMsg struct {
	entries []chatEntry
	usage   llmtypes.Usage
	err     error
}

type transcriptRefreshMsg struct{}

type tuiSink struct {
	ch    chan<- tea.Msg
	runID int
}

func (s tuiSink) Send(event webui.ChatEvent) error {
	s.ch <- chatEventMsg{runID: s.runID, event: event}
	return nil
}
