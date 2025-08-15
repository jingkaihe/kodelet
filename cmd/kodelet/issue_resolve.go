package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/fragments"
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

		// Use the builtin fragment system for the prompt
		prompt, err := loadIssueResolvePrompt(ctx, bin, config.IssueURL, config.BotMention)
		if err != nil {
			presenter.Error(err, "Failed to load issue resolution prompt")
			os.Exit(1)
		}

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

// loadIssueResolvePrompt loads the issue resolution prompt using the builtin fragment system
//
// Parameters:
//   - ctx: the context for logging and cancellation
//   - bin: path to the kodelet executable for subagent commands
//   - issueURL: the GitHub issue URL to resolve
//   - botMention: the bot mention string to look for in comments (e.g., "@kodelet")
//
// Returns the processed prompt from the builtin fragment with variables substituted
func loadIssueResolvePrompt(ctx context.Context, bin, issueURL, botMention string) (string, error) {
	// Create fragment processor with builtin fragments enabled
	processor, err := fragments.NewFragmentProcessor(fragments.WithBuiltinFragments())
	if err != nil {
		return "", errors.Wrap(err, "failed to create fragment processor")
	}

	// Prepare fragment arguments
	args := map[string]string{
		"BinPath":    bin,
		"IssueURL":   issueURL,
		"BotMention": botMention,
	}

	// Load and process the builtin issue-resolve fragment
	fragmentConfig := &fragments.Config{
		FragmentName: "issue-resolve",
		Arguments:    args,
	}

	fragment, err := processor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to load issue-resolve fragment")
	}

	return fragment.Content, nil
}
