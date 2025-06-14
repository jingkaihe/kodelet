# ADR-013: Kodelet CLI Background Support

## Status
Proposed

## Context
The Background Kodelet implementation (ADR-012) requires CLI enhancements to support autonomous GitHub issue processing within GitHub Actions workflows. The current kodelet CLI supports interactive conversations and one-shot queries, but lacks the specific capabilities needed for:

1. **GitHub Issue Processing**: Fetching and parsing GitHub issues into kodelet-consumable format
2. **Git Branch Management**: Creating appropriately named branches from issue context
3. **Issue Commenting**: Providing status updates back to the original GitHub issue
4. **Orchestrated Execution**: Coordinating the full pipeline from issue → branch → work → PR → comment

The CLI enhancements must integrate cleanly with the existing architecture while providing both individual commands for manual use and an orchestration command for GitHub Actions automation.

## Decision Drivers
- **Reuse Existing Architecture**: Leverage current pkg/llm, pkg/conversations, pkg/tools, and cmd/ patterns
- **Backward Compatibility**: Maintain existing CLI behavior and commands
- **GitHub Actions Integration**: Simple single-command execution for workflows
- **Manual Flexibility**: Individual commands available for developer use
- **Error Handling**: Robust error reporting back to GitHub issues
- **Progress Transparency**: Real-time status updates during long-running executions

## Options Analysis

### Option 1: Shell Script Orchestration
**Pros:**
- No CLI changes needed
- Simple GitHub Actions implementation
- Full flexibility in command composition

**Cons:**
- Poor error handling and recovery
- No unified progress reporting
- Complex GitHub Actions workflow
- Difficult state management between commands

### Option 2: Individual CLI Commands + External Orchestration
**Pros:**
- Clean separation of concerns
- Reusable individual commands
- Existing patterns maintained

**Cons:**
- Complex state sharing between commands
- Difficult error recovery
- Multiple command invocations in GitHub Actions

### Option 3: Master Orchestration Command + Individual Commands
**Pros:**
- Single command for GitHub Actions simplicity
- Individual commands for manual flexibility
- Unified error handling and progress reporting
- Clean state management within single process

**Cons:**
- More complex CLI implementation
- Potential code duplication between orchestrator and individual commands

### Option 4: Prompt-Based Orchestration (Like `kodelet pr`)
**Pros:**
- **Maximum Simplicity**: Reuses existing `llm.SendMessageAndGetTextWithUsage()` infrastructure
- **Consistency**: Follows established `kodelet pr` command pattern
- **Easy Testing**: Prompt modifications don't require code changes
- **Minimal Code**: Single command file following existing patterns
- **Tool Integration**: Automatic access to all existing tools via LLM
- **Error Handling**: Built-in via LLM reasoning and tool error handling

**Cons:**
- Less granular progress reporting (relies on LLM to provide updates)
- All logic embedded in prompt rather than structured code
- Potential token usage higher due to comprehensive prompt

## Decision

**Selected Approach: Option 4 - Prompt-Based Orchestration (Like `kodelet pr`)**

We will implement:
1. **Single `kodelet resolve` command** that orchestrates the entire workflow via LLM prompt
2. **Reuse existing architecture** including tools, LLM clients, and conversation storage
3. **Follow `kodelet pr` patterns** for prerequisites checking and command structure

## Implementation Architecture

### New CLI Command

#### Issue Resolution Command
```bash
kodelet resolve --issue-url <github-issue-url> [--max-tokens 8192] [--model claude-sonnet-4-0]
# Executes: fetch → branch → resolve → pr → comment via single LLM prompt
```

### Package Structure

**No new packages required** - reuses existing architecture:
```
pkg/
├── llm/                 # Existing: LLM client integration
├── tools/               # Existing: Tool registry and implementations
├── conversations/       # Existing: Conversation storage
└── [existing packages] # No changes needed
```

### Integration with Existing Architecture

