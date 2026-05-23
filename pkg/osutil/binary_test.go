package osutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsBinaryFile(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{
			name:     "text file without null bytes",
			content:  []byte("plain text\nwith multiple lines\n"),
			expected: false,
		},
		{
			name:     "file containing null byte",
			content:  []byte{'P', 'N', 'G', 0x00, 'd', 'a', 't', 'a'},
			expected: true,
		},
		{
			name:     "empty file",
			content:  []byte{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tempDir, tt.name)
			require.NoError(t, os.WriteFile(path, tt.content, 0o644))

			assert.Equal(t, tt.expected, IsBinaryFile(path))
		})
	}
}

func TestIsBinaryFileReturnsFalseForUnreadablePath(t *testing.T) {
	assert.False(t, IsBinaryFile(filepath.Join(t.TempDir(), "missing.bin")))
}
