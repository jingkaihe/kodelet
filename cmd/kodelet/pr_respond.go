package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/spf13/cobra"
)

// PRRespondConfig holds configuration for the pr-respond command
type PRRespondConfig struct {
	Provider  string
	PRURL     string
	CommentID string
}

// PRData holds prefetched PR information
type PRData struct {
	BasicInfo    string
	Comments     string
	Reviews      string
}

// NewPRRespondConfig creates a new PRRespondConfig with default values
func NewPRRespondConfig() *PRRespondConfig {
	return &PRRespondConfig{
		Provider:  "github",
		PRURL:     "",
		CommentID: "",
	}
}

// Validate validates the PRRespondConfig and returns an error if invalid
func (c *PRRespondConfig) Validate() error {
	if c.Provider != "github" {
		return fmt.Errorf("unsupported provider: %s, only 'github' is supported", c.Provider)
	}

	if c.PRURL == "" {
		return fmt.Errorf("PR URL cannot be empty")
	}

	return nil
}

var prRespondCmd = &cobra.Command{
	Use:   "pr-respond",
	Short: "Respond to a specific PR comment with code changes",
	Long: `Respond to a specific pull request comment by analyzing the feedback and implementing the requested changes.

This command focuses on addressing a specific comment or review feedback within a PR. If no comment ID is provided, it will address the most recent @kodelet mention. Currently supports GitHub PRs only.`,
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

		// Get pr-respond config from flags
		config := getPRRespondConfigFromFlags(cmd)

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

		// Prefetch PR data
		fmt.Println("Prefetching PR data...")
		prData, err := prefetchPRData(config.PRURL)
		if err != nil {
			fmt.Printf("Error prefetching PR data: %v\n", err)
			os.Exit(1)
		}

		// Generate comprehensive prompt with prefetched data
		prompt := generatePRRespondPrompt(bin, config.PRURL, config.CommentID, prData)

		// Send to LLM using existing architecture
		fmt.Println("Analyzing specific PR comment and implementing response...")
		fmt.Println("-----------------------------------------------------------")

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt,
			llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
				PromptCache: true,
			})

		fmt.Println(out)
		fmt.Println("-----------------------------------------------------------")

		// Display usage statistics
		fmt.Printf("\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
			usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

		fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
			usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())
	},
}

func init() {
	defaults := NewPRRespondConfig()
	prRespondCmd.Flags().StringP("provider", "p", defaults.Provider, "The PR provider to use")
	prRespondCmd.Flags().String("pr-url", defaults.PRURL, "PR URL (required)")
	prRespondCmd.Flags().String("comment-id", defaults.CommentID, "Specific comment ID to respond to (optional, will find latest @kodelet mention if not provided)")
	prRespondCmd.MarkFlagRequired("pr-url")
}

// getPRRespondConfigFromFlags extracts pr-respond configuration from command flags
func getPRRespondConfigFromFlags(cmd *cobra.Command) *PRRespondConfig {
	config := NewPRRespondConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if prURL, err := cmd.Flags().GetString("pr-url"); err == nil {
		config.PRURL = prURL
	}
	if commentID, err := cmd.Flags().GetString("comment-id"); err == nil {
		config.CommentID = commentID
	}

	return config
}

// prefetchPRData fetches PR information, comments, and reviews using gh CLI
func prefetchPRData(prURL string) (*PRData, error) {
	data := &PRData{}
	
	// Get basic PR information
	cmd := exec.Command("gh", "pr", "view", prURL)
	basicInfoOutput, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR basic info: %w", err)
	}
	data.BasicInfo = strings.TrimSpace(string(basicInfoOutput))
	
	// Get PR comments
	cmd = exec.Command("gh", "pr", "view", prURL, "--comments")
	commentsOutput, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get PR comments: %w", err)
	}
	data.Comments = strings.TrimSpace(string(commentsOutput))
	
	// Get PR reviews (try to get them, but don't fail if not available)
	cmd = exec.Command("gh", "pr", "view", prURL, "--json", "reviews")
	reviewsOutput, err := cmd.Output()
	if err == nil {
		data.Reviews = strings.TrimSpace(string(reviewsOutput))
	} else {
		data.Reviews = "No reviews data available"
	}
	
	return data, nil
}

func generatePRRespondPrompt(bin, prURL, commentID string, prData *PRData) string {
	commentInstruction := ""
	if commentID != "" {
		commentInstruction = fmt.Sprintf(`

Focus on the specific comment ID: %s by reviewing the comments data above.`, commentID)
	} else {
		commentInstruction = `

Find the most recent @kodelet mention by reviewing the comments data above. If no @kodelet mention is found, address the most recent review comment.`
	}

	return fmt.Sprintf(`Please respond to a specific comment in pull request %s following the steps below:

<pr_basic_info>
%s
</pr_basic_info>

<pr_comments>
%s
</pr_comments>

<pr_reviews>
%s
</pr_reviews>

Based on the PR information provided above:%s

1. Check the current state of the PR branch:
   - Use "git checkout <pr-branch>" to switch to the PR branch
   - Run "git pull origin <pr-branch>" to ensure latest changes
   - Check current working directory state

2. Analyze the specific comment request:
   - Review the PR comments section above to understand exactly what is being asked for
   - Determine if it requires code changes, documentation, tests, or clarification
   - Create a focused todo list for this specific request
   - If the request is unclear, ask for clarification in your comment response, do not implement any changes

3. Implement the specific change:
   - Focus only on what was requested in the comment
   - Make precise, targeted changes
   - Avoid scope creep or unrelated improvements

4. Respond appropriately:
   - Make necessary code changes if requested
   - Ask subagent to run "%s commit --short --no-confirm" for changes
   - Push updates with "git push origin <pr-branch>"
   - Reply to the specific comment with a summary of actions taken

IMPORTANT:
- !!!CRITICAL!!!: You should never update user's git config under any circumstances.
- Focus ONLY on the specific comment/request - don't address other feedback
- Be precise and targeted in your response
- If the request is unclear, ask for clarification in your comment response
- Always acknowledge the specific comment you're responding to
`,
		prURL, prData.BasicInfo, prData.Comments, prData.Reviews, commentInstruction, bin)
}
