package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func remotePRCommandForTest(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := remoteRunCommandForTest()
	cmd.Use = "pr"
	cmd.SetContext(t.Context())
	cmd.Flags().String("target", "main", "")
	cmd.Flags().String("template-file", "", "")
	cmd.Flags().Bool("draft", false, "")
	require.NoError(t, cmd.ParseFlags(append([]string{"--provider=github"}, args...)))
	return cmd
}

func TestRemotePRProviderIsVCSNotModelProvider(t *testing.T) {
	cmd := remotePRCommandForTest(t, "--target=develop", "--draft", "--template-file=/runner-only/template with spaces.md", "--model=central-model", "--allowed-tools=", "--no-tools=false", "--max-turns=0")
	request, err := remotePRRequest(cmd)
	require.NoError(t, err)
	assert.Nil(t, request.Options.Provider, "github must never become a model provider")
	assert.Equal(t, new("central-model"), request.Options.Model)
	assert.Equal(t, new(false), request.Options.NoTools)
	assert.Equal(t, new([]string{}), request.Options.AllowedTools)
	assert.Equal(t, new(0), request.Options.MaxTurns)
	assert.Contains(t, request.Message, "/github/pr ")
	assert.Contains(t, request.Message, "target=develop")
	assert.Contains(t, request.Message, "draft=true")
	assert.Contains(t, request.Message, `/runner-only/template with spaces.md`)
	_, err = remoteRunExecutionOptions(cmd)
	require.ErrorContains(t, err, "unsupported execution provider", "only the PR adapter may ignore its VCS flag")
}

func TestRemotePRRejectsUnsupportedOptionsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer daemon.Close()
	for _, flag := range []string{"--provider=gitlab", "--target=", "--no-save=false", "--max-tokens=0", "--sysprompt=/client/prompt", "--allowed-domains-file=/client/policy"} {
		cmd := remotePRCommandForTest(t, "--server="+daemon.URL, "--auth-token=client", flag)
		require.Error(t, runRemotePR(cmd), flag)
	}
	assert.Zero(t, calls.Load())
}

func TestRemotePRProcessDoesNotReadClientGitOrProviderState(t *testing.T) {
	var submissions atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/chat/settings":
			require.NoError(t, json.NewEncoder(w).Encode(chat.ControlPlaneChatSettings{DefaultRunnerID: "registered", DefaultRunnerReady: true}))
		case "/api/chat":
			submissions.Add(1)
			var request chat.ChatRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, "registered", request.RunnerID)
			assert.Equal(t, "/runner-only/repo", request.CWD)
			assert.Contains(t, request.Message, "/github/pr ")
			assert.Contains(t, request.Message, "target=develop")
			assert.Nil(t, request.Options.Provider)
			assert.Equal(t, new("daemon-model"), request.Options.Model)
			assert.NotEmpty(t, request.ConversationID)
			assert.NotEmpty(t, request.TurnID)
			w.Header().Set("Content-Type", "application/x-ndjson")
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "result", Result: new("created remotely")}))
			require.NoError(t, json.NewEncoder(w).Encode(chat.ChatEvent{Kind: "done"}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	invalidStore := filepath.Join(root, "no-client-state")
	require.NoError(t, os.WriteFile(invalidStore, []byte("not a directory"), 0o600))
	// Empty PATH also proves no local git/gh command is invoked by the client.
	env := []string{"PATH=" + root, "HOME=" + root, "KODELET_TEST_CLI_PROCESS=1", "KODELET_BASE_PATH=" + invalidStore}
	process := daemonCLIProcess(ctx, t, root, env, "pr", "--server="+daemon.URL, "--auth-token=client", "--provider=github", "--target=develop", "--model=daemon-model", "--cwd=/runner-only/repo", "--template-file=/runner-only/template.md", "--result-only")
	output, err := process.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.Equal(t, "created remotely\n", string(output))
	assert.EqualValues(t, 1, submissions.Load())
}

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
	assert.Contains(t, prompt, "mcp__github_create_pull_request", "Expected MCP tool instruction")
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
	assert.False(t, config.ResultOnly, "Expected default ResultOnly to be false")
}

func TestGetPRConfigFromFlags(t *testing.T) {
	defaults := NewPRConfig()
	cmd := &cobra.Command{}
	cmd.Flags().StringP("provider", "p", defaults.Provider, "")
	cmd.Flags().StringP("target", "t", defaults.Target, "")
	cmd.Flags().String("template-file", defaults.TemplateFile, "")
	cmd.Flags().BoolP("draft", "d", defaults.Draft, "")
	cmd.Flags().Bool("result-only", defaults.ResultOnly, "")

	require.NoError(t, cmd.Flags().Set("provider", "github"))
	require.NoError(t, cmd.Flags().Set("target", "develop"))
	require.NoError(t, cmd.Flags().Set("template-file", "/tmp/template.md"))
	require.NoError(t, cmd.Flags().Set("draft", "true"))
	require.NoError(t, cmd.Flags().Set("result-only", "true"))

	config := getPRConfigFromFlags(cmd)

	assert.Equal(t, "github", config.Provider)
	assert.Equal(t, "develop", config.Target)
	assert.Equal(t, "/tmp/template.md", config.TemplateFile)
	assert.True(t, config.Draft)
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
