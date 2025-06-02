package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

// ResolveConfig holds configuration for the resolve command
type ResolveConfig struct {
	Provider string
	IssueURL string
}

// NewResolveConfig creates a new ResolveConfig with default values
func NewResolveConfig() *ResolveConfig {
	return &ResolveConfig{
		Provider: "github",
		IssueURL: "",
	}
}

// Validate validates the ResolveConfig and returns an error if invalid
func (c *ResolveConfig) Validate() error {
	if c.Provider != "github" {
		return fmt.Errorf("unsupported provider: %s, only 'github' is supported", c.Provider)
	}

	if c.IssueURL == "" {
		return fmt.Errorf("issue URL cannot be empty")
	}

	return nil
}

var resolveCmd = &cobra.Command{
	Use:   "resolve",
	Short: "Resolve an issue autonomously",
	Long: `Resolve an issue by fetching details, creating a branch, implementing fixes, and creating a PR.

This command analyzes the issue, creates an appropriate branch, works on the issue resolution, and automatically creates a pull request with updates back to the original issue. Currently supports GitHub issues only.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// Set up signal handling
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\n\033[1;33m[kodelet]: Cancellation requested, shutting down...\033[0m")
			cancel()
		}()

		mcpManager, err := tools.CreateMCPManagerFromViper(ctx)
		if err != nil {
			fmt.Printf("\n\033[1;31mError creating MCP manager: %v\033[0m\n", err)
			return
		}

		s := tools.NewBasicState(ctx, tools.WithMCPTools(mcpManager))

		// Get resolve config from flags
		config := getResolveConfigFromFlags(cmd)

		// Validate configuration
		if err := config.Validate(); err != nil {
			fmt.Printf("Error: %s\n", err)
			os.Exit(1)
		}

		// Prerequisites checking
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

		bin, err := os.Executable()
		if err != nil {
			fmt.Println("Error: Failed to get executable path")
			os.Exit(1)
		}

		// Generate comprehensive prompt
		prompt := generateIssueResolutionPrompt(bin, config.IssueURL)

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
	defaults := NewResolveConfig()
	resolveCmd.Flags().StringP("provider", "p", defaults.Provider, "The issue provider to use")
	resolveCmd.Flags().String("issue-url", defaults.IssueURL, "Issue URL (required)")
	resolveCmd.MarkFlagRequired("issue-url")
}

// getResolveConfigFromFlags extracts resolve configuration from command flags
func getResolveConfigFromFlags(cmd *cobra.Command) *ResolveConfig {
	config := NewResolveConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if issueURL, err := cmd.Flags().GetString("issue-url"); err == nil {
		config.IssueURL = issueURL
	}

	return config
}

func generateIssueResolutionPrompt(bin, issueURL string) string {
	return fmt.Sprintf(`Please resolve the github issue %s following the steps below:

1. use "gh issue view %s --comments" to get the issue details.
- review the issue details and understand the issue.
- especially pay attention to the latest comment with @kodelet - this is the instruction from the user.
- extract the issue number from the issue URL for branch naming

2. based on the issue details, come up with a branch name and checkout the branch via "git checkout -b kodelet/issue-${ISSUE_NUMBER}-${BRANCH_NAME}"
3. start to work on the issue.
- think step by step before you start to work on the issue.
- if the issue is complex, you should add extra steps to the todo list to help you keep track of the progress.
- do not commit during this step.

4. once you have resolved the issue, ask the subagent to run "%s commit --short --no-confirm" to commit the changes.
5. after committing the changes, ask the subagent to run "%s pr" to create a pull request. Please instruct the subagent to always returning the PR link in the final response.
6. once the pull request is created, comment on the issue with the link to the pull request. If the pull request is not created, ask the subagent to create a pull request.

IMPORTANT:
*!!!CRITICAL!!!: You should never update user's git config under any circumstances.
* Use a checklist to keep track of the progress.
`,
		issueURL, issueURL, bin, bin)
}
