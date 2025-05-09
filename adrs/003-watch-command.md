# ADR 003: Watch Command

## Status

Proposed

## Context

Kodelet currently offers one-shot queries and interactive chat mode for users to get assistance with SRE and platform engineering tasks. However, there is currently no way for Kodelet to automatically respond to changes in the codebase as they happen.

Developers often make incremental changes to their code and would benefit from immediate feedback or assistance based on those changes. Existing file watching tools don't provide AI-powered insights specific to the changed files.

## Decision

We will implement a new `kodelet watch` command that continuously monitors file changes in the current directory and provides AI-powered insights or assistance whenever changes are detected.

## Details

The Watch Command will include the following capabilities:

1. **File Monitoring**:
   - Watch for file creation, modification, and deletion events in the current directory and subdirectories
   - Support for configurable inclusion/exclusion patterns (e.g., ignore `.git/`, `node_modules/`). By default `.git/` and `node_modules/` are ignored.
   - Efficient event debouncing to handle rapid successive changes

2. **Change Processing**:
   - Detect the specific files that changed
   - Extract contextual information about the changes (diff, surrounding code)
   - Send the changes to the Anthropic Claude API for analysis

3. **Response Actions**:
   - Provide immediate feedback on potential issues, improvements, or suggestions
   - Optionally offer to make automated fixes based on AI recommendations
   - Support customizable response templates for different file types

4. **User Experience**:
   - Clear terminal UI showing watched directories and recent events
   - Interactive mode to accept/reject suggestions
   - Configurable verbosity levels

5. **Performance Considerations**:
   - Efficient filesystem watching with minimal resource usage
   - Smart throttling to prevent excessive API calls
   - Caching mechanisms to avoid redundant processing

## Consequences

### Advantages

- Provides immediate, proactive assistance as users work on their code
- Reduces context-switching between coding and requesting assistance
- Enables continuous quality improvement through real-time feedback
- Streamlines the development workflow with automated suggestions

### Challenges

- Need for efficient file watching to avoid performance impacts
- Managing rate limits and costs for API calls
- Ensuring relevance and quality of automated suggestions
- Handling complex project structures and large directories
- Designing appropriate interaction patterns for suggestion review

### Alternatives Considered

1. **Editor Integration**: Rather than a standalone watch command, integrate directly with code editors. However, this would limit Kodelet's editor-agnostic approach.
2. **Git-based Tracking**: Only analyze changes when committed or staged in git. This would be less resource-intensive but wouldn't provide real-time feedback.
3. **Manual Trigger**: Require users to manually trigger analysis of recent changes. This would be more deliberate but lose the seamless experience.

## Implementation Plan

1. Research and select a reliable file watching library compatible with Go
2. Implement basic file change detection with debouncing
3. Create the change analysis pipeline for different file types
4. Design and implement the terminal UI for the watch mode
5. Add configuration options and customization capabilities
6. Add automated suggestion implementation with user confirmation
7. Optimize performance and resource usage

## References

- [fsnotify](https://github.com/fsnotify/fsnotify) - Cross-platform file system notifications for Go
- [Anthropic Claude API documentation](https://docs.anthropic.com/claude/reference) - For AI integration
- [watchexec](https://github.com/watchexec/watchexec) - Example of a well-designed file watching tool
