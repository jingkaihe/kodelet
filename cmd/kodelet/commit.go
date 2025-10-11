package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type CommitConfig struct {
	NoSign     bool
	Template   string
	Short      bool
	NoConfirm  bool
	NoCoauthor bool
	NoSave     bool
}

func NewCommitConfig() *CommitConfig {
	return &CommitConfig{
		NoSign:     false,
		Template:   "",
		Short:      false,
		NoConfirm:  false,
		NoCoauthor: false,
		NoSave:     false,
	}
}

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Generate and create a git commit with an AI-generated message",
	Long: `Generate a meaningful commit message based on staged changes and create a signed git commit.
This command analyzes your 'git diff --cached' and uses AI to generate an appropriate commit message.
You must stage your changes (using 'git add') before running this command.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		llmConfig, err := llm.GetConfigFromViper()
		if err != nil {
			presenter.Error(err, "Failed to load configuration")
			return
		}
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig))

		config := getCommitConfigFromFlags(cmd)

		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		if !hasStagedChanges() {
			presenter.Error(errors.New("no staged changes found"), "Please stage your changes using 'git add' first")
			os.Exit(1)
		}

		processor, err := fragments.NewFragmentProcessor()
		if err != nil {
			presenter.Error(err, "Failed to create fragment processor")
			os.Exit(1)
		}

		fragmentArgs := map[string]string{}

		if config.Template != "" {
			fragmentArgs["template"] = config.Template
		}

		if config.Short {
			fragmentArgs["short"] = "true"
		}

		fragment, err := processor.LoadFragment(ctx, &fragments.Config{
			FragmentName: "commit",
			Arguments:    fragmentArgs,
		})
		if err != nil {
			presenter.Error(err, "Failed to load built-in commit recipe")
			os.Exit(1)
		}

		prompt := fragment.Content

		presenter.Info("Analyzing staged changes and generating commit message...")

		commitMsg, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt, llmConfig, true, llmtypes.MessageOpt{
			UseWeakModel:       true,
			PromptCache:        false,
			NoToolUse:          true,
			DisableUsageLog:    true,
			NoSaveConversation: config.NoSave,
		})
		commitMsg = sanitizeCommitMessage(commitMsg)

		presenter.Section("Generated Commit Message")
		presenter.Info(commitMsg)

		// Display usage statistics
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)

		finalCommitMsg := commitMsg
		if !config.NoConfirm {
			confirmed, editedMsg := confirmCommit(commitMsg)
			if !confirmed {
				os.Exit(0)
			}
			finalCommitMsg = editedMsg
		}

		if err := createCommit(ctx, finalCommitMsg, !config.NoSign, config); err != nil {
			presenter.Error(err, "Failed to create commit")
			os.Exit(1)
		}

		presenter.Success("Commit created successfully!")
	},
}

func init() {
	defaults := NewCommitConfig()
	commitCmd.Flags().Bool("no-sign", defaults.NoSign, "Disable commit signing")
	commitCmd.Flags().StringP("template", "t", defaults.Template, "Template for commit message")
	commitCmd.Flags().Bool("short", defaults.Short, "Generate a short commit message with just a description, no bullet points")
	commitCmd.Flags().Bool("no-confirm", defaults.NoConfirm, "Skip confirmation prompt and create commit automatically")
	commitCmd.Flags().Bool("no-coauthor", defaults.NoCoauthor, "Disable coauthor attribution in commit messages")
	commitCmd.Flags().Bool("no-save", defaults.NoSave, "Disable conversation persistence")
}

func getCommitConfigFromFlags(cmd *cobra.Command) *CommitConfig {
	config := NewCommitConfig()

	if noSign, err := cmd.Flags().GetBool("no-sign"); err == nil {
		config.NoSign = noSign
	}
	if template, err := cmd.Flags().GetString("template"); err == nil {
		config.Template = template
	}
	if short, err := cmd.Flags().GetBool("short"); err == nil {
		config.Short = short
	}
	if noConfirm, err := cmd.Flags().GetBool("no-confirm"); err == nil {
		config.NoConfirm = noConfirm
	}
	if noCoauthor, err := cmd.Flags().GetBool("no-coauthor"); err == nil {
		config.NoCoauthor = noCoauthor
	}
	if noSave, err := cmd.Flags().GetBool("no-save"); err == nil {
		config.NoSave = noSave
	}

	return config
}

func sanitizeCommitMessage(message string) string {
	message = strings.TrimPrefix(message, "```")
	message = strings.TrimSuffix(message, "```")
	return message
}

func isGitRepository() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	return err == nil
}

func hasStagedChanges() bool {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	err := cmd.Run()
	return err != nil
}

func confirmCommit(message string) (bool, string) {
	response := presenter.Prompt("Create commit with this message?", "Y/n/e (edit)")
	response = strings.ToLower(response)

	switch response {
	case "", "y", "yes":
		return true, message
	case "e", "edit":
		editedMsg := editMessage(message)
		if editedMsg == "" {
			presenter.Warning("Commit message is empty. Aborting.")
			return false, message
		}
		return confirmCommit(editedMsg)
	}

	presenter.Info("Commit aborted.")
	return false, message
}

func editMessage(message string) string {
	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		presenter.Error(err, "Failed to create temporary file")
		return message
	}
	defer os.Remove(tempFile.Name())

	tempFile.WriteString(message)
	tempFile.Close()

	editor := getEditor()

	cmd := exec.Command(editor, tempFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		presenter.Error(err, "Failed to open editor")
		return message
	}

	content, err := os.ReadFile(tempFile.Name())
	if err != nil {
		presenter.Error(err, "Failed to read edited message")
		return message
	}

	return string(content)
}

func getEditor() string {
	gitEditor, err := exec.Command("git", "config", "--get", "core.editor").Output()
	if err == nil && len(gitEditor) > 0 {
		return strings.TrimSpace(string(gitEditor))
	}

	if editor := os.Getenv("GIT_EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	return "vim"
}

func createCommit(_ context.Context, message string, sign bool, config *CommitConfig) error {
	if !config.NoCoauthor {
		coauthorEnabled := viper.GetBool("commit.coauthor.enabled")
		if coauthorEnabled {
			coauthorName := viper.GetString("commit.coauthor.name")
			coauthorEmail := viper.GetString("commit.coauthor.email")
			message = message + fmt.Sprintf("\n\nCo-authored-by: %s <%s>", coauthorName, coauthorEmail)
		}
	}

	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		return errors.Wrapf(err, "error creating temporary file")
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(message); err != nil {
		return errors.Wrapf(err, "error writing to temporary file")
	}
	tempFile.Close()

	args := []string{"commit", "-F", tempFile.Name()}
	if sign {
		args = append(args, "-s")
	}

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
