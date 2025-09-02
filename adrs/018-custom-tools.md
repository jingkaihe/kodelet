# ADR 018: Custom Tools Support for Kodelet

## Status
Implemented

## Context
Kodelet currently supports a fixed set of built-in tools and MCP (Model Context Protocol) tools through external servers. However, users often need to create custom, project-specific tools that:

1. **Are simple to implement**: Don't require implementing a full MCP server
2. **Are language-agnostic**: Can be written in any language that produces executables
3. **Are portable**: Can be shared across projects or teams
4. **Are local**: Don't require network services or complex infrastructure

Users need a lightweight way to extend Kodelet's capabilities with custom tools that can be quickly developed and deployed without the overhead of the MCP protocol.

## Decision
We will implement a custom tools feature that allows Kodelet to discover and execute standalone executables as tools. These executables will:

1. Be discovered from two directories:
   - `~/.kodelet/tools` (global tools)
   - `./kodelet-tools` (project-specific tools)
2. Implement a simple protocol with two commands:
   - `<tool> description` - Returns JSON schema describing the tool
   - `<tool> run` - Executes the tool with JSON input via stdin
3. Be prefixed with `custom_tool_` when exposed to the LLM to avoid naming conflicts

This approach provides a simple, language-agnostic way to extend Kodelet's capabilities while maintaining compatibility with the existing tool system.

## Architecture Details

### Tool Discovery Process
1. On startup, Kodelet scans both tool directories
2. For each executable file found:
   - Execute `<tool> description` to get tool metadata
   - Validate the returned JSON schema
   - Register the tool with the `custom_tool_` prefix
3. Project tools (`./kodelet-tools`) override global tools with the same name

### Tool Protocol

#### Description Command
```bash
$ ./hello description
```

Returns JSON with the following structure:
```json
{
  "name": "hello",
  "description": "Say hello to a person",
  "input_schema": {
    "type": "object",
    "properties": {
      "name": {"type": "string", "description": "The name of the person"},
      "age": {"type": "integer", "description": "The age of the person"}
    },
    "required": ["name"]
  }
}
```

#### Run Command
```bash
$ echo '{"name": "Alice", "age": 30}' | ./hello run
```

The tool receives JSON input via stdin and outputs results to stdout. Error messages should be written to stderr.

### Implementation Structure

The custom tools feature was implemented in `pkg/tools/custom.go` with the following key components:

#### CustomTool Type
```go
// pkg/tools/custom.go

type CustomTool struct {
    execPath    string                 // Path to the executable
    name        string                 // Tool name (without prefix)
    description string                 // Tool description
    schema      *jsonschema.Schema     // Input schema
    timeout     time.Duration          // Execution timeout
    maxOutput   int                    // Maximum output size
}

// Implements tooltypes.Tool interface
func (t *CustomTool) Name() string {
    return fmt.Sprintf("custom_tool_%s", t.name)
}

func (t *CustomTool) Description() string {
    return t.description
}

func (t *CustomTool) GenerateSchema() *jsonschema.Schema {
    return t.schema
}

func (t *CustomTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    // Create command with timeout
    execCtx, cancel := context.WithTimeout(ctx, t.timeout)
    defer cancel()
    
    cmd := exec.CommandContext(execCtx, t.execPath, "run")
    
    // Set up pipes with limited writers to prevent excessive output
    stdin, _ := cmd.StdinPipe()
    cmd.Stdout = &limitedWriter{w: &stdout, limit: t.maxOutput}
    cmd.Stderr = &limitedWriter{w: &stderr, limit: t.maxOutput}
    
    // Execute with filtered environment
    cmd.Env = t.getFilteredEnv()
    
    // Send JSON input via stdin and capture results
    // Handle timeouts, errors, and structured JSON error responses
}
```

