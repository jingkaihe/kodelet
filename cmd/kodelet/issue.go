package main

import (
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

var issueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Resolve a GitHub issue autonomously",
	Long: `Resolve a GitHub issue by fetching details, creating a branch, implementing fixes, and creating a PR.

This command analyzes the GitHub issue, creates an appropriate branch, works on the issue resolution, and automatically creates a pull request with updates back to the original issue.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		s := tools.NewBasicState(ctx)

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

		// Get issue URL from flags
		issueURL, _ := cmd.Flags().GetString("issue-url")
		if issueURL == "" {
			fmt.Println("Error: --issue-url is required")
			os.Exit(1)
		}

		bin, err := os.Executable()
		if err != nil {
			fmt.Println("Error: Failed to get executable path")
			os.Exit(1)
		}

		// Generate comprehensive prompt
		prompt := generateIssueResolutionPrompt(bin, issueURL)

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
	issueCmd.Flags().String("issue-url", "", "GitHub issue URL (required)")
	issueCmd.MarkFlagRequired("issue-url")
}

func generateIssueResolutionPrompt(bin, issueURL string) string {
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

4. once you have resolved the issue, ask the subagent to run "%s commit --short --no-confirm" to commit the changes.
5. after committing the changes, ask the subagent to run "%s pr" to create a pull request. Please instruct the subagent to always returning the PR link in the final response.
6. once the pull request is created, comment on the issue with the link to the pull request. If the pull request is not created, ask the subagent to create a pull request.

IMPORTANT:
*!!!CRITICAL!!!: You should never update user's git config under any circumstances.
* Use a checklist to keep track of the progress.
`,
		issueURL, issueURL, bin, bin)
}
