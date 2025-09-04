# Agent Context File Enhancement Design

## Executive Summary

This document outlines the design for enhancing the AGENTS.md/KODELET.md context file system in kodelet to be more intelligent and flexible. The enhancement will implement context file discovery at the State layer, allowing kodelet to dynamically discover and use context files based on file access patterns, supporting multi-level context hierarchies and dynamic prompt regeneration.

## Current State

### How it Works Today
- Context files (AGENTS.md or KODELET.md) are loaded only from the current working directory
- System prompts are generated once at the beginning of an LLM conversation
- The prompt remains static throughout the entire conversation
- Context loading happens in `pkg/sysprompt/context.go:loadContexts()`
- System prompt generation occurs in `pkg/llm/{anthropic,openai}/*.go` at the start of `Send()` method
- The `tooltypes.State` interface tracks file access but doesn't determine context relevance

### Limitations
1. Single-directory context limitation
2. No awareness of project structure or file access patterns
3. Static prompts don't adapt to conversation evolution
4. No support for user-wide default contexts
5. No multi-context composition for complex projects

## Proposed Design

### Architecture Overview

The context discovery logic will be implemented at the **State layer** rather than the sysprompt package. This provides better separation of concerns:
- **State Layer**: Responsible for tracking file access AND determining relevant context files
- **Sysprompt Layer**: Simply formats the contexts provided by State into the prompt
- **LLM Layer**: Queries State for contexts and passes them to sysprompt

### Context File Discovery Strategy

The State will implement a hierarchical context discovery with the following priority order:

1. **Working Directory Context** (Highest Priority)
   - Check for `AGENTS.md` or `KODELET.md` in current working directory
   - This maintains backward compatibility

2. **Access-Based Context Discovery** (Project-Specific)
   - Track files accessed/modified during conversation via `State.FileLastAccess()`
   - For each accessed file, walk up directory tree to find nearest context file
   - Example: If `./pkg/llm/config.go` is modified, check:
     - `./pkg/llm/AGENTS.md` or `./pkg/llm/KODELET.md`
     - `./pkg/AGENTS.md` or `./pkg/KODELET.md`
     - `./AGENTS.md` or `./KODELET.md`
   - Collect all unique context files found

3. **User Home Context**
   - Check `~/.kodelet/AGENTS.md` or `~/.kodelet/KODELET.md`
   - Always loaded if it exists, providing user-wide defaults and preferences

### Context Composition Strategy

When multiple context files are discovered:
1. **Merge Strategy**: All discovered contexts are included in the prompt
2. **Deduplication**: If same file appears multiple times, use only once
3. **Path Identification**: Each context tagged with its full file path for clarity

### Dynamic Prompt Regeneration

System prompts will be regenerated before each LLM exchange based on:
1. Current file access patterns from `State.GetRelevantContexts()`
2. Any changes in context file availability
3. Conversation progression and tool usage

## Implementation Plan

### Phase 1: Enhance State Interface and Implementation

#### 1.1 Update State Interface
```go
// pkg/types/tools/types.go
type State interface {
    // ... existing methods ...
    
    // Context discovery methods
    GetRelevantContexts() map[string]ContextInfo  // Returns all relevant context files
}

type ContextInfo struct {
    Content      string
    Path         string    // Full path to the context file
    LastModified time.Time
}
```

#### 1.2 Implement Context Discovery in State
```go
// pkg/tools/state.go
import (
    "os"
    "path/filepath"
    "time"
    "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type BasicState struct {
    // ... existing fields ...
    
    // Context discovery fields
    contextCache        map[string]*ContextInfo
    contextDiscovery    *ContextDiscovery
}

// Context discovery component handles finding relevant context files
type ContextDiscovery struct {
    workingDir      string
    homeDir         string
    contextPatterns []string // ["AGENTS.md", "KODELET.md"]
}

// NewBasicState creates a new BasicState with context discovery initialized
func NewBasicState() *BasicState {
    homeDir, _ := os.UserHomeDir()
    workingDir, _ := os.Getwd()
    
    return &BasicState{
        // ... initialize existing fields ...
        contextCache: make(map[string]*ContextInfo),
        contextDiscovery: &ContextDiscovery{
            workingDir:      workingDir,
            homeDir:         filepath.Join(homeDir, ".kodelet"),
            contextPatterns: []string{"AGENTS.md", "KODELET.md"},
        },
    }
}

func (s *BasicState) GetRelevantContexts() map[string]ContextInfo {
    contexts := make(map[string]ContextInfo)
    
    // 1. Add working directory context
    if ctx := s.loadWorkingDirContext(); ctx != nil {
        contexts[ctx.Path] = *ctx
    }
    
    // 2. Add access-based contexts
    for path := range s.fileLastAccess {
        if ctx := s.findContextForPath(path); ctx != nil {
            contexts[ctx.Path] = *ctx
        }
    }
    
    // 3. Add home directory context
    if ctx := s.loadHomeContext(); ctx != nil {
        contexts[ctx.Path] = *ctx
    }
    
    return contexts
}

func (s *BasicState) findContextForPath(filePath string) *ContextInfo {
    dir := filepath.Dir(filePath)
    for dir != "/" && dir != "." {
        for _, pattern := range s.contextDiscovery.contextPatterns {
            contextPath := filepath.Join(dir, pattern)
            if info := s.loadContextFile(contextPath); info != nil {
                return info
            }
        }
        dir = filepath.Dir(dir)
    }
    return nil
}

// Helper methods
func (s *BasicState) loadContextFile(path string) *ContextInfo {
    // Check if file exists and get modification time
    stat, err := os.Stat(path)
    if err != nil {
        return nil
    }
    
    // Check cache - only use if file hasn't been modified
    if cached, ok := s.contextCache[path]; ok {
        if cached.LastModified.Equal(stat.ModTime()) {
            return cached
        }
    }
    
    // Load from disk
    content, err := os.ReadFile(path)
    if err != nil {
        return nil
    }
    
    info := &ContextInfo{
        Content:      string(content),
        Path:         path,
        LastModified: stat.ModTime(),
    }
    
    // Update cache
    s.contextCache[path] = info
    return info
}



func (s *BasicState) loadWorkingDirContext() *ContextInfo {
    // Try AGENTS.md first, then KODELET.md
    for _, pattern := range s.contextDiscovery.contextPatterns {
        if info := s.loadContextFile(filepath.Join(s.contextDiscovery.workingDir, pattern)); info != nil {
            return info
        }
    }
    return nil
}

func (s *BasicState) loadHomeContext() *ContextInfo {
    // Try AGENTS.md first, then KODELET.md in home directory
    for _, pattern := range s.contextDiscovery.contextPatterns {
        if info := s.loadContextFile(filepath.Join(s.contextDiscovery.homeDir, pattern)); info != nil {
            return info
        }
    }
    return nil
}
```