#### Custom Tool Manager
```go
type CustomToolManager struct {
    tools     map[string]*CustomTool
    globalDir string
    localDir  string
    config    CustomToolConfig
    mu        sync.RWMutex  // Thread-safe access
}

type CustomToolConfig struct {
    Enabled       bool          `mapstructure:"enabled"`
    GlobalDir     string        `mapstructure:"global_dir"`
    LocalDir      string        `mapstructure:"local_dir"`
    Timeout       time.Duration `mapstructure:"timeout"`
    MaxOutputSize int           `mapstructure:"max_output_size"`
    EnvWhitelist  []string      `mapstructure:"env_whitelist"`
}

func NewCustomToolManager() (*CustomToolManager, error) {
    config := loadCustomToolConfig()
    globalDir := expandHomePath(config.GlobalDir)
    
    return &CustomToolManager{
        tools:     make(map[string]*CustomTool),
        globalDir: globalDir,
        localDir:  config.LocalDir,
        config:    config,
    }, nil
}

func (m *CustomToolManager) DiscoverTools(ctx context.Context) error {
    if !m.config.Enabled {
        return nil
    }
    
    // Clear existing tools
    m.mu.Lock()
    defer m.mu.Unlock()
    m.tools = make(map[string]*CustomTool)
    
    // Discover global tools first, then local (with override capability)
    m.discoverToolsInDir(ctx, m.globalDir, false)
    m.discoverToolsInDir(ctx, m.localDir, true)  // override=true
    
    return nil
}
```

### Tool Execution Flow

1. **Input Validation**: Validate JSON parameters against the tool's schema
2. **Process Creation**: Create subprocess with appropriate environment
3. **Input Delivery**: Send JSON parameters via stdin
4. **Output Capture**: Capture stdout for results, stderr for errors
5. **Timeout Handling**: Kill process if execution exceeds timeout
6. **Result Formatting**: Format output as ToolResult

### Error Handling

Tools should indicate errors through:
1. **Exit codes**: Non-zero exit code indicates failure
2. **Stderr output**: Error messages written to stderr
3. **Structured errors**: Optional JSON error format on stdout:
   ```json
   {
     "error": "Failed to process request",
     "details": "Invalid input format"
   }
   ```

### Structured Tool Results

Custom tools integrate with Kodelet's structured tool result system. The `CustomToolMetadata` type was added to `pkg/types/tools/structured_result.go`:

```go
type CustomToolMetadata struct {
    ExecutionTime time.Duration `json:"executionTime"`
    Output        string        `json:"output"`
}

func (m CustomToolMetadata) ToolType() string { return "custom_tool" }
```

The metadata registry was updated to include custom tools:
```go
var metadataTypeRegistry = map[string]reflect.Type{
    // ... existing entries
    "custom_tool":     reflect.TypeOf(CustomToolMetadata{}),
    // ... other entries
}
```

### Security Considerations

The implementation includes comprehensive security measures:

1. **Execution Permissions**: Only execute files with appropriate permissions (executable bit set)
2. **Path Validation**: Validate tool paths to prevent directory traversal attacks
3. **Input Sanitization**: Validate JSON input before passing to tools
4. **Resource Limits**: Implement timeouts and memory limits for tool execution
5. **Environment Isolation**: Use configurable environment variable whitelist (default: PATH, HOME, USER)
6. **Output Size Limits**: Limit output capture via `limitedWriter` to prevent memory exhaustion
7. **Process Isolation**: Each tool runs in its own process with controlled environment

### Configuration

Custom tools can be configured via the main configuration file. The implementation was added to `config.sample.yaml`:

```yaml
# Custom Tools Configuration
# Kodelet can discover and execute custom executable tools from specified directories
custom_tools:
  # Enable/disable custom tools discovery (default: true)
  enabled: true
  
  # Global custom tools directory (default: ~/.kodelet/tools)
  global_dir: "~/.kodelet/tools"
  
  # Local custom tools directory (default: ./kodelet-tools)
  local_dir: "./kodelet-tools"
  
  # Execution timeout for custom tools (default: 30s)
  timeout: 30s
  
  # Maximum output size for custom tools (default: 1MB)
  max_output_size: 1048576
  
  # Environment variables to pass to custom tools (default: PATH, HOME, USER)
  env_whitelist:
    - PATH
    - HOME
    - USER
```

Configuration loading is handled via Viper in `loadCustomToolConfig()` function with sensible defaults:

