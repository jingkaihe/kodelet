package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type PRRespondConfig struct {
	Provider        string
	PRURL           string
	ReviewCommentID string
	IssueCommentID  string
}

func NewPRRespondConfig() *PRRespondConfig {
	return &PRRespondConfig{
		Provider:        "github",
		PRURL:           "",
		ReviewCommentID: "",
		IssueCommentID:  "",
	}
}

func (c *PRRespondConfig) Validate() error {
	if c.Provider != "github" {
		return fmt.Errorf("unsupported provider: %s, only 'github' is supported", c.Provider)
	}

	if c.PRURL == "" {
		return errors.New("PR URL cannot be empty")
	}

	// Check that exactly one comment ID is provided
	reviewCommentProvided := c.ReviewCommentID != ""
	issueCommentProvided := c.IssueCommentID != ""

	if !reviewCommentProvided && !issueCommentProvided {
		return errors.New("either --review-id or --issue-comment-id must be provided")
	}

	if reviewCommentProvided && issueCommentProvided {
		return errors.New("only one of --review-id or --issue-comment-id can be provided, not both")
	}

	return nil
}

var prRespondCmd = &cobra.Command{
	Use:   "pr-respond",
	Short: "Intelligently respond to PR comments based on their type",
	Long: `Respond to pull request comments using appropriate workflows based on comment type:

CODE CHANGE REQUESTS (bug fixes, feature updates, implementation feedback):
- Analyzes the specific comment and requirements
- Makes targeted code changes to the PR branch
- Commits changes and pushes updates
- Responds to the comment with a summary of changes

QUESTION REQUESTS (clarifications, explanations, discussions):
- Analyzes the codebase and PR context to understand the question
- Researches relevant code and documentation
- Provides comprehensive answers directly in comment replies
- No code changes or commits made

CODE REVIEW REQUESTS (code quality assessment, security analysis, best practices):
- Conducts comprehensive code review of the PR changes
- Analyzes for security vulnerabilities, performance issues, and best practices
- Provides detailed feedback with specific recommendations
- Organizes findings by category and severity
- No code changes or commits made

The command automatically detects comment type and applies the appropriate workflow.
You must provide either --review-id for review comments or --issue-comment-id for issue comments.
Currently supports GitHub PRs only.

Examples:
  kodelet pr-respond --pr-url https://github.com/user/repo/pull/123 --review-id 456789
  kodelet pr-respond --pr-url https://github.com/user/repo/pull/123 --issue-comment-id 987654`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

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

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			return
		}

		llmConfig, err := llm.GetConfigFromViperWithCmd(cmd)
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager), tools.WithCustomTools(customManager))

		config := getPRRespondConfigFromFlags(cmd)

		if err := config.Validate(); err != nil {
			presenter.Error(err, "Configuration validation failed")
			os.Exit(1)
		}

		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(errors.New("GitHub CLI not installed"), "Please install GitHub CLI first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(errors.New("not authenticated with GitHub"), "Please run 'gh auth login' first")
			os.Exit(1)
		}

		bin, err := os.Executable()
		if err != nil {
			presenter.Error(err, "Failed to get executable path")
			os.Exit(1)
		}

		owner, repo, prNumber, err := parseGitHubURL(config.PRURL)
		if err != nil {
			presenter.Error(err, "Failed to parse GitHub URL")
			os.Exit(1)
		}

		processor, err := fragments.NewFragmentProcessor()
		if err != nil {
			presenter.Error(err, "Failed to create fragment processor")
			os.Exit(1)
		}

		fragmentArgs := map[string]string{
			"pr_url":    config.PRURL,
			"owner":     owner,
			"repo":      repo,
			"pr_number": prNumber,
			"bin":       bin,
		}

		if config.ReviewCommentID != "" {
			fragmentArgs["review_id"] = config.ReviewCommentID
		}
		if config.IssueCommentID != "" {
			fragmentArgs["issue_comment_id"] = config.IssueCommentID
		}

		fragment, err := processor.LoadFragment(ctx, &fragments.Config{
			FragmentName: "github/pr-respond",
			Arguments:    fragmentArgs,
		})
		if err != nil {
			presenter.Error(err, "Failed to load built-in pr-respond recipe")
			os.Exit(1)
		}

		prompt := fragment.Content

		presenter.Info("Analyzing PR comment and determining appropriate response workflow...")
		presenter.Separator()

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
			llmConfig, false, llmtypes.MessageOpt{
				PromptCache: true,
			})

		fmt.Println(out)
		presenter.Separator()

		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)
	},
}

func init() {
	defaults := NewPRRespondConfig()
	prRespondCmd.Flags().StringP("provider", "p", defaults.Provider, "The PR provider to use")
	prRespondCmd.Flags().String("pr-url", defaults.PRURL, "PR URL (required)")
	prRespondCmd.Flags().String("review-id", defaults.ReviewCommentID, "Specific review comment ID to respond to")
	prRespondCmd.Flags().String("issue-comment-id", defaults.IssueCommentID, "Specific issue comment ID to respond to")
	prRespondCmd.MarkFlagRequired("pr-url")
}

func getPRRespondConfigFromFlags(cmd *cobra.Command) *PRRespondConfig {
	config := NewPRRespondConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if prURL, err := cmd.Flags().GetString("pr-url"); err == nil {
		config.PRURL = prURL
	}
	if reviewCommentID, err := cmd.Flags().GetString("review-id"); err == nil {
		config.ReviewCommentID = reviewCommentID
	}
	if issueCommentID, err := cmd.Flags().GetString("issue-comment-id"); err == nil {
		config.IssueCommentID = issueCommentID
	}

	return config
}

// parseGitHubURL extracts owner, repo, and PR number from GitHub PR URL
// Expected URL format: https://github.com/owner/repo/pull/123
// When split by "/", the parts array becomes:
//
//	parts[0]: "https:"
//	parts[1]: "" (empty string)
//	parts[2]: "github.com"
//	parts[3]: "owner" (GitHub username/organization)
//	parts[4]: "repo" (repository name)
//	parts[5]: "pull" (literal "pull")
//	parts[6]: "123" (PR number)
func parseGitHubURL(prURL string) (owner, repo, prNumber string, err error) {
	parts := strings.Split(prURL, "/")
	if len(parts) < 7 {
		return "", "", "", errors.New("invalid PR URL format")
	}
	// Extract: owner (parts[3]), repo (parts[4]), prNumber (parts[6])
	return parts[3], parts[4], parts[6], nil
}
