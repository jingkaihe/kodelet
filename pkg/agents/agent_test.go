package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentProcessor_LoadAgent(t *testing.T) {
	// Create temporary directory for test agents
	tempDir := t.TempDir()
	agentFile := filepath.Join(tempDir, "test_agent.md")

	// Create a test agent file
	agentContent := `---
name: test_agent
description: A test agent for unit testing
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
allowed_tools: [bash, file_read, file_write]
allowed_commands: ["git *", "npm test"]
thinking_budget: 1000
---

You are a test agent designed for unit testing purposes.
Your task is to help with testing functionality.
`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	require.NoError(t, err)

	// Create processor with custom directory
	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	// Load the agent
	ctx := context.Background()
	agent, err := processor.LoadAgent(ctx, "test_agent")
	require.NoError(t, err)

	// Verify agent metadata
	assert.Equal(t, "test_agent", agent.Metadata.Name)
	assert.Equal(t, "A test agent for unit testing", agent.Metadata.Description)
	assert.Equal(t, "anthropic", agent.Metadata.Provider)
	assert.Equal(t, "claude-3-5-sonnet-20241022", agent.Metadata.Model)
	assert.Equal(t, 4096, agent.Metadata.MaxTokens)
	assert.Equal(t, 1000, agent.Metadata.ThinkingBudget)
	assert.Equal(t, []string{"bash", "file_read", "file_write"}, agent.Metadata.AllowedTools)
	assert.Equal(t, []string{"git *", "npm test"}, agent.Metadata.AllowedCommands)

	// Verify system prompt
	expectedPrompt := `
You are a test agent designed for unit testing purposes.
Your task is to help with testing functionality.
`
	assert.Equal(t, expectedPrompt, agent.SystemPrompt)
	assert.Equal(t, agentFile, agent.Path)
}

func TestAgentProcessor_LoadAgentOpenAI(t *testing.T) {
	tempDir := t.TempDir()
	agentFile := filepath.Join(tempDir, "openai_agent.md")

	agentContent := `---
name: openai_agent
description: An OpenAI test agent
provider: openai
model: gpt-4
max_tokens: 2048
reasoning_effort: high
openai:
  base_url: https://api.openai.com/v1
  api_key_env_var: OPENAI_API_KEY
---

You are an OpenAI-powered agent for testing.
`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	require.NoError(t, err)

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	ctx := context.Background()
	agent, err := processor.LoadAgent(ctx, "openai_agent")
	require.NoError(t, err)

	assert.Equal(t, "openai_agent", agent.Metadata.Name)
	assert.Equal(t, "openai", agent.Metadata.Provider)
	assert.Equal(t, "gpt-4", agent.Metadata.Model)
	assert.Equal(t, 2048, agent.Metadata.MaxTokens)
	assert.Equal(t, "high", agent.Metadata.ReasoningEffort)

	require.NotNil(t, agent.Metadata.OpenAIConfig)
	assert.Equal(t, "https://api.openai.com/v1", agent.Metadata.OpenAIConfig.BaseURL)
	assert.Equal(t, "OPENAI_API_KEY", agent.Metadata.OpenAIConfig.APIKeyEnvVar)
}

func TestAgentProcessor_LoadAgentMissingFields(t *testing.T) {
	tempDir := t.TempDir()
	agentFile := filepath.Join(tempDir, "minimal_agent.md")

	// Agent with minimal configuration - should use defaults
	agentContent := `---
name: minimal_agent
description: A minimal agent that uses defaults
---

This agent uses default provider (anthropic), model, and max_tokens.
`

	err := os.WriteFile(agentFile, []byte(agentContent), 0644)
	require.NoError(t, err)

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	ctx := context.Background()

	// Should succeed with defaults applied
	agent, err := processor.LoadAgent(ctx, "minimal_agent")
	require.NoError(t, err)
	
	// Verify defaults were applied
	assert.Equal(t, "minimal_agent", agent.Metadata.Name)
	assert.Equal(t, "anthropic", agent.Metadata.Provider)
	assert.Equal(t, "claude-3-5-sonnet-20241022", agent.Metadata.Model)
	assert.Equal(t, 4096, agent.Metadata.MaxTokens)
}

func TestAgentProcessor_ListAgents(t *testing.T) {
	tempDir := t.TempDir()

	// Create multiple agent files
	agent1Content := `---
name: agent1
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
---

Agent 1 content.
`

	agent2Content := `---
name: agent2
provider: openai
model: gpt-4
max_tokens: 2048
---

Agent 2 content.
`

	err := os.WriteFile(filepath.Join(tempDir, "agent1.md"), []byte(agent1Content), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "agent2.md"), []byte(agent2Content), 0644)
	require.NoError(t, err)

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	ctx := context.Background()
	agents, err := processor.ListAgents(ctx)
	require.NoError(t, err)

	assert.Len(t, agents, 2)

	// Check that both agents are loaded
	agentNames := make(map[string]bool)
	for _, agent := range agents {
		agentNames[agent.Metadata.Name] = true
	}

	assert.True(t, agentNames["agent1"])
	assert.True(t, agentNames["agent2"])
}

