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

## Decision

**Selected Approach: Option 3 - Master Orchestration Command + Individual Commands**

We will implement:
1. **New individual commands** for GitHub/Git operations
2. **Master `kodelet background` command** for full orchestration
3. **Shared packages** for GitHub API, Git operations, and orchestration logic

## Implementation Architecture

### New CLI Commands

#### 1. Issue Processing
```bash
kodelet issue fetch <github-issue-url>
# Creates: ISSUE.md, .kodelet/issue.json
```

#### 2. Branch Management  
```bash
kodelet branch create --from-issue [--issue-url <url>]
# Creates branch: issue-{number}-{slug}
```

#### 3. Issue Commenting
```bash
kodelet comment <github-issue-url> [--message <text>] [--file <path>] [--template <type>] [--update-comment-id <id>]
# Templates: success, error, progress
# --update-comment-id: Update existing comment instead of creating new one
```

#### 4. Master Orchestration
```bash
kodelet background --issue-url <github-issue-url> [--max-time <duration>] [--progress-freq <duration>]
# Executes: fetch → branch → run → pr → comment
```

### Package Structure

```
pkg/
├── background/          # New: Orchestration logic
│   ├── orchestrator.go  # Main pipeline execution
│   ├── progress.go      # Progress reporting
│   └── config.go        # Configuration management
├── github/              # New: GitHub API integration
│   ├── issue.go         # Issue fetching and processing
│   ├── comment.go       # Issue commenting with update support
│   ├── client.go        # GitHub API client setup
│   └── manager.go       # Comment management (create/update single comment)
├── git/                 # New: Git operations
│   ├── branch.go        # Branch creation and management
│   └── utils.go         # Git utility functions
└── [existing packages] # No changes to existing architecture
```

### Integration with Existing Architecture

#### LLM Thread Integration
```go
// Reuse existing pkg/llm.Thread for kodelet execution
func (o *Orchestrator) runKodelet(ctx context.Context) error {
    content, err := os.ReadFile("ISSUE.md")
    if err != nil {
        return err
    }
    
    // Create thread using existing patterns
    thread, err := o.createLLMThread(ctx)
    if err != nil {
        return err
    }
    
    prompt := fmt.Sprintf("Work on this GitHub issue:\n\n%s", string(content))
    return thread.SendMessage(ctx, prompt)
}
```

#### Conversation Storage
```go
// Store background executions as conversations
func (o *Orchestrator) Execute(ctx context.Context) error {
    conversationID := uuid.New().String()
    
    // Use existing conversation storage
    conversation := &conversations.Conversation{
        ID:      conversationID,
        Type:    "background",
        Metadata: map[string]string{
            "issue_url": o.config.IssueURL,
            "branch":    getCurrentBranch(),
        },
    }
    
    return o.executeWithConversation(ctx, conversation)
}
```

#### Tool Integration
```go
// All existing tools available during background execution
func (o *Orchestrator) createLLMThread(ctx context.Context) (*llm.Thread, error) {
    // Reuse existing tool registry and LLM client creation
    return llm.NewThread(ctx, o.llmClient, o.toolRegistry)
}
```

### Command Implementation

#### Background Command
```go
// cmd/kodelet/background.go
func NewBackgroundCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "background",
        Short: "Execute kodelet for a GitHub issue autonomously",
        Long: `Fetch a GitHub issue, create a branch, run kodelet, create PR, and report back.
Designed for GitHub Actions automation but can be used manually.`,
        RunE: runBackground,
    }
    
    cmd.Flags().String("issue-url", "", "GitHub issue URL (required)")
    cmd.Flags().Duration("max-time", time.Hour*5, "Maximum execution time")
    cmd.Flags().Duration("progress-freq", time.Minute*10, "Progress update frequency")
    cmd.MarkFlagRequired("issue-url")
    
    return cmd
}

func runBackground(cmd *cobra.Command, args []string) error {
    ctx := cmd.Context()
    log := logger.G(ctx)
    
    // Parse flags
    issueURL, _ := cmd.Flags().GetString("issue-url")
    maxTime, _ := cmd.Flags().GetDuration("max-time")
    progressFreq, _ := cmd.Flags().GetDuration("progress-freq")
    
    // Create orchestrator with existing patterns
    orchestrator, err := background.NewOrchestrator(&background.Config{
        IssueURL:     issueURL,
        MaxTime:      maxTime,
        ProgressFreq: progressFreq,
        Logger:       log,
    })
    if err != nil {
        return err
    }
    
    // Execute with timeout
    ctx, cancel := context.WithTimeout(ctx, maxTime)
    defer cancel()
    
    return orchestrator.Execute(ctx)
}
```

