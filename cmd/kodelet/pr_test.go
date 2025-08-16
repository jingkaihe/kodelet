package main

import (
	"context"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPRFragmentContent(t *testing.T) {
	ctx := context.Background()
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Test default format
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "github/pr",
		Arguments: map[string]string{
			"target": "main",
		},
	})
	require.NoError(t, err, "Failed to load pr fragment")

	prompt := fragment.Content

	// Test that the prompt contains expected elements for default format
	assert.Contains(t, prompt, "Create a pull request", "Expected PR creation instruction")
	assert.Contains(t, prompt, "git status", "Expected git status instruction")
	assert.Contains(t, prompt, "git diff main...HEAD", "Expected target branch diff instruction")
	assert.Contains(t, prompt, "mcp_create_pull_request", "Expected MCP tool instruction")
	assert.Contains(t, prompt, "## Description", "Expected default template")
	assert.Contains(t, prompt, "## Changes", "Expected default template")
	assert.Contains(t, prompt, "## Impact", "Expected default template")
}

func TestPRFragmentWithCustomTemplate(t *testing.T) {
	ctx := context.Background()
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Test custom template format
	fragment, err := processor.LoadFragment(ctx, &fragments.Config{
		FragmentName: "github/pr",
		Arguments: map[string]string{
			"target":        "develop",
			"template_file": "/tmp/custom_template.md",
		},
	})
	require.NoError(t, err, "Failed to load pr fragment")

	prompt := fragment.Content

	// Test that the prompt contains expected elements for custom template
	assert.Contains(t, prompt, "git diff develop...HEAD", "Expected custom target branch diff instruction")
	assert.Contains(t, prompt, "/tmp/custom_template.md", "Expected template file path")
	assert.NotContains(t, prompt, "## Description", "Should not contain default template when custom template is specified")
}

func TestPRFragmentMetadata(t *testing.T) {
	processor, err := fragments.NewFragmentProcessor()
	require.NoError(t, err, "Failed to create fragment processor")

	// Get the metadata for the built-in pr fragment
	fragment, err := processor.GetFragmentMetadata("github/pr")
	require.NoError(t, err, "Failed to get pr fragment metadata")

	// Test metadata
	assert.Equal(t, "GitHub Pull Request Generator", fragment.Metadata.Name, "Expected fragment name to be 'GitHub Pull Request Generator'")
	assert.Contains(t, fragment.Metadata.Description, "pull requests", "Expected description to mention pull requests")
	assert.Contains(t, fragment.Path, "builtin:", "Expected path to indicate built-in fragment")
}

func TestPRConfigDefaults(t *testing.T) {
	config := NewPRConfig()

	assert.Equal(t, "github", config.Provider, "Expected default Provider to be 'github'")
	assert.Equal(t, "main", config.Target, "Expected default Target to be 'main'")
	assert.Empty(t, config.TemplateFile, "Expected default TemplateFile to be empty")
}

func TestPRConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *PRConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &PRConfig{
				Provider: "github",
				Target:   "main",
			},
			wantErr: false,
		},
		{
			name: "invalid provider",
			config: &PRConfig{
				Provider: "gitlab",
				Target:   "main",
			},
			wantErr: true,
		},
		{
			name: "empty target",
			config: &PRConfig{
				Provider: "github",
				Target:   "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
