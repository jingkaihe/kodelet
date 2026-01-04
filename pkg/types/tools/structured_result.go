package tools

import (
	"encoding/json"
	"reflect"
	"time"

	"github.com/pkg/errors"
)

// StructuredToolResult represents a tool's execution result with structured metadata
type StructuredToolResult struct {
	ToolName  string       `json:"toolName"`
	Success   bool         `json:"success"`
	Error     string       `json:"error,omitempty"`
	Metadata  ToolMetadata `json:"metadata,omitempty"`
	Timestamp time.Time    `json:"timestamp"`
}

// rawStructuredToolResult is used for JSON marshaling/unmarshaling
type rawStructuredToolResult struct {
	ToolName     string          `json:"toolName"`
	Success      bool            `json:"success"`
	Error        string          `json:"error,omitempty"`
	MetadataType string          `json:"metadataType,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
}

// MarshalJSON implements custom JSON marshaling for StructuredToolResult
func (s StructuredToolResult) MarshalJSON() ([]byte, error) {
	raw := rawStructuredToolResult{
		ToolName:  s.ToolName,
		Success:   s.Success,
		Error:     s.Error,
		Timestamp: s.Timestamp,
	}

	if s.Metadata != nil {
		// Get the type identifier
		raw.MetadataType = s.Metadata.ToolType()

		// Marshal the metadata
		metadataBytes, err := json.Marshal(s.Metadata)
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal metadata")
		}
		raw.Metadata = metadataBytes
	}

	return json.Marshal(raw)
}

// metadataTypeRegistry maps metadata type strings to their corresponding Go types
var metadataTypeRegistry = map[string]reflect.Type{
	"file_read":  reflect.TypeOf(FileReadMetadata{}),
	"file_write": reflect.TypeOf(FileWriteMetadata{}),
	"file_edit":  reflect.TypeOf(FileEditMetadata{}),

	"grep_tool":       reflect.TypeOf(GrepMetadata{}),
	"glob_tool":       reflect.TypeOf(GlobMetadata{}),
	"bash":            reflect.TypeOf(BashMetadata{}),
	"bash_background": reflect.TypeOf(BackgroundBashMetadata{}),
	"mcp_tool":        reflect.TypeOf(MCPToolMetadata{}),
	"custom_tool":     reflect.TypeOf(CustomToolMetadata{}),
	"todo":            reflect.TypeOf(TodoMetadata{}),
	"thinking":        reflect.TypeOf(ThinkingMetadata{}),

	"image_recognition":         reflect.TypeOf(ImageRecognitionMetadata{}),
	"subagent":                  reflect.TypeOf(SubAgentMetadata{}),
	"web_fetch":                 reflect.TypeOf(WebFetchMetadata{}),
	"view_background_processes": reflect.TypeOf(ViewBackgroundProcessesMetadata{}),
	"code_execution":            reflect.TypeOf(CodeExecutionMetadata{}),
	"skill":                     reflect.TypeOf(SkillMetadata{}),
	"blocked":                   reflect.TypeOf(BlockedMetadata{}),
}

// UnmarshalJSON implements custom JSON unmarshaling for StructuredToolResult
func (s *StructuredToolResult) UnmarshalJSON(data []byte) error {
	var raw rawStructuredToolResult
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	s.ToolName = raw.ToolName
	s.Success = raw.Success
	s.Error = raw.Error
	s.Timestamp = raw.Timestamp

	// Handle metadata based on type
	if raw.MetadataType != "" && len(raw.Metadata) > 0 {
		metadataType, exists := metadataTypeRegistry[raw.MetadataType]
		if !exists {
			// Unknown metadata type, leave as nil
			return nil
		}

		// Create a new instance of the metadata type
		metadataPtr := reflect.New(metadataType)

		// Unmarshal the JSON into the new instance
		if err := json.Unmarshal(raw.Metadata, metadataPtr.Interface()); err != nil {
			return errors.Wrapf(err, "failed to unmarshal metadata of type %s", raw.MetadataType)
		}

		// Set the metadata (as a value type, not pointer)
		s.Metadata = metadataPtr.Elem().Interface().(ToolMetadata)
	}

	return nil
}

// ToolMetadata is a marker interface for tool-specific metadata structures
type ToolMetadata interface {
	ToolType() string
}

// File operation metadata structures

// FileReadMetadata contains metadata about a file read operation
type FileReadMetadata struct {
	FilePath       string   `json:"filePath"`
	Offset         int      `json:"offset"`
	LineLimit      int      `json:"lineLimit"`
	Lines          []string `json:"lines"`
	Language       string   `json:"language,omitempty"`
	Truncated      bool     `json:"truncated"`
	RemainingLines int      `json:"remainingLines,omitempty"`
}

// ToolType returns the tool type identifier for file read operations
func (m FileReadMetadata) ToolType() string { return "file_read" }

// FileWriteMetadata contains metadata about a file write operation
type FileWriteMetadata struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	Language string `json:"language,omitempty"`
}

// ToolType returns the tool type identifier for file write operations
func (m FileWriteMetadata) ToolType() string { return "file_write" }

// FileEditMetadata contains metadata about a file edit operation
type FileEditMetadata struct {
	FilePath      string `json:"filePath"`
	Edits         []Edit `json:"edits"`
	Language      string `json:"language,omitempty"`
	ReplaceAll    bool   `json:"replaceAll,omitempty"`
	ReplacedCount int    `json:"replacedCount,omitempty"`
}

// Edit represents a single text replacement in a file
type Edit struct {
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	OldContent string `json:"oldContent"`
	NewContent string `json:"newContent"`
}

// ToolType returns the tool type identifier for file edit operations
func (m FileEditMetadata) ToolType() string { return "file_edit" }

// Search tool metadata structures

// GrepMetadata contains metadata about a grep search operation
type GrepMetadata struct {
	Pattern   string         `json:"pattern"`
	Path      string         `json:"path,omitempty"`
	Include   string         `json:"include,omitempty"`
	Results   []SearchResult `json:"results"`
	Truncated bool           `json:"truncated"`
}

// SearchResult represents the search results for a single file
type SearchResult struct {
	FilePath string        `json:"filePath"`
	Language string        `json:"language,omitempty"`
	Matches  []SearchMatch `json:"matches"`
}

// SearchMatch represents a single match in a search result
type SearchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Content    string `json:"content"`
	MatchStart int    `json:"matchStart"`
	MatchEnd   int    `json:"matchEnd"`
	IsContext  bool   `json:"isContext,omitempty"`
}

// ToolType returns the tool type identifier for grep operations
func (m GrepMetadata) ToolType() string { return "grep_tool" }

// GlobMetadata contains metadata about a glob pattern match operation
type GlobMetadata struct {
	Pattern   string     `json:"pattern"`
	Path      string     `json:"path,omitempty"`
	Files     []FileInfo `json:"files"`
	Truncated bool       `json:"truncated"`
}

// FileInfo represents information about a matched file
type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	Type     string    `json:"type"` // "file" or "directory"
	Language string    `json:"language,omitempty"`
}

// ToolType returns the tool type identifier for glob operations
func (m GlobMetadata) ToolType() string { return "glob_tool" }

// Command execution metadata

// BashMetadata contains metadata about a bash command execution
type BashMetadata struct {
	Command       string        `json:"command"`
	ExitCode      int           `json:"exitCode"`
	Output        string        `json:"output"`
	ExecutionTime time.Duration `json:"executionTime"`
	WorkingDir    string        `json:"workingDir,omitempty"`
}

// ToolType returns the tool type identifier for bash command execution
func (m BashMetadata) ToolType() string { return "bash" }

// BackgroundBashMetadata contains metadata about a background bash process
type BackgroundBashMetadata struct {
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	LogPath   string    `json:"logPath"`
	StartTime time.Time `json:"startTime"`
}

// ToolType returns the tool type identifier for background bash processes
func (m BackgroundBashMetadata) ToolType() string { return "bash_background" }

// MCP tool metadata

// MCPToolMetadata contains metadata about an MCP tool execution
type MCPToolMetadata struct {
	MCPToolName   string         `json:"mcpToolName"`
	ServerName    string         `json:"serverName,omitempty"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	Content       []MCPContent   `json:"content"`
	ContentText   string         `json:"contentText"`
	ExecutionTime time.Duration  `json:"executionTime"`
}

