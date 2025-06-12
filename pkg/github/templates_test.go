package github

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderBackgroundAgentWorkflow(t *testing.T) {
	tests := []struct {
		name     string
		data     WorkflowTemplateData
		contains []string
	}{
		{
			name: "basic template rendering",
			data: WorkflowTemplateData{
				AuthGatewayEndpoint: "https://gha-auth-gateway.kodelet.com/api/github",
			},
			contains: []string{
				"name: Background Kodelet",
				"auth-gateway-endpoint: https://gha-auth-gateway.kodelet.com/api/github",
				"jingkaihe/kodelet-action@v0.1.7-alpha",
				"claude-sonnet-4-0",
				"github.event_name == 'issues'",
				"@kodelet",
			},
		},
		{
			name: "custom auth gateway",
			data: WorkflowTemplateData{
				AuthGatewayEndpoint: "https://custom.endpoint.com/auth",
			},
			contains: []string{
				"auth-gateway-endpoint: https://custom.endpoint.com/auth",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RenderBackgroundAgentWorkflow(tt.data)
			require.NoError(t, err)
			require.NotEmpty(t, result)

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected,
					"Expected workflow to contain: %s", expected)
			}

			// Verify it's valid YAML by checking basic structure
			assert.True(t, strings.HasPrefix(result, "name: Background Kodelet"),
				"Workflow should start with name field")
			assert.Contains(t, result, "on:")
			assert.Contains(t, result, "jobs:")
			assert.Contains(t, result, "steps:")
		})
	}
}

func TestRenderBackgroundAgentWorkflow_ErrorHandling(t *testing.T) {
	// Test with valid data to ensure no errors
	data := WorkflowTemplateData{
		AuthGatewayEndpoint: "https://test.com",
	}

	result, err := RenderBackgroundAgentWorkflow(data)
	require.NoError(t, err)
	require.NotEmpty(t, result)
}