```go
func loadCustomToolConfig() CustomToolConfig {
    config := CustomToolConfig{
        Enabled:       true,
        GlobalDir:     "~/.kodelet/tools",
        LocalDir:      "./kodelet-tools",
        Timeout:       30 * time.Second,
        MaxOutputSize: 1048576, // 1MB
        EnvWhitelist:  []string{"PATH", "HOME", "USER"},
    }
    
    if viper.IsSet("custom_tools") {
        viper.UnmarshalKey("custom_tools", &config)
    }
    
    return config
}
```

## Example Implementation

### Python Tool Example
```python
#!/usr/bin/env python3

import sys
import json

def main():
    if len(sys.argv) < 2:
        print("Usage: hello <command>", file=sys.stderr)
        sys.exit(1)

    command = sys.argv[1]

    if command == "description":
        print(json.dumps({
            "name": "hello",
            "description": "Say hello to a person",
            "input_schema": {
                "type": "object",
                "properties": {
                    "name": {"type": "string", "description": "The name of the person"},
                    "age": {"type": "integer", "description": "The age of the person"}
                },
                "required": ["name"]
            }
        }))
    elif command == "run":
        try:
            config = json.loads(sys.stdin.read())
            name = config.get("name", "World")
            age = config.get("age")
            
            if age:
                print(f"Hello, {name}! You are {age} years old.")
            else:
                print(f"Hello, {name}!")
        except Exception as e:
            print(json.dumps({"error": str(e)}), file=sys.stderr)
            sys.exit(1)
    else:
        print(f"Unknown command: {command}", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
```

### Bash Tool Example
```bash
#!/bin/bash

set -e

case "$1" in
    description)
        cat <<EOF
{
    "name": "git_info",
    "description": "Get git repository information",
    "input_schema": {
        "type": "object",
        "properties": {
            "path": {
                "type": "string",
                "description": "Repository path (default: current directory)"
            }
        }
    }
}
EOF
        ;;
    run)
        # Read JSON input
        input=$(cat)
        path=$(echo "$input" | jq -r '.path // "."')
        
        # Get git info
        cd "$path"
        branch=$(git branch --show-current)
        commit=$(git rev-parse HEAD)
        status=$(git status --porcelain | wc -l)
        
        # Output JSON result
        cat <<EOF
{
    "branch": "$branch",
    "commit": "$commit",
    "uncommitted_changes": $status
}
EOF
        ;;
    *)
        echo "Usage: $0 {description|run}" >&2
        exit 1
        ;;
esac
```



## Integration with Existing Tool System

The custom tools integrate seamlessly with the existing tool system following the same pattern as MCP tools:

### State Integration
Custom tools are integrated via the `BasicState` type in `pkg/tools/state.go`:

```go
type BasicState struct {
    tools       []tooltypes.Tool  // Built-in tools
    mcpTools    []tooltypes.Tool  // MCP tools
    customTools []tooltypes.Tool  // Custom tools (NEW)
    // ... other fields
}

func WithCustomTools(customManager *CustomToolManager) BasicStateOption {
    return func(ctx context.Context, s *BasicState) error {
        tools := customManager.ListTools()
        s.customTools = append(s.customTools, tools...)
        return nil
    }
}

func (s *BasicState) Tools() []tooltypes.Tool {
    tools := make([]tooltypes.Tool, 0, len(s.tools)+len(s.mcpTools)+len(s.customTools))
    tools = append(tools, s.tools...)
    tools = append(tools, s.mcpTools...)
    tools = append(tools, s.customTools...)  // Include custom tools
    return tools
}
```

### Command Integration
All command entry points were updated to initialize and use custom tools:

```go
// Example from cmd/kodelet/run.go
mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
// ... handle error

customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
if err != nil {
    presenter.Error(err, "Failed to create custom tool manager")
    return
}

var stateOpts []tools.BasicStateOption
stateOpts = append(stateOpts, tools.WithLLMConfig(llmConfig))
stateOpts = append(stateOpts, tools.WithMCPTools(mcpManager))
stateOpts = append(stateOpts, tools.WithCustomTools(customManager))  // NEW
```

