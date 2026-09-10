package controlplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePublicBaseURL(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{" https://images.example.com/ ", "https://images.example.com"},
		{"https://images.example.com/kodelet///", "https://images.example.com/kodelet"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://images.example.com:8080/prefix", "http://images.example.com:8080/prefix"},
		{"http://[::1]:8080", "http://[::1]:8080"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := NormalizePublicBaseURL(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	for _, input := range []string{
		"images.example.com",
		"/images",
		"//images.example.com",
		"ftp://images.example.com",
		"https:///images",
		"https://user:pass@images.example.com",
		"https://images.example.com?token=secret",
		"https://images.example.com?",
		"https://images.example.com#fragment",
		"https://images.example.com#",
		"https://images.example.com/path space",
		"https://images.example.com/../other",
		"https://images.example.com/%2e%2e/other",
		"https://images.example.com/%0a",
		"https://images.example.com/%5cother",
		"https://images.example.com:bad",
	} {
		t.Run(input, func(t *testing.T) {
			_, err := NormalizePublicBaseURL(input)
			require.Error(t, err)
		})
	}
}
