package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/spf13/cobra"
)

func TestAccountTokenStatus(t *testing.T) {
	now := time.Now()
	assert.Equal(t, "valid", accountTokenStatus(now.Add(30*time.Minute).Unix()))
	assert.Equal(t, "needs refresh", accountTokenStatus(now.Add(5*time.Minute).Unix()))
	assert.Equal(t, "expired", accountTokenStatus(now.Add(-time.Minute).Unix()))
}

func TestListAccountsCmd(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		command, output := remoteAccountsCommandForTest(t, "list", nil)
		require.NoError(t, runRemoteAnthropicAccounts(command, nil))
		assert.Contains(t, output.String(), "No Anthropic accounts found")
	})

	t.Run("populated sorted with default marker", func(t *testing.T) {
		command, buffer := remoteAccountsCommandForTest(t, "list", []chat.AnthropicAccountSummary{
			{Alias: "zeta", Email: "zeta@example.com", ExpiresAt: time.Now().Add(-time.Hour).Unix(), IsDefault: true},
			{Alias: "alpha", Email: "alpha@example.com", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()},
		})
		require.NoError(t, runRemoteAnthropicAccounts(command, nil))
		output := buffer.String()

		assert.Contains(t, output, "ALIAS")
		assert.Contains(t, output, "alpha@example.com")
		assert.Contains(t, output, "* zeta")
		assert.Contains(t, output, "expired")
		assert.Contains(t, output, "needs refresh")
		assert.Less(t, strings.Index(output, "alpha"), strings.Index(output, "zeta"))
	})
}

func TestShowDefaultAccountCmdWithoutDefault(t *testing.T) {
	command, output := remoteAccountsCommandForTest(t, "default", nil)
	require.NoError(t, runRemoteAnthropicAccounts(command, nil))
	assert.Contains(t, output.String(), "No default account set")
}

func TestAccountsUsageFormatting(t *testing.T) {
	assert.Equal(t, "✓ allowed", formatStatus("allowed"))
	assert.Equal(t, "⚠ limited", formatStatus("limited"))
	assert.Equal(t, "other", formatStatus("other"))

	assert.Equal(t, "unknown", formatResetTime(time.Time{}))
	assert.Contains(t, formatResetTime(time.Now().Add(-time.Minute)), "(passed)")
	future := formatResetTime(time.Now().Add(25*time.Hour + 2*time.Minute))
	assert.Contains(t, future, "(in 1d 1h")
}

func remoteAccountsCommandForTest(t *testing.T, name string, accounts []chat.AnthropicAccountSummary) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer client", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/providers/anthropic/accounts", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(chat.AnthropicAccounts{Accounts: accounts}))
	}))
	t.Cleanup(daemon.Close)
	command := &cobra.Command{Use: name}
	command.SetContext(t.Context())
	addRemoteAdministrationFlags(command)
	require.NoError(t, command.ParseFlags([]string{"--server=" + daemon.URL, "--auth-token=client"}))
	output := &bytes.Buffer{}
	command.SetOut(output)
	return command, output
}

func captureAllStdout(t *testing.T, f func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	stdoutFD := int(os.Stdout.Fd())
	savedStdoutFD, err := unix.Dup(stdoutFD)
	require.NoError(t, err)
	defer unix.Close(savedStdoutFD)

	require.NoError(t, unix.Dup2(int(w.Fd()), stdoutFD))

	output := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output <- buf.String()
	}()

	f()

	require.NoError(t, unix.Dup2(savedStdoutFD, stdoutFD))
	require.NoError(t, w.Close())

	return <-output
}
