package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

// Message represents a chat message
type Message struct {
	Content  string
	IsUser   bool
	IsSystem bool
}

// Model represents the main TUI model
type Model struct {
	messageCh          chan llmtypes.MessageEvent
	messages           []Message
	viewport           viewport.Model
	textarea           textarea.Model
	ready              bool
	width              int
	height             int
	isProcessing       bool
	spinnerIndex       int
	showCommands       bool
	windowSizeMsg      tea.WindowSizeMsg
	statusMessage      string
	senderStyle        lipgloss.Style
	userStyle          lipgloss.Style
	assistantStyle     lipgloss.Style
	systemStyle        lipgloss.Style
	assistant          *AssistantClient
	ctx                context.Context
	cancel             context.CancelFunc
	ctrlCPressCount    int
	lastCtrlCPressTime time.Time
	usageText          string
	costText           string

	// Command auto-completion
	showCommandDropdown bool
	availableCommands   []string
	selectedCommandIdx  int
}

// NewModel creates a new TUI model
func NewModel(ctx context.Context, conversationID string, enablePersistence bool, mcpManager *tools.MCPManager) Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(true) // Support multiline input

	// Style the textarea
	ta.Prompt = "❯ "

	// Set custom styles for the textarea
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	ta.FocusedStyle.Base = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	vp := viewport.New(0, 0)
	vp.KeyMap.PageDown.SetEnabled(true)
	vp.KeyMap.PageUp.SetEnabled(true)

	// Define available slash commands
	availableCommands := []string{
		"/bash",
		"/help",
		"/clear",
	}

	// Create status message
	statusMessage := "Ready"

	// Create assistant client
	assistant := NewAssistantClient(ctx, conversationID, enablePersistence, mcpManager)

	ctx, cancel := context.WithCancel(ctx)

	// Create the initial model
	model := Model{
		messageCh:          make(chan llmtypes.MessageEvent),
		messages:           []Message{},
		textarea:           ta,
		viewport:           vp,
		statusMessage:      statusMessage,
		senderStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true),
		userStyle:          lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true),
		assistantStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true),
		systemStyle:        lipgloss.NewStyle().Foreground(lipgloss.Color("yellow")).Bold(true),
		assistant:          assistant,
		ctx:                ctx,
		cancel:             cancel,
		availableCommands:  availableCommands,
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
	m.messages = append(m.messages, Message{
		Content: content,
		IsUser:  isUser,
	})
	m.updateViewportContent()
	m.viewport.GotoBottom()
}