#### 1.3 Modify Sysprompt Functions
```go
// pkg/sysprompt/system.go
import (
    "context"
    "github.com/jingkaihe/kodelet/pkg/logger"
    "github.com/jingkaihe/kodelet/pkg/types/llm"
    "github.com/jingkaihe/kodelet/pkg/types/tools" // For ContextInfo
)

func SystemPrompt(model string, llmConfig llm.Config, contexts map[string]tools.ContextInfo) string {
    // Create prompt context with discovered contexts
    promptCtx := NewPromptContext()
    
    // Convert ContextInfo to simple map for prompt generation
    if contexts != nil && len(contexts) > 0 {
        contextFiles := make(map[string]string)
        for path, info := range contexts {
            contextFiles[path] = info.Content
        }
        promptCtx.ContextFiles = contextFiles
    } else {
        // Fallback to current behavior if no contexts provided
        promptCtx.ContextFiles = loadContexts()
    }
    
    // Create renderer and config
    renderer := NewRenderer(TemplateFS)
    config := NewDefaultConfig().WithModel(model)
    
    // Update context with config and LLM settings
    updateContextWithConfig(promptCtx, config)
    promptCtx.BashAllowedCommands = llmConfig.AllowedCommands
    
    // Render the system prompt
    prompt, err := renderer.RenderSystemPrompt(promptCtx)
    if err != nil {
        ctx := context.Background()
        log := logger.G(ctx)
        log.WithError(err).Fatal("Error rendering system prompt")
    }
    
    return prompt
}

// Update SubAgentPrompt similarly
func SubAgentPrompt(model string, llmConfig llm.Config, contexts map[string]tools.ContextInfo) string {
    // Similar implementation to SystemPrompt but uses SubagentTemplate
    // ... (same pattern as SystemPrompt)
}

### Phase 2: LLM Provider Integration

#### 2.1 Modify Anthropic Provider
```go
// pkg/llm/anthropic/anthropic.go
func (t *AnthropicThread) Send(ctx context.Context, message string, handler llmtypes.ResponseHandler, opt llmtypes.SendOptions) (string, error) {
    // ... existing code ...
    
    // Move system prompt generation inside the OUTER loop
    OUTER:
    for {
        // Get relevant contexts from state
        contexts := t.state.GetRelevantContexts()
        
        // Regenerate system prompt with current contexts
        var systemPrompt string
        if t.config.IsSubAgent {
            systemPrompt = sysprompt.SubAgentPrompt(string(model), t.config, contexts)
        } else {
            systemPrompt = sysprompt.SystemPrompt(string(model), t.config, contexts)
        }
        
        // ... rest of loop
    }
}
```

#### 2.2 Modify OpenAI Provider
```go
// pkg/llm/openai/openai.go
func (t *OpenAIThread) Send(ctx context.Context, message string, handler llmtypes.ResponseHandler, opt llmtypes.SendOptions) (string, error) {
    // ... existing code ...
    
    OUTER:
    for {
        // Get relevant contexts from state
        contexts := t.state.GetRelevantContexts()
        
        // Generate system prompt with current contexts
        var systemPrompt string
        if t.config.IsSubAgent {
            systemPrompt = sysprompt.SubAgentPrompt(model, t.config, contexts)
        } else {
            systemPrompt = sysprompt.SystemPrompt(model, t.config, contexts)
        }
        
        // ... rest of loop
    }
}
```

### Phase 3: Caching and Testing

#### 3.1 Context File Caching (in State Implementation)
- Cache loaded context files at the State layer with modification time checks
- Automatically reload when files are modified (precise file modification tracking)
- State manages cache lifecycle and invalidation

#### 3.2 Context Discovery Optimization
- State tracks accessed files to determine relevant context directories
- Context files are always current - no stale data risk
- Simple cache invalidation based on file modification time

## Migration Path

### Backward Compatibility
- All existing behavior preserved by default
- Single context file in CWD continues to work exactly as before
- No breaking changes to public APIs
- Additional contexts are simply added to the existing system

### Gradual Rollout
1. **Phase 1**: Implement feature with comprehensive testing
2. **Phase 2**: Deploy with monitoring and validation
3. **Phase 3**: Full rollout as part of regular release

## Testing Strategy

### Unit Tests
- Context discovery for various directory structures
- Cache invalidation and modification time behavior
- State tracking integration

## Security Considerations

### Path Traversal Prevention
- Validate all file paths
- Restrict context discovery to project boundaries
- No symlink following outside project root

### Sensitive Information
- Respect .gitignore patterns
- Allow context file exclusion patterns
- Never include files from system directories

## Benefits of State-Layer Implementation

### Architectural Advantages
1. **Single Source of Truth**: State becomes the authoritative source for both file access tracking and context relevance
2. **Better Encapsulation**: Context discovery logic is encapsulated where the data (file access) lives
3. **Reduced Coupling**: Sysprompt package doesn't need to know about file access patterns
4. **Easier Testing**: State methods can be tested independently of prompt generation
5. **Performance**: State can optimize caching and discovery based on actual access patterns

### Implementation Benefits
1. **Cleaner API**: Sysprompt functions take only the data they need (context map)
2. **Reduced Coupling**: Sysprompt doesn't depend on State internals, only on ContextInfo type
3. **Reusability**: Other components can query State for relevant contexts if needed
4. **Consistency**: File access tracking and context discovery use the same data structures
5. **Simplicity**: Always enabled - no configuration complexity
6. **Testability**: Easy to test sysprompt functions with mock context data

## Implementation Notes

### State Implementation Location
The State interface is defined in `pkg/types/tools/types.go` and implemented as `BasicState` in `pkg/tools/state.go`. The context discovery will be added as new methods to the existing `BasicState` implementation, maintaining backward compatibility.

### Minimal Changes Required
1. **State Interface**: Add 1 new method (`GetRelevantContexts()`) and 1 new type (`ContextInfo`)
2. **State Implementation**: Add context discovery logic and initialization
3. **Sysprompt**: Add new parameter to existing SystemPrompt and SubAgentPrompt functions
4. **LLM Providers**: Call state.GetRelevantContexts() and pass to sysprompt functions (2 lines per provider)

## Example Workflow

### Scenario: Multi-Module Project
Consider a project with the following structure:
```
myproject/
├── AGENTS.md                    # Project-wide context
├── pkg/
│   ├── auth/
│   │   ├── AGENTS.md           # Auth module context  
│   │   └── handler.go
│   └── database/
│       ├── KODELET.md          # Database module context
│       └── connection.go
└── ~/.kodelet/
    └── AGENTS.md               # User's global preferences
