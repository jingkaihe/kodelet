package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// PRConfig holds configuration for the pr command
type PRConfig struct {
	Provider     string
	Target       string
	TemplateFile string
	Draft        bool
}

// NewPRConfig creates a new PRConfig with default values
func NewPRConfig() *PRConfig {
	return &PRConfig{
		Provider:     "github",
		Target:       "main",
		TemplateFile: "",
		Draft:        false,
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

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Create a pull request with AI-generated title and description",
	Long: `Create a pull request for the changes you have made on the current branch.

This command analyzes the current branch changes compared to the target branch and generates an appropriate PR title and description.

Use the --draft flag to create a draft pull request that is not ready for review.`,
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

		// Load the built-in pr fragment
		processor, err := fragments.NewFragmentProcessor()
		if err != nil {
			presenter.Error(err, "Failed to create fragment processor")
			os.Exit(1)
		}

		// Prepare template arguments
		fragmentArgs := map[string]string{
			"target": config.Target,
		}

		// Add template file if provided
		if config.TemplateFile != "" {
			fragmentArgs["template_file"] = config.TemplateFile
		}

		// Add draft flag
		if config.Draft {
			fragmentArgs["draft"] = "true"
		}

		fragment, err := processor.LoadFragment(ctx, &fragments.Config{
			FragmentName: "github/pr",
			Arguments:    fragmentArgs,
		})
		if err != nil {
			presenter.Error(err, "Failed to load built-in pr recipe")
			os.Exit(1)
		}

		prompt := fragment.Content

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
	prCmd.Flags().BoolP("draft", "d", defaults.Draft, "Create the pull request as a draft")
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
	if draft, err := cmd.Flags().GetBool("draft"); err == nil {
		config.Draft = draft
	}

	return config
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
