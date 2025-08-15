package main

import (
	"bufio"
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

// CommitConfig holds configuration for the commit command
type CommitConfig struct {
	NoSign     bool
	Template   string
	Short      bool
	NoConfirm  bool
	NoCoauthor bool
}

// NewCommitConfig creates a new CommitConfig with default values
func NewCommitConfig() *CommitConfig {
	return &CommitConfig{
		NoSign:     false,
		Template:   "",
		Short:      false,
		NoConfirm:  false,
		NoCoauthor: false,
	}
}

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Generate and create a git commit with an AI-generated message",
	Long: `Generate a meaningful commit message based on staged changes and create a signed git commit.
This command analyzes your 'git diff --cached' and uses AI to generate an appropriate commit message.
You must stage your changes (using 'git add') before running this command.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create a new state for the commit operation
		ctx := cmd.Context()

		llmConfig := llm.GetConfigFromViper()
		s := tools.NewBasicState(ctx, tools.WithLLMConfig(llmConfig))

		// Get commit config from flags
		config := getCommitConfigFromFlags(cmd)

		// Check if we're in a git repository
		if !isGitRepository() {
			presenter.Error(errors.New("not a git repository"), "Please run this command from a git repository")
			os.Exit(1)
		}

		// Check if there are staged changes
		if !hasStagedChanges() {
			presenter.Error(errors.New("no staged changes found"), "Please stage your changes using 'git add' first")
			os.Exit(1)
		}

		// Get the git diff of staged changes
		diff, err := getGitDiff()
		if err != nil {
			presenter.Error(err, "Failed to get git diff")
			os.Exit(1)
		}

		// If diff is empty, notify the user and exit
		if len(strings.TrimSpace(diff)) == 0 {
			presenter.Warning("No changes detected. Please stage changes using 'git add' before committing")
			os.Exit(1)
		}

		// Generate commit message using builtin fragment
		prompt, err := loadCommitMessagePrompt(ctx, config)
		if err != nil {
			presenter.Error(err, "Failed to load commit message prompt")
			os.Exit(1)
		}

		presenter.Info("Analyzing staged changes and generating commit message...")

		// Get the commit message using the Thread abstraction with usage stats
		commitMsg, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt, llmConfig, true, llmtypes.MessageOpt{
			UseWeakModel:    true,
			PromptCache:     false,
			NoToolUse:       true,
			DisableUsageLog: true,
		})
		commitMsg = sanitizeCommitMessage(commitMsg)

		presenter.Section("Generated Commit Message")
		fmt.Printf("%s\n\n", commitMsg)

		// Display usage statistics
		usageStats := presenter.ConvertUsageStats(&usage)
		presenter.Stats(usageStats)

		// Confirm with user (unless --no-confirm is set)
		if !config.NoConfirm && !confirmCommit(commitMsg) {
			os.Exit(0)
		}

		// Create the commit
		if err := createCommit(ctx, commitMsg, !config.NoSign, config); err != nil {
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
}

// getCommitConfigFromFlags extracts commit configuration from command flags
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

	return config
}

func sanitizeCommitMessage(message string) string {
	// Remove the starting and ending backticks
	message = strings.TrimPrefix(message, "```")
	message = strings.TrimSuffix(message, "```")
	return message
}

// isGitRepository checks if the current directory is a git repository
func isGitRepository() bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	err := cmd.Run()
	return err == nil
}

// hasStagedChanges checks if there are staged changes ready to commit
func hasStagedChanges() bool {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	err := cmd.Run()
	return err != nil // Non-zero exit code means there are staged changes
}

// getGitDiff returns the git diff for staged changes
func getGitDiff() (string, error) {
	cmd := exec.Command("git", "diff", "--cached")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// confirmCommit asks the user to confirm the commit
func confirmCommit(message string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Create commit with this message? [Y/n/e (edit)]: ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	switch response {
	case "", "y", "yes":
		return true
	case "e", "edit":
		// Allow user to edit the message
		editedMsg := editMessage(message)
		if editedMsg == "" {
			fmt.Println("Commit message is empty. Aborting.")
			return false
		}
		return confirmCommit(editedMsg)
	}

	fmt.Println("Commit aborted.")
	return false
}

// editMessage allows the user to edit the commit message
func editMessage(message string) string {
	// Create a temporary file
	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		fmt.Printf("Error creating temporary file: %s\n", err)
		return message
	}
	defer os.Remove(tempFile.Name())

	// Write the message to the file
	tempFile.WriteString(message)
	tempFile.Close()

	// Get the editor from git config or environment
	editor := getEditor()

	// Open the file in the editor
	cmd := exec.Command(editor, tempFile.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		fmt.Printf("Error opening editor: %s\n", err)
		return message
	}

	// Read the edited message
	content, err := os.ReadFile(tempFile.Name())
	if err != nil {
		fmt.Printf("Error reading temporary file: %s\n", err)
		return message
	}

	return string(content)
}

// getEditor gets the editor to use for editing the commit message
func getEditor() string {
	// Try to get the editor from git config
	gitEditor, err := exec.Command("git", "config", "--get", "core.editor").Output()
	if err == nil && len(gitEditor) > 0 {
		return strings.TrimSpace(string(gitEditor))
	}

	// Try environment variables
	if editor := os.Getenv("GIT_EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	// Default to vim
	return "vim"
}

// createCommit creates a git commit with the provided message
func createCommit(_ context.Context, message string, sign bool, config *CommitConfig) error {
	// Add co-authorship attribution if enabled
	if !config.NoCoauthor {
		coauthorEnabled := viper.GetBool("commit.coauthor.enabled")
		if coauthorEnabled {
			coauthorName := viper.GetString("commit.coauthor.name")
			coauthorEmail := viper.GetString("commit.coauthor.email")
			message = message + fmt.Sprintf("\n\nCo-authored-by: %s <%s>", coauthorName, coauthorEmail)
		}
	}

	// Create a temporary file for the commit message
	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		return errors.Wrapf(err, "error creating temporary file")
	}
	defer os.Remove(tempFile.Name())

	// Write the message to the file
	if _, err := tempFile.WriteString(message); err != nil {
		return errors.Wrapf(err, "error writing to temporary file")
	}
	tempFile.Close()

	// Prepare git commit command
	args := []string{"commit", "-F", tempFile.Name()}
	if sign {
		args = append(args, "-s")
	}

	// Execute git commit
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// loadCommitMessagePrompt loads the commit message prompt using the builtin fragment system
func loadCommitMessagePrompt(ctx context.Context, config *CommitConfig) (string, error) {
	// Create fragment processor with builtin fragments enabled
	processor, err := fragments.NewFragmentProcessor(fragments.WithBuiltinFragments())
	if err != nil {
		return "", errors.Wrap(err, "failed to create fragment processor")
	}

	// Prepare fragment arguments
	args := map[string]string{}
	
	// Add context based on configuration
	var contextParts []string
	if config.Template != "" {
		contextParts = append(contextParts, fmt.Sprintf("Use this template: %s", config.Template))
	}
	if config.Short {
		contextParts = append(contextParts, "Generate a short, single-line commit message with no bullet points")
	}
	if len(contextParts) > 0 {
		args["Context"] = strings.Join(contextParts, ". ")
	}

	// Load and process the builtin commit-message fragment
	fragmentConfig := &fragments.Config{
		FragmentName: "commit-message",
		Arguments:    args,
	}

	fragment, err := processor.LoadFragment(ctx, fragmentConfig)
	if err != nil {
		return "", errors.Wrap(err, "failed to load commit-message fragment")
	}

	return fragment.Content, nil
}
