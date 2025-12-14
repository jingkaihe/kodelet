// Package tui provides terminal user interface components using the Bubble Tea
// framework. It implements interactive chat functionality, command execution
// views, and real-time conversation display for kodelet's CLI interface.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// Model represents the main TUI model
type Model struct {
	messageCh          chan llmtypes.MessageEvent
	messages           []llmtypes.Message
	imagePaths         []string
	viewport           viewport.Model
	textarea           textarea.Model
	ready              bool
	width              int
	height             int
	isProcessing       bool
	spinnerIndex       int
	windowSizeMsg      tea.WindowSizeMsg
	statusMessage      string
	assistant          *AssistantClient
	ctx                context.Context
	cancel             context.CancelFunc
	ctrlCPressCount    int
	lastCtrlCPressTime time.Time
	messageFormatter   *MessageFormatter

	// Streaming state
	isStreaming       bool // True when receiving streaming deltas
	streamingMsgIndex int  // Index of the message being streamed to

	// Command auto-completion
	showCommandDropdown bool
	availableCommands   []string
	selectedCommandIdx  int
}

// NewModel creates a new TUI model
func NewModel(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager, customManager *tools.CustomToolManager, maxTurns int, compactRatio float64, disableAutoCompact bool, ideMode bool, noSkills bool) Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true) // Support multiline input

	// Style the textarea
	ta.Prompt = "❯ "

	// Set custom styles for the textarea (Tokyo Night)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))              // Comment
	ta.FocusedStyle.Base = lipgloss.NewStyle().Foreground(lipgloss.Color("#c0caf5"))              // Foreground
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")).Bold(true) // Blue
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89"))            // Comment

	vp := viewport.New(0, 0)
	vp.KeyMap.PageDown.SetEnabled(true)
	vp.KeyMap.PageUp.SetEnabled(true)

	statusMessage := "Ready"

	assistant := NewAssistantClient(ctx, conversationID, enablePersistence, mcpManager, customManager, maxTurns, compactRatio, disableAutoCompact, ideMode, noSkills)

	ctx, cancel := context.WithCancel(ctx)

	formatter := NewMessageFormatter(80) // Initial width, will be updated on resize

	// Create the initial model
	model := Model{
		messageCh:          make(chan llmtypes.MessageEvent),
		messages:           []llmtypes.Message{},
		imagePaths:         []string{},
		textarea:           ta,
		viewport:           vp,
		statusMessage:      statusMessage,
		assistant:          assistant,
		ctx:                ctx,
		cancel:             cancel,
		messageFormatter:   formatter,
		availableCommands:  GetAvailableCommands(),
		selectedCommandIdx: 0,
	}

	// Populate messages from loaded conversation if it exists
	if conversationID != "" && enablePersistence {
		if loadedMessages, err := assistant.GetThreadMessages(); err == nil && len(loadedMessages) > 0 {
			model.messages = loadedMessages
			model.updateViewportContent()
			model.viewport.GotoBottom()
			model.AddSystemMessage(fmt.Sprintf("Loaded conversation: %s", conversationID))
		}
	}

	return model
}

// AddMessage adds a new message to the chat history
func (m *Model) AddMessage(content string, isUser bool) {
	var role string
	if isUser {
		role = "user"
	} else {
		role = "assistant"
	}
	m.messages = append(m.messages, llmtypes.Message{
		Content: content,
		Role:    role,
	})
	m.updateViewportContent()
	m.viewport.GotoBottom()
}

// AddSystemMessage adds a system message to the chat history
func (m *Model) AddSystemMessage(content string) {
	m.messages = append(m.messages, llmtypes.Message{
		Content: content,
		Role:    "", // no role for system messages
	})
	// m.assistant.AddUserMessage(content)
	m.updateViewportContent()
	m.viewport.GotoBottom()
}

// SetProcessing sets the processing state
func (m *Model) SetProcessing(isProcessing bool) {
	m.isProcessing = isProcessing
	if isProcessing {
		m.statusMessage = "Processing..."
	} else {
		m.statusMessage = "Ready"
	}
}

