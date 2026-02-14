package base

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveImageInputPath(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedKind ImageInputKind
		expectedPath string
	}{
		{
			name:         "https url",
			input:        "https://example.com/a.png",
			expectedKind: ImageInputHTTPSURL,
			expectedPath: "https://example.com/a.png",
		},
		{
			name:         "data url",
			input:        "data:image/png;base64,abc",
			expectedKind: ImageInputDataURL,
			expectedPath: "data:image/png;base64,abc",
		},
		{
			name:         "file url",
			input:        "file:///tmp/a.png",
			expectedKind: ImageInputLocalFile,
			expectedPath: "/tmp/a.png",
		},
		{
			name:         "local file",
			input:        "./a.png",
			expectedKind: ImageInputLocalFile,
			expectedPath: "./a.png",
		},
		{
			name:         "http treated as local path",
			input:        "http://example.com/a.png",
			expectedKind: ImageInputLocalFile,
			expectedPath: "http://example.com/a.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, path := ResolveImageInputPath(tt.input)
			assert.Equal(t, tt.expectedKind, kind)
			assert.Equal(t, tt.expectedPath, path)
		})
	}
}

func TestIsInsecureHTTPURL(t *testing.T) {
	assert.True(t, IsInsecureHTTPURL("http://example.com/a.png"))
	assert.False(t, IsInsecureHTTPURL("https://example.com/a.png"))
	assert.False(t, IsInsecureHTTPURL("data:image/png;base64,abc"))
	assert.False(t, IsInsecureHTTPURL("./a.png"))
}
