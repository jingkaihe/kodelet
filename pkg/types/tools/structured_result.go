package tools

import (
	"encoding/json"
	"fmt"
	"time"
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
			return nil, fmt.Errorf("failed to marshal metadata: %w", err)
		}
		raw.Metadata = metadataBytes
	}

	return json.Marshal(raw)
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
		var metadata ToolMetadata
		var err error

		switch raw.MetadataType {
		case "file_read":
			var m FileReadMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "file_write":
			var m FileWriteMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "file_edit":
			var m FileEditMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "file_multi_edit":
			var m FileMultiEditMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "grep_tool":
			var m GrepMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "glob_tool":
			var m GlobMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "bash":
			var m BashMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "mcp_tool":
			var m MCPToolMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "todo":
			var m TodoMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "thinking":
			var m ThinkingMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "batch":
			var m BatchMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_navigate":
			var m BrowserNavigateMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_click":
			var m BrowserClickMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_get_page":
			var m BrowserGetPageMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_screenshot":
			var m BrowserScreenshotMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_type":
			var m BrowserTypeMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "browser_wait_for":
			var m BrowserWaitForMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "image_recognition":
			var m ImageRecognitionMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "subagent":
			var m SubAgentMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "web_fetch":
			var m WebFetchMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		case "view_background_processes":
			var m ViewBackgroundProcessesMetadata
			err = json.Unmarshal(raw.Metadata, &m)
			metadata = m
		default:
			// Unknown metadata type, leave as nil
			return nil
		}

		if err != nil {
			return fmt.Errorf("failed to unmarshal metadata of type %s: %w", raw.MetadataType, err)
		}
		s.Metadata = metadata
	}

	return nil
}

// ToolMetadata is a marker interface for tool-specific metadata structures
type ToolMetadata interface {
	ToolType() string
}

// File operation metadata structures

type FileReadMetadata struct {
	FilePath  string   `json:"filePath"`
	Offset    int      `json:"offset"`
	Lines     []string `json:"lines"`
	Language  string   `json:"language,omitempty"`
	Truncated bool     `json:"truncated"`
}

func (m FileReadMetadata) ToolType() string { return "file_read" }

type FileWriteMetadata struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	Language string `json:"language,omitempty"`
}

func (m FileWriteMetadata) ToolType() string { return "file_write" }

type FileEditMetadata struct {
	FilePath string `json:"filePath"`
	Edits    []Edit `json:"edits"`
	Language string `json:"language,omitempty"`
}

type Edit struct {
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	OldContent string `json:"oldContent"`
	NewContent string `json:"newContent"`
}

func (m FileEditMetadata) ToolType() string { return "file_edit" }

// Search tool metadata structures

type GrepMetadata struct {
	Pattern   string         `json:"pattern"`
	Path      string         `json:"path,omitempty"`
	Include   string         `json:"include,omitempty"`
	Results   []SearchResult `json:"results"`
	Truncated bool           `json:"truncated"`
}

type SearchResult struct {
	FilePath string        `json:"filePath"`
	Language string        `json:"language,omitempty"`
	Matches  []SearchMatch `json:"matches"`
}

type SearchMatch struct {
	LineNumber int    `json:"lineNumber"`
	Content    string `json:"content"`
	MatchStart int    `json:"matchStart"`
	MatchEnd   int    `json:"matchEnd"`
}

func (m GrepMetadata) ToolType() string { return "grep_tool" }

type GlobMetadata struct {
	Pattern   string     `json:"pattern"`
	Path      string     `json:"path,omitempty"`
	Files     []FileInfo `json:"files"`
	Truncated bool       `json:"truncated"`
}

type FileInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	Type     string    `json:"type"` // "file" or "directory"
	Language string    `json:"language,omitempty"`
}

func (m GlobMetadata) ToolType() string { return "glob_tool" }

// Command execution metadata

type BashMetadata struct {
	Command       string        `json:"command"`
	ExitCode      int           `json:"exitCode"`
	Output        string        `json:"output"`
	ExecutionTime time.Duration `json:"executionTime"`
	WorkingDir    string        `json:"workingDir,omitempty"`
}

func (m BashMetadata) ToolType() string { return "bash" }

// MCP tool metadata

