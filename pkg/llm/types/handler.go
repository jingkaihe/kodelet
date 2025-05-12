package types

// MessageHandler defines how message events should be processed
type MessageHandler interface {
	HandleText(text string)
	HandleToolUse(toolName string, input string)
	HandleToolResult(toolName string, result string)
	HandleDone()
}

// MessageEvent represents an event from processing a message
type MessageEvent struct {
	Type    string
	Content string
	Done    bool
}

// Event types
const (
	EventTypeText       = "text"
	EventTypeToolUse    = "tool_use"
	EventTypeToolResult = "tool_result"
)