```

### Execution Flow
1. **User starts kodelet** in `myproject/`
   - State loads `myproject/AGENTS.md` (working directory context)
   - State loads `~/.kodelet/AGENTS.md` (home context)

2. **User asks to fix auth issue**
   - Kodelet accesses `pkg/auth/handler.go`
   - State detects file access and discovers `pkg/auth/AGENTS.md`
   - Next LLM call includes all three contexts

3. **User asks about database connections**
   - Kodelet accesses `pkg/database/connection.go`
   - State discovers `pkg/database/KODELET.md`
   - Next LLM call includes all four contexts

4. **All Contexts Included in Prompt**
   ```
   [System Prompt Base]
   
   <context filename="/home/user/myproject/pkg/auth/AGENTS.md">
   [Auth module specific guidelines]
   </context>
   
   <context filename="/home/user/myproject/pkg/database/KODELET.md">
   [Database module specific guidelines]
   </context>
   
   <context filename="/home/user/myproject/AGENTS.md">
   [Project-wide guidelines]
   </context>
   
   <context filename="/home/user/.kodelet/AGENTS.md">
   [User's global preferences]
   </context>
   ```

## Conclusion

This enhancement will make kodelet's context system significantly more intelligent and user-friendly, automatically adapting to project structures and user workflows while maintaining full backward compatibility. The system will include all discovered contexts - working directory, access-based, and home contexts - whenever they exist, providing comprehensive context information to the LLM. 

By implementing context discovery at the State layer and passing only the necessary context data to sysprompt functions, we achieve excellent separation of concerns and a clean, testable architecture. The LLM providers simply call `state.GetRelevantContexts()` and pass the result to the existing sysprompt functions. The phased implementation approach ensures low risk and allows for iterative improvements based on user feedback.