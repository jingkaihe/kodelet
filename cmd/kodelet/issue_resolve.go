package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Supported issue providers
const (
	GitHubProvider = "github"
)

// Default configuration values
const (
	DefaultBotMention = "@kodelet"
)

// IssueResolveConfig holds configuration for the issue-resolve command
type IssueResolveConfig struct {
	Provider   string
	IssueURL   string
	BotMention string
}

// NewIssueResolveConfig creates a new IssueResolveConfig with default values
func NewIssueResolveConfig() *IssueResolveConfig {
	return &IssueResolveConfig{
		Provider:   GitHubProvider,
		IssueURL:   "",
		BotMention: DefaultBotMention,
	}
}

// Validate validates the IssueResolveConfig and returns an error if invalid
func (c *IssueResolveConfig) Validate() error {
	if c.Provider != GitHubProvider {
		return errors.New(fmt.Sprintf("unsupported provider: %s, only '%s' is supported", c.Provider, GitHubProvider))
	}

	if c.IssueURL == "" {
		return errors.New("issue URL cannot be empty")
	}

	return nil
}

var issueResolveCmd = &cobra.Command{
	Use:   "issue-resolve",
	Short: "Intelligently resolve GitHub issues based on their type",
	Long: `Resolve GitHub issues using appropriate workflows based on issue type:

IMPLEMENTATION ISSUES (features, bugs, code changes):
- Analyzes the issue and creates a feature branch
- Implements the required changes
- Commits changes and creates a pull request
- Links the PR back to the original issue

QUESTION ISSUES (information requests, clarifications):
- Analyzes the codebase to understand the question
- Researches relevant code and documentation
- Provides comprehensive answers directly in issue comments
- No code changes or pull requests created

The command automatically detects issue type and applies the appropriate workflow.
Currently supports GitHub issues only.

Examples:
  kodelet issue-resolve --issue-url https://github.com/user/repo/issues/123
  kodelet issue-resolve --issue-url https://github.com/user/repo/issues/456 --bot-mention @mybot`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			presenter.Warning("Cancellation requested, shutting down...")
			cancel()
		}()

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create MCP manager")
			return
		}

		llmConfig := llm.GetConfigFromViper()
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager))

		// Get issue-resolve config from flags
		config := getIssueResolveConfigFromFlags(cmd)

		// Validate configuration
		if err := config.Validate(); err != nil {
			presenter.Error(err, "Configuration validation failed")
			os.Exit(1)
		}

		// Validate prerequisites (git repository, gh CLI, authentication)
		if err := validatePrerequisites(); err != nil {
			presenter.Error(err, "Prerequisites validation failed")
			os.Exit(1)
		}

		bin, err := os.Executable()
		if err != nil {
			presenter.Error(err, "Failed to get executable path")
			os.Exit(1)
		}

		// Generate intelligent issue resolution prompt
		prompt := generateIssueResolutionPrompt(bin, config.IssueURL, config.BotMention)

		// Send to LLM using existing architecture
		presenter.Info("Analyzing GitHub issue and determining appropriate resolution workflow...")
		presenter.Separator()

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
			llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
				PromptCache: true,
			})

		presenter.Info(out)
		presenter.Separator()

		// Display usage statistics
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)
	},
}

func init() {
	defaults := NewIssueResolveConfig()
	issueResolveCmd.Flags().StringP("provider", "p", defaults.Provider, "The issue provider to use (currently only 'github')")
	issueResolveCmd.Flags().String("issue-url", defaults.IssueURL, "GitHub issue URL (required)")
	issueResolveCmd.Flags().String("bot-mention", defaults.BotMention, "Bot mention to look for in issue comments")
	issueResolveCmd.MarkFlagRequired("issue-url")
}

// getIssueResolveConfigFromFlags extracts issue-resolve configuration from command flags
func getIssueResolveConfigFromFlags(cmd *cobra.Command) *IssueResolveConfig {
	config := NewIssueResolveConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if issueURL, err := cmd.Flags().GetString("issue-url"); err == nil {
		config.IssueURL = issueURL
	}
	if botMention, err := cmd.Flags().GetString("bot-mention"); err == nil {
		config.BotMention = botMention
	}

	return config
}