#### Prompt-Based Orchestration Template
```text
Please resolve the github issue ${ISSUE_URL} following the steps below:

1. use `gh issue view ${ISSUE_URL}` to get the issue details.
- review the issue details and understand the issue.
- especially pay attention to the latest comment with @kodelet - this is the instruction from the user.
- extract the issue number from the issue URL for branch naming

2. based on the issue details, come up with a branch name and checkout the branch via `git checkout -b kodelet/issue-${ISSUE_NUMBER}-${BRANCH_NAME}`

3. start to work on the issue.
- think step by step before you start to work on the issue.
- if the issue is complex, you should add extra steps to the todo list to help you keep track of the progress.
- do not commit during this step.

4. once you have resolved the issue, ask the subagent to run `kodelet commit --short` to commit the changes.

5. after committing the changes, ask the subagent to run `kodelet pr` to create a pull request. Please instruct the subagent to always returning the PR link in the final response.

6. once the pull request is created, comment on the issue with the link to the pull request. If the pull request is not created, ask the subagent to create a pull request.

IMPORTANT:
!!!CRITICAL!!!: You should never update user's git config under any circumstances.
```

#### LLM Integration (Following `kodelet pr` Pattern)
```go
// cmd/kodelet/resolve.go - Similar to pr.go
func runIssue(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    s := tools.NewBasicState(ctx)

    // Prerequisites checking (like pr.go)
    if !isGitRepository() { /* error handling */ }
    if !isGhCliInstalled() { /* error handling */ }
    if !isGhAuthenticated() { /* error handling */ }

    // Generate prompt with issue URL
    issueURL, _ := cmd.Flags().GetString("issue-url")
    prompt := generateIssuePrompt(issueURL)

    // Send to LLM using existing patterns
    out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
        llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
            PromptCache: true,
        })

    fmt.Println(out)
    // Display usage stats...
}
```

### Command Implementation

#### Issue Resolution Command (Following `pr.go` Pattern)
```go
// cmd/kodelet/resolve.go
var resolveCmd = &cobra.Command{
    Use:   "issue",
    Short: "Resolve a GitHub issue autonomously",
    Long: `Resolve a GitHub issue by fetching details, creating a branch, implementing fixes, and creating a PR.

This command analyzes the GitHub issue, creates an appropriate branch, works on the issue resolution, and automatically creates a pull request with updates back to the original issue.`,
    Run: func(cmd *cobra.Command, args []string) {
        ctx := cmd.Context()
        s := tools.NewBasicState(ctx)

        // Prerequisites checking - Done in code, not in prompt (same as pr.go)
        if !isGitRepository() {
            fmt.Println("Error: Not a git repository. Please run this command from a git repository.")
            os.Exit(1)
        }

        if !isGhCliInstalled() {
            fmt.Println("Error: GitHub CLI (gh) is not installed. Please install it first.")
            fmt.Println("Visit https://cli.github.com/ for installation instructions.")
            os.Exit(1)
        }

        if !isGhAuthenticated() {
            fmt.Println("Error: You are not authenticated with GitHub. Please run 'gh auth login' first.")
            os.Exit(1)
        }

        // Get issue URL from flags
        issueURL, _ := cmd.Flags().GetString("issue-url")
        if issueURL == "" {
            fmt.Println("Error: --issue-url is required")
            os.Exit(1)
        }

        // Generate comprehensive prompt
        prompt := generateIssueResolutionPrompt(issueURL)

        // Send to LLM using existing architecture
        fmt.Println("Analyzing GitHub issue and starting resolution process...")
        fmt.Println("-----------------------------------------------------------")

        out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
            llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
                PromptCache: true,
            })

        fmt.Println(out)
        fmt.Println("-----------------------------------------------------------")

        // Display usage statistics (same as pr.go)
        fmt.Printf("\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
            usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

        fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
            usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())
    },
}

func init() {
    resolveCmd.Flags().String("issue-url", "", "GitHub issue URL (required)")
    resolveCmd.MarkFlagRequired("issue-url")
}

func generateIssueResolutionPrompt(issueURL string) string {
    return fmt.Sprintf(`Please resolve the github issue %s following the steps below:

1. use "gh issue view %s" to get the issue details.
- review the issue details and understand the issue.
- especially pay attention to the latest comment with @kodelet - this is the instruction from the user.
- extract the issue number from the issue URL for branch naming

2. based on the issue details, come up with a branch name and checkout the branch via "git checkout -b kodelet/issue-${ISSUE_NUMBER}-${BRANCH_NAME}"

3. start to work on the issue.
- think step by step before you start to work on the issue.
- if the issue is complex, you should add extra steps to the todo list to help you keep track of the progress.
- do not commit during this step.

4. once you have resolved the issue, ask the subagent to run "kodelet commit --short" to commit the changes.

5. after committing the changes, ask the subagent to run "kodelet pr" to create a pull request. Please instruct the subagent to always returning the PR link in the final response.