// MCPContent represents a content block returned by an MCP tool
type MCPContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Data     string         `json:"data,omitempty"`
	MimeType string         `json:"mimeType,omitempty"`
	URI      string         `json:"uri,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolType returns the tool type identifier for MCP tool execution
func (m MCPToolMetadata) ToolType() string { return "mcp_tool" }

// Custom tool metadata

// CustomToolMetadata contains metadata about a custom tool execution
type CustomToolMetadata struct {
	ExecutionTime time.Duration `json:"executionTime"`
	Output        string        `json:"output"`
}

// ToolType returns the tool type identifier for custom tool execution
func (m CustomToolMetadata) ToolType() string { return "custom_tool" }

// Other tool metadata

// TodoMetadata contains metadata about a todo list operation
type TodoMetadata struct {
	Action     string     `json:"action"` // "read" or "write"
	TodoList   []TodoItem `json:"todoList"`
	Statistics TodoStats  `json:"statistics,omitempty"`
}

// TodoItem represents a single todo list item
type TodoItem struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Priority  string    `json:"priority"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// TodoStats contains statistics about the todo list
type TodoStats struct {
	Total      int `json:"total"`
	Completed  int `json:"completed"`
	InProgress int `json:"inProgress"`
	Pending    int `json:"pending"`
}