// updateViewportContent updates the content of the viewport
func (m *Model) updateViewportContent() {
	content := m.messageFormatter.FormatMessages(m.messages)
	m.viewport.SetContent(content)
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Custom message types
type (
	userInputMsg  string
	bashInputMsg  string
	resetCtrlCMsg struct{}
)

// resetCtrlCCmd creates a command that resets the Ctrl+C counter after a timeout
func resetCtrlCCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return resetCtrlCMsg{}
	})
}

// Update handles the message updates
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case resetCtrlCMsg:
		if m.statusMessage == "Press Ctrl+C again to quit" {
			m.statusMessage = "Ready"
			m.ctrlCPressCount = 0
		}
		return m, nil

	// Handle Enter key specially when dropdown is visible
	case tea.KeyMsg:
		if msg.Type == tea.KeyEnter && m.showCommandDropdown && !m.isProcessing {
			// Select the command from dropdown when Enter is pressed
			selectedCommand := m.availableCommands[m.selectedCommandIdx]
			m.textarea.SetValue(selectedCommand + " ")
			m.showCommandDropdown = false
			// Return a no-op command to ensure state updates
			return m, func() tea.Msg { return nil }
		}

		// Continue with normal message handling
		switch msg.Type {
		case tea.KeyCtrlC:
			// Check if this is a second ctrl+c press within 2 seconds
			now := time.Now()
			if m.ctrlCPressCount > 0 && now.Sub(m.lastCtrlCPressTime) < 2*time.Second {
				m.cancel()
				return m, tea.Quit
			}

			// First ctrl+c press or timeout expired
			m.ctrlCPressCount = 1
			m.lastCtrlCPressTime = now
			m.statusMessage = "Press Ctrl+C again to quit"

			// Schedule a reset using the proper command system
			return m, resetCtrlCCmd()
		case tea.KeyCtrlH:
			m.AddSystemMessage(GetHelpText())
		case tea.KeyCtrlL:
			m.messages = []llmtypes.Message{}
			m.updateViewportContent()
			m.AddSystemMessage("Screen cleared")
		case tea.KeyPgUp:
			m.viewport.PageUp()
		case tea.KeyPgDown:
			m.viewport.PageDown()
		case tea.KeyCtrlS:
			m.showCommandDropdown = false

			if !m.isProcessing {
				content := m.textarea.Value()
				if content != "" {
					cmd, args, isCommand := ParseCommand(content)

					if isCommand {
						switch Command(cmd) {
						case CommandHelp:
							m.AddMessage(content, true)
							m.textarea.Reset()
							m.AddSystemMessage(GetHelpText())
							return m, nil

						case CommandClear:
							m.AddMessage(content, true)
							m.textarea.Reset()
							m.messages = []llmtypes.Message{}
							m.updateViewportContent()
							m.AddSystemMessage("Screen cleared")
							return m, nil

						case CommandBash:
							m.AddMessage(content, true)
							m.textarea.Reset()
							m.SetProcessing(true)
							return m, func() tea.Msg {
								return bashInputMsg(args)
							}

						case CommandAddImage:
							m.textarea.Reset()
							if args != "" {
								m.imagePaths = append(m.imagePaths, args)
								m.AddSystemMessage(fmt.Sprintf("Added image: %s", args))
							}
							return m, nil

						case CommandRemoveImage:
							m.AddMessage(content, true)
							m.textarea.Reset()
							if args != "" {
								m.imagePaths = slices.DeleteFunc(m.imagePaths, func(path string) bool {
									return path == args
								})
								m.AddSystemMessage(fmt.Sprintf("Removed image: %s", args))
							}
							return m, nil
						}
					}

					// Default handling for non-command messages
					m.AddMessage(content, true)
					m.textarea.Reset()
					m.SetProcessing(true)
					return m, func() tea.Msg {
						return userInputMsg(content)
					}
				}
			}
		}
	case userInputMsg:
		go func() {
			defer func() {
				m.imagePaths = []string{}
			}()
			err := m.assistant.SendMessage(m.ctx, string(msg), m.messageCh, m.imagePaths...)
			if err != nil {
				m.AddSystemMessage("Error: " + err.Error())
			}
		}()
		return m, func() tea.Msg {
			return <-m.messageCh
		}
	case bashInputMsg:
		cmd := exec.Command("bash", "-c", string(msg))
		output, err := cmd.CombinedOutput()
		if err != nil {
			m.AddSystemMessage("Error: " + err.Error())
		}
		cmdOut := FormatBashOutput(string(msg), string(output))
		m.AddMessage(cmdOut, true)
		m.SetProcessing(false)
	case llmtypes.MessageEvent:
		if msg.Done {
			m.SetProcessing(false)
			m.isStreaming = false
			return m, nil
		}

		switch msg.Type {
		case llmtypes.EventTypeThinkingStart:
			// Start a new thinking message
			m.messages = append(m.messages, llmtypes.Message{
				Content: "💭 Thinking: ",
				Role:    "assistant",
			})
			m.streamingMsgIndex = len(m.messages) - 1
			m.isStreaming = true
			m.updateViewportContent()
			m.viewport.GotoBottom()

		case llmtypes.EventTypeThinkingDelta, llmtypes.EventTypeTextDelta:
			if m.isStreaming && m.streamingMsgIndex >= 0 && m.streamingMsgIndex < len(m.messages) {
				// Append to existing streaming message
				m.messages[m.streamingMsgIndex].Content += msg.Content
				m.updateViewportContent()
				m.viewport.GotoBottom()
			} else {
				// Start a new text message if not already streaming
				m.messages = append(m.messages, llmtypes.Message{
					Content: msg.Content,
					Role:    "assistant",
				})
				m.streamingMsgIndex = len(m.messages) - 1
				m.isStreaming = true
				m.updateViewportContent()
				m.viewport.GotoBottom()
			}

		case llmtypes.EventTypeContentBlockEnd:
			// End the current streaming block
			m.isStreaming = false
			m.streamingMsgIndex = -1

		case llmtypes.EventTypeText, llmtypes.EventTypeThinking:
			// Non-streaming complete content (fallback for non-streaming handlers)
			m.AddMessage(FormatAssistantEvent(msg), false)

		case llmtypes.EventTypeToolUse, llmtypes.EventTypeToolResult:
			// Tool events are not streamed, add as separate messages
			m.isStreaming = false
			m.streamingMsgIndex = -1
			m.AddMessage(FormatAssistantEvent(msg), false)
		}

		return m, func() tea.Msg {
			return <-m.messageCh
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.windowSizeMsg = msg

		headerHeight := 1
		footerHeight := 5 // textarea height + status bar + padding
		verticalMargins := headerHeight + footerHeight

		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - verticalMargins
		m.textarea.SetWidth(msg.Width - 2)
		m.messageFormatter.SetWidth(msg.Width)

		if !m.ready {
			m.ready = true
		}
		m.updateViewportContent()
	}

	// Handle viewport updates
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	// Handle textarea updates
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	// Check for slash commands in the textarea
	currentInput := m.textarea.Value()
	shouldShow := ShouldShowCommandDropdown(currentInput, m.availableCommands, m.isProcessing)

	if shouldShow {
		// Show dropdown if it's not already showing
		if !m.showCommandDropdown {
			m.showCommandDropdown = true
			m.selectedCommandIdx = 0
		}

		// Handle slash command navigation with Tab, Up, Down
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			switch keyMsg.Type {
			case tea.KeyTab, tea.KeyDown:
				// Move to the next suggestion
				m.selectedCommandIdx = (m.selectedCommandIdx + 1) % len(m.availableCommands)
			case tea.KeyShiftTab, tea.KeyUp:
				// Move to the previous suggestion
				m.selectedCommandIdx = (m.selectedCommandIdx - 1)
				if m.selectedCommandIdx < 0 {
					m.selectedCommandIdx = len(m.availableCommands) - 1
				}
			case tea.KeyEnter:
				// If showing dropdown and Enter is pressed, select the command
				if m.showCommandDropdown {
					selectedCommand := m.availableCommands[m.selectedCommandIdx]
					m.textarea.SetValue(selectedCommand + " ")
					m.showCommandDropdown = false
					// Don't process the Enter key further to avoid sending the command
					return m, tea.Batch(cmds...)
				}
			}
		}
	} else {
		// Hide dropdown if conditions not met
		m.showCommandDropdown = false
	}

	// Update spinner animation when processing
	if m.isProcessing {
		m.spinnerIndex = (m.spinnerIndex + 1) % 8
	}

	return m, tea.Batch(cmds...)
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Create a more polished input box (Tokyo Night)
	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7aa2f7")). // Blue
		Padding(0, 2).
		PaddingTop(0).
		PaddingBottom(0).
		Width(m.width - 2).
		Align(lipgloss.Left).
		BorderBottom(true).
		BorderTop(true).
		BorderLeft(true).
		BorderRight(true).
		Bold(false).
		Render(m.textarea.View())

	// Add a subtle shadow effect (Tokyo Night)
	inputBox = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7aa2f7")). // Blue
		Render(inputBox)

	var commandDropdown string
	if m.showCommandDropdown && len(m.availableCommands) > 0 {
		var dropdownContent string

		for i, cmd := range m.availableCommands {
			style := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)

			if i == m.selectedCommandIdx {
				style = style.
					Background(lipgloss.Color("#7aa2f7")). // Blue
					Foreground(lipgloss.Color("#1a1b26"))  // Background dark
			} else {
				style = style.
					Background(lipgloss.Color("#292e42")). // Background highlight
					Foreground(lipgloss.Color("#c0caf5"))  // Foreground
			}

			dropdownContent += style.Render(cmd) + "\n"
		}

		// Create dropdown box with border and navigation hint (Tokyo Night)
		hintText := "↑↓:Navigate Tab:Next Enter:Select"
		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89")). // Comment
			Align(lipgloss.Center).
			Render(hintText)

		commandDropdown = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7aa2f7")). // Blue
			Width(40).
			Render(dropdownContent + "\n" + hint)
	}

	// Layout with better spacing
	layout := lipgloss.JoinVertical(
		lipgloss.Left,
		// Add a small gap above the chat history
		lipgloss.NewStyle().
			PaddingBottom(1).
			Render(m.viewport.View()),
		// Add spacing around the input box
		lipgloss.NewStyle().
			PaddingTop(0).
			PaddingBottom(0).
			Render(inputBox),
		// Style the status bar
		m.statusView(),
	)

	// If showing command dropdown, place it above the status bar
	if m.showCommandDropdown {
		// Calculate the position for the dropdown (near the textarea)
		// This is a simple placement - in a real app, you might calculate
		// the exact cursor position
		dropdown := lipgloss.Place(
			m.width,
			5, // Height of dropdown
			lipgloss.Left,
			lipgloss.Top,
			commandDropdown,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1b26")), // Background dark
		)

		// Insert the dropdown right after the input box
		parts := strings.Split(layout, m.statusView())
		if len(parts) == 2 {
			layout = parts[0] + dropdown + m.statusView()
		}
	}

	return layout
}

