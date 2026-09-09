package main

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetQueryFromStdinOrArgs(t *testing.T) {
	t.Run("uses piped stdin", func(t *testing.T) {
		withPipeStdin(t, "from stdin\n", func() bool {
			query, err := getQueryFromStdinOrArgs(nil)
			require.NoError(t, err)
			assert.Equal(t, "from stdin\n", query)
			return true
		})
	})
	t.Run("combines arguments before stdin", func(t *testing.T) {
		withPipeStdin(t, "details", func() bool {
			query, err := getQueryFromStdinOrArgs([]string{"summarize", "this"})
			require.NoError(t, err)
			assert.Equal(t, "summarize this\ndetails", query)
			return true
		})
	})
	t.Run("arguments without piped input", func(t *testing.T) {
		withDevNullStdin(t, func() bool {
			query, err := getQueryFromStdinOrArgs([]string{"hello", "world"})
			require.NoError(t, err)
			assert.Equal(t, "hello world", query)
			_, err = getQueryFromStdinOrArgs(nil)
			require.ErrorContains(t, err, "no query provided")
			return true
		})
	})
}

func TestFormatFragmentDisplayArgs(t *testing.T) {
	assert.Equal(t, `draft=true title="my feature"`, formatFragmentDisplayArgs(map[string]string{"title": "my feature", "draft": "true"}))
	assert.Empty(t, formatFragmentDisplayArgs(nil))
	assert.Equal(t, "b=2 c=3", formatFragmentDisplayArgs(map[string]string{"": "ignored", "c": "3", "b": "2"}))
}

func TestRunFlagsRejectNoSave(t *testing.T) {
	cmd := &cobra.Command{Use: "run"}
	addRunFlags(cmd)
	assert.Nil(t, cmd.Flags().Lookup("no-save"))
	require.ErrorContains(t, cmd.ParseFlags([]string{"--no-save"}), "unknown flag: --no-save")
	assert.Nil(t, prCmd.Flags().Lookup("no-save"))
	assert.Nil(t, commitCmd.Flags().Lookup("save"))
}

func withPipeStdin[T any](t *testing.T, input string, f func() T) T {
	t.Helper()
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = r.Close()
	})
	return f()
}

func withDevNullStdin[T any](t *testing.T, f func() T) T {
	t.Helper()
	oldStdin := os.Stdin
	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)
	os.Stdin = devNull
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = devNull.Close()
	})
	return f()
}