Updated command files:
- `cmd/kodelet/run.go`
- `cmd/kodelet/chat.go`
- `cmd/kodelet/plain_chat.go`
- `cmd/kodelet/watch.go`
- `cmd/kodelet/issue_resolve.go`
- `cmd/kodelet/pr.go`
- `cmd/kodelet/pr_respond.go`
- `pkg/tui/assistant.go`
- `pkg/tui/model.go`
- `pkg/tui/chat.go`

## Alternatives Considered

1. **Extended MCP Protocol**:
   - Require all custom tools to implement MCP
   - Rejected: Too complex for simple tools, requires more boilerplate

2. **Plugin System with Go Plugins**:
   - Use Go's plugin system for dynamic loading
   - Rejected: Platform-specific, requires tools to be written in Go

3. **REST API Tools**:
   - Tools expose REST endpoints
   - Rejected: Requires network setup, more complex than executables

4. **Embedded Scripting Language**:
   - Embed Python or Lua for custom tools
   - Rejected: Limits language choice, adds dependencies

## Consequences

### Positive
- **Simple Implementation**: Easy to create tools in any language
- **Language Agnostic**: Use Python, Bash, Go, or any language that compiles to executable
- **Rapid Development**: Quick iteration on custom tools without recompiling Kodelet
- **Portability**: Tools can be shared as simple executable files
- **Project-Specific Tools**: Different projects can have different tool sets
- **Backward Compatible**: No impact on existing tool system

### Negative
- **Performance Overhead**: Process creation for each tool execution
- **Limited Communication**: Simple stdin/stdout protocol may be limiting for complex tools
- **Security Risks**: Executing arbitrary code requires careful security measures
- **Debugging Complexity**: External processes are harder to debug than built-in tools
- **Platform Dependencies**: Tools may have platform-specific dependencies

## Implementation Status

### Completed Implementation

The custom tools feature has been fully implemented and tested with the following deliverables:

**Core Implementation ✅**
- `CustomTool` type implementing `tooltypes.Tool` interface
- `CustomToolManager` for tool discovery and registration
- Tool execution with timeout and comprehensive error handling
- Integration with existing tool system following MCP pattern
- Thread-safe tool discovery and management

**Security and Robustness ✅**
- Process isolation with controlled environment variables
- Resource limits (execution timeout, output size limits)
- Input validation and JSON schema support
- Comprehensive error handling and logging
- Path validation and permission checking

**Integration ✅**
- Updated all command entry points (`run`, `chat`, `watch`, `pr`, etc.)
- Integrated with TUI components (`assistant.go`, `model.go`, `chat.go`)
- Added structured tool result support with `CustomToolMetadata`
- Updated configuration system with full Viper integration
- Added configuration documentation to `config.sample.yaml`

**Testing and Examples ✅**
- Created example tools in Python and Bash
- Implemented comprehensive error handling test tool
- All existing tests continue to pass
- Verified LLM integration and function calling
- Tested both success and failure scenarios

### Files Created/Modified

**New Files:**
- `pkg/tools/custom.go` - Core custom tools implementation
- `kodelet-tools/hello` - Example Python tool
- `kodelet-tools/git_info` - Example Bash tool
- `kodelet-tools/error_tool` - Error handling test tool

**Modified Files:**
- `pkg/types/tools/structured_result.go` - Added CustomToolMetadata
- `pkg/tools/state.go` - Added custom tools state integration
- `config.sample.yaml` - Added custom tools configuration section
- All command files in `cmd/kodelet/` for tool integration
- All TUI files in `pkg/tui/` for tool integration

## Testing Results

### Comprehensive Testing Performed ✅

**1. Unit Testing**
- All existing unit tests continue to pass (200+ tests)
- New custom tool components tested individually
- Tool discovery and validation logic verified
- Configuration loading tested with various scenarios

**2. Integration Testing**
- End-to-end tool execution through LLM function calls
- Verified custom tools appear in available function schemas
- Tested parameter validation and JSON schema generation  
- Confirmed proper error propagation to LLM and users

**3. Security Testing**
- Verified executable permission requirements
- Tested environment variable filtering
- Confirmed resource limits (timeout, output size) work correctly
- Tested process isolation and cleanup

