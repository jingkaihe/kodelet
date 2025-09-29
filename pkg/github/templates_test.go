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
				"claude-sonnet-4-5-20250929",
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

func TestRenderBackgroundAgentWorkflow_Validation(t *testing.T) {
	tests := []struct {
		name        string
		data        WorkflowTemplateData
		shouldError bool
	}{
		{
			name: "empty auth gateway endpoint",
			data: WorkflowTemplateData{
				AuthGatewayEndpoint: "",
			},
			shouldError: false, // Template should still render with empty value
		},
		{
			name: "auth gateway with special characters",
			data: WorkflowTemplateData{
				AuthGatewayEndpoint: "https://gateway.com/api?key=value&token=123",
			},
			shouldError: false,
		},
		{
			name: "auth gateway with spaces",
			data: WorkflowTemplateData{
				AuthGatewayEndpoint: "https://gateway.com/api with spaces",
			},
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RenderBackgroundAgentWorkflow(tt.data)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result)
				// Verify the endpoint is included in the output
				if tt.data.AuthGatewayEndpoint != "" {
					assert.Contains(t, result, tt.data.AuthGatewayEndpoint)
				}
			}
		})
	}
}
