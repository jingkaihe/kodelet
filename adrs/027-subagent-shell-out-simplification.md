# ADR 027: Subagent Shell-Out Simplification

## Status

Proposed

## Context

Kodelet's subagent functionality allows the main agent to delegate complex tasks (code search, architecture analysis, codebase understanding) to a separate agent instance. The current implementation uses an in-process factory pattern that has grown complex due to:

1. **Cross-Provider Support**: Subagents can use a different LLM provider than the main agent (e.g., Anthropic main + OpenAI subagent), requiring complex configuration propagation.

2. **SubagentContextFactory Pattern**: A function type injected into every provider's constructor:
   ```go
   type SubagentContextFactory func(ctx context.Context, parentThread Thread, 
       handler MessageHandler, compactRatio float64, disableAutoCompact bool) context.Context
   ```

3. **Per-Provider NewSubAgent Methods**: Each of the 4 providers (Anthropic, OpenAI Chat, OpenAI Responses, Google) implements its own `NewSubAgent()` method to handle client reuse and provider-specific configuration.

4. **Centralized Cross-Provider Logic**: `NewSubagentThread()` in `pkg/llm/thread.go` handles the decision of whether to create a same-provider or cross-provider subagent.

### Current Code Distribution

| Component | Location | Lines |
|-----------|----------|-------|
| `SubagentContextFactory` type | `pkg/types/llm/thread.go` | 5 |
| Factory field in base.Thread | `pkg/llm/base/base.go` | 10 |
| Factory injection per provider | 4 provider files | ~80 |
| `NewSubagentThread()` | `pkg/llm/thread.go` | 65 |
| `NewSubagentContext()` | `pkg/llm/thread.go` | 22 |
| Provider `NewSubAgent()` methods | 4 provider files | ~80 |
| `SubAgentConfigSettings` struct | `pkg/types/llm/config.go` | 15 |
| `SubAgentTool.Execute()` context retrieval | `pkg/tools/subagent.go` | 20 |
| `WithSubAgentTools()` state setup | `pkg/tools/state.go` | 15 |
| Cross-provider subagent config | `pkg/types/llm/config.go` | 10 |
| Subagent prompt generation | `pkg/sysprompt/subagent.go` | 56 |
| Subagent prompt template | `pkg/sysprompt/templates/subagent.tmpl` | 26 |
| **Total** | **~12 files** | **~400 lines** |

### Problems with Current Approach

1. **Cross-Cutting Concern**: Every provider constructor must accept and store `SubagentContextFactory`, even when subagents aren't used.

2. **Dual Creation Paths**: Logic split between `NewSubagentThread()` (cross-provider) and `NewSubAgent()` (same-provider).

3. **Complex Context Threading**: Subagent configuration passed via `context.Value()` through multiple layers.

4. **Testing Difficulty**: Factory injection makes unit testing providers more complex.

5. **Circular Dependency Risk**: Factory pattern requires careful import management between `pkg/llm` and `pkg/tools`.

## Decision

Replace the in-process factory pattern with a shell-out approach using `kodelet run --result-only --as-subagent "<question>"`.

### Simplified Configuration: `subagent_args`

Instead of a complex nested `subagent:` block, use a single string of CLI arguments:

```yaml
# Old approach (to be removed)
subagent:
  provider: openai
  model: gpt-4.1
  reasoning_effort: medium
  allowed_tools:
    - bash
    - file_read

# New approach - just CLI flags
subagent_args: "--use-weak-model"
```

Common patterns:

```yaml
# Use weak model (same provider as parent)
subagent_args: "--use-weak-model"

# Use a different profile for subagent
profiles:
  premium:
    provider: anthropic
    model: claude-sonnet-4-20250514
    subagent_args: "--profile cheap"  # Delegate to cheaper profile
  
  cheap:
    provider: openai
    model: gpt-4o-mini

# Empty = subagent uses default config
subagent_args: ""
```

This leverages existing CLI flags and profile system - no special subagent configuration needed.

### The `--as-subagent` Flag Behavior

When `kodelet run --as-subagent` is invoked:

1. **Disable Subagent Tool**: Exclude the `subagent` tool from available tools to prevent infinite recursion.

