package acptypes

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvMap_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected EnvMap
	}{
		{
			name:     "map format",
			input:    `{"BRAVE_API_KEY": "secret123", "OTHER_KEY": "value"}`,
			expected: EnvMap{"BRAVE_API_KEY": "secret123", "OTHER_KEY": "value"},
		},
		{
			name:     "array format",
			input:    `[{"name": "BRAVE_API_KEY", "value": "secret123"}, {"name": "OTHER_KEY", "value": "value"}]`,
			expected: EnvMap{"BRAVE_API_KEY": "secret123", "OTHER_KEY": "value"},
		},
		{
			name:     "empty map",
			input:    `{}`,
			expected: EnvMap{},
		},
		{
			name:     "empty array",
			input:    `[]`,
			expected: EnvMap{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env EnvMap
			err := json.Unmarshal([]byte(tt.input), &env)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, env)
		})
	}
}

func TestMCPServer_UnmarshalJSON_WithArrayEnv(t *testing.T) {
	input := `{
		"name": "brave-search-mcp-server",
		"command": "npx",
		"args": ["-y", "@brave/brave-mcp-server"],
		"env": [{"name": "BRAVE_API_KEY", "value": "secret123"}]
	}`

	var server MCPServer
	err := json.Unmarshal([]byte(input), &server)
	require.NoError(t, err)

	assert.Equal(t, "brave-search-mcp-server", server.Name)
	assert.Equal(t, "npx", server.Command)
	assert.Equal(t, []string{"-y", "@brave/brave-mcp-server"}, server.Args)
	assert.Equal(t, EnvMap{"BRAVE_API_KEY": "secret123"}, server.Env)
}
