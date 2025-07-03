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
2. Provides tool-specific rendering using structured tool result data (see ADR 015)
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

- `GET /` - Conversation list page (HTML)
- `GET /c/:id` - Individual conversation page (HTML shell)
- `GET /api/conversations` - REST API for conversation listing with pagination
- `GET /api/conversations/:id` - REST API for conversation metadata and message structure
- `GET /api/conversations/:id/tools/:toolCallId` - REST API for individual tool results
- `GET /static/*` - Static assets (CSS, JS, images)

### Implementation Strategy

1. **Backend Implementation**
   - Create new `cmd/kodelet/web.go` for the web server command
   - Implement JSON API handlers that serve structured tool result data
   - Add conversation ID resolution for short IDs using `GenerateID()` logic

2. **Frontend Implementation**
   - Use Go's `embed` package to bundle HTML shell and JavaScript assets
   - Implement JavaScript tool renderers for each tool type using structured data
   - Create responsive design with Tailwind CSS and DaisyUI components
   - Add client-side state management and routing

3. **Asset Management**
   - Bundle all static assets using Go's `embed` package
   - Minify CSS and JS during build process
   - Use CDN links for external dependencies (Tailwind, DaisyUI) with local fallbacks

### Frontend Technology Stack

**Core Frameworks:**
- **Tailwind CSS** - Utility-first CSS framework for styling
- **DaisyUI** - Component library built on Tailwind for consistent UI elements
- **Alpine.js** or **Lit** - Lightweight JavaScript framework for component rendering
- **Prism.js** or **Highlight.js** - Syntax highlighting for code blocks
- **Diff2html** - Diff visualization library for file edits

**Specialized Libraries:**
- **Monaco Editor** (web version) - Advanced code viewing and diff rendering
- **Markdown-it** - Markdown to HTML conversion with plugin support
- **Ansi-to-html** - Convert ANSI terminal colors to HTML
- **Chart.js** or **D3.js** - For todo progress visualization and statistics
- **Fuse.js** - Client-side fuzzy search for conversations and content

**API & Utility Libraries:**
- **Fetch API / Axios** - HTTP client for API communication
- **Date-fns** - Date formatting and manipulation
- **Copy-to-clipboard** - Easy copy functionality for code/commands
- **Intersection Observer API** - Lazy loading and scroll optimization
- **ResizeObserver API** - Responsive behavior for dynamic content

### Tool-Specific Rendering

The web UI will receive structured metadata via JSON API and render client-side using JavaScript (see ADR 015 for data structures):

```go
// Backend API endpoint
GET /api/conversations/:id/tools/:toolCallId
{
  "toolName": "file_read",
  "success": true,
  "timestamp": "2025-01-03T10:30:00Z",
  "metadata": {
    "filePath": "/path/to/file.go",
    "offset": 5,
    "lines": ["package main", "import fmt", ...],
    "language": "go",
    "truncated": false
  }
}
```

```javascript
// Frontend rendering registry
class ToolRendererRegistry {
  constructor() {
    this.renderers = new Map();
    this.registerDefaultRenderers();
  }

  registerDefaultRenderers() {
    this.register('file_read', new FileReadRenderer());
    this.register('file_write', new FileWriteRenderer());
    this.register('file_edit', new FileEditRenderer());
    this.register('bash', new BashRenderer());
    this.register('grep_tool', new GrepRenderer());
    // ... register all other tools
  }

  register(toolName, renderer) {
    this.renderers.set(toolName, renderer);
  }

  render(toolResult) {
    const renderer = this.renderers.get(toolResult.toolName);
    if (!renderer) {
      return this.renderFallback(toolResult);
    }
    return renderer.render(toolResult);
  }

  renderFallback(toolResult) {
    if (!toolResult.success) {
      return `<div class="error">Error (${toolResult.toolName}): ${toolResult.error}</div>`;
    }
    return `<div class="unknown-tool">Tool Result (${toolResult.toolName}): ${JSON.stringify(toolResult.metadata)}</div>`;
  }
}

// Example file read renderer
class FileReadRenderer {
  render(toolResult) {
    if (!toolResult.success) {
      return `<div class="error">${toolResult.error}</div>`;
    }
    
    const { metadata } = toolResult;
    return createCodeBlock({
      lines: metadata.lines,
      language: metadata.language,
      startLine: metadata.offset,
      filePath: metadata.filePath,
      interactive: true // Enable folding, copy, etc.
    });
  }
}

// Usage in conversation viewer
const rendererRegistry = new ToolRendererRegistry();
const toolResultElement = rendererRegistry.render(toolResult);
```