2. **Set Internal Flag**: Mark `config.IsSubAgent = true` for any provider-specific behavior.

That's it. All other configuration comes from standard CLI flags (`--profile`, `--use-weak-model`, `--image`, etc.).

### Shell-Out Implementation

```go
func (t *SubAgentTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    input := &SubAgentInput{}
    if err := json.Unmarshal([]byte(parameters), input); err != nil {
        return &SubAgentToolResult{err: err.Error()}
    }
    
    exe, err := os.Executable()
    if err != nil {
        return &SubAgentToolResult{err: errors.Wrap(err, "failed to get executable path").Error()}
    }
    
    // Build command with subagent_args from config
    args := []string{"run", "--result-only", "--as-subagent"}
    
    // Append user-configured subagent args (e.g., "--profile openai --use-weak-model")
    if subagentArgs := state.GetLLMConfig().SubagentArgs; subagentArgs != "" {
        parsedArgs, _ := shlex.Split(subagentArgs)
        args = append(args, parsedArgs...)
    }
    
    args = append(args, input.Question)
    
    cmd := exec.CommandContext(ctx, exe, args...)
    cmd.Env = os.Environ()
    
    output, err := cmd.Output()
    if err != nil {
        return &SubAgentToolResult{err: err.Error(), question: input.Question}
    }
    
    return &SubAgentToolResult{
        result:   string(output),
        question: input.Question,
    }
}
```

## Implementation

### Phase 1: Add CLI Support (~20 lines)

1. Add `--as-subagent` flag to `run` command that:
   - Disables the `subagent` tool (prevents recursion)
   - Sets `config.IsSubAgent = true`

Note: Stdin support already exists via `getQueryFromStdinOrArgs()` - content piped to stdin is combined with the query args.

### Phase 2: Simplify SubAgentTool (~40 lines)

1. Replace context-based thread retrieval with `exec.Command`
2. Parse `subagent_args` config and append to command args
3. Return stdout as result (subagent logs its own usage separately)

### Phase 3: Consolidate System Prompts

Remove the separate subagent prompt template and use the main system prompt with conditionals:

1. Remove `pkg/sysprompt/templates/subagent.tmpl`
2. Remove `pkg/sysprompt/subagent.go` (and test file)
3. Update `system.tmpl` to conditionally exclude subagent examples:
   ```go
   {{if not .Features.isSubagent}}
   {{include "templates/components/examples/subagent_tool_usage.tmpl" .}}
   {{end}}
   ```
4. Remove `RenderSubagentPrompt()` from renderer

The `--as-subagent` flag sets `isSubagent = true`, which:
- Disables the subagent tool (prevents recursion)
- Excludes subagent usage examples from the prompt

### Phase 4: Remove Factory Pattern and Simplify Config (~-300 lines)

**Remove factory infrastructure:**
1. Remove `SubagentContextFactory` type from `pkg/types/llm/thread.go`
2. Remove factory field from `base.Thread`
3. Remove factory parameter from all provider constructors:
   - `NewAnthropicThread()`
   - `NewOpenAIThread()` / `NewThread()` (factory)
   - `NewGoogleThread()`
   - OpenAI Responses `NewThread()`
4. Remove `NewSubagentThread()` and `NewSubagentContext()` from `pkg/llm/thread.go`
5. Remove `NewSubAgent()` methods from all 4 providers
6. Remove `SubAgentConfig` and `SubAgentConfigKey` from `pkg/types/llm/thread.go`
7. Remove `WithSubAgentTools()` from state.go

**Simplify configuration:**
8. Remove `SubAgentConfigSettings` struct from `pkg/types/llm/config.go`
9. Replace `SubAgent *SubAgentConfigSettings` with `SubagentArgs string` in config
10. Update `config.sample.yaml` to use `subagent_args` instead of `subagent:` block
11. Remove cross-provider config fields (`subagent.openai`, `subagent.google`)

### Phase 5: Refactor Other LLM-Dependent Tools

Two other tools use the subagent context for LLM access. Both use the same shell-out pattern, respecting `subagent_args` and adding tool-specific flags:

