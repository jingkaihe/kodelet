package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/llm/types"
	"github.com/jingkaihe/kodelet/pkg/state"
	"github.com/spf13/cobra"
)

var (
	noSign   bool
	template string
	short    bool
)

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Generate and create a git commit with an AI-generated message",
	Long: `Generate a meaningful commit message based on staged changes and create a signed git commit.
This command analyzes your 'git diff --cached' and uses AI to generate an appropriate commit message.
You must stage your changes (using 'git add') before running this command.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Create a new state for the commit operation
		s := state.NewBasicState()
		ctx := context.Background()

		// Check if we're in a git repository
		if !isGitRepository() {
			fmt.Println("Error: Not a git repository. Please run this command from a git repository.")
			os.Exit(1)
		}

		// Check if there are staged changes
		if !hasStagedChanges() {
			fmt.Println("Error: No staged changes found. Please stage your changes using 'git add' first.")
			os.Exit(1)
		}

		// Get the git diff of staged changes
		diff, err := getGitDiff()
		if err != nil {
			fmt.Printf("Error getting git diff: %s\n", err)
			os.Exit(1)
		}

		// If diff is empty, notify the user and exit
		if len(strings.TrimSpace(diff)) == 0 {
			fmt.Println("No changes detected. Please stage changes using 'git add' before committing.")
			os.Exit(1)
		}

		// Generate commit message based on diff
		var prompt string
		if template != "" {
			prompt = fmt.Sprintf("Generate a commit message following this template: '%s' for the following git diff:\n\n%s", template, diff)
		} else if short {
			prompt = fmt.Sprintf(`Generate a concise commit message following conventional commits format for the following git diff.
The commit message should have only a short, descriptive title that summarizes the changes.

IMPORTANT: The output of the commit message should be a single line with no bullet points or additional descriptions. It should not be wrapped with any markdown code block.
%s`, diff)
		} else {
			prompt = fmt.Sprintf(`Generate a concise commit message following conventional commits format for the following git diff.
The commit message should have:
* A short description as the title
* Bullet points that breaks down the changes, with 2-3 sentences for each bullet point, while maintaining the accuracy and completeness of the git diff.

IMPORTANT: The output of the commit message should not be wrapped with any markdown code block.
%s`, diff)
		}

		fmt.Println("Analyzing staged changes and generating commit message...")
		fmt.Println("-----------------------------------------------------------")

		// Get the commit message using the Thread abstraction with usage stats
		commitMsg, usage := llm.SendMessageAndGetTextWithUsage(ctx, s, prompt, llm.GetConfigFromViper(), true, types.MessageOpt{
			UseWeakModel: true,
		})
		commitMsg = sanitizeCommitMessage(commitMsg)

		fmt.Println("-----------------------------------------------------------")
		fmt.Printf("\nGenerated commit message:\n\n%s\n\n", commitMsg)

		// Display usage statistics
		fmt.Printf("\033[1;36m[Usage Stats] Input tokens: %d | Output tokens: %d | Cache write: %d | Cache read: %d | Total: %d\033[0m\n",
			usage.InputTokens, usage.OutputTokens, usage.CacheCreationInputTokens, usage.CacheReadInputTokens, usage.TotalTokens())

		// Display cost information
		fmt.Printf("\033[1;36m[Cost Stats] Input: $%.4f | Output: $%.4f | Cache write: $%.4f | Cache read: $%.4f | Total: $%.4f\033[0m\n",
			usage.InputCost, usage.OutputCost, usage.CacheCreationCost, usage.CacheReadCost, usage.TotalCost())

		// Confirm with user
		if !confirmCommit(commitMsg) {
			os.Exit(0)
		}

		// Create the commit
		if err := createCommit(commitMsg, !noSign); err != nil {
			fmt.Printf("Error creating commit: %s\n", err)
			os.Exit(1)
		}

		fmt.Println("Commit created successfully!")
	},
}

func init() {
	commitCmd.Flags().BoolVar(&noSign, "no-sign", false, "Disable commit signing")
	commitCmd.Flags().StringVarP(&template, "template", "t", "", "Template for commit message")
	commitCmd.Flags().BoolVar(&short, "short", false, "Generate a short commit message with just a description, no bullet points")
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

// captureOutput captures the output of the provided function
func captureOutput(f func()) string {
	// Redirect stdout to a pipe
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Call the function
	f()

	// Reset stdout and close the pipe
	w.Close()
	os.Stdout = oldStdout

	// Read the output
	var buf strings.Builder
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		buf.WriteString(scanner.Text() + "\n")
	}

	return buf.String()
}

// confirmCommit asks the user to confirm the commit
func confirmCommit(message string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Create commit with this message? [Y/n/e (edit)]: ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response == "" || response == "y" || response == "yes" {
		return true
	} else if response == "e" || response == "edit" {
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
func createCommit(message string, sign bool) error {
	// Add co-authorship attribution
	message = message + "\n\nCo-authored-by: Kodelet <kodelet@tryopsmate.ai>"

	// Create a temporary file for the commit message
	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())

	// Write the message to the file
	if _, err := tempFile.WriteString(message); err != nil {
		return fmt.Errorf("error writing to temporary file: %w", err)
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
