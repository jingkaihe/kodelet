package anthropic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrefixToolNames(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single tool",
			input:    `{"model":"claude-3","tools":[{"name":"file_read","description":"Read a file"}]}`,
			expected: `{"model":"claude-3","tools":[{"name":"oc_file_read","description":"Read a file"}]}`,
		},
		{
			name:     "multiple tools",
			input:    `{"tools":[{"name":"file_read"},{"name":"bash"},{"name":"grep_tool"}]}`,
			expected: `{"tools":[{"name":"oc_file_read"},{"name":"oc_bash"},{"name":"oc_grep_tool"}]}`,
		},
		{
			name:     "no tools array",
			input:    `{"model":"claude-3","messages":[]}`,
			expected: `{"model":"claude-3","messages":[]}`,
		},
		{
			name:     "empty tools array",
			input:    `{"tools":[]}`,
			expected: `{"tools":[]}`,
		},
		{
			name:     "tool without name",
			input:    `{"tools":[{"description":"A tool"}]}`,
			expected: `{"tools":[{"description":"A tool"}]}`,
		},
		{
			name:     "preserves other tool properties",
			input:    `{"tools":[{"name":"file_read","description":"Read","input_schema":{"type":"object"}}]}`,
			expected: `{"tools":[{"name":"oc_file_read","description":"Read","input_schema":{"type":"object"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prefixToolNames([]byte(tt.input))
			assert.JSONEq(t, tt.expected, string(result))
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{-1, "-1"},
		{-123, "-123"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := itoa(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestStripToolNamePrefix(t *testing.T) {
	tests := []struct {
		name            string
		useSubscription bool
		toolName        string
		expected        string
	}{
		{
			name:            "subscription mode strips prefix",
			useSubscription: true,
			toolName:        "oc_file_read",
			expected:        "file_read",
		},
		{
			name:            "subscription mode with no prefix",
			useSubscription: true,
			toolName:        "file_read",
			expected:        "file_read",
		},
		{
			name:            "non-subscription mode keeps prefix",
			useSubscription: false,
			toolName:        "oc_file_read",
			expected:        "oc_file_read",
		},
		{
			name:            "non-subscription mode normal name",
			useSubscription: false,
			toolName:        "file_read",
			expected:        "file_read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			thread := &Thread{useSubscription: tt.useSubscription}
			result := thread.stripToolNamePrefix(tt.toolName)
			require.Equal(t, tt.expected, result)
		})
	}
}