6. once the pull request is created, comment on the issue with the link to the pull request. If the pull request is not created, ask the subagent to create a pull request.

IMPORTANT:
!!!CRITICAL!!!: You should never update user's git config under any circumstances.`,
issueURL, issueURL)
}
```

### Prerequisites Functions (Reused from `pr.go`)

```go
// Helper functions reused from existing pr.go
func isGitRepository() bool {
    cmd := exec.Command("git", "rev-parse", "--git-dir")
    err := cmd.Run()
    return err == nil
}

func isGhCliInstalled() bool {
    cmd := exec.Command("gh", "--version")
    err := cmd.Run()
    return err == nil
}

func isGhAuthenticated() bool {
    cmd := exec.Command("gh", "auth", "status")
    err := cmd.Run()
    return err == nil
}
```

## GitHub Actions Integration

```yaml
# .github/workflows/kodelet-resolve.yml
- name: Run Kodelet Issue Resolution
  run: kodelet resolve --issue-url ${{ github.event.issue.html_url }}
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

## Configuration Management

### Environment Variables
```bash
GITHUB_TOKEN=<token>           # GitHub API access
ANTHROPIC_API_KEY=<key>        # LLM API access
KODELET_MODEL=<model>          # Optional: Override default model
KODELET_MAX_TOKENS=<tokens>    # Optional: Override token limit
```

### No Additional State Files Required
The prompt-based approach leverages:
- Existing conversation storage for LLM interactions
- Git repository state for tracking changes
- GitHub API for issue and PR management

## Testing Strategy

### Unit Tests
- Command flag parsing and validation
- Prerequisites checking functions
- Prompt generation logic
- Integration with existing LLM infrastructure

### Integration Tests
- Full issue resolution workflow
- Error handling scenarios
- GitHub CLI authentication checks
- Tool execution coordination

### End-to-End Tests
- Real GitHub issue processing
- Branch creation and PR workflow
- Issue commenting verification
- Conversation storage validation

## Migration Path

### Phase 1: Command Implementation (Day 1)
- Implement `kodelet resolve` command following `pr.go` pattern
- Add prerequisites checking
- Create comprehensive prompt template

### Phase 2: Testing and Refinement (Day 2)
- Unit tests for command functionality
- Integration testing with GitHub API
- Prompt optimization based on testing results

### Phase 3: Documentation and Integration (Day 3)
- GitHub Actions workflow setup
- Documentation updates
- End-to-end testing validation

## Consequences

### Positive
- **Maximum Simplicity**: Single command file, minimal code changes required
- **Consistency**: Follows established `kodelet pr` command pattern exactly
- **Maintainability**: Easy to modify prompts without code changes
- **Tool Integration**: Automatic access to all existing tools via LLM
- **Error Handling**: Built-in error handling via LLM reasoning
- **Conversation Storage**: Automatic conversation tracking via existing infrastructure
- **Backward Compatibility**: No changes to existing commands or architecture

### Negative
- **Token Usage**: Higher token consumption due to comprehensive prompt
- **Prompt Engineering**: Logic embedded in prompt rather than structured code
- **Progress Granularity**: Less fine-grained progress reporting than structured approach

### Risks
- **Prompt Complexity**: Large prompts may lead to inconsistent execution
- **LLM Dependency**: Entire workflow depends on LLM understanding and execution
- **Error Recovery**: Recovery logic must be embedded in prompt instructions

## Success Metrics
- **Implementation Time**: Command implemented within 1 day following `pr.go` pattern
- **Error Rate**: <5% failure rate in issue resolution executions
- **Code Simplicity**: <200 lines of new code (vs >1000 lines for Option 3)
- **Developer Experience**: Clear error messages and consistent behavior with existing commands

## Conclusion

The prompt-based orchestration approach (Option 4) provides the simplest and most maintainable solution for Background Kodelet functionality. By following the established `kodelet pr` pattern, this approach:

1. **Minimizes implementation complexity** - Single command file vs complex orchestration architecture
2. **Maximizes consistency** - Uses identical patterns to existing successful commands
3. **Leverages existing infrastructure** - No new packages or architectural changes required
4. **Enables rapid iteration** - Prompt modifications for behavior changes vs code refactoring

This approach represents the optimal balance between functionality and maintainability, delivering autonomous GitHub issue processing capability with minimal technical debt.
