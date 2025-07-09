package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// PRConfig holds configuration for the pr command
type PRConfig struct {
	Provider     string
	Target       string
	TemplateFile string
}

// NewPRConfig creates a new PRConfig with default values
func NewPRConfig() *PRConfig {
	return &PRConfig{
		Provider:     "github",
		Target:       "main",
		TemplateFile: "",
	}
}

// Validate validates the PRConfig and returns an error if invalid
func (c *PRConfig) Validate() error {
	if c.Provider != "github" {
		return errors.New(fmt.Sprintf("unsupported provider: %s, only 'github' is supported", c.Provider))
	}

	if c.Target == "" {
		return errors.New("target branch cannot be empty")
	}

	return nil
}

// Default PR template
const defaultTemplate = `## Description
<high level summary of the changes>

## Changes
<changes in a few bullet points>

## Impact
<impact in a few bullet points>
`

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Create a pull request with AI-generated title and description",
	Long: `Create a pull request for the changes you have made on the current branch.

This command analyzes the current branch changes compared to the target branch and generates an appropriate PR title and description.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create a new state for the PR operation
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
			os.Exit(1)
		}

		llmConfig := llm.GetConfigFromViper()
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager))

		// Get PR config from flags
		config := getPRConfigFromFlags(cmd)

		// Check prerequisites
		// 1. Check if we're in a git repository
		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		// 2. Check if the user has GitHub CLI installed
		if !isGhCliInstalled() {
			presenter.Error(errors.New("GitHub CLI not installed"), "GitHub CLI (gh) is not installed. Please install it first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		// 3. Check if the user is authenticated with GitHub
		if !isGhAuthenticated() {
			presenter.Error(errors.New("not authenticated with GitHub"), "You are not authenticated with GitHub. Please run 'gh auth login' first")
			os.Exit(1)
		}

		// 4. Check if there are uncommitted changes
		// if hasUncommittedChanges() {
		// 	fmt.Println("Error: You have uncommitted changes. Please commit or stash them before creating a PR.")
		// 	os.Exit(1)
		// }

		// Load the template
		template := loadTemplate(config.TemplateFile)

		// Generate the prompt for the LLM
		prompt := fmt.Sprintf(`Create a pull request for the changes you have made on the current branch.

Please create a pull request following the steps below:

1. make sure that the branch is up to date with the target branch. Push the branch to the remote repository if it is not already up to date.

2. To understand the current state of the branch, use the batch tool to parallelise the following checks:
  - Run "git status" to check the the current status and any untracked files
  - Run "git diff" to check the changes to the working directory
  - Run "git diff --cached" to check the changes to the staging area
  - Run "git diff %s...HEAD" to understand the changes to the target branch
  - Run "git log --oneline %s...HEAD" to understand the commit history

3. Thoroughly review and analyse the changes, and wrap up your thoughts into the following sections:
- The category of the changes (chore, feat, fix, refactor, perf, test, style, docs, build, ci, revert)
- A summary of the changes as a title
- A detailed description of the changes based on the changes impact on the project.
- Break down the changes into a few bullet points

4. Create a pull request against the target branch %s:
- **MUST USE** the 'mcp_create_pull_request' MCP tool if it is available in your tool list
- The 'mcp_create_pull_request' tool requires: owner, repo, title, body, head (current branch), base (target branch)
- Only use 'gh pr create ...' bash command as a last resort fallback if the MCP tool is not available

The body of the pull request should follow the following format:

<pr_body_format>
%s
</pr_body_format>

IMPORTANT:
- After the batch command run, when you performing the PR analysis, do not carry out extra tool calls to gather extra information, but instead use the information provided by the batch tool.
- Once you have created the PR, provide a link to the PR in your final response.
- !!!CRITICAL!!!: You should never update user's git config under any circumstances.`, config.Target, config.Target, config.Target, template)

		// Send the prompt to the LLM
		presenter.Info("Analyzing branch changes and generating PR description...")
		presenter.Separator()

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt, llm.GetConfigFromViper(), false, llmtypes.MessageOpt{
			// UseWeakModel:       false,
			PromptCache: true,
			// NoToolUse:          false,
			// NoSaveConversation: true,
		})

		fmt.Println(out)

		presenter.Separator()

		// Display usage statistics
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)
	},
}

func init() {
	defaults := NewPRConfig()
	prCmd.Flags().StringP("provider", "p", defaults.Provider, "The CVS provider to use")
	prCmd.Flags().StringP("target", "t", defaults.Target, "The target branch to create the pull request on")
	prCmd.Flags().String("template-file", defaults.TemplateFile, "The path to the template file for the pull request")
}

// getPRConfigFromFlags extracts PR configuration from command flags
func getPRConfigFromFlags(cmd *cobra.Command) *PRConfig {
	config := NewPRConfig()

	if provider, err := cmd.Flags().GetString("provider"); err == nil {
		config.Provider = provider
	}
	if target, err := cmd.Flags().GetString("target"); err == nil {
		config.Target = target
	}
	if templateFile, err := cmd.Flags().GetString("template-file"); err == nil {
		config.TemplateFile = templateFile
	}

	return config
}

// loadTemplate loads the template from a file or returns the default template
func loadTemplate(templateFile string) string {
	if templateFile == "" {
		return defaultTemplate
	}

	content, err := os.ReadFile(templateFile)
	if err != nil {
		presenter.Warning(fmt.Sprintf("Could not read template file %s: %s", templateFile, err))
		presenter.Info("Using default template instead")
		return defaultTemplate
	}

	return string(content)
}

// isGhCliInstalled checks if GitHub CLI is installed
func isGhCliInstalled() bool {
	cmd := exec.Command("gh", "--version")
	err := cmd.Run()
	return err == nil
}

// isGhAuthenticated checks if the user is authenticated with GitHub
func isGhAuthenticated() bool {
	cmd := exec.Command("gh", "auth", "status")
	err := cmd.Run()
	return err == nil
}
