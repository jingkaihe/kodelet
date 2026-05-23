package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitFragmentContent(t *testing.T) {
	ctx := context.Background()
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Test default format
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "commit",
		Arguments:    map[string]string{},
	})
	require.NoError(t, err, "Failed to load commit fragment")

	prompt := fragment.Content

	// Test that the prompt contains expected elements for default format
	assert.Contains(t, prompt, "conventional commits format", "Expected conventional commits mention")
	assert.Contains(t, prompt, "Short description as the title", "Expected title instruction")
	assert.Contains(t, prompt, "Bullet points", "Expected bullet points instruction")
	assert.Contains(t, prompt, "markdown code block", "Expected markdown instruction")
}

func TestCommitFragmentWithShortFormat(t *testing.T) {
	ctx := context.Background()
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Test short format
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "commit",
		Arguments: map[string]string{
			"short": "true",
		},
	})
	require.NoError(t, err, "Failed to load commit fragment")

	prompt := fragment.Content

	// Test that the prompt contains expected elements for short format
	assert.Contains(t, prompt, "Single line commit message only", "Expected single line instruction")
	assert.Contains(t, prompt, "No bullet points or additional descriptions", "Expected no bullet points instruction")
	assert.NotContains(t, prompt, "Bullet points that break down", "Should not contain bullet points instruction")
}

func TestCommitFragmentWithCustomTemplate(t *testing.T) {
	ctx := context.Background()
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	customTemplate := "feat: [COMPONENT] - brief description"

	// Test custom template format
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "commit",
		Arguments: map[string]string{
			"template": customTemplate,
		},
	})
	require.NoError(t, err, "Failed to load commit fragment")

	prompt := fragment.Content

	// Test that the prompt contains expected elements for custom template
	assert.Contains(t, prompt, "following this template", "Expected template instruction")
	assert.Contains(t, prompt, customTemplate, "Expected custom template to be included")
	assert.NotContains(t, prompt, "conventional commits", "Should not contain conventional commits instruction")
}

func TestCommitFragmentMetadata(t *testing.T) {
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Get the metadata for the built-in commit fragment
	fragment, err := processor.GetFragmentMetadata("commit")
	require.NoError(t, err, "Failed to get commit fragment metadata")

	// Test metadata
	assert.Equal(t, "Git Commit Message Generator", fragment.Metadata.Name, "Expected fragment name to be 'Git Commit Message Generator'")
	assert.Contains(t, fragment.Metadata.Description, "commit messages", "Expected description to mention commit messages")
	assert.Contains(t, fragment.Path, "builtin:", "Expected path to indicate built-in fragment")
}

func TestCommitConfigDefaults(t *testing.T) {
	config := NewCommitConfig()

	assert.False(t, config.NoSign, "Expected default NoSign to be false")
	assert.Empty(t, config.Template, "Expected default Template to be empty")
	assert.True(t, config.Short, "Expected default Short to be true")
	assert.Empty(t, config.Prefix, "Expected default Prefix to be empty")
	assert.False(t, config.NoConfirm, "Expected default NoConfirm to be false")
	assert.False(t, config.Save, "Expected default Save to be false")
}

func TestGetCommitConfigFromFlags(t *testing.T) {
	defaults := NewCommitConfig()
	cmd := &cobra.Command{}
	cmd.Flags().Bool("no-sign", defaults.NoSign, "")
	cmd.Flags().StringP("template", "t", defaults.Template, "")
	cmd.Flags().Bool("short", defaults.Short, "")
	cmd.Flags().String("prefix", defaults.Prefix, "")
	cmd.Flags().Bool("no-confirm", defaults.NoConfirm, "")
	cmd.Flags().Bool("save", defaults.Save, "")

	require.NoError(t, cmd.Flags().Set("no-sign", "true"))
	require.NoError(t, cmd.Flags().Set("template", "custom template"))
	require.NoError(t, cmd.Flags().Set("short", "false"))
	require.NoError(t, cmd.Flags().Set("prefix", "PROJ-123"))
	require.NoError(t, cmd.Flags().Set("no-confirm", "true"))
	require.NoError(t, cmd.Flags().Set("save", "true"))

	config := getCommitConfigFromFlags(cmd)

	assert.True(t, config.NoSign)
	assert.Equal(t, "custom template", config.Template)
	assert.False(t, config.Short)
	assert.Equal(t, "PROJ-123", config.Prefix)
	assert.True(t, config.NoConfirm)
	assert.True(t, config.Save)
}

func TestSanitizeCommitMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes leading backticks",
			input:    "```feat: add new feature",
			expected: "feat: add new feature",
		},
		{
			name:     "removes trailing backticks",
			input:    "feat: add new feature```",
			expected: "feat: add new feature",
		},
		{
			name:     "removes both leading and trailing backticks",
			input:    "```feat: add new feature```",
			expected: "feat: add new feature",
		},
		{
			name:     "leaves message unchanged if no backticks",
			input:    "feat: add new feature",
			expected: "feat: add new feature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeCommitMessage(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPrefixCommitMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		prefix   string
		expected string
	}{
		{
			name:     "empty prefix leaves message unchanged",
			message:  "feat: add prefix flag",
			prefix:   "",
			expected: "feat: add prefix flag",
		},
		{
			name:     "prefix prepended to message",
			message:  "feat: add prefix flag",
			prefix:   "TICKET-123",
			expected: "TICKET-123 feat: add prefix flag",
		},
		{
			name:     "prefix whitespace is trimmed",
			message:  "feat: add prefix flag",
			prefix:   " TICKET-123 ",
			expected: "TICKET-123 feat: add prefix flag",
		},
		{
			name:     "message leading whitespace is trimmed",
			message:  "\t feat: add prefix flag",
			prefix:   "TICKET-123",
			expected: "TICKET-123 feat: add prefix flag",
		},
		{
			name:     "message leading newline is trimmed",
			message:  "\nfeat: add prefix flag\n",
			prefix:   "TICKET-123",
			expected: "TICKET-123 feat: add prefix flag",
		},
		{
			name:     "whitespace-only message returns prefix",
			message:  "\n\t ",
			prefix:   "TICKET-123",
			expected: "TICKET-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prefixCommitMessage(tt.message, tt.prefix)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetEditorEnvironmentFallbacks(t *testing.T) {
	withTempHomeAndCWD(t, t.TempDir(), t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "true")

	t.Run("prefers git editor env", func(t *testing.T) {
		t.Setenv("GIT_EDITOR", "code --wait")
		t.Setenv("VISUAL", "nano")
		t.Setenv("EDITOR", "vim")
		assert.Equal(t, "code --wait", getEditor())
	})

	t.Run("uses visual before editor", func(t *testing.T) {
		t.Setenv("GIT_EDITOR", "")
		t.Setenv("VISUAL", "nano")
		t.Setenv("EDITOR", "vim")
		assert.Equal(t, "nano", getEditor())
	})

	t.Run("defaults to vim", func(t *testing.T) {
		t.Setenv("GIT_EDITOR", "")
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "")
		assert.Equal(t, "vim", getEditor())
	})
}

func TestGetEditorPrefersGitConfig(t *testing.T) {
	withTempHomeAndCWD(t, t.TempDir(), t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "true")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.editor")
	t.Setenv("GIT_CONFIG_VALUE_0", "git-config-editor")
	t.Setenv("GIT_EDITOR", "git-env-editor")
	t.Setenv("VISUAL", "visual-editor")
	t.Setenv("EDITOR", "env-editor")

	assert.Equal(t, "git-config-editor", getEditor())
}

func TestConfirmCommit(t *testing.T) {
	type confirmResult struct {
		confirmed bool
		message   string
	}

	t.Run("accepts default yes", func(t *testing.T) {
		got := withStdin(t, "\n", func() confirmResult {
			confirmed, message := confirmCommit("feat: keep")
			return confirmResult{confirmed: confirmed, message: message}
		})

		assert.True(t, got.confirmed)
		assert.Equal(t, "feat: keep", got.message)
	})

	t.Run("rejects no", func(t *testing.T) {
		got := withStdin(t, "n\n", func() confirmResult {
			confirmed, message := confirmCommit("feat: reject")
			return confirmResult{confirmed: confirmed, message: message}
		})

		assert.False(t, got.confirmed)
		assert.Equal(t, "feat: reject", got.message)
	})
}

func TestConfirmCommitEditFlowWithTempEditor(t *testing.T) {
	type confirmResult struct {
		confirmed bool
		message   string
	}

	editor := writeTempScript(t, "editor", `#!/bin/sh
printf 'fix: edited message
' > "$1"
`)
	withTempHomeAndCWD(t, t.TempDir(), t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "true")
	t.Setenv("GIT_EDITOR", editor)

	got := withStdin(t, "e\ny\n", func() confirmResult {
		confirmed, message := confirmCommit("feat: original")
		return confirmResult{confirmed: confirmed, message: message}
	})

	assert.True(t, got.confirmed)
	assert.Equal(t, "fix: edited message\n", got.message)
}

func TestEditMessageReturnsOriginalWhenEditorFails(t *testing.T) {
	editor := writeTempScript(t, "failing-editor", `#!/bin/sh
exit 7
`)
	withTempHomeAndCWD(t, t.TempDir(), t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "true")
	t.Setenv("GIT_EDITOR", editor)

	assert.Equal(t, "feat: original", editMessage("feat: original"))
}

func TestCreateCommitUsesTempMessageFileAndSignFlag(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "git.log")
	stubDir := filepath.Join(tempDir, "bin")
	require.NoError(t, os.MkdirAll(stubDir, 0o755))
	stub := filepath.Join(stubDir, "git")
	require.NoError(t, os.WriteFile(stub, []byte(`#!/bin/sh
printf '%s
' "$@" > "`+logPath+`"
cat "$3" > "`+filepath.Join(tempDir, "message.txt")+`"
`), 0o755))
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	require.NoError(t, createCommit("feat: from temp file", true))

	args, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(args), "commit\n-F\n")
	assert.Contains(t, string(args), "-s\n")
	message, err := os.ReadFile(filepath.Join(tempDir, "message.txt"))
	require.NoError(t, err)
	assert.Equal(t, "feat: from temp file", string(message))

	require.NoError(t, createCommit("fix: unsigned", false))
	args, err = os.ReadFile(logPath)
	require.NoError(t, err)
	assert.NotContains(t, string(args), "-s\n")
}

func TestGitRepositoryHelpers(t *testing.T) {
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Chdir(oldWd)) }()

	outside := t.TempDir()
	require.NoError(t, os.Chdir(outside))
	assert.False(t, isGitRepository())

	repo := t.TempDir()
	runGitForTest(t, repo, "init")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello\n"), 0o644))
	runGitForTest(t, repo, "add", "file.txt")
	require.NoError(t, os.Chdir(repo))

	assert.True(t, isGitRepository())
	assert.True(t, hasStagedChanges())
}

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
}

func writeTempScript(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}