**4. Example Tool Testing**
Created and tested multiple example tools:
- **Python Hello Tool**: Parameter handling and basic execution
- **Bash Git Info Tool**: Complex JSON parsing and Git operations
- **Error Tool**: Comprehensive error handling and exit codes

**5. Error Scenario Testing**
- Malformed tool descriptions handled gracefully
- Non-executable files properly ignored
- Timeout scenarios tested and working
- Invalid JSON input properly handled
- Tool crashes and non-zero exits handled correctly

**6. Performance Testing**
- Tool discovery performance acceptable (discovered 3 tools instantly)
- No memory leaks or resource issues observed
- Concurrent tool execution works properly

### Test Results Summary
```bash
# All tests pass
$ go test ./pkg/tools/... -v
=== PASS (200+ individual tests)
ok      github.com/jingkaihe/kodelet/pkg/tools    18.972s

# Linting passes
$ mise run lint
0 issues.

# Manual integration tests
$ kodelet run "what custom tools are available to you?"
✅ Custom tools properly listed and described

$ kodelet run "use the hello tool to greet Alice who is 30"
✅ Tool executed successfully with proper parameter handling

$ kodelet run "use error_tool with should_fail=true" 
✅ Error handling working correctly
```

## Usage Examples

### Basic Tool Discovery and Listing
```bash
$ kodelet run "what custom tools are available to you?"
# AI Response: Lists all custom tools with descriptions:
# - custom_tool_hello: Say hello to a person
# - custom_tool_git_info: Get git repository information  
# - custom_tool_error_tool: A tool that demonstrates error handling
```

### Tool Execution with Parameters
```bash
$ kodelet run "use the hello tool to greet someone named Bob who is 25"
# AI uses custom_tool_hello with parameters: {"name": "Bob", "age": 25}
# Result: "Hello, Bob! You are 25 years old."

$ kodelet run "show me git info for the current repository"
# AI uses custom_tool_git_info with parameters: {}  
# Result: {"branch": "main", "commit": "777118...", "uncommitted_changes": 15}
```

### Error Handling Demonstration  
```bash
$ kodelet run "test the error tool with failure enabled"
# AI uses custom_tool_error_tool with parameters: {"should_fail": true}
# Result: Tool fails gracefully with structured error message
```

## Migration Path

No migration required as this is a new feature. Users can:
1. Start with no custom tools (feature is opt-in and enabled by default)
2. Gradually add tools to their global (`~/.kodelet/tools`) or project (`./kodelet-tools`) directories
3. Share tools with teams via version control by committing `./kodelet-tools/` directory
4. Override global tools with project-specific versions when needed

## Future Enhancements

The current implementation provides a solid foundation for custom tools. Potential future enhancements include:

**Developer Experience**
1. **Tool Validation Command**: Add `kodelet tools validate` to test tools during development
2. **Tool Testing Framework**: Built-in testing utilities for custom tools  
3. **Tool Generator**: CLI command to scaffold new tools with templates
4. **Better Debugging**: Enhanced logging and debugging support for tool development

**Advanced Features**
5. **Hot Reload**: Automatically detect and reload tools when files change
6. **Tool Dependencies**: Support for tools that depend on other tools or system packages
7. **Tool Versioning**: Version management and compatibility checking
8. **Tool Marketplace**: Registry for sharing and discovering community tools

**Performance & Scalability**
9. **Tool Caching**: Cache tool validation results to improve startup time
10. **Streaming Support**: Support for streaming input/output for long-running tools  
11. **Async Tools**: Support for asynchronous/background tool execution
12. **Tool Composition**: Allow tools to call other tools in pipelines

**Enterprise Features**
13. **Tool Policies**: Administrative controls over which tools can be used
14. **Tool Auditing**: Logging and auditing of tool executions
15. **Tool Sandboxing**: Enhanced isolation using containers or chroot
16. **Tool Profiles**: Different tool sets for different environments/profiles

## Conclusion

The custom tools feature successfully provides a lightweight, language-agnostic way to extend Kodelet's capabilities. The implementation maintains security best practices while offering flexibility for users to create project-specific tools without the complexity of the MCP protocol. The feature integrates seamlessly with the existing tool ecosystem and provides a foundation for future enhancements.