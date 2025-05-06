package utils

import (
	"strings"
	"testing"
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
			if result != tc.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tc.expected, result)
			}
		})
	}
}

// TestContentWithLineNumberAlignment tests the alignment feature more specifically
func TestContentWithLineNumberAlignment(t *testing.T) {
	lines := []string{"line", "line", "line"}
	result := ContentWithLineNumber(lines, 9)

	// Split result into lines and check each line starts with properly padded number
	resultLines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	if len(resultLines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(resultLines))
	}

	// Each line should start with " 9: ", "10: ", "11: " respectively
	expectedPrefixes := []string{" 9: ", "10: ", "11: "}
	for i, line := range resultLines {
		if !strings.HasSuffix(line, "line") {
			t.Errorf("Line %d doesn't end with 'line': %s", i, line)
		}

		// Check if padding is correct
		prefixLen := len(line) - 4 // 4 is the length of "line"
		prefix := line[:prefixLen]

		if prefix != expectedPrefixes[i] {
			t.Errorf("Expected prefix '%s', got '%s'", expectedPrefixes[i], prefix)
		}
	}
}
