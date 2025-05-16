# ADR 008: Refactoring the Sysprompt Package for Improved Modularity

## Status
Proposed

## Context
The current sysprompt package contains large string literals for system prompts with manual string replacement for variable substitution. This approach has several issues:

1. **Maintainability Issues**:
   - Large string literals are difficult to maintain and modify
   - Duplication exists between system prompt and subagent prompt
   - String replacement is error-prone and not type-safe

2. **Extensibility Concerns**:
   - Adding new sections or modifying existing ones requires changing large string literals
   - No clear separation of prompt components (tone, tools, examples, etc.)
   - Difficult to conditionally include sections based on configuration

3. **Testability Problems**:
   - Testing individual prompt components is challenging
   - Difficult to verify correct variable substitution

## Decision
We will refactor the sysprompt package to create a more modular and maintainable structure with the following key changes:

1. **Modular Prompt Structure**:
   - Break down monolithic prompt strings into separate components organized by function (tone, style, examples, tool usage, etc.)
   - Create a dedicated package structure to isolate concerns

2. **Use Go Templates**:
   - Replace manual string replacement with Go's text/template package
   - Define a structured template context for type safety
   - Support conditional inclusion of template sections

3. **Create Flexible Composition API**:
   - Allow prompt sections to be conditionally included or excluded
   - Enable configuration-driven prompt generation
   - Support model-specific prompt variations

## Architecture

### Directory Structure
```
pkg/
  sysprompt/
    templates/                 # Template files directory
      components/              # Individual template components
        tone.tmpl              # Tone and style guidance 
        tools.tmpl             # Tool usage instructions
        task_management.tmpl   # Task management guidance
        examples/              # Example templates
          simple.tmpl          # Simple examples
          tool_usage.tmpl      # Tool usage examples
      system.tmpl              # Main system prompt template
      subagent.tmpl            # Subagent prompt template
    context.go                 # Context generation code
    renderer.go                # Template rendering logic
    system.go                  # System prompt generation 
    subagent.go                # Subagent prompt generation
    config.go                  # Configuration types for templates
    constants.go               # Common constants
```

### Template Context
```go
// PromptContext holds all variables for template rendering
type PromptContext struct {
    // System info
    WorkingDirectory string
    IsGitRepo        bool
    Platform         string
    OSVersion        string
    Date             string
    
    // Tool names
    ToolNames        map[string]string
    
    // Content contexts (README, KODELET.md)
    ContextFiles     map[string]string
    
    // Feature flags
    Features         map[string]bool
}
```

### Template Rendering
Use Go's text/template package to render templates with proper error handling:

```go
// Renderer provides prompt template rendering capabilities
type Renderer struct {
    templateFS   fs.FS
    cache        map[string]*template.Template
}

// RenderPrompt renders a named template with the provided context
func (r *Renderer) RenderPrompt(name string, ctx PromptContext) (string, error) {
    // Load and parse template
    // Execute template with context
    // Return rendered string
}
```

### Sample Template (tools.tmpl)
```
# Tool Usage
* !!!IMPORTANT!!! You MUST use {{.ToolNames.batch}} tool for calling multiple INDEPENDENT tools AS MUCH AS POSSIBLE. This parallelises the tool calls and massively reduces the latency and context usage by avoiding back and forth communication.

{{if .Features.grepToolEnabled}}
* Use {{.ToolNames.grep}} tool for simple code search when the keywords for search can be described in regex.
{{end}}

{{if .Features.subagentEnabled}}
* Use {{.ToolNames.subagent}} tool for semantic code search when the subject you are searching is nuanced and cannot be described in regex.
{{end}}
```

## Benefits

1. **Improved Maintainability**:
   - Smaller, focused template files are easier to understand and modify
   - Type-safe context reduces the risk of errors during variable substitution
   - Clear separation of concerns makes updates more localized

2. **Enhanced Flexibility**:
   - Conditional template sections support different models or configurations
   - Easier to add new sections or examples
   - Composable prompt structure

3. **Better Testability**:
   - Template components can be tested individually
   - Templates can be validated at compile time

4. **Consistent Output**:
   - The refactoring will maintain the same prompt output while improving the code structure
   - Ensures backward compatibility during the transition

## Consequences

1. **Development Effort**: Initial refactoring will require significant effort to create the template structure and migration path.

2. **Learning Curve**: Team members will need to learn the Go template syntax and new package structure.

3. **Migration Strategy**: We'll need a phased approach to ensure compatibility during the transition:
   - Phase 1: Create new template-based implementation alongside existing implementation
   - Phase 2: Add tests to verify identical output between implementations
   - Phase 3: Switch to new implementation
   - Phase 4: Remove legacy implementation

## Implementation Plan

1. Create the new directory structure and template files
2. Implement the basic renderer with Go templates
3. Convert existing prompts to templates
4. Add tests to verify output matches current implementation
5. Update system.go and subagent.go to use the new renderer
6. Refactor Context() and SystemInfo() functions to work with templates
7. Gradually migrate all string operations to use templates

## Alternatives Considered

1. **String Builder Pattern**: Using strings.Builder for constructing prompts. Rejected due to lack of structure and type safety.

2. **Custom DSL**: Creating a domain-specific language for prompts. Rejected as too complex for current needs.

3. **External Template Files**: Storing templates as external files. Rejected in favor of embedded templates for better distribution and compilation checks.

## References

- [Go text/template Package](https://golang.org/pkg/text/template/)
- [Go embed Package](https://golang.org/pkg/embed/)