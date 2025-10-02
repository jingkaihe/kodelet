package osutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentWithLineNumber(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		offset   int
		expected string
	}{
		{
			name:     "basic functionality",
			lines:    []string{"first line", "second line", "third line"},
			offset:   1,
			expected: "1: first line\n2: second line\n3: third line\n",
		},
		{
			name:     "empty input",
			lines:    []string{},
			offset:   1,
			expected: "",
		},
		{
			name:     "custom offset",
			lines:    []string{"first line", "second line"},
			offset:   10,
			expected: "10: first line\n11: second line\n",
		},
		{
			name:     "large offset for padding",
			lines:    []string{"line one", "line two"},
			offset:   998,
			expected: "998: line one\n999: line two\n",
		},
		{
			name:     "different digit counts in line numbers",
			lines:    []string{"first line", "second line", "third line"},
			offset:   99,
			expected: " 99: first line\n100: second line\n101: third line\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ContentWithLineNumber(tc.lines, tc.offset)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// TestContentWithLineNumberAlignment tests the alignment feature more specifically
func TestContentWithLineNumberAlignment(t *testing.T) {
	lines := []string{"line", "line", "line"}
	result := ContentWithLineNumber(lines, 9)

	// Split result into lines and check each line starts with properly padded number
	resultLines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	require.Equal(t, 3, len(resultLines))

	// Each line should start with " 9: ", "10: ", "11: " respectively
	expectedPrefixes := []string{" 9: ", "10: ", "11: "}
	for i, line := range resultLines {
		assert.True(t, strings.HasSuffix(line, "line"), "Line %d doesn't end with 'line': %s", i, line)

		// Check if padding is correct
		prefixLen := len(line) - 4 // 4 is the length of "line"
		prefix := line[:prefixLen]

		assert.Equal(t, expectedPrefixes[i], prefix)
	}
}
