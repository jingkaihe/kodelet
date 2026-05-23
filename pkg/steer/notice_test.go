package steer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatPendingNotice(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		imageCount int
		expected   string
	}{
		{
			name:       "text only steering",
			content:    "keep going",
			imageCount: 0,
			expected:   "🗣️ User steering: keep going",
		},
		{
			name:       "one image",
			content:    "use this mockup",
			imageCount: 1,
			expected:   "🗣️ User steering: use this mockup (1 image)",
		},
		{
			name:       "multiple images",
			content:    "compare these",
			imageCount: 3,
			expected:   "🗣️ User steering: compare these (3 images)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, FormatPendingNotice(tt.content, tt.imageCount))
		})
	}
}

func TestPluralSuffix(t *testing.T) {
	assert.Equal(t, "", pluralSuffix(1))
	assert.Equal(t, "s", pluralSuffix(0))
	assert.Equal(t, "s", pluralSuffix(2))
	assert.Equal(t, "s", pluralSuffix(-1))
}
