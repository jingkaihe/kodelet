package main

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
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
	assert.False(t, config.Short, "Expected default Short to be false")
	assert.False(t, config.NoConfirm, "Expected default NoConfirm to be false")
	assert.False(t, config.NoCoauthor, "Expected default NoCoauthor to be false")
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
