package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
)

func TestNamedAgentTool_Name(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name: "test_agent",
		},
	}

	tool := NewNamedAgentTool(agent)
	assert.Equal(t, "test_agent", tool.Name())
}

func TestNamedAgentTool_Description(t *testing.T) {
	tests := []struct {
		name        string
		agent       *Agent
		expectedDesc string
	}{
		{
			name: "with description",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:        "test_agent",
					Description: "A test agent for unit testing",
					Provider:    "anthropic",
					Model:       "claude-3-5-sonnet-20241022",
				},
			},
			expectedDesc: "A test agent for unit testing",
		},
		{
			name: "without description",
			agent: &Agent{
				Metadata: AgentMetadata{
					Name:     "test_agent",
					Provider: "openai",
					Model:    "gpt-4",
				},
			},
			expectedDesc: "Named agent: test_agent (Provider: openai, Model: gpt-4)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewNamedAgentTool(tt.agent)
			assert.Equal(t, tt.expectedDesc, tool.Description())
		})
	}
}

func TestNamedAgentTool_GenerateSchema(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name: "test_agent",
		},
	}

	tool := NewNamedAgentTool(agent)
	schema := tool.GenerateSchema()

	require.NotNil(t, schema)
	require.NotNil(t, schema.Properties)

	// Check that the schema has the expected query parameter
	queryProp, exists := schema.Properties.Get("query")
	assert.True(t, exists)
	assert.NotNil(t, queryProp)
}

func TestNamedAgentTool_ValidateInput(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name: "test_agent",
		},
	}

	tool := NewNamedAgentTool(agent)

	tests := []struct {
		name       string
		parameters string
		expectErr  bool
		errMsg     string
	}{
		{
			name:       "valid input",
			parameters: `{"query": "What is the weather today?"}`,
			expectErr:  false,
		},
		{
			name:       "empty query",
			parameters: `{"query": ""}`,
			expectErr:  true,
			errMsg:     "query is required",
		},
		{
			name:       "whitespace only query",
			parameters: `{"query": "   "}`,
			expectErr:  true,
			errMsg:     "query is required",
		},
		{
			name:       "missing query field",
			parameters: `{}`,
			expectErr:  true,
			errMsg:     "query is required",
		},
		{
			name:       "invalid json",
			parameters: `{invalid json}`,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.ValidateInput(nil, tt.parameters)
			if tt.expectErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNamedAgentTool_TracingKVs(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name:     "test_agent",
			Provider: "anthropic",
			Model:    "claude-3-5-sonnet-20241022",
		},
	}

	tool := NewNamedAgentTool(agent)
	parameters := `{"query": "What is the weather today?"}`

	kvs, err := tool.TracingKVs(parameters)
	require.NoError(t, err)
	require.Len(t, kvs, 4)

	// Convert to map for easier testing
	kvMap := make(map[string]string)
	for _, kv := range kvs {
		kvMap[string(kv.Key)] = kv.Value.AsString()
	}

	assert.Equal(t, "test_agent", kvMap["agent_name"])
	assert.Equal(t, "anthropic", kvMap["agent_provider"])
	assert.Equal(t, "claude-3-5-sonnet-20241022", kvMap["agent_model"])
	assert.Equal(t, "What is the weather today?", kvMap["query"])
}

func TestNamedAgentTool_CreateAgentLLMConfig_Anthropic(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name:             "anthropic_agent",
			Provider:         "anthropic",
			Model:            "claude-3-5-sonnet-20241022",
			MaxTokens:        4096,
			AllowedTools:     []string{"bash", "file_read"},
			AllowedCommands:  []string{"git *"},
			ThinkingBudget:   1000,
		},
	}

	tool := NewNamedAgentTool(agent)
	
	// Test that the method doesn't panic and creates a config
	// We can't test the full structure due to import cycle issues
	// but we can verify the method executes without error
	baseConfig := createTestLLMConfig()
	config := tool.createAgentLLMConfig(baseConfig)
	assert.NotNil(t, config)
}