// validatePrerequisites checks that all necessary tools and authentication are in place
func validatePrerequisites() error {
	if !isGitRepository() {
		return errors.New("not a git repository. Please run this command from a git repository")
	}

	if !isGhCliInstalled() {
		return errors.New("GitHub CLI (gh) is not installed. Please install it first.\nVisit https://cli.github.com/ for installation instructions")
	}

	if !isGhAuthenticated() {
		return errors.New("you are not authenticated with GitHub. Please run 'gh auth login' first")
	}

	return nil
}

// generateIssueResolutionPrompt creates a comprehensive prompt for resolving GitHub issues
// with intelligent workflow selection based on issue type (implementation vs question).
//
// Parameters:
//   - bin: path to the kodelet executable for subagent commands
//   - issueURL: the GitHub issue URL to resolve
//   - botMention: the bot mention string to look for in comments (e.g., "@kodelet")
//
// Returns a detailed prompt that instructs the AI to:
//  1. Analyze the issue type (implementation vs question)
//  2. Apply the appropriate workflow
//  3. Follow best practices for each type
func generateIssueResolutionPrompt(bin, issueURL, botMention string) string {
	return fmt.Sprintf(`Please resolve the github issue %s following the appropriate workflow based on the issue type:

## Step 1: Analyze the Issue
1. Get the issue details and its comments
   - Preferrably use 'mcp_get_issue_comments' if it is available
	 - If not, use 'gh issue view %s' and 'gh issue view %s --comments' to get the issue details and its comments.
2. Review the issue details and understand the issue.
3. Pay special attention to the latest comment with %s - this is the instruction from the user.
4. Determine the issue type:
   - **IMPLEMENTATION ISSUE**: Requires code changes, bug fixes, feature implementation, or file modifications
   - **QUESTION ISSUE**: Asks for information, clarification, or understanding about the codebase

## Step 2: Choose the Appropriate Workflow

### For IMPLEMENTATION ISSUES (Feature/Fix/Code Changes):
1. Extract the issue number from the issue URL for branch naming
2. Create and checkout a new branch: "git checkout -b kodelet/issue-${ISSUE_NUMBER}-${BRANCH_NAME}"
3. Work on the issue:
   - Think step by step before starting
   - Add extra steps to the todo list for complex issues
   - Do not commit during this step
	 - Make sure that you run 'git add ...' to add the changes to the staging area before you commit.
4. Once resolved, use subagent to run "%s commit --short --no-confirm" to commit changes
5. Use subagent to run "%s pr" (60s timeout) to create a pull request
6. Comment on the issue with the PR link
   - Preferrably use 'mcp_add_issue_comment' if it is available
	 - If not, use 'gh issue comment ...' to comment on the issue.

### For QUESTION ISSUES (Information/Clarification):
1. Understand the question by reading issue comments and analyzing the codebase
2. Research the codebase to gather relevant information to answer the question
3. Once you have a comprehensive understanding, comment directly on the issue with your answer
4. Do NOT create branches, make code changes, or create pull requests

## Examples:

**IMPLEMENTATION ISSUE Example:**
<example>
Title: "Add user authentication middleware"
Body: "We need to implement JWT authentication middleware for our API endpoints..."
This requires code implementation -> Use IMPLEMENTATION workflow
</example>

**QUESTION ISSUE Example:**
<example>
Title: "How does the logging system work?"
Body: "Can someone explain how our current logging implementation handles different log levels..."
This asks for information -> Use QUESTION workflow
</example>

**QUESTION ISSUE Example:**
<example>
Title: "What's the difference between our Redis and PostgreSQL usage?"
Body: "@kodelet can you explain how we use Redis vs PostgreSQL in our architecture..."
This asks for clarification -> Use QUESTION workflow
</example>

**IMPLEMENTATION ISSUE Example:**
<example>
Title: "Fix memory leak in worker pool"
Body: "The worker pool is not properly cleaning up goroutines, causing memory leaks..."
This requires bug fix -> Use IMPLEMENTATION workflow
</example>

IMPORTANT:
* !!!CRITICAL!!!: Never update user's git config under any circumstances
* Use a checklist to keep track of progress
* For questions, focus on providing accurate, helpful information rather than code changes
* For implementation, follow the full development workflow with proper branching and PR creation
`,
		issueURL, issueURL, issueURL, botMention, bin, bin)
}
