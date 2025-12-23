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
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type PRConfig struct {
	Provider     string
	Target       string
	TemplateFile string
	Draft        bool
	NoSave       bool
	ResultOnly   bool
}

func NewPRConfig() *PRConfig {
	return &PRConfig{
		Provider:     "github",
		Target:       "main",
		TemplateFile: "",
		Draft:        false,
		NoSave:       false,
		ResultOnly:   false,
	}
}

func (c *PRConfig) Validate() error {
	if c.Provider != "github" {
		return fmt.Errorf("unsupported provider: %s, only 'github' is supported", c.Provider)
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
			os.Exit(1)
		}

		customManager, err := tools.CreateCustomToolManagerFromViper(ctx)
		if err != nil {
			presenter.Error(err, "Failed to create custom tool manager")
			os.Exit(1)
		}

		llmConfig, err := llm.GetConfigFromViper()
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}
		llmConfig.NoHooks = true // Disable hooks by default for pr command
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig), tools.WithMCPTools(mcpManager), tools.WithCustomTools(customManager))

		config := getPRConfigFromFlags(cmd)

		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !isGhCliInstalled() {
			presenter.Error(errors.New("GitHub CLI not installed"), "GitHub CLI (gh) is not installed. Please install it first")
			presenter.Info("Visit https://cli.github.com/ for installation instructions")
			os.Exit(1)
		}

		if !isGhAuthenticated() {
			presenter.Error(errors.New("not authenticated with GitHub"), "You are not authenticated with GitHub. Please run 'gh auth login' first")
			os.Exit(1)
		}

		processor, err := fragments.NewFragmentProcessor()
		if err != nil {
			presenter.Error(err, "Failed to create fragment processor")
			os.Exit(1)
		}

		fragmentArgs := map[string]string{
			"target": config.Target,
		}

		if config.TemplateFile != "" {
			fragmentArgs["template_file"] = config.TemplateFile
		}

		if config.Draft {
			fragmentArgs["draft"] = "true"
		} else {
			fragmentArgs["draft"] = "false"
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

		if config.ResultOnly {
			presenter.SetQuiet(true)
			logger.SetLogLevel("error")
		} else {
			presenter.Info("Analyzing branch changes and generating PR description...")
			presenter.Separator()
		}

		out, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt, llmConfig, config.ResultOnly, llmtypes.MessageOpt{
			PromptCache:        true,
			NoSaveConversation: config.NoSave,
		})

		fmt.Println(out)

		if !config.ResultOnly {
			presenter.Separator()

			usageStats := presenter.ConvertUsageStats(&usage)
			presenter.Stats(usageStats)
		}
	},
}

func init() {
	defaults := NewPRConfig()
	prCmd.Flags().StringP("provider", "p", defaults.Provider, "The CVS provider to use")
	prCmd.Flags().StringP("target", "t", defaults.Target, "The target branch to create the pull request on")
	prCmd.Flags().String("template-file", defaults.TemplateFile, "The path to the template file for the pull request")
	prCmd.Flags().BoolP("draft", "d", defaults.Draft, "Create the pull request as a draft")
	prCmd.Flags().Bool("no-save", defaults.NoSave, "Disable conversation persistence")
	prCmd.Flags().Bool("result-only", defaults.ResultOnly, "Only print the final agent message, suppressing all intermediate output and usage statistics")
}

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
	if noSave, err := cmd.Flags().GetBool("no-save"); err == nil {
		config.NoSave = noSave
	}
	if resultOnly, err := cmd.Flags().GetBool("result-only"); err == nil {
		config.ResultOnly = resultOnly
	}

	return config
}

func isGhCliInstalled() bool {
	cmd := exec.Command("gh", "--version")
	err := cmd.Run()
	return err == nil
}

func isGhAuthenticated() bool {
	cmd := exec.Command("gh", "auth", "status")
	err := cmd.Run()
	return err == nil
}
