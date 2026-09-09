package main

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

type CommitConfig struct {
	NoSign    bool
	Template  string
	Short     bool
	Prefix    string
	NoConfirm bool
}

func NewCommitConfig() *CommitConfig {
	return &CommitConfig{
		NoSign:    false,
		Template:  "",
		Short:     true,
		Prefix:    "",
		NoConfirm: false,
	}
}

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Generate and create a git commit with an AI-generated message",
	Long: `Generate a meaningful commit message based on staged changes and create a signed-off git commit.
This command analyzes your 'git diff --cached' and uses AI to generate an appropriate commit message.
You must stage your changes (using 'git add') before running this command.`,
	RunE: func(cmd *cobra.Command, _ []string) error { return runRemoteCommit(cmd) },
}

func init() {
	defaults := NewCommitConfig()
	addRemoteRunFlags(commitCmd)
	commitCmd.Flags().String("cwd", "", "Repository directory on the selected runner")
	commitCmd.Flags().Bool("no-sign", defaults.NoSign, "Omit the Signed-off-by trailer")
	commitCmd.Flags().StringP("template", "t", defaults.Template, "Template for commit message")
	commitCmd.Flags().Bool("short", defaults.Short, "Generate a short commit message with just a description, no bullet points")
	commitCmd.Flags().String("prefix", defaults.Prefix, "Prefix to prepend to the generated commit message")
	commitCmd.Flags().Bool("no-confirm", defaults.NoConfirm, "Skip confirmation prompt and create commit automatically")
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
	if prefix, err := cmd.Flags().GetString("prefix"); err == nil {
		config.Prefix = prefix
	}
	if noConfirm, err := cmd.Flags().GetBool("no-confirm"); err == nil {
		config.NoConfirm = noConfirm
	}
	return config
}

func sanitizeCommitMessage(message string) string {
	message = strings.TrimPrefix(message, "```")
	message = strings.TrimSuffix(message, "```")
	return message
}

func prefixCommitMessage(message, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return message
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return prefix
	}
	return prefix + " " + message
}

func editMessageWithEditor(ctx context.Context, message, editor string) string {
	tempFile, err := os.CreateTemp("", "kodelet-commit-*.txt")
	if err != nil {
		presenter.Error(err, "Failed to create temporary file")
		return message
	}
	defer os.Remove(tempFile.Name())

	tempFile.WriteString(message)
	tempFile.Close()

	cmd := exec.CommandContext(ctx, editor, tempFile.Name())
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