#### Individual Commands
```go
// cmd/kodelet/issue.go
func NewIssueCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "issue",
        Short: "GitHub issue operations",
    }
    
    cmd.AddCommand(NewIssueFetchCommand())
    return cmd
}

func NewIssueFetchCommand() *cobra.Command {
    return &cobra.Command{
        Use:   "fetch <github-issue-url>",
        Short: "Fetch GitHub issue and create ISSUE.md",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            processor := github.NewIssueProcessor()
            issueData, err := processor.FetchAndProcess(cmd.Context(), args[0])
            if err != nil {
                return err
            }
            return processor.WriteIssueFile(issueData)
        },
    }
}

// cmd/kodelet/comment.go
func NewCommentCommand() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "comment <github-issue-url>",
        Short: "Create or update GitHub issue comment",
        Args:  cobra.ExactArgs(1),
        RunE:  runComment,
    }
    
    cmd.Flags().String("message", "", "Comment message")
    cmd.Flags().String("file", "", "File containing comment message")
    cmd.Flags().String("template", "", "Comment template (progress, success, error)")
    cmd.Flags().Int64("update-comment-id", 0, "Comment ID to update (0 creates new)")
    
    return cmd
}

func runComment(cmd *cobra.Command, args []string) error {
    // Parse issue URL and flags
    issueURL := args[0]
    updateCommentID, _ := cmd.Flags().GetInt64("update-comment-id")
    message, _ := cmd.Flags().GetString("message")
    
    // Create GitHub client
    client := github.NewClient(cmd.Context(), os.Getenv("GITHUB_TOKEN"))
    
    if updateCommentID > 0 {
        // Update existing comment
        return client.UpdateComment(cmd.Context(), updateCommentID, message)
    } else {
        // Create new comment
        return client.CreateComment(cmd.Context(), issueURL, message)
    }
}
```

### Orchestration Pipeline

```go
// pkg/background/orchestrator.go
type Orchestrator struct {
    githubClient   *github.Client
    commentManager *github.CommentManager
    issueData      *github.IssueData
    config         *Config
    logger         *logrus.Entry
}

func (o *Orchestrator) Execute(ctx context.Context) error {
    // Initialize comment manager for single progress comment
    o.commentManager = o.githubClient.NewCommentManager(
        o.issueData.Owner, o.issueData.Repo, o.issueData.Number)
    
    // Create initial progress comment
    initialComment := `🤖 **Kodelet Background Execution Started**

**Status:** Initializing...
**Branch:** Creating branch...
**Started:** ` + time.Now().Format(time.RFC3339) + `

_Kodelet is working on your issue. This comment will be updated with progress._`
    
    if err := o.commentManager.CreateOrUpdateComment(ctx, initialComment); err != nil {
        o.logger.WithError(err).Warn("Failed to create initial comment")
    }

    steps := []struct {
        name string
        fn   func(context.Context) error
    }{
        {"fetch-issue", o.fetchIssue},
        {"create-branch", o.createBranch},
        {"run-kodelet", o.runKodelet},
        {"create-pr", o.createPR},
    }
    
    // Start progress reporting
    progressCtx, cancel := context.WithCancel(ctx)
    defer cancel()
    go o.reportProgress(progressCtx)
    
    for _, step := range steps {
        o.logger.Infof("Executing step: %s", step.name)
        if err := step.fn(ctx); err != nil {
            o.commentError(ctx, step.name, err)
            return fmt.Errorf("step %s failed: %w", step.name, err)
        }
    }
    
    // Create final success comment (separate from progress)
    o.commentSuccess(ctx)
    return nil
}
```

### Error Handling and Recovery

```go
func (o *Orchestrator) commentError(ctx context.Context, step string, err error) {
    // Create a separate error comment, don't update progress comment
    comment := fmt.Sprintf(`🚨 **Kodelet Background Execution Failed**

**Step:** %s
**Error:** %s
**Branch:** %s
**Timestamp:** %s

The kodelet background execution encountered an error. Please review the logs and retry if needed.

_This is an automated error report_
`, step, err.Error(), getCurrentBranch(), time.Now().Format(time.RFC3339))
    
    _ = o.commentManager.CreateFinalComment(ctx, comment)
}

func (o *Orchestrator) commentSuccess(ctx context.Context) {
    // Create a separate success comment
    comment := fmt.Sprintf(`✅ **Kodelet Background Execution Completed**

**Branch:** %s
**Pull Request:** %s
**Files Modified:** %d
**Completed:** %s

Kodelet has successfully processed your issue and created a pull request. Please review the changes.

_This is an automated completion report_
`, getCurrentBranch(), o.getPRURL(), o.getFilesModified(), time.Now().Format(time.RFC3339))
    
    _ = o.commentManager.CreateFinalComment(ctx, comment)
}
```

### Progress Reporting

```go
func (o *Orchestrator) reportProgress(ctx context.Context) {
    ticker := time.NewTicker(o.config.ProgressFreq)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            status := o.getCurrentStatus()
            comment := fmt.Sprintf(`🤖 **Kodelet Background Execution In Progress**

**Status:** %s
**Branch:** %s
**Last Activity:** %s
**Files Modified:** %d
**Started:** %s

_Kodelet is working on your issue. This comment updates automatically._
`, 
                status.Message, 
                status.Branch, 
                status.LastActivity.Format("15:04:05"), 
                status.FilesModified,
                o.config.StartTime.Format(time.RFC3339))
            
            // Update the same comment instead of creating new ones
            if err := o.commentManager.CreateOrUpdateComment(ctx, comment); err != nil {
                o.logger.WithError(err).Warn("Failed to update progress comment")
            }
        }
    }
}
```

