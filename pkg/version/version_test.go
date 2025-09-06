package version

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGet(t *testing.T) {
	info := Get()

	assert.Equal(t, Version, info.Version)
	assert.Equal(t, GitCommit, info.GitCommit)
	assert.Equal(t, BuildTime, info.BuildTime)
	assert.NotEmpty(t, info.GoVersion, "Go version should not be empty")
	assert.Contains(t, info.GoVersion, "go", "Go version should contain 'go'")
}

func TestInfo_String(t *testing.T) {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildTime: "Sun Aug 25 09:34:29 AM UTC 2025",
		GoVersion: "go1.25.1",
	}

	result := info.String()
	expected := "Version: 1.0.0, GitCommit: abc123, BuildTime: Sun Aug 25 09:34:29 AM UTC 2025, GoVersion: go1.25.1"
	assert.Equal(t, expected, result)
}

func TestInfo_JSON(t *testing.T) {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildTime: "Sun Aug 25 09:34:29 AM UTC 2025",
		GoVersion: "go1.25.1",
	}

	jsonString, err := info.JSON()
	require.NoError(t, err)

	// Verify it's valid JSON
	var parsed Info
	err = json.Unmarshal([]byte(jsonString), &parsed)
	require.NoError(t, err)

	assert.Equal(t, info.Version, parsed.Version)
	assert.Equal(t, info.GitCommit, parsed.GitCommit)
	assert.Equal(t, info.BuildTime, parsed.BuildTime)
	assert.Equal(t, info.GoVersion, parsed.GoVersion)

	// Verify all fields are present in JSON
	assert.True(t, strings.Contains(jsonString, `"version"`))
	assert.True(t, strings.Contains(jsonString, `"gitCommit"`))
	assert.True(t, strings.Contains(jsonString, `"buildTime"`))
	assert.True(t, strings.Contains(jsonString, `"goVersion"`))
}

func TestInfo_JSONFormat(t *testing.T) {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildTime: "Sun Aug 25 09:34:29 AM UTC 2025",
		GoVersion: "go1.25.1",
	}

	jsonString, err := info.JSON()
	require.NoError(t, err)

	expectedJSON := `{
  "version": "1.0.0",
  "gitCommit": "abc123",
  "buildTime": "Sun Aug 25 09:34:29 AM UTC 2025",
  "goVersion": "go1.25.1"
}`

	assert.Equal(t, expectedJSON, jsonString)
}
