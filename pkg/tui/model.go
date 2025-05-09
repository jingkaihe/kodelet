package tui

import (
	"context"
	
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Message represents a chat message
type Message struct {
	Content  string
	IsUser   bool
	IsSystem bool
}

// Model represents the main TUI model
type Model struct {
	messages       []Message
	viewport       viewport.Model
	textarea       textarea.Model
	ready          bool
	width          int
	height         int
	isProcessing   bool
	showCommands   bool
	windowSizeMsg  tea.WindowSizeMsg
	statusMessage  string
	senderStyle    lipgloss.Style
	userStyle      lipgloss.Style
	assistantStyle lipgloss.Style
	systemStyle    lipgloss.Style
	assistant      *AssistantClient
	ctx            context.Context
}

// NewModel creates a new TUI model
func NewModel() Model {
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Use Enter to send

	vp := viewport.New(0, 0)
	vp.KeyMap.PageDown.SetEnabled(true)
	vp.KeyMap.PageUp.SetEnabled(true)

	return Model{
		messages:       []Message{},
		textarea:       ta,
		viewport:       vp,
		statusMessage:  "Ready",
		senderStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true),
		userStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true),
		assistantStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true),
		systemStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("yellow")).Bold(true),
		assistant:      NewAssistantClient(),
		ctx:            context.Background(),
	}
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
			renderedMsg = m.systemStyle.Render("System") + ": " + msg.Content
		} else if msg.IsUser {
			renderedMsg = m.userStyle.Render("You") + ": " + msg.Content
		} else {
			renderedMsg = m.assistantStyle.Render("Assistant") + ": " + msg.Content
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

// Update handles the message updates
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "ctrl+h":
			// Show help/shortcuts
			m.AddSystemMessage("Keyboard Shortcuts:\n" +
				"Ctrl+C, Esc: Quit\n" +
				"Enter: Send message\n" +
				"Ctrl+H: Show this help\n" +
				"Ctrl+L: Clear screen\n" +
				"PageUp/PageDown: Scroll history\n" +
				"Up/Down: Navigate history")
		case "ctrl+l":
			// Clear the screen
			m.messages = []Message{}
			m.updateViewportContent()
			m.AddSystemMessage("Screen cleared")
		case "enter":
			if !m.isProcessing {
				content := m.textarea.Value()
				if content != "" {
					m.AddMessage(content, true)
					m.textarea.Reset()
					m.SetProcessing(true)
					// Send the message to the assistant
					return m, func() tea.Msg {
						return userInputMsg(content)
					}
				}
			}
		case "pgup":
			// Scroll up a page
			m.viewport.PageUp()
		case "pgdown":
			// Scroll down a page
			m.viewport.PageDown()
		}
	case userInputMsg:
		// Send the message to the assistant
		return m, func() tea.Msg {
			events, err := m.assistant.SendMessage(m.ctx, string(msg))
			if err != nil {
				return assistantErrorMsg(err.Error())
			}
			return assistantResponseMsg{events: events}
		}
	case assistantResponseMsg:
		response := ProcessAssistantEvents(msg.events)
		m.AddMessage(response, false)
		m.SetProcessing(false)
	case assistantErrorMsg:
		m.AddSystemMessage("Error: " + string(msg))
		m.SetProcessing(false)
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

	return m, tea.Batch(cmds...)
}

// View renders the UI
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Layout
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.viewport.View(),
		lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(m.textarea.View()),
		m.statusView(),
	)
}

// statusView renders the status bar
func (m Model) statusView() string {
	statusText := m.statusMessage
	if m.isProcessing {
		statusText += " 🔄"
	}

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Render(statusText + " | Ctrl+C: Quit | Ctrl+H: Help | ↑/↓: Scroll")
}

// Custom message types
type userInputMsg string
type assistantErrorMsg string

// assistantResponseMsg contains the events from the assistant
type assistantResponseMsg struct {
	events []MessageEvent
}