### GitHub Client Implementation

```go
// pkg/github/client.go
import "github.com/google/go-github/v57/github"

type Client struct {
    client *github.Client
    logger *logrus.Entry
}

func NewClient(ctx context.Context, token string) *Client {
    ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
    tc := oauth2.NewClient(ctx, ts)
    
    return &Client{
        client: github.NewClient(tc),
        logger: logger.G(ctx),
    }
}

// CommentManager handles creating and updating a single comment
type CommentManager struct {
    client      *Client
    owner       string
    repo        string
    issueNumber int
    commentID   *int64  // nil means no comment created yet
}

func (c *Client) NewCommentManager(owner, repo string, issueNumber int) *CommentManager {
    return &CommentManager{
        client:      c,
        owner:       owner,
        repo:        repo,
        issueNumber: issueNumber,
    }
}

// CreateOrUpdateComment creates a new comment or updates existing one
func (cm *CommentManager) CreateOrUpdateComment(ctx context.Context, body string) error {
    if cm.commentID == nil {
        // Create new comment
        comment := &github.IssueComment{Body: &body}
        created, _, err := cm.client.client.Issues.CreateComment(
            ctx, cm.owner, cm.repo, cm.issueNumber, comment)
        if err != nil {
            return err
        }
        cm.commentID = created.ID
        cm.client.logger.WithField("comment_id", *cm.commentID).Info("Created progress comment")
        return nil
    } else {
        // Update existing comment
        comment := &github.IssueComment{Body: &body}
        _, _, err := cm.client.client.Issues.EditComment(
            ctx, cm.owner, cm.repo, *cm.commentID, comment)
        if err != nil {
            return err
        }
        cm.client.logger.WithField("comment_id", *cm.commentID).Info("Updated progress comment")
        return nil
    }
}

// CreateFinalComment creates a separate final comment (success/error)
func (cm *CommentManager) CreateFinalComment(ctx context.Context, body string) error {
    comment := &github.IssueComment{Body: &body}
    _, _, err := cm.client.client.Issues.CreateComment(
        ctx, cm.owner, cm.repo, cm.issueNumber, comment)
    return err
}
```

## GitHub Actions Integration

```yaml
# .github/workflows/kodelet-background.yml
- name: Run Kodelet Background
  run: kodelet background --issue-url ${{ github.event.issue.html_url }}
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
```

## Configuration Management

### State Files
```
.kodelet/
├── issue.json          # Issue metadata for command coordination
├── background.json     # Background execution state
└── progress.log        # Progress tracking for status updates
```

### Environment Variables
```bash
GITHUB_TOKEN=<token>           # GitHub API access
ANTHROPIC_API_KEY=<key>        # LLM API access
KODELET_PROGRESS_FREQ=10m      # Progress update frequency
KODELET_MAX_TIME=5h            # Maximum execution time
```

## Testing Strategy

### Unit Tests
- Individual command functionality
- GitHub API integration
- Git operations
- Orchestration logic

### Integration Tests
- Full pipeline execution
- Error handling and recovery
- Progress reporting
- GitHub API mocking

### End-to-End Tests
- Real GitHub issue processing
- Branch creation and PR workflow
- Comment verification

## Migration Path

### Phase 1: Individual Commands (Days 1-2)
- Implement `kodelet issue fetch`
- Implement `kodelet branch create`
- Implement `kodelet comment`

### Phase 2: Orchestration (Days 3-4)
- Implement `kodelet background`
- Add progress reporting
- Add error handling

### Phase 3: Integration (Days 5-6)
- GitHub Actions workflow integration
- End-to-end testing
- Documentation and examples

## Consequences

### Positive
- **Clean Architecture**: Maintains existing patterns while adding new capabilities
- **Flexible Usage**: Both manual and automated execution modes
- **Robust Error Handling**: Comprehensive error reporting back to GitHub
- **Progress Transparency**: Real-time status updates during execution
- **Anti-Spam Design**: Single progress comment updated in-place instead of multiple comments
- **Backward Compatibility**: No changes to existing commands

### Negative
- **Increased Complexity**: More CLI commands and packages to maintain
- **GitHub Dependency**: New functionality tightly coupled to GitHub API
- **State Management**: Additional complexity in coordinating between commands

### Risks
- **GitHub API Rate Limits**: Progress updates and comments may hit limits
- **Authentication Complexity**: Multiple tokens and permissions required
- **Error Recovery**: Partial execution state may be difficult to recover

## Success Metrics
- **Command Coverage**: All background workflow steps available as individual commands
- **Error Rate**: <5% failure rate in background executions
- **Performance**: Background execution completes within configured timeouts
- **Developer Experience**: Manual commands provide clear feedback and error messages

## Conclusion

The proposed CLI enhancements provide a solid foundation for Background Kodelet functionality while maintaining the existing architecture's integrity. The dual approach of individual commands + orchestration command offers maximum flexibility for both automated and manual use cases.

The implementation leverages existing kodelet patterns and packages, ensuring consistency and maintainability while adding the specific capabilities needed for autonomous GitHub issue processing.