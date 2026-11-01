package extension

import "encoding/base64"

// ToolResult is the final result of a tool call.
type ToolResult struct {
	// Content is the text returned to the model.
	Content string `json:"content"`
	// Data carries presentation and application data, for example
	// {"presentation": extension.Presentation{...}}.
	Data map[string]any `json:"data,omitempty"`
	// StructuredContent is the canonical JSON result for programmatic callers,
	// separate from presentation data.
	StructuredContent any `json:"structuredContent,omitempty"`
	// Error marks the call as failed with a model-visible message.
	Error string `json:"error,omitempty"`
	// Attachments are images ingested and persisted by the host; each must have
	// exactly one source. At most eight are accepted per result.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Text returns a ToolResult with only content.
func Text(content string) ToolResult {
	return ToolResult{Content: content}
}

// Presentation is generic display metadata for extension tool calls.
type Presentation struct {
	// Summary is the complete compact label; the host normalizes it.
	Summary string `json:"summary"`
	// Body is expanded content; hosts fall back to tool content and may truncate it.
	Body string `json:"body,omitempty"`
	// Format is "text" (default) or "markdown".
	Format string `json:"format,omitempty"`
}

// Attachment is a tool result attachment. Provide exactly one of Path or Data.
type Attachment struct {
	Type string `json:"type"`
	// Path is a runner-local file.
	Path string `json:"path,omitempty"`
	// Data is strict base64 image bytes, not a data URL.
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Filename string `json:"filename,omitempty"`
	Alt      string `json:"alt,omitempty"`
}

// ImageFile returns an image attachment read by the host from a runner-local path.
func ImageFile(path string) Attachment {
	return Attachment{Type: "image", Path: path}
}

// ImageData returns an inline image attachment.
func ImageData(data []byte, mimeType string) Attachment {
	return Attachment{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType}
}

// CommandAction selects how Kodelet continues after a command.
type CommandAction string

// Command actions.
const (
	ActionPass     CommandAction = "pass"
	ActionRespond  CommandAction = "respond"
	ActionRunAgent CommandAction = "runAgent"
)

// CommandResult is returned by command handlers. The zero value passes.
type CommandResult struct {
	Action     CommandAction `json:"action"`
	Response   string        `json:"response,omitempty"`
	Prompt     string        `json:"prompt,omitempty"`
	RecipeName string        `json:"recipeName,omitempty"`
	Display    string        `json:"display,omitempty"`
}

// Pass lets the next command route or the model handle the prompt.
func Pass() CommandResult {
	return CommandResult{Action: ActionPass}
}

// Respond answers the prompt directly without running the agent.
func Respond(response string) CommandResult {
	return CommandResult{Action: ActionRespond, Response: response}
}

// RunAgent runs the agent with prompt instead of the user's raw input.
func RunAgent(prompt string) CommandResult {
	return CommandResult{Action: ActionRunAgent, Prompt: prompt}
}

// ShortcutResult asks the native TUI to submit a message. The zero value
// means no follow-up.
type ShortcutResult struct {
	Action  string `json:"action"`
	Message string `json:"message"`
}

// Submit returns a shortcut result that submits message to the conversation.
// Hosts must advertise capabilities.shortcuts.submit.
func Submit(message string) ShortcutResult {
	return ShortcutResult{Action: "submit", Message: message}
}
