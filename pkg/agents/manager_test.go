package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentManager_LoadAllAgents(t *testing.T) {
	tempDir := t.TempDir()

	// Create test agent files
	agent1Content := `---
name: agent1
description: First test agent
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
---

Agent 1 system prompt.
`

	agent2Content := `---
name: agent2
description: Second test agent
provider: openai
model: gpt-4
max_tokens: 2048
---

Agent 2 system prompt.
`

	// Create a minimal agent file that should be loaded with defaults
	minimalAgentContent := `---
name: minimal_agent
description: Minimal agent that uses defaults
---

This agent will be loaded with default provider, model, and max_tokens.
`

	err := os.WriteFile(filepath.Join(tempDir, "agent1.md"), []byte(agent1Content), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "agent2.md"), []byte(agent2Content), 0644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tempDir, "minimal_agent.md"), []byte(minimalAgentContent), 0644)
	require.NoError(t, err)

	// Create manager with custom agent directory
	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	// Should load all 3 agents (agent1, agent2, minimal_agent)
	assert.Len(t, manager.agents, 3)
	assert.Len(t, manager.tools, 3)

	// Verify agent names
	agentNames := manager.ListAgentNames()
	assert.Contains(t, agentNames, "agent1")
	assert.Contains(t, agentNames, "agent2")
	assert.Contains(t, agentNames, "minimal_agent")

	// Verify tools are created
	tools := manager.GetAgentTools()
	assert.Len(t, tools, 3)

	// Check tool names match agent names
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name()] = true
	}
	assert.True(t, toolNames["agent1"])
	assert.True(t, toolNames["agent2"])
	assert.True(t, toolNames["minimal_agent"])
}

func TestAgentManager_GetAgent(t *testing.T) {
	tempDir := t.TempDir()

	agentContent := `---
name: test_agent
description: Test agent
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
---

Test agent system prompt.
`

	err := os.WriteFile(filepath.Join(tempDir, "test_agent.md"), []byte(agentContent), 0644)
	require.NoError(t, err)

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	// Test getting existing agent
	agent, err := manager.GetAgent("test_agent")
	require.NoError(t, err)
	assert.Equal(t, "test_agent", agent.Metadata.Name)
	assert.Equal(t, "Test agent", agent.Metadata.Description)

	// Test getting non-existent agent
	_, err = manager.GetAgent("non_existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "agent 'non_existent' not found")
}

func TestAgentManager_ListAgentNames(t *testing.T) {
	tempDir := t.TempDir()

	// Create multiple agents
	agentNames := []string{"alpha", "beta", "gamma"}
	for _, name := range agentNames {
		agentContent := `---
name: ` + name + `
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
---

` + name + ` agent system prompt.
`
		err := os.WriteFile(filepath.Join(tempDir, name+".md"), []byte(agentContent), 0644)
		require.NoError(t, err)
	}

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	names := manager.ListAgentNames()
	assert.Len(t, names, 3)

	// Check all expected names are present
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}

	for _, expectedName := range agentNames {
		assert.True(t, nameSet[expectedName], "Expected agent name %s not found", expectedName)
	}
}

func TestAgentManager_GetAgentTools(t *testing.T) {
	tempDir := t.TempDir()

	agentContent := `---
name: tool_agent
description: Agent for tool testing
provider: openai
model: gpt-4
max_tokens: 2048
---

Tool agent system prompt.
`

	err := os.WriteFile(filepath.Join(tempDir, "tool_agent.md"), []byte(agentContent), 0644)
	require.NoError(t, err)

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	tools := manager.GetAgentTools()
	assert.Len(t, tools, 1)

	tool := tools[0]
	assert.Equal(t, "tool_agent", tool.Name())
	assert.Equal(t, "Agent for tool testing", tool.Description())

	// Verify it's a NamedAgentTool
	namedTool, ok := tool.(*NamedAgentTool)
	assert.True(t, ok)
	assert.Equal(t, "tool_agent", namedTool.agent.Metadata.Name)
}

func TestNewAgentManager(t *testing.T) {
	ctx := context.Background()
	manager, err := NewAgentManager(ctx)
	require.NoError(t, err)

	assert.NotNil(t, manager.processor)
	assert.Empty(t, manager.agents)
	assert.Empty(t, manager.tools)
}

func TestCreateAgentManagerFromContext(t *testing.T) {
	tempDir := t.TempDir()

	// Create a test agent
	agentContent := `---
name: context_agent
provider: anthropic
model: claude-3-5-sonnet-20241022
max_tokens: 4096
---

Context agent system prompt.
`

	err := os.WriteFile(filepath.Join(tempDir, "context_agent.md"), []byte(agentContent), 0644)
	require.NoError(t, err)

	// Temporarily change the working directory for this test
	// This simulates the agent being in the default ./agents directory
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalWd)

	// Create agents directory in temp dir and change to it
	agentsDir := filepath.Join(tempDir, "agents")
	err = os.MkdirAll(agentsDir, 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(agentsDir, "context_agent.md"), []byte(agentContent), 0644)
	require.NoError(t, err)

	err = os.Chdir(tempDir)
	require.NoError(t, err)

	ctx := context.Background()
	manager, err := CreateAgentManagerFromContext(ctx)

	// The function should succeed even if no agents are found (graceful degradation)
	// In this case, it should find our test agent
	require.NoError(t, err)
	assert.NotNil(t, manager)

	// If agents were found, verify them
	if len(manager.agents) > 0 {
		names := manager.ListAgentNames()
		assert.Contains(t, names, "context_agent")
	}
}

func TestAgentManager_LoadAllAgents_EmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	// Create empty directory

	processor, err := NewAgentProcessor(WithAgentDirs(tempDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	// Should handle empty directory gracefully
	assert.Empty(t, manager.agents)
	assert.Empty(t, manager.tools)
	assert.Empty(t, manager.ListAgentNames())
	assert.Empty(t, manager.GetAgentTools())
}

func TestAgentManager_LoadAllAgents_NonExistentDirectory(t *testing.T) {
	nonExistentDir := "/this/directory/does/not/exist"

	processor, err := NewAgentProcessor(WithAgentDirs(nonExistentDir))
	require.NoError(t, err)

	manager := &AgentManager{
		processor: processor,
	}

	ctx := context.Background()
	err = manager.LoadAllAgents(ctx)
	require.NoError(t, err)

	// Should handle non-existent directory gracefully
	assert.Empty(t, manager.agents)
	assert.Empty(t, manager.tools)
}