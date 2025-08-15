package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

		// Generate the prompt using builtin fragment
		prompt, err := loadPRGenerationPrompt(ctx, config, template)
		if err != nil {
			presenter.Error(err, "Failed to load PR generation prompt")
			os.Exit(1)
		}

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

// loadPRGenerationPrompt loads the PR generation prompt using the builtin fragment system
func loadPRGenerationPrompt(ctx context.Context, config *PRConfig, template string) (string, error) {
	// Create fragment processor with builtin fragments enabled
	processor, err := fragments.NewFragmentProcessor(fragments.WithBuiltinFragments())
	if err != nil {
		return "", errors.Wrap(err, "failed to create fragment processor")
	}

	// Prepare fragment arguments
	args := map[string]string{
		"TargetBranch": config.Target,
		"Template":     template,
	}

	// Add context based on configuration if needed
	var contextParts []string
	if config.Provider != "github" {
		contextParts = append(contextParts, fmt.Sprintf("Using provider: %s", config.Provider))
	}
	if config.TemplateFile != "" {
		contextParts = append(contextParts, fmt.Sprintf("Using custom template from: %s", config.TemplateFile))
	}
	if len(contextParts) > 0 {
		args["Context"] = fmt.Sprintf("Configuration notes: %s", strings.Join(contextParts, "; "))
	}

	// Load and process the builtin pr-generation fragment
	fragmentConfig := &fragments.Config{
		FragmentName: "pr-generation",
		Arguments:    args,
	}

	fragment, err := processor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to load pr-generation fragment")
	}

	return fragment.Content, nil
}