// AddSystemMessage adds a system message to the chat history
func (m *Model) AddSystemMessage(content string) {
	m.messages = append(m.messages, Message{
		Content:  content,
		IsSystem: true,
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
	var content string

	// Format and render each message
	for i, msg := range m.messages {
		var renderedMsg string

		if msg.IsSystem {
			// No prefix for system messages
			renderedMsg = msg.Content
		} else if msg.IsUser {
			// Create a styled user message
			userPrefix := m.userStyle.Render("You")
			messageText := lipgloss.NewStyle().
				PaddingLeft(1).
				Width(m.width - 15). // Ensure text wraps within viewport width
				Render(msg.Content)
			renderedMsg = userPrefix + " → " + messageText
		} else {
			// Create a styled assistant message
			assistantPrefix := m.assistantStyle.Render("Assistant")
			messageText := lipgloss.NewStyle().
				PaddingLeft(1).
				Width(m.width - 15). // Ensure text wraps within viewport width
				Render(msg.Content)
			renderedMsg = assistantPrefix + " → " + messageText
		}

		// Add padding between messages
		if i > 0 {
			content += "\n\n"
		}

		content += renderedMsg
	}

	// Set the viewport content
	m.viewport.SetContent(content)
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Custom message types
type userInputMsg string
type bashInputMsg string
type resetCtrlCMsg struct{}

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
				// Save the conversation
				m.assistant.SaveConversation(m.ctx)
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
			// Show help/shortcuts
			m.AddSystemMessage("Keyboard Shortcuts:\n" +
				"Ctrl+C (twice): Quit\n" +
				"Ctrl+S: Send message\n" +
				"Ctrl+H: Show this help\n" +
				"Ctrl+L: Clear screen\n" +
				"PageUp/PageDown: Scroll history\n" +
				"Up/Down: Navigate history\n\n" +
				"Commands:\n" +
				"/bash [command]: Execute a bash command and include result in chat context\n" +
				"/help: Show this help message\n" +
				"/clear: Clear the screen")
		case tea.KeyCtrlL:
			// Clear the screen
			m.messages = []Message{}
			m.updateViewportContent()
			m.AddSystemMessage("Screen cleared")
		case tea.KeyPgUp:
			// Scroll up a page
			m.viewport.PageUp()
		case tea.KeyPgDown:
			// Scroll down a page
			m.viewport.PageDown()
		case tea.KeyCtrlS:
			// Always hide dropdown on Enter regardless of what happens next
			m.showCommandDropdown = false

			if !m.isProcessing {
				content := m.textarea.Value()
				if content != "" {
					// Handle slash commands
					if strings.HasPrefix(content, "/") {
						// First check for exact command matches
						command := strings.TrimSpace(content)
						commandParts := strings.SplitN(command, " ", 2)

						switch commandParts[0] {
						case "/help":
							m.AddMessage(content, true)
							m.textarea.Reset()
							// Show help message
							m.AddSystemMessage("Keyboard Shortcuts:\n" +
								"Ctrl+C (twice): Quit\n" +
								"Ctrl+S: Send message\n" +
								"Ctrl+H: Show this help\n" +
								"Ctrl+L: Clear screen\n" +
								"PageUp/PageDown: Scroll history\n" +
								"Up/Down: Navigate history\n\n" +
								"Commands:\n" +
								"/bash [command]: Execute a bash command and include result in chat context\n" +
								"/help: Show this help message\n" +
								"/clear: Clear the screen")
							return m, nil

						case "/clear":
							m.AddMessage(content, true)
							m.textarea.Reset()
							// Clear the screen
							m.messages = []Message{}
							m.updateViewportContent()
							m.AddSystemMessage("Screen cleared")
							return m, nil

						case "/bash":
							m.AddMessage(content, true)
							m.textarea.Reset()
							m.SetProcessing(true)

							// Extract bash command from the input
							var bashCommand string
							if len(commandParts) > 1 {
								bashCommand = commandParts[1]
							}

							return m, func() tea.Msg {
								return bashInputMsg(bashCommand)
							}
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
			err := m.assistant.SendMessage(m.ctx, string(msg), m.messageCh)
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
		cmd_out := `
## command
` + string(msg) + `

## output
` + string(output) + `
`
		m.AddMessage(cmd_out, true)
		m.SetProcessing(false)
	case llmtypes.MessageEvent:
		if !msg.Done {
			m.AddMessage(ProcessAssistantEvent(msg), false)
			return m, func() tea.Msg {
				return <-m.messageCh
			}
		} else {
			m.SetProcessing(false)
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
	if strings.HasPrefix(currentInput, "/") && !m.commandSelectionCompleted(currentInput) && !m.isProcessing {
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
		// Hide dropdown if input doesn't start with "/"
		m.showCommandDropdown = false
	}

	// Update spinner animation when processing
	if m.isProcessing {
		m.spinnerIndex = (m.spinnerIndex + 1) % 8
	}

	return m, tea.Batch(cmds...)
}

func (m Model) commandSelectionCompleted(currentInput string) bool {
	for _, cmd := range m.availableCommands {
		if strings.HasPrefix(currentInput, cmd) {
			return true
		}
	}

	return false
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Create a more polished input box
	inputBox := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
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

	// Add a subtle shadow effect
	inputBox = lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Render(inputBox)

	// Render command dropdown if needed
	var commandDropdown string
	if m.showCommandDropdown && len(m.availableCommands) > 0 {
		var dropdownContent string

		for i, cmd := range m.availableCommands {
			style := lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)

			// Highlight the selected command
			if i == m.selectedCommandIdx {
				style = style.
					Background(lipgloss.Color("205")).
					Foreground(lipgloss.Color("0"))
			} else {
				style = style.
					Background(lipgloss.Color("236")).
					Foreground(lipgloss.Color("252"))
			}

			// Add the styled command to the dropdown
			dropdownContent += style.Render(cmd) + "\n"
		}

		// Create dropdown box with border
		commandDropdown = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Width(20).
			Render(dropdownContent)

		// Create dropdown box with border and navigation hint
		hintText := "↑↓:Navigate Tab:Next Enter:Select"
		hint := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Align(lipgloss.Center).
			Render(hintText)

		commandDropdown = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
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
			lipgloss.WithWhitespaceForeground(lipgloss.Color("0")),
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
		spinChars := []string{".", "∘", "○", "◌", "◍", "◉", "◎", "●"}
		statusText = fmt.Sprintf("%s %s", spinChars[m.spinnerIndex%8], m.statusMessage)
	} else {
		statusText = m.statusMessage
	}

	m.usageText, m.costText = m.updateUsage()

	// Add conversation ID to status if persistence is enabled
	var persistenceStatus string
	if m.assistant.IsPersisted() {
		persistenceStatus = fmt.Sprintf(" │ Conv: %s", m.assistant.GetConversationID())
	}

	// Create main status line with controls
	mainStatus := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		MarginTop(0).
		Bold(true).
		Render(statusText + persistenceStatus + " │ Ctrl+C (twice): Quit │ Ctrl+H (/help): Help │ Ctrl+S: Submit │ ↑/↓: Scroll")

	// Create separate usage and cost line if available
	if m.usageText != "" {
		usageLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Background(lipgloss.Color("236")).
			Padding(0, 1).
			Bold(true).
			Render(m.usageText + m.costText)

		return lipgloss.JoinVertical(lipgloss.Left, mainStatus, usageLine)
	}

	return mainStatus
}

func (m *Model) updateUsage() (usageText string, costText string) {
	usage := m.assistant.GetUsage()

	if usage.TotalTokens() > 0 {
		usageText = fmt.Sprintf("Tokens: %d in / %d out / %d cw / %d cr / %d total | Ctx: %d / %d",
			usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens(),
			usage.CurrentContextWindow, usage.MaxContextWindow)

		// Add cost information if available
		if usage.TotalCost() > 0 {
			costText = fmt.Sprintf(" | Cost: $%.4f", usage.TotalCost())
		}
	}

	return usageText, costText
}
