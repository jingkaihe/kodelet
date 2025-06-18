package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCleanupWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "multiple_spaces",
			input:    "Text  with   multiple    spaces",
			expected: "Text with multiple spaces",
		},
		{
			name:     "multiple_newlines",
			input:    "Line1\n\n\n\nLine2",
			expected: "Line1\n\nLine2",
		},
		{
			name:     "mixed_whitespace",
			input:    "  Text\n\n\n  with   spaces  \n\n\n  and newlines  ",
			expected: "Text\n\n with spaces \n\n and newlines",
		},
		{
			name:     "leading_trailing_spaces",
			input:    "   trimmed   ",
			expected: "trimmed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanupWhitespace(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManagerCreation(t *testing.T) {
	manager := NewManager()

	assert.NotNil(t, manager)
	assert.Nil(t, manager.ctx)
	assert.Nil(t, manager.cancelCtx)
	assert.False(t, manager.isActive)
	assert.NotNil(t, manager.elementBuffer)
}

func TestScreenshotHelpers(t *testing.T) {
	t.Run("create_screenshot_dir", func(t *testing.T) {
		dir, err := CreateScreenshotDir()
		assert.NoError(t, err)
		assert.NotEmpty(t, dir)
		assert.Contains(t, dir, ".kodelet")
		assert.Contains(t, dir, "screenshots")
	})

	t.Run("generate_screenshot_path", func(t *testing.T) {
		tests := []struct {
			format   string
			expected string
		}{
			{"png", ".png"},
			{"jpeg", ".jpeg"},
			{"jpg", ".jpg"},
		}

		for _, tt := range tests {
			t.Run(tt.format, func(t *testing.T) {
				path, err := GenerateScreenshotPath(tt.format)
				assert.NoError(t, err)
				assert.Contains(t, path, tt.expected)
				assert.Contains(t, path, ".kodelet")
				assert.Contains(t, path, "screenshots")
			})
		}
	})
}