// ToolType returns the tool type identifier for todo operations
func (m TodoMetadata) ToolType() string { return "todo" }

// ThinkingMetadata contains metadata about a thinking operation
type ThinkingMetadata struct {
	Thought  string `json:"thought"`
	Category string `json:"category,omitempty"`
}

// ToolType returns the tool type identifier for thinking operations
func (m ThinkingMetadata) ToolType() string { return "thinking" }

// Additional tool metadata structures

// ImageRecognitionMetadata contains metadata about an image recognition operation
type ImageRecognitionMetadata struct {
	ImagePath string          `json:"imagePath"`
	ImageType string          `json:"imageType"` // "local" or "remote"
	Prompt    string          `json:"prompt"`
	Analysis  string          `json:"analysis"`
	ImageSize ImageDimensions `json:"imageSize,omitempty"`
}

// ImageDimensions represents the dimensions of an image
type ImageDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ToolType returns the tool type identifier for image recognition operations
func (m ImageRecognitionMetadata) ToolType() string { return "image_recognition" }

// SubAgentMetadata contains metadata about a sub-agent invocation
type SubAgentMetadata struct {
	Question string `json:"question"`
	Response string `json:"response"`
}

// ToolType returns the tool type identifier for sub-agent operations
func (m SubAgentMetadata) ToolType() string { return "subagent" }

// WebFetchMetadata contains metadata about a web fetch operation
type WebFetchMetadata struct {
	URL           string `json:"url"`
	ContentType   string `json:"contentType"`
	Size          int64  `json:"size"`
	SavedPath     string `json:"savedPath,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	ProcessedType string `json:"processedType"` // "saved", "markdown", "ai_extracted"
	Content       string `json:"content"`       // The actual fetched content
}

// ToolType returns the tool type identifier for web fetch operations
func (m WebFetchMetadata) ToolType() string { return "web_fetch" }

// ViewBackgroundProcessesMetadata contains metadata about viewing background processes
type ViewBackgroundProcessesMetadata struct {
	Processes []BackgroundProcessInfo `json:"processes"`
	Count     int                     `json:"count"`
}

// BackgroundProcessInfo represents information about a single background process
type BackgroundProcessInfo struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	LogPath   string    `json:"logPath"`
	StartTime time.Time `json:"startTime"`
	Status    string    `json:"status"` // "running", "stopped"
}

// ToolType returns the tool type identifier for viewing background processes
func (m ViewBackgroundProcessesMetadata) ToolType() string { return "view_background_processes" }

// ExtractMetadata is a helper that handles both pointer and value type assertions
// This is necessary because JSON unmarshaling creates value types, while
// direct creation uses pointer types
func ExtractMetadata(metadata ToolMetadata, target interface{}) bool {
	if metadata == nil {
		return false
	}

	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Ptr || targetValue.IsNil() {
		return false
	}

	targetElem := targetValue.Elem()
	metadataValue := reflect.ValueOf(metadata)

	// If metadata is a pointer, dereference it
	if metadataValue.Kind() == reflect.Ptr && !metadataValue.IsNil() {
		metadataValue = metadataValue.Elem()
	}

	// Check if the types match (comparing the base types, not pointer vs value)
	if targetElem.Type() != metadataValue.Type() {
		return false
	}

	// Set the target to the metadata value
	targetElem.Set(metadataValue)
	return true
}

// CodeExecutionMetadata contains metadata about a code execution operation
type CodeExecutionMetadata struct {
	Code    string `json:"code"`
	Output  string `json:"output"`
	Runtime string `json:"runtime"`
}

// ToolType returns the tool type identifier for code execution operations
func (m CodeExecutionMetadata) ToolType() string { return "code_execution" }

// SkillMetadata contains metadata about a skill invocation
type SkillMetadata struct {
	SkillName string `json:"skillName"`
	Directory string `json:"directory"`
}

// ToolType returns the tool type identifier for skill operations
func (m SkillMetadata) ToolType() string { return "skill" }