func TestAgentProcessor_ValidateAgent(t *testing.T) {
	processor, err := NewAgentProcessor()
	require.NoError(t, err)

	tests := []struct {
		name      string
		agent     *Agent
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid anthropic agent",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "valid_agent",
					Provider:  "anthropic",
					Model:     "claude-3-5-sonnet-20241022",
					MaxTokens: 4096,
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: false,
		},
		{
			name: "valid openai agent",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:            "valid_openai",
					Provider:        "openai",
					Model:           "gpt-4",
					MaxTokens:       2048,
					ReasoningEffort: "medium",
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: false,
		},
		{
			name: "missing name",
			agent: &Agent{
				Metadata: AgentMetadata{
					Provider:  "anthropic",
					Model:     "claude-3-5-sonnet-20241022",
					MaxTokens: 4096,
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: true,
			errMsg:    "agent name is required",
		},
		{
			name: "missing provider (should have default)",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "test",
					Provider:  "anthropic", // Default provider applied during LoadAgent
					Model:     "claude-3-5-sonnet-20241022", // Default model applied
					MaxTokens: 4096, // Default max_tokens applied
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: false,
		},
		{
			name: "missing model (should have default)",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "test",
					Provider:  "anthropic",
					Model:     "claude-3-5-sonnet-20241022", // Default model applied during LoadAgent
					MaxTokens: 4096,
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: false,
		},
		{
			name: "missing max_tokens (should have default)",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "test",
					Provider:  "anthropic",
					Model:     "claude-3-5-sonnet-20241022",
					MaxTokens: 4096, // Default max_tokens applied during LoadAgent
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: false,
		},
		{
			name: "empty system prompt",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "test",
					Provider:  "anthropic",
					Model:     "claude-3-5-sonnet-20241022",
					MaxTokens: 4096,
				},
				SystemPrompt: "",
			},
			expectErr: true,
			errMsg:    "agent system prompt cannot be empty",
		},
		{
			name: "invalid reasoning effort",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:            "test",
					Provider:        "openai",
					Model:           "gpt-4",
					MaxTokens:       2048,
					ReasoningEffort: "invalid",
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: true,
			errMsg:    "invalid reasoning_effort 'invalid'",
		},
		{
			name: "unsupported provider",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:      "test",
					Provider:  "unsupported",
					Model:     "some-model",
					MaxTokens: 2048,
				},
				SystemPrompt: "Valid system prompt",
			},
			expectErr: true,
			errMsg:    "unsupported provider 'unsupported'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := processor.ValidateAgent(tt.agent)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAgentProcessor_ParseStringArrayField(t *testing.T) {
	processor := &AgentProcessor{}

	tests := []struct {
		name     string
		input    interface{}
		expected []string
	}{
		{
			name:     "yaml array",
			input:    []interface{}{"tool1", "tool2", "tool3"},
			expected: []string{"tool1", "tool2", "tool3"},
		},
		{
			name:     "comma separated string",
			input:    "tool1,tool2,tool3",
			expected: []string{"tool1", "tool2", "tool3"},
		},
		{
			name:     "comma separated with spaces",
			input:    "tool1, tool2 , tool3",
			expected: []string{"tool1", "tool2", "tool3"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: []string{},
		},
		{
			name:     "nil input",
			input:    nil,
			expected: []string{},
		},
		{
			name:     "single item",
			input:    "tool1",
			expected: []string{"tool1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processor.parseStringArrayField(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAgentProcessor_DirectoryPrecedence(t *testing.T) {
	// Create two temporary directories to simulate repo and home directories
	repoDir := filepath.Join(t.TempDir(), "repo")
	homeDir := filepath.Join(t.TempDir(), "home")

	err := os.MkdirAll(repoDir, 0755)
	require.NoError(t, err)
	err = os.MkdirAll(homeDir, 0755)
	require.NoError(t, err)

	// Create agent with same name in both directories
	repoAgentContent := `---
name: same_agent
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
description: Repo version
---

Repo agent content.
`

	homeAgentContent := `---
name: same_agent
provider: openai
model: gpt-4
max_tokens: 2048
description: Home version
---

Home agent content.
`

	err = os.WriteFile(filepath.Join(repoDir, "same_agent.md"), []byte(repoAgentContent), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(homeDir, "same_agent.md"), []byte(homeAgentContent), 0644)
	require.NoError(t, err)

	// Test that repo directory takes precedence
	processor, err := NewAgentProcessor(WithAgentDirs(repoDir, homeDir))
	require.NoError(t, err)

	ctx := context.Background()
	agent, err := processor.LoadAgent(ctx, "same_agent")
	require.NoError(t, err)

	// Should load the repo version (anthropic provider)
	assert.Equal(t, "anthropic", agent.Metadata.Provider)
	assert.Equal(t, "Repo version", agent.Metadata.Description)
}

func TestNewAgentProcessor_DefaultDirs(t *testing.T) {
	processor, err := NewAgentProcessor()
	require.NoError(t, err)

	// Should have default directories
	assert.Len(t, processor.agentDirs, 2)
	assert.Equal(t, "./agents", processor.agentDirs[0])
	// Second directory should be ~/.kodelet/agents (can't test exact path due to test environment)
	assert.Contains(t, processor.agentDirs[1], ".kodelet/agents")
}