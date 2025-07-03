# ADR 015: Structured Tool Result Storage

## Status

Proposed

## Context

Currently, Kodelet stores tool results in conversations using a simple map (`UserFacingToolResults map[string]string`) that maps tool call IDs to user-facing string results. This approach has several limitations:

1. **Web UI Rendering Challenge**: The upcoming web UI (ADR 014) requires parsing `UserFacing()` strings to extract structured data for proper visualization, which is fragile and error-prone.

2. **Format Coupling**: CLI display format is tightly coupled with data storage, making it difficult to change presentation without affecting stored data.

3. **Lost Semantic Information**: Rich tool results (file paths, line numbers, diff context, search matches, etc.) are flattened into strings, losing valuable metadata.

4. **Maintenance Burden**: Each tool's string format must be parsed with custom regex patterns, creating a maintenance nightmare as tools evolve.

5. **Inconsistent Data Access**: Different tools have different string formats, making unified processing impossible.

## Decision

We will replace the existing string-based `UserFacingToolResults` with structured tool result metadata. All tool outputs will be stored as structured data, and CLI/web interfaces will render this data appropriately at display time. This eliminates string parsing entirely and creates a single source of truth for tool results.

**Breaking Change Rationale**: Given the project's early stage, we prioritize clean architecture over backward compatibility. This approach eliminates the complexity of dual storage, migration utilities, and fallback mechanisms, resulting in a much simpler and more maintainable system.

## Details

### Current Architecture

```go
type ConversationRecord struct {
    ID                    string                 `json:"id"`
    RawMessages           json.RawMessage        `json:"rawMessages"`
    ModelType             string                 `json:"modelType"`
    UserFacingToolResults map[string]string      `json:"userFacingToolResults,omitempty"`
    // ... other fields
}
```

### New Architecture

```go
type ConversationRecord struct {
    ID                    string                                   `json:"id"`
    RawMessages           json.RawMessage                          `json:"rawMessages"`
    ModelType             string                                   `json:"modelType"`
    ToolResults           map[string]StructuredToolResult          `json:"toolResults,omitempty"`
    // ... other fields
}

type StructuredToolResult struct {
    ToolName   string          `json:"toolName"`
    Success    bool            `json:"success"`
    Error      string          `json:"error,omitempty"`
    Metadata   ToolMetadata    `json:"metadata,omitempty"`
    Timestamp  time.Time       `json:"timestamp"`
}

type ToolMetadata interface {
    ToolType() string
}
```

### Tool-Specific Metadata Structures

Each tool type implements its own metadata structure:

#### File Operations

```go
type FileReadMetadata struct {
    FilePath     string   `json:"filePath"`
    Offset       int      `json:"offset"`
    Lines        []string `json:"lines"`
    Language     string   `json:"language"`
    Truncated    bool     `json:"truncated"`
}

type FileWriteMetadata struct {
    FilePath    string   `json:"filePath"`
    Content     string   `json:"content"`
    Size        int64    `json:"size"`
    Language    string   `json:"language"`
}

type FileEditMetadata struct {
    FilePath    string        `json:"filePath"`
    StartLine   int           `json:"startLine"`
    EndLine     int           `json:"endLine"`
    OldContent  string        `json:"oldContent"`
    NewContent  string        `json:"newContent"`
    DiffHunks   []DiffHunk    `json:"diffHunks"`
}

type DiffHunk struct {
    OldStart    int           `json:"oldStart"`
    OldCount    int           `json:"oldCount"`
    NewStart    int           `json:"newStart"`
    NewCount    int           `json:"newCount"`
    Lines       []DiffLine    `json:"lines"`
}

type DiffLine struct {
    Type        string        `json:"type"` // "context", "added", "removed"
    Content     string        `json:"content"`
    LineNumber  int           `json:"lineNumber"`
}
```

#### Search Tools

```go
type GrepMetadata struct {
    Pattern     string          `json:"pattern"`
    Path        string          `json:"path"`
    Include     string          `json:"include"`
    Results     []SearchResult  `json:"results"`
    Truncated   bool            `json:"truncated"`
}

type SearchResult struct {
    FilePath     string          `json:"filePath"`
    Language     string          `json:"language"`
    Matches      []SearchMatch   `json:"matches"`
}

type SearchMatch struct {
    LineNumber   int             `json:"lineNumber"`
    Content      string          `json:"content"`
    MatchStart   int             `json:"matchStart"`
    MatchEnd     int             `json:"matchEnd"`
    Context      []ContextLine   `json:"context,omitempty"`
}

type GlobMetadata struct {
    Pattern     string          `json:"pattern"`
    Path        string          `json:"path"`
    Files       []FileInfo      `json:"files"`
    Truncated   bool            `json:"truncated"`
}

type FileInfo struct {
    Path        string          `json:"path"`
    Size        int64           `json:"size"`
    ModTime     time.Time       `json:"modTime"`
    Type        string          `json:"type"` // "file", "directory"
    Language    string          `json:"language,omitempty"`
}
```

#### Command Execution

```go
type BashMetadata struct {
    Command        string            `json:"command"`
    ExitCode       int               `json:"exitCode"`
    Output         string            `json:"output"`
    ExecutionTime  time.Duration     `json:"executionTime"`
    WorkingDir     string            `json:"workingDir"`
}

type BackgroundBashMetadata struct {
    Command        string            `json:"command"`
    PID            int               `json:"pid"`
    LogPath        string            `json:"logPath"`
    Status         string            `json:"status"` // "running", "stopped"
    StartTime      time.Time         `json:"startTime"`
}
```

#### Content Processing

```go
type WebFetchMetadata struct {
    URL            string            `json:"url"`
    ContentType    string            `json:"contentType"`
    Size           int64             `json:"size"`
    SavedPath      string            `json:"savedPath,omitempty"`
    Prompt         string            `json:"prompt,omitempty"`
    ProcessedType  string            `json:"processedType"` // "saved", "markdown", "ai_extracted"
}

type ImageRecognitionMetadata struct {
    ImagePath      string            `json:"imagePath"`
    ImageType      string            `json:"imageType"` // "local", "remote"
    Prompt         string            `json:"prompt"`
    Analysis       string            `json:"analysis"`
    ImageSize      ImageDimensions   `json:"imageSize,omitempty"`
}

type ImageDimensions struct {
    Width          int               `json:"width"`
    Height         int               `json:"height"`
}
```

#### Organization Tools

```go
type ThinkingMetadata struct {
    Thought        string            `json:"thought"`
    Category       string            `json:"category,omitempty"`
}

type TodoMetadata struct {
    Action         string            `json:"action"` // "read", "write"
    TodoList       []TodoItem        `json:"todoList"`
    Statistics     TodoStats         `json:"statistics"`
}

type TodoItem struct {
    ID             int               `json:"id"`
    Content        string            `json:"content"`
    Status         string            `json:"status"`
    Priority       string            `json:"priority"`
    CreatedAt      time.Time         `json:"createdAt,omitempty"`
    UpdatedAt      time.Time         `json:"updatedAt,omitempty"`
}

type TodoStats struct {
    Total          int               `json:"total"`
    Completed      int               `json:"completed"`
    InProgress     int               `json:"inProgress"`
    Pending        int               `json:"pending"`
}

type SubAgentMetadata struct {
    Question       string            `json:"question"`
    ModelStrength  string            `json:"modelStrength"`
    Response       string            `json:"response"`
}

type BatchMetadata struct {
    Description    string                      `json:"description"`
    SubResults     []StructuredToolResult      `json:"subResults"`
    ExecutionTime  time.Duration               `json:"executionTime"`
    SuccessCount   int                         `json:"successCount"`
    FailureCount   int                         `json:"failureCount"`
}
```

#### Browser Tools

```go
type BrowserNavigateMetadata struct {
    URL            string            `json:"url"`
    FinalURL       string            `json:"finalURL"`
    Title          string            `json:"title"`
    LoadTime       time.Duration     `json:"loadTime"`
}

type BrowserScreenshotMetadata struct {
    OutputPath     string            `json:"outputPath"`
    Width          int               `json:"width"`
    Height         int               `json:"height"`
    Format         string            `json:"format"`
    FullPage       bool              `json:"fullPage"`
    FileSize       int64             `json:"fileSize"`
}
```

#### MCP (Model Context Protocol) Tools

```go
type MCPToolMetadata struct {
    MCPToolName    string               `json:"mcpToolName"`    // Original tool name from MCP server
    ServerName     string               `json:"serverName"`     // Name of the MCP server
    Parameters     map[string]any       `json:"parameters"`     // Input parameters sent to MCP tool
    Content        []MCPContent         `json:"content"`        // Structured content from MCP server
    ContentText    string               `json:"contentText"`    // Concatenated text content for fallback
    ExecutionTime  time.Duration        `json:"executionTime"`  // Time taken to execute the tool
}

type MCPContent struct {
    Type           string               `json:"type"`           // "text", "image", "resource", etc.
    Text           string               `json:"text,omitempty"` // Text content
    Data           string               `json:"data,omitempty"` // Base64 encoded data for images/resources
    MimeType       string               `json:"mimeType,omitempty"` // MIME type for non-text content
    URI            string               `json:"uri,omitempty"`      // URI for resource content
    Metadata       map[string]any       `json:"metadata,omitempty"` // Additional metadata from MCP server
}
```

### Simplified Tool Interface

Replace the existing tool interface with a cleaner structure:

```go
type ToolResult interface {
    GetError() string
    IsError() bool
    AssistantFacing() string
    // Only method needed - returns structured data
    StructuredData() StructuredToolResult
}

// Example implementation for FileReadToolResult
func (r *FileReadToolResult) StructuredData() StructuredToolResult {
    result := StructuredToolResult{
        ToolName:  "file_read",
        Success:   !r.IsError(),
        Timestamp: time.Now(),
    }

    if r.IsError() {
        result.Error = r.GetError()
        return result
    }

    result.Metadata = &FileReadMetadata{
        FilePath:  r.filename,
        Offset:    r.offset,
        Lines:     r.lines,
        Language:  detectLanguage(r.filename),
        Truncated: len(r.lines) > MaxOutputLines,
    }

    return result
}
```

### CLI Renderer Architecture

Create renderers that generate CLI output from structured data:

```go
type CLIRenderer interface {
    RenderCLI(result StructuredToolResult) string
}

// Renderer registry for dispatching based on tool name
type RendererRegistry struct {
    renderers map[string]CLIRenderer
    patterns  map[string]CLIRenderer // For pattern-based matching like "mcp_*"
}

func NewRendererRegistry() *RendererRegistry {
    registry := &RendererRegistry{
        renderers: make(map[string]CLIRenderer),
        patterns:  make(map[string]CLIRenderer),
    }

    // Register all tool renderers
    registry.Register("file_read", &FileReadRenderer{})
    registry.Register("file_write", &FileWriteRenderer{})
    registry.Register("file_edit", &FileEditRenderer{})
    registry.Register("bash", &BashRenderer{})
    registry.Register("grep_tool", &GrepRenderer{})

    // Register MCP tools - pattern matches any tool prefixed with "mcp_"
    registry.RegisterPattern("mcp_*", &MCPToolRenderer{})

    // ... register all other tools

    return registry
}

func (r *RendererRegistry) Register(toolName string, renderer CLIRenderer) {
    r.renderers[toolName] = renderer
}

func (r *RendererRegistry) RegisterPattern(pattern string, renderer CLIRenderer) {
    r.patterns[pattern] = renderer
}

func (r *RendererRegistry) Render(result StructuredToolResult) string {
    // First try exact match
    renderer, exists := r.renderers[result.ToolName]
    if exists {
        return renderer.RenderCLI(result)
    }

    // Then try pattern matching
    for pattern, patternRenderer := range r.patterns {
        if r.matchesPattern(result.ToolName, pattern) {
            return patternRenderer.RenderCLI(result)
        }
    }

    // Fallback renderer for unknown tools
    return r.renderFallback(result)
}

func (r *RendererRegistry) matchesPattern(toolName, pattern string) bool {
    // Simple pattern matching for "mcp_*" style patterns
    if strings.HasSuffix(pattern, "*") {
        prefix := strings.TrimSuffix(pattern, "*")
        return strings.HasPrefix(toolName, prefix)
    }
    return toolName == pattern
}

func (r *RendererRegistry) renderFallback(result StructuredToolResult) string {
    if !result.Success {
        return fmt.Sprintf("Error (%s): %s", result.ToolName, result.Error)
    }
    return fmt.Sprintf("Tool Result (%s): %+v", result.ToolName, result.Metadata)
}

// Example file read renderer
type FileReadRenderer struct{}

func (r *FileReadRenderer) RenderCLI(result StructuredToolResult) string {
    if !result.Success {
        return fmt.Sprintf("Error: %s", result.Error)
    }

    meta := result.Metadata.(*FileReadMetadata)
    buf := bytes.NewBufferString(fmt.Sprintf("File Read: %s\n", meta.FilePath))
    fmt.Fprintf(buf, "Offset: %d\n", meta.Offset)
    buf.WriteString(utils.ContentWithLineNumber(meta.Lines, meta.Offset))
    return buf.String()
}

// Example MCP tool renderer
type MCPToolRenderer struct{}

func (r *MCPToolRenderer) RenderCLI(result StructuredToolResult) string {
    if !result.Success {
        return fmt.Sprintf("Error: %s", result.Error)
    }

    meta := result.Metadata.(*MCPToolMetadata)
    buf := bytes.NewBufferString(fmt.Sprintf("MCP Tool: %s", meta.MCPToolName))
    if meta.ServerName != "" {
        fmt.Fprintf(buf, " (server: %s)", meta.ServerName)
    }
    buf.WriteString("\n")

    // Show parameters if present
    if len(meta.Parameters) > 0 {
        buf.WriteString("Parameters:\n")
        for k, v := range meta.Parameters {
            fmt.Fprintf(buf, "  %s: %v\n", k, v)
        }
        buf.WriteString("\n")
    }

    // Render structured content
    if len(meta.Content) > 0 {
        buf.WriteString("Content:\n")
        for i, content := range meta.Content {
            if i > 0 {
                buf.WriteString("\n")
            }
            switch content.Type {
            case "text":
                buf.WriteString(content.Text)
            case "image":
                fmt.Fprintf(buf, "[Image: %s, size: %d bytes]", content.MimeType, len(content.Data))
            case "resource":
                fmt.Fprintf(buf, "[Resource: %s (%s)]", content.URI, content.MimeType)
            default:
                fmt.Fprintf(buf, "[%s content]", content.Type)
                if content.Text != "" {
                    buf.WriteString(": ")
                    buf.WriteString(content.Text)
                }
            }
        }
    } else {
        // Fallback to concatenated text content
        buf.WriteString(meta.ContentText)
    }

    return buf.String()
}
```

**Renderer Dispatch Mechanism:**
1. **Tool Name Mapping**: `StructuredToolResult.ToolName` field identifies which renderer to use
2. **Registry Pattern**: Central registry maps tool names to renderer instances
3. **Fallback Handling**: Unknown tools get generic rendering to prevent crashes
4. **Type-Safe Metadata**: Each renderer casts metadata to its specific type

**Note**: Web UI rendering will be handled client-side using JavaScript that consumes the structured data via JSON API (see ADR 014).
```

### Data Storage Impact

```go
// Before: ~1KB per tool result (string only)
UserFacingToolResults: map[string]string

// After: ~3-5KB per tool result (structured data only)
ToolResults: map[string]StructuredToolResult // Single source of truth
```

**Storage Changes:**
- **Size**: Moderate increase (~200-400% per tool result)
- **Structure**: Rich metadata instead of flat strings
- **Benefits**: Eliminates runtime parsing, enables rich UI features
- **Trade-off**: Larger files for significantly better functionality

## Consequences

### Advantages

- **Single Source of Truth**: One structured data format for all interfaces
- **No String Parsing**: Eliminates fragile regex-based parsing entirely
- **Rich Interfaces**: Both CLI and web can render sophisticated visualizations
- **Type Safety**: Compile-time guarantees for all tool result data
- **Performance**: Direct access to structured data without parsing overhead
- **Maintainable**: Interface changes don't affect data storage
- **Extensible**: Easy to add new metadata fields and tool types

### Challenges

- **Breaking Change**: Requires updating all existing tools simultaneously
- **Storage Increase**: Larger conversation files (~200-400% increase)
- **Schema Evolution**: Need versioning strategy for metadata structures
- **Implementation Effort**: Significant upfront work to convert all tools

### Trade-offs

- **Storage vs. Functionality**: Larger files enable much richer interfaces
- **Breaking Changes vs. Clean Architecture**: Short-term disruption for long-term benefits
- **Implementation Effort vs. Maintainability**: Heavy upfront work but much easier future development

## Implementation Plan

### Phase 1: Architecture & Core (Week 1-2)
1. Define `StructuredToolResult` and `ToolMetadata` interfaces
2. Update `ConversationRecord` to use `ToolResults` field only
3. Replace tool interface `UserFacing()` with `StructuredData()` method
4. Create CLI renderer framework with registry dispatch system

### Phase 2: File & Command Tools (Week 3-4)
1. Convert file operations (`file_read`, `file_write`, `file_edit`, `file_multi_edit`)
2. Update command execution tools (`bash`, background processes)
3. Add search tools (`grep_tool`, `glob_tool`)
4. Implement corresponding CLI renderers

### Phase 3: Specialized Tools (Week 5-6)
1. Convert content processing tools (`web_fetch`, `image_recognition`)
2. Update organization tools (`thinking`, `todo_read/write`, `subagent`)
3. Handle browser automation tools with rich metadata
4. Convert MCP tools (generic `mcp_*` pattern) to preserve structured content from MCP servers
5. Implement batch tool with nested structured results

### Phase 4: Integration & Polish (Week 7-8)
1. Update conversation storage/loading to use new format
2. Integrate structured CLI renderers into display logic
3. Create JSON API endpoints for web UI consumption (ADR 014)
4. Add JSON schema validation for metadata structures

### Phase 5: Testing & Optimization (Week 9-10)
1. Comprehensive testing of all tool result types
2. Performance optimization for large structured datasets
3. Add metadata schema versioning system
4. Documentation and examples for adding new tools

## References

- [ADR 014: Web UI Conversation Viewer](./014-web-ui-conversation-viewer.md)
- [Existing conversation storage](./pkg/conversations/conversation.go)
- [Tool result interfaces](./pkg/types/tools/)
- [JSON Schema for validation](https://json-schema.org/)
- [Semantic Versioning for schema evolution](https://semver.org/)
