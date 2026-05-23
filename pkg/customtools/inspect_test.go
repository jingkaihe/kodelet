package customtools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspect(t *testing.T) {
	t.Run("valid description", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  description)
    printf '%s\n' '{"name":"demo","description":"Demo tool","input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}'
    ;;
esac
`)

		metadata, err := Inspect(context.Background(), toolPath, 0)
		require.NoError(t, err)
		require.NotNil(t, metadata)
		assert.Equal(t, "demo", metadata.Name)
		assert.Equal(t, "Demo tool", metadata.Description)
		require.NotNil(t, metadata.Schema)
		assert.Equal(t, "object", metadata.Schema.Type)
	})

	t.Run("command failure includes stderr", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  description)
    echo 'boom' >&2
    exit 7
    ;;
esac
`)

		metadata, err := Inspect(context.Background(), toolPath, time.Second)
		assert.Nil(t, metadata)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to run description command")
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  description)
    printf '{not-json'
    ;;
esac
`)

		metadata, err := Inspect(context.Background(), toolPath, time.Second)
		assert.Nil(t, metadata)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse tool description")
	})

	t.Run("missing required fields", func(t *testing.T) {
		for _, tt := range []struct {
			name       string
			payload    string
			wantErrMsg string
		}{
			{
				name:       "missing name",
				payload:    `{"description":"Demo tool","input_schema":{"type":"object"}}`,
				wantErrMsg: "tool name is required",
			},
			{
				name:       "missing description",
				payload:    `{"name":"demo","input_schema":{"type":"object"}}`,
				wantErrMsg: "tool description is required",
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				toolPath := writeCustomToolScript(t, `
case "$1" in
  description)
    printf '%s\n' '`+tt.payload+`'
    ;;
esac
`)

				metadata, err := Inspect(context.Background(), toolPath, time.Second)
				assert.Nil(t, metadata)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			})
		}
	})
}

func TestInspectConfig(t *testing.T) {
	t.Run("valid timeout", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  config)
    printf '%s\n' '{"timeout":"3s"}'
    ;;
esac
`)

		config, err := InspectConfig(context.Background(), toolPath, 0)
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(t, 3*time.Second, config.Timeout)
	})

	t.Run("optional command failure is ignored", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  config)
    exit 2
    ;;
esac
`)

		config, err := InspectConfig(context.Background(), toolPath, time.Second)
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Zero(t, config.Timeout)
	})

	t.Run("empty and non JSON output are ignored", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			body string
		}{
			{name: "empty", body: ""},
			{name: "plain text", body: "not-json"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				toolPath := writeCustomToolScript(t, `
case "$1" in
  config)
    printf '%s' '`+tt.body+`'
    ;;
esac
`)

				config, err := InspectConfig(context.Background(), toolPath, time.Second)
				require.NoError(t, err)
				require.NotNil(t, config)
				assert.Zero(t, config.Timeout)
			})
		}
	})

	t.Run("invalid config JSON", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  config)
    printf '{'
    ;;
esac
`)

		config, err := InspectConfig(context.Background(), toolPath, time.Second)
		assert.Nil(t, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse tool config")
	})

	t.Run("invalid timeout", func(t *testing.T) {
		toolPath := writeCustomToolScript(t, `
case "$1" in
  config)
    printf '%s\n' '{"timeout":"soon"}'
    ;;
esac
`)

		config, err := InspectConfig(context.Background(), toolPath, time.Second)
		assert.Nil(t, config)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse tool config timeout")
	})
}

func writeCustomToolScript(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tool")
	content := "#!/bin/sh\nset -eu\n" + body
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}