type MCPToolMetadata struct {
	MCPToolName   string         `json:"mcpToolName"`
	ServerName    string         `json:"serverName,omitempty"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	Content       []MCPContent   `json:"content"`
	ContentText   string         `json:"contentText"`
	ExecutionTime time.Duration  `json:"executionTime"`
}

type MCPContent struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Data     string         `json:"data,omitempty"`
	MimeType string         `json:"mimeType,omitempty"`
	URI      string         `json:"uri,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (m MCPToolMetadata) ToolType() string { return "mcp_tool" }

// Other tool metadata

type TodoMetadata struct {
	Action     string     `json:"action"` // "read" or "write"
	TodoList   []TodoItem `json:"todoList"`
	Statistics TodoStats  `json:"statistics,omitempty"`
}

type TodoItem struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Priority  string    `json:"priority"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

type TodoStats struct {
	Total      int `json:"total"`
	Completed  int `json:"completed"`
	InProgress int `json:"inProgress"`
	Pending    int `json:"pending"`
}

func (m TodoMetadata) ToolType() string { return "todo" }

type ThinkingMetadata struct {
	Thought  string `json:"thought"`
	Category string `json:"category,omitempty"`
}

func (m ThinkingMetadata) ToolType() string { return "thinking" }

type BatchMetadata struct {
	Description   string                 `json:"description"`
	SubResults    []StructuredToolResult `json:"subResults"`
	ExecutionTime time.Duration          `json:"executionTime"`
	SuccessCount  int                    `json:"successCount"`
	FailureCount  int                    `json:"failureCount"`
}

func (m BatchMetadata) ToolType() string { return "batch" }

// Browser tool metadata structures

type BrowserNavigateMetadata struct {
	URL      string        `json:"url"`
	FinalURL string        `json:"finalURL,omitempty"`
	Title    string        `json:"title,omitempty"`
	LoadTime time.Duration `json:"loadTime,omitempty"`
}

func (m BrowserNavigateMetadata) ToolType() string { return "browser_navigate" }

type BrowserClickMetadata struct {
	ElementID    int    `json:"elementId"`
	ElementFound bool   `json:"elementFound"`
	ElementType  string `json:"elementType,omitempty"`
	ElementText  string `json:"elementText,omitempty"`
}

func (m BrowserClickMetadata) ToolType() string { return "browser_click" }

type BrowserGetPageMetadata struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	HTMLSize  int    `json:"htmlSize"`
	Truncated bool   `json:"truncated"`
}

func (m BrowserGetPageMetadata) ToolType() string { return "browser_get_page" }

type BrowserScreenshotMetadata struct {
	OutputPath string `json:"outputPath"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Format     string `json:"format"`
	FullPage   bool   `json:"fullPage"`
	FileSize   int64  `json:"fileSize"`
}

func (m BrowserScreenshotMetadata) ToolType() string { return "browser_screenshot" }

type BrowserTypeMetadata struct {
	ElementID int    `json:"elementId"`
	Text      string `json:"text"`
	Cleared   bool   `json:"cleared"`
}

func (m BrowserTypeMetadata) ToolType() string { return "browser_type" }

type BrowserWaitForMetadata struct {
	Condition string        `json:"condition"`
	Selector  string        `json:"selector,omitempty"`
	Timeout   time.Duration `json:"timeout"`
	Found     bool          `json:"found"`
}

func (m BrowserWaitForMetadata) ToolType() string { return "browser_wait_for" }

// Additional tool metadata structures

type ImageRecognitionMetadata struct {
	ImagePath string          `json:"imagePath"`
	ImageType string          `json:"imageType"` // "local" or "remote"
	Prompt    string          `json:"prompt"`
	Analysis  string          `json:"analysis"`
	ImageSize ImageDimensions `json:"imageSize,omitempty"`
}

type ImageDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (m ImageRecognitionMetadata) ToolType() string { return "image_recognition" }

type SubAgentMetadata struct {
	Question      string `json:"question"`
	ModelStrength string `json:"modelStrength"`
	Response      string `json:"response"`
}

func (m SubAgentMetadata) ToolType() string { return "subagent" }

type WebFetchMetadata struct {
	URL           string `json:"url"`
	ContentType   string `json:"contentType"`
	Size          int64  `json:"size"`
	SavedPath     string `json:"savedPath,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	ProcessedType string `json:"processedType"` // "saved", "markdown", "ai_extracted"
	Content       string `json:"content"`      // The actual fetched content
}

func (m WebFetchMetadata) ToolType() string { return "web_fetch" }

type ViewBackgroundProcessesMetadata struct {
	Processes []BackgroundProcessInfo `json:"processes"`
	Count     int                     `json:"count"`
}

type BackgroundProcessInfo struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	LogPath   string    `json:"logPath"`
	StartTime time.Time `json:"startTime"`
	Status    string    `json:"status"` // "running", "stopped"
}

func (m ViewBackgroundProcessesMetadata) ToolType() string { return "view_background_processes" }

type FileMultiEditMetadata struct {
	FilePath string `json:"filePath"`
	Edits    []Edit `json:"edits"`
	Language string `json:"language,omitempty"`
}

func (m FileMultiEditMetadata) ToolType() string { return "file_multi_edit" }