**`image_recognition`** - adds `--image` flag:
```go
func (t *ImageRecognitionTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
    // ... input parsing ...
    
    exe, err := os.Executable()
    if err != nil {
        return &ImageRecognitionToolResult{err: errors.Wrap(err, "failed to get executable path").Error()}
    }
    
    args := []string{"run", "--result-only", "--as-subagent"}
    
    // Append user-configured subagent args
    if subagentArgs := state.GetLLMConfig().SubagentArgs; subagentArgs != "" {
        parsedArgs, _ := shlex.Split(subagentArgs)
        args = append(args, parsedArgs...)
    }
    
    // Add tool-specific flags
    args = append(args, "--image", input.ImagePath, analysisPrompt)
    
    cmd := exec.CommandContext(ctx, exe, args...)
    cmd.Env = os.Environ()
    
    output, err := cmd.Output()
    // ... return string(output) as result ...
}
```

**`web_fetch`** - adds `--use-weak-model` flag and uses stdin:
```go
func (t *WebFetchTool) handleHTMLMarkdownWithPrompt(ctx context.Context, state tooltypes.State, input *WebFetchInput, processedContent string) tooltypes.ToolResult {
    exe, err := os.Executable()
    if err != nil {
        return &WebFetchToolResult{err: errors.Wrap(err, "failed to get executable path").Error()}
    }
    
    args := []string{"run", "--result-only", "--as-subagent"}
    
    // Append user-configured subagent args
    if subagentArgs := state.GetLLMConfig().SubagentArgs; subagentArgs != "" {
        parsedArgs, _ := shlex.Split(subagentArgs)
        args = append(args, parsedArgs...)
    }
    
    // Add tool-specific flags
    prompt := fmt.Sprintf("Extract from the content provided via stdin: %s", input.Prompt)
    args = append(args, "--use-weak-model", prompt)
    
    cmd := exec.CommandContext(ctx, exe, args...)
    cmd.Env = os.Environ()
    cmd.Stdin = strings.NewReader(processedContent)
    
    output, err := cmd.Output()
    // ... return string(output) as result ...
}
```

All three tools follow the same pattern:
1. Start with `["run", "--result-only", "--as-subagent"]`
2. Append parsed `subagent_args` from config
3. Add tool-specific flags and prompt

## Consequences

### Benefits

1. **~75% Code Reduction**: ~400 lines of subagent infrastructure reduced to ~100 lines
2. **Eliminated Cross-Cutting Concern**: Providers no longer need factory injection
3. **Natural Cross-Provider Support**: Use `--profile` flag instead of special subagent config
4. **Simplified Configuration**: Single `subagent_args` string replaces complex nested struct
5. **Simplified Testing**: Subagent is just a CLI invocation, easily mocked
6. **Better Isolation**: Subagent failures don't affect main agent memory
7. **Debuggability**: Can test subagent behavior directly via CLI
8. **Unified Pattern**: All LLM-dependent tools (`subagent`, `image_recognition`, `web_fetch`) use the same shell-out pattern

### Trade-offs

1. **Process Overhead**: ~10-50ms spawn time per subagent invocation
   - Acceptable: Subagent tasks are typically long-running (seconds to minutes)
   
2. **No Client Reuse**: Each subagent creates new HTTP client
   - Acceptable: Connection overhead is minimal compared to LLM response time
   
3. **Higher Memory**: Separate process for each subagent
   - Acceptable: Subagent lifetime is short, and isolation is beneficial
   
4. **Environment Dependency**: Requires API keys in environment
   - Already the case: `kodelet run` reads from environment

### Breaking Changes

This is a breaking change. Users with `subagent:` configuration blocks must update to `subagent_args`:

```yaml
# Old (no longer supported)
subagent:
  provider: openai
  model: gpt-4.1
  reasoning_effort: medium

# New - create a profile and reference it
profiles:
  openai-subagent:
    provider: openai
    model: gpt-4.1
    reasoning_effort: medium

subagent_args: "--profile openai-subagent"
```

## References

- ADR 004: Unify LLM Client Interfaces
- ADR 023: Unified Thread Base Package
- Current implementation: `pkg/llm/thread.go`, `pkg/tools/subagent.go`