// statusView renders the status bar
func (m Model) statusView() string {
	var statusText string
	if m.isProcessing {
		statusText = fmt.Sprintf("%s %s", GetSpinnerChar(m.spinnerIndex), m.statusMessage)
	} else {
		statusText = m.statusMessage
	}

	usageText, costText := FormatUsageStats(m.assistant.GetUsage())

	provider, model := m.assistant.GetModelInfo()
	modelInfo := FormatModelInfo(provider, model)

	var persistenceStatus string
	if m.assistant.IsPersisted() {
		persistenceStatus = fmt.Sprintf(" │ Conv: %s", m.assistant.GetConversationID())
	}

	mainStatus := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7aa2f7")). // Blue
		Background(lipgloss.Color("#292e42")). // Background highlight
		Padding(0, 1).
		MarginTop(0).
		Bold(true).
		Render(statusText + " │ Model: " + modelInfo + persistenceStatus + " │ Ctrl+C (twice): Quit │ Ctrl+H (/help): Help │ Ctrl+S: Submit │ ↑/↓: Scroll")

	if usageText != "" {
		usageLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7aa2f7")). // Blue
			Background(lipgloss.Color("#292e42")). // Background highlight
			Padding(0, 1).
			Bold(true).
			Render(usageText + costText)

		return lipgloss.JoinVertical(lipgloss.Left, mainStatus, usageLine)
	}

	return mainStatus
}
