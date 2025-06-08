package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// ResolveConfig holds configuration for the resolve command
type ResolveConfig struct {
	Provider   string
	IssueURL   string
	BotMention string
}

// NewResolveConfig creates a new ResolveConfig with default values
func NewResolveConfig() *ResolveConfig {
	return &ResolveConfig{
		Provider:   "github",
		IssueURL:   "",
		BotMention: "@kodelet",
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
	Use:        "resolve",
	Short:      "[DEPRECATED] Use 'issue-resolve' instead - Resolve an issue autonomously",
	Long:       `[DEPRECATED] This command is deprecated. Please use 'kodelet issue-resolve' instead.`,
	Deprecated: "Use 'kodelet issue-resolve' instead",
	Run: func(cmd *cobra.Command, args []string) {
		// Forward to issue-resolve command
		issueResolveCmd.Run(cmd, args)
	},
}

func init() {
	defaults := NewResolveConfig()
	resolveCmd.Flags().StringP("provider", "p", defaults.Provider, "The issue provider to use")
	resolveCmd.Flags().String("issue-url", defaults.IssueURL, "Issue URL (required)")
	resolveCmd.Flags().String("bot-mention", defaults.BotMention, "Bot mention to look for in comments (e.g., @kodelet)")
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
	if botMention, err := cmd.Flags().GetString("bot-mention"); err == nil {
		config.BotMention = botMention
	}

	return config
}

func generateIssueResolutionPrompt(bin, issueURL, botMention string) string {
	return fmt.Sprintf(`Please resolve the github issue %s following the steps below:

1. use "gh issue view %s --comments" to get the issue details.
- review the issue details and understand the issue.
- especially pay attention to the latest comment with %s - this is the instruction from the user.
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
		issueURL, issueURL, botMention, bin, bin)
}