**Client-Side Renderer Dispatch:**
1. **Tool Name Mapping**: JavaScript registry maps `toolResult.toolName` to renderer classes
2. **Dynamic Rendering**: Each renderer produces DOM elements or HTML strings  
3. **Fallback Support**: Unknown tools get basic JSON display to prevent crashes
4. **Type-Safe Access**: Renderers access `toolResult.metadata` with known structure

**Benefits of Client-Side Rendering:**
- Rich interactive features (code folding, syntax highlighting, filtering)
- Progressive loading for large datasets
- Modern web app UX without page refreshes
- Clean API separation between data and presentation

#### File Operations Tools

**`file_read`** - Code file viewer with syntax highlighting:
- **Header**: File path with icon, offset information
- **Content**: Syntax-highlighted code block with line numbers
- **Features**: Collapsible, copy button, language detection from metadata
- **Data**: Direct access to `FileReadMetadata{FilePath, Offset, Lines, Language, Truncated}`

**`file_write`** - File creation/overwrite display:
- **Header**: "File Written" with path and success indicator
- **Content**: Syntax-highlighted code block showing written content
- **Features**: Collapsible, shows file size, modification timestamp
- **Pattern**: Parse "File Written: {path}\n{content_with_line_numbers}"

**`file_edit`** - Diff viewer with before/after comparison:
- **Header**: "File Edit" with path and line range (Lines X-Y)
- **Content**: Side-by-side or unified diff view with:
  - Red highlighting for removed lines (-)
  - Green highlighting for added lines (+)
  - Context lines in neutral color
- **Features**: Switch between unified/split view, syntax highlighting in diff
- **Data**: Structured `DiffHunk[]` with typed `DiffLine{Type, Content, LineNumber}`

**`file_multi_edit`** - Multiple occurrence editor:
- **Header**: "File Multi Edit" with replacement count
- **Content**: Similar to file_edit but shows aggregate diff
- **Features**: Statistics of replacements made
- **Data**: Replacement count and structured diff metadata

#### Search & Discovery Tools

**`grep_tool`** - Search results with file grouping:
- **Header**: Search pattern, path, include pattern, match count
- **Content**: Grouped by file with:
  - File icons and relative paths
  - Line numbers with matched content highlighted
  - Pattern matches in bold/colored
- **Features**: Expandable file groups, jump to file links
- **Data**: `SearchResult[]` with `SearchMatch{LineNumber, Content, MatchStart, MatchEnd}`

**`glob_tool`** - File listing with metadata:
- **Header**: Glob pattern, search path, file count
- **Content**: File list with:
  - File type icons
  - Relative paths as clickable links
  - File modification times
- **Features**: Filter by file type, sort options
- **Data**: `FileInfo[]` with `{Path, Size, ModTime, Type, Language}`

#### Command Execution Tools

**`bash`** - Terminal-style command output:
- **Header**: "Command" with the executed command
- **Content**: Terminal-style output with:
  - Command in monospace with shell prompt styling
  - Output in console font with preserved formatting
  - Error messages in red
- **Features**: Copy command button, expand/collapse output
- **Data**: `BashMetadata{Command, ExitCode, Output, ExecutionTime, WorkingDir}`

**Background `bash`** - Process management display:
- **Header**: "Background Process" with status indicator
- **Content**: Process details in card format:
  - PID and command
  - Log file path as clickable link
  - Status (running/stopped) with color coding
- **Features**: Log viewer modal

#### Content Processing Tools

**`web_fetch`** - Web content display:
- **Header**: "Web Fetch" with URL and content type
- **Content**: Varies by content type:
  - Saved files: File path link + syntax highlighted preview
  - HTML/Markdown: Rendered markdown with proper styling
  - With prompt: AI-extracted content in formatted blocks
- **Features**: External link icon, content type badges
- **Data**: `WebFetchMetadata{URL, ContentType, SavedPath, Prompt, ProcessedType}`

**`image_recognition`** - Image analysis display:
- **Header**: "Image Recognition" with image path/URL
- **Content**: 
  - Image thumbnail (if local file)
  - Analysis prompt in quote style
  - AI analysis results in formatted text
