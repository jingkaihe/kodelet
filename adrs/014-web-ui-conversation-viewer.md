# ADR 014: Web UI Conversation Viewer

## Status

Proposed

## Context

Kodelet currently stores conversations in JSON files in the filesystem (`~/.cache/kodelet/conversations`) and provides CLI commands (`kodelet conversation [list|show]`) to view them. While this works well for programmatic access, users would benefit from a more visual and interactive way to browse and view their conversation history.

A web UI would provide:
- Better readability with syntax highlighting and formatted output
- More intuitive navigation through conversation history
- Responsive design for different screen sizes
- Tool-specific rendering tailored to each tool's output format

The web UI should be read-only to maintain the simplicity of the current architecture and avoid the complexity of implementing write operations through the web interface.

## Decision

We will implement a web UI for viewing conversations that:
1. Reuses the existing conversation storage and retrieval infrastructure
2. Provides tool-specific rendering of results using the existing `UserFacing()` methods
3. Runs as an embedded HTTP server within the kodelet binary
4. Uses modern web technologies (Tailwind CSS, DaisyUI) for responsive design
5. Bundles all assets into the binary for easy deployment

## Details

### Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Web Browser   │◄──►│   HTTP Server   │◄──►│ Conversation    │
│                 │    │   (embedded)    │    │ Store           │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                                │
                                ▼
                       ┌─────────────────┐
                       │ Tool Result     │
                       │ Renderers       │
                       └─────────────────┘
```

### Key Components

1. **HTTP Server**
   - Embedded in the main kodelet binary
   - Serves static assets and API endpoints
   - Handles conversation ID resolution (both full and short IDs)

2. **Frontend Components**
   - Responsive conversation list with pagination
   - Individual conversation viewer with tool-specific rendering
   - Search and filtering capabilities
   - Loading states and error handling

3. **Tool Result Renderers**
   - `file_read` / `file_write`: Foldable syntax-highlighted code blocks
   - `file_edit`: Diff view with before/after comparison
   - `bash`: Command execution with output formatting
   - `grep` / `glob`: Structured search results
   - `thinking`: Collapsible thought processes
   - Other tools: Contextual formatting based on tool type

### API Endpoints

- `GET /` - Conversation list page
- `GET /c/:id` - Individual conversation view (supports both full and short IDs)
- `GET /api/conversations` - REST API for conversation listing with pagination
- `GET /api/conversations/:id` - REST API for individual conversation data
- `GET /static/*` - Static assets (CSS, JS, images)

### Implementation Strategy

1. **Backend Implementation**
   - Create new `cmd/kodelet/web.go` for the web server command
   - Implement HTTP handlers that reuse existing conversation store logic
   - Add conversation ID resolution for short IDs using `GenerateID()` logic
   - Create tool result rendering service that leverages existing `UserFacing()` methods

2. **Frontend Implementation**
   - Use Go's `embed` package to bundle HTML, CSS, and JS into the binary
   - Implement responsive design with Tailwind CSS and DaisyUI components
   - Create tool-specific renderers that parse and format `UserFacing()` output
   - Add client-side search and filtering capabilities

3. **Asset Management**
   - Bundle all static assets using Go's `embed` package
   - Minify CSS and JS during build process
   - Use CDN links for external dependencies (Tailwind, DaisyUI) with local fallbacks

### Tool-Specific Rendering

Each tool's output will be rendered based on parsing the `UserFacing()` string:

```go
type ToolRenderer interface {
    CanRender(toolName string) bool
    Render(toolName string, userFacingResult string) RenderedContent
}

type RenderedContent struct {
    HTML        string
    CSS         string
    JavaScript  string
    Collapsible bool
}
```

#### Specific Tool Renderers

- **File Operations** (`file_read`, `file_write`): 
  - Parse file content and apply syntax highlighting
  - Make code blocks foldable
  - Show file paths and line numbers

- **File Edit** (`file_edit`):
  - Parse diff output and render with proper diff highlighting
  - Show before/after sections
  - Highlight changed lines

- **Bash** (`bash`):
  - Format command and output separately
  - Apply terminal-like styling
  - Handle ANSI color codes

- **Search Tools** (`grep`, `glob`):
  - Structure results in searchable format
  - Highlight matches
  - Group by file

### Data Flow

1. **Conversation List**:
   ```
   Browser → GET /api/conversations → ConversationStore.Query() → JSON Response
   ```

2. **Individual Conversation**:
   ```
   Browser → GET /api/conversations/:id → ConversationStore.Load() → 
   ExtractMessages() → Render with UserFacing() → JSON Response
   ```

3. **Tool Result Rendering**:
   ```
   UserFacingToolResult → ToolRenderer → RenderedContent → HTML/CSS/JS
   ```

### Security Considerations

- Read-only access prevents modification of conversation data
- No authentication needed since conversations are local to the user
- Server bound to localhost only
- Input sanitization for conversation IDs
- XSS prevention in rendered tool outputs

## Consequences

### Advantages

- **Enhanced User Experience**: Visual browsing and reading of conversations
- **Tool-Specific Formatting**: Each tool's output is optimally presented
- **Responsive Design**: Works on different screen sizes
- **Zero Dependencies**: Self-contained binary with embedded assets
- **Maintains Existing Architecture**: Reuses conversation store and tool result system

### Challenges

- **Bundle Size**: Embedding web assets increases binary size
- **Maintenance**: Additional frontend code to maintain
- **Tool Renderer Complexity**: Need to parse `UserFacing()` strings reliably
- **Browser Compatibility**: Must work across different browsers

### Trade-offs

- **Performance vs. Convenience**: Slightly larger binary but no external dependencies
- **Feature Scope**: Read-only limits functionality but reduces complexity
- **Rendering Approach**: Server-side rendering vs. client-side for tool results

## Implementation Plan

### Phase 1: Basic Infrastructure
1. Create web server command and basic HTTP handlers
2. Implement conversation listing API endpoint
3. Create basic HTML templates with Tailwind CSS
4. Add asset embedding and build process

### Phase 2: Core Functionality
1. Implement individual conversation viewer
2. Add conversation ID resolution (short ID support)
3. Create basic tool result rendering
4. Add search and filtering capabilities

### Phase 3: Enhanced Tool Rendering
1. Implement file operation renderers (syntax highlighting, folding)
2. Add diff rendering for file edits
3. Create bash command/output formatting
4. Add search result structuring

### Phase 4: Polish and Optimization
1. Add loading states and error handling
2. Implement responsive design improvements
3. Add keyboard shortcuts and accessibility
4. Performance optimization and caching

### Phase 5: Documentation and Testing
1. Update CLI documentation
2. Add usage examples
3. Create integration tests
4. User acceptance testing

## References

- [Existing conversation.go implementation](./cmd/kodelet/conversation.go)
- [Conversation storage in pkg/conversations](./pkg/conversations/)
- [Tool result UserFacing() methods](./pkg/tools/)
- [Tailwind CSS Documentation](https://tailwindcss.com/docs)
- [DaisyUI Component Library](https://daisyui.com/)
- [Go embed package](https://pkg.go.dev/embed)