package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/spf13/cobra"
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
	assert.Contains(t, prompt, "git diff origin/main...HEAD", "Expected target branch diff instruction")
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
	assert.Contains(t, prompt, "git diff origin/develop...HEAD", "Expected custom target branch diff instruction")
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
	assert.Contains(t, fragment.Metadata.Description, "pull request", "Expected description to mention pull request")
	assert.Contains(t, fragment.Path, "builtin:", "Expected path to indicate built-in fragment")
}

func TestPRConfigDefaults(t *testing.T) {
	config := NewPRConfig()

	assert.Equal(t, "github", config.Provider, "Expected default Provider to be 'github'")
	assert.Equal(t, "main", config.Target, "Expected default Target to be 'main'")
	assert.Empty(t, config.TemplateFile, "Expected default TemplateFile to be empty")
	assert.False(t, config.Draft, "Expected default Draft to be false")
	assert.False(t, config.NoSave, "Expected default NoSave to be false")
	assert.False(t, config.ResultOnly, "Expected default ResultOnly to be false")
}

func TestGetPRConfigFromFlags(t *testing.T) {
	defaults := NewPRConfig()
	cmd := &cobra.Command{}
	cmd.Flags().StringP("provider", "p", defaults.Provider, "")
	cmd.Flags().StringP("target", "t", defaults.Target, "")
	cmd.Flags().String("template-file", defaults.TemplateFile, "")
	cmd.Flags().BoolP("draft", "d", defaults.Draft, "")
	cmd.Flags().Bool("no-save", defaults.NoSave, "")
	cmd.Flags().Bool("result-only", defaults.ResultOnly, "")

	require.NoError(t, cmd.Flags().Set("provider", "github"))
	require.NoError(t, cmd.Flags().Set("target", "develop"))
	require.NoError(t, cmd.Flags().Set("template-file", "/tmp/template.md"))
	require.NoError(t, cmd.Flags().Set("draft", "true"))
	require.NoError(t, cmd.Flags().Set("no-save", "true"))
	require.NoError(t, cmd.Flags().Set("result-only", "true"))

	config := getPRConfigFromFlags(cmd)

	assert.Equal(t, "github", config.Provider)
	assert.Equal(t, "develop", config.Target)
	assert.Equal(t, "/tmp/template.md", config.TemplateFile)
	assert.True(t, config.Draft)
	assert.True(t, config.NoSave)
	assert.True(t, config.ResultOnly)
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

func TestGhHelpersUsePathStubs(t *testing.T) {
	t.Run("installed and authenticated", func(t *testing.T) {
		stubDir := t.TempDir()
		writeGHStub(t, stubDir, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  exit 0
fi
exit 0
`)
		t.Setenv("PATH", stubDir)

		assert.True(t, isGhCliInstalled())
		assert.True(t, isGhAuthenticated())
	})

	t.Run("installed but not authenticated", func(t *testing.T) {
		stubDir := t.TempDir()
		writeGHStub(t, stubDir, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  exit 1
fi
exit 0
`)
		t.Setenv("PATH", stubDir)

		assert.True(t, isGhCliInstalled())
		assert.False(t, isGhAuthenticated())
	})

	t.Run("missing gh", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())

		assert.False(t, isGhCliInstalled())
		assert.False(t, isGhAuthenticated())
	})
}

func writeGHStub(t *testing.T, dir, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), []byte(content), 0o755))
}