- **Features**: Image modal view, prompt highlighting
- **Data**: `ImageRecognitionMetadata{ImagePath, ImageType, Prompt, Analysis, ImageSize}`

#### Organization & Meta Tools

**`thinking`** - Collapsible thought process:
- **Header**: "Thought" with brain icon
- **Content**: Thought content in quote/bubble style
- **Features**: Always collapsible, subtle background styling
- **Data**: `ThinkingMetadata{Thought, Category}`

**`todo_read`/`todo_write`** - Task management interface:
- **Header**: "Todo List" with task statistics
- **Content**: Interactive todo table with:
  - Status icons (✓ ⏳ ❌ ⏸️)
  - Priority badges (high/medium/low with colors)
  - Task content with proper wrapping
- **Features**: Status filtering, priority sorting, progress indicators
- **Data**: `TodoMetadata{Action, TodoList[], Statistics}`

**`subagent`** - Delegated task results:
- **Header**: "Sub-agent" with model strength indicator
- **Content**: Question and response in conversation style
- **Features**: Nested conversation styling, model indicator badge

**`batch`** - Aggregated tool results:
- **Header**: "Batch Operation" with description and tool count
- **Content**: Each sub-tool result rendered with its own renderer
- **Features**: Collapsible sub-results, execution summary
- **Data**: `BatchMetadata{Description, SubResults[], ExecutionTime, SuccessCount, FailureCount}`

#### Browser Automation Tools

**`browser_navigate`** - Navigation status:
- **Header**: "Navigation" with success/failure indicator (✅/❌)
- **Content**: URL and page title with external link icon
- **Features**: Clickable URL, success/failure styling

**`browser_screenshot`** - Image capture display:
- **Header**: "Screenshot" with dimensions and file info
- **Content**: Inline image preview with metadata
- **Features**: Full-size modal, download link, dimension display

**`browser_*`** (click, type, etc.) - Action results:
- **Header**: Action type with status emoji
- **Content**: Action details and results
- **Features**: Success/failure color coding

#### Background Process Tools

**`view_background_processes`** - Process status table:
- **Header**: "Background Processes" with active count
- **Content**: Responsive table with:
  - PID, Status (with color indicators), Start Time
  - Command (truncated with tooltip)
  - Log file links
- **Features**: Filterable, sortable

#### Error Handling

For all tools, error states display:
- Red error indicator in header
- Error message in alert-style container
- Suggested actions when appropriate
- No collapsing for errors (always visible)

#### General Features

All tool renderers include:
- **Responsive design** that works on mobile/tablet
- **Dark/light mode** support via CSS variables
- **Copy functionality** for code/command content
- **Accessibility** with proper ARIA labels and keyboard navigation
- **Performance** with lazy loading for large content

### Data Flow

1. **Conversation List**:
   ```
   Browser → GET /api/conversations → ConversationStore.Query() → JSON Response
   Frontend → Render conversation cards with metadata
   ```

2. **Individual Conversation**:
   ```
   Browser → GET /c/:id → HTML shell with JavaScript app
   JavaScript → GET /api/conversations/:id → Conversation metadata + message structure
   JavaScript → GET /api/conversations/:id/tools/:toolCallId → Individual tool results
   Frontend → Client-side rendering of messages and tool results
   ```

3. **Tool Result Rendering**:
   ```
   StructuredToolResult → JSON API → Frontend JavaScript → DOM manipulation
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
- **Renderer Implementation**: Need to implement web renderers for all tool types
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
1. **File Operations**: Implement syntax highlighting, diff viewers, code folding
2. **Search Tools**: Create file grouping, match highlighting, interactive filters
3. **Command Execution**: Terminal styling, ANSI color support, process status display
4. **Content Processing**: Markdown rendering, image handling, AI result formatting
5. **Organization Tools**: Todo list interfaces, thought bubble styling, sub-agent conversations
6. **Browser Tools**: Success/failure indicators, image previews, action status displays

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

- [ADR 015: Structured Tool Result Storage](./015-structured-tool-result-storage.md)
- [Existing conversation.go implementation](./cmd/kodelet/conversation.go)
- [Conversation storage in pkg/conversations](./pkg/conversations/)
- [Tool result interfaces](./pkg/tools/)
- [Tailwind CSS Documentation](https://tailwindcss.com/docs)
- [DaisyUI Component Library](https://daisyui.com/)
- [Go embed package](https://pkg.go.dev/embed)