func TestNamedAgentTool_CreateAgentLLMConfig_OpenAI(t *testing.T) {
	agent := &Agent{
		Metadata: AgentMetadata{
			Name:            "openai_agent",
			Provider:        "openai",
			Model:           "gpt-4",
			MaxTokens:       2048,
			ReasoningEffort: "high",
			OpenAIConfig: &OpenAIAgentConfig{
				BaseURL:      "https://api.openai.com/v1",
				APIKeyEnvVar: "OPENAI_API_KEY",
			},
		},
	}

	tool := NewNamedAgentTool(agent)
	
	// Test that the method doesn't panic and creates a config
	baseConfig := createTestLLMConfig()
	config := tool.createAgentLLMConfig(baseConfig)
	assert.NotNil(t, config)
}

func TestNamedAgentTool_ConstructFullPrompt(t *testing.T) {
	tests := []struct {
		name           string
		systemPrompt   string
		userQuery      string
		expectedPrompt string
	}{
		{
			name:         "with system prompt",
			systemPrompt: "You are a helpful assistant.",
			userQuery:    "What is 2+2?",
			expectedPrompt: `You are a helpful assistant.

---

User Query: What is 2+2?`,
		},
		{
			name:           "empty system prompt",
			systemPrompt:   "",
			userQuery:      "What is 2+2?",
			expectedPrompt: "What is 2+2?",
		},
		{
			name:         "whitespace only system prompt",
			systemPrompt: "   \n\t  ",
			userQuery:    "What is 2+2?",
			expectedPrompt: "What is 2+2?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &Agent{
				Metadata:     AgentMetadata{Name: "test"},
				SystemPrompt: tt.systemPrompt,
			}

			tool := NewNamedAgentTool(agent)
			result := tool.constructFullPrompt(tt.userQuery)
			assert.Equal(t, tt.expectedPrompt, result)
		})
	}
}

func TestNamedAgentToolResult_Methods(t *testing.T) {
	result := &NamedAgentToolResult{
		agentName: "test_agent",
		query:     "test query",
		result:    "test result",
		err:       "",
	}

	assert.Equal(t, "test result", result.GetResult())
	assert.Equal(t, "", result.GetError())
	assert.False(t, result.IsError())

	// Test error case
	errorResult := &NamedAgentToolResult{
		agentName: "test_agent",
		query:     "test query",
		result:    "",
		err:       "test error",
	}

	assert.Equal(t, "", errorResult.GetResult())
	assert.Equal(t, "test error", errorResult.GetError())
	assert.True(t, errorResult.IsError())
}

func TestNamedAgentToolResult_StructuredData(t *testing.T) {
	result := &NamedAgentToolResult{
		agentName: "test_agent",
		query:     "test query",
		result:    "test result",
		err:       "",
	}

	structured := result.StructuredData()
	assert.Equal(t, "test_agent", structured.ToolName)
	assert.True(t, structured.Success)
	assert.Equal(t, "", structured.Error)
	assert.NotNil(t, structured.Metadata)

	// Verify metadata
	metadata, ok := structured.Metadata.(*NamedAgentMetadata)
	require.True(t, ok)
	assert.Equal(t, "test query", metadata.Query)
	assert.Equal(t, "test result", metadata.Response)

	// Test error case
	errorResult := &NamedAgentToolResult{
		agentName: "test_agent",
		query:     "test query",
		result:    "",
		err:       "test error",
	}

	errorStructured := errorResult.StructuredData()
	assert.Equal(t, "test_agent", errorStructured.ToolName)
	assert.False(t, errorStructured.Success)
	assert.Equal(t, "test error", errorStructured.Error)
}

func TestNamedAgentMetadata_ToolType(t *testing.T) {
	metadata := NamedAgentMetadata{}
	assert.Equal(t, "named_agent", metadata.ToolType())
}

// Helper function to create a minimal LLM config for testing
func createTestLLMConfig() llmtypes.Config {
	return llmtypes.Config{
		Provider:  "test",
		Model:     "test-model",
		MaxTokens: 1000,
	}
}