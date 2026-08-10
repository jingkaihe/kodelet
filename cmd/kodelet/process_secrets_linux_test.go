//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScrubProcessFlagValues(t *testing.T) {
	args := []string{
		mutableProcessArgument("kodelet"),
		mutableProcessArgument("acp"),
		mutableProcessArgument("--auth-token=api-secret"),
		mutableProcessArgument("--runner-auth-token"),
		mutableProcessArgument("runner-secret"),
		mutableProcessArgument("--server"),
		mutableProcessArgument("https://kodelet.example"),
	}
	capturedToken := strings.Clone(strings.TrimPrefix(args[2], "--auth-token="))

	scrubProcessFlagValues(args, "auth-token", "runner-auth-token")

	joined := strings.Join(args, "\x00")
	assert.NotContains(t, joined, "api-secret")
	assert.NotContains(t, joined, "runner-secret")
	assert.Contains(t, joined, "https://kodelet.example")
	assert.Equal(t, "api-secret", capturedToken)
}

func mutableProcessArgument(value string) string {
	return string(append([]byte(nil), value...))
}
