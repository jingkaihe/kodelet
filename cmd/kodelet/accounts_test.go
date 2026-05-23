package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/auth"
)

func TestAccountTokenStatus(t *testing.T) {
	now := time.Now()
	assert.Equal(t, "valid", accountTokenStatus(now.Add(30*time.Minute).Unix()))
	assert.Equal(t, "needs refresh", accountTokenStatus(now.Add(5*time.Minute).Unix()))
	assert.Equal(t, "expired", accountTokenStatus(now.Add(-time.Minute).Unix()))
}

func TestListAccountsCmd(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		setupAnthropicAccountsTestHome(t)

		output := captureAllStdout(t, func() {
			listAccountsCmd()
		})

		assert.Contains(t, output, "No Anthropic accounts found")
	})

	t.Run("populated sorted with default marker", func(t *testing.T) {
		setupAnthropicAccountsTestHome(t)
		saveTestAnthropicAccount(t, "zeta", "zeta@example.com", time.Now().Add(-time.Hour))
		saveTestAnthropicAccount(t, "alpha", "alpha@example.com", time.Now().Add(5*time.Minute))
		require.NoError(t, auth.SetDefaultAnthropicAccount("zeta"))

		output := captureStdout(t, func() {
			listAccountsCmd()
		})

		assert.Contains(t, output, "ALIAS")
		assert.Contains(t, output, "alpha@example.com")
		assert.Contains(t, output, "* zeta")
		assert.Contains(t, output, "expired")
		assert.Contains(t, output, "needs refresh")
		assert.Less(t, strings.Index(output, "alpha"), strings.Index(output, "zeta"))
	})
}

func TestAccountDefaultRenameAndRemoveCommands(t *testing.T) {
	setupAnthropicAccountsTestHome(t)
	saveTestAnthropicAccount(t, "work", "work@example.com", time.Now().Add(time.Hour))
	saveTestAnthropicAccount(t, "personal", "personal@example.com", time.Now().Add(time.Hour))

	showOutput := captureAllStdout(t, func() {
		showDefaultAccountCmd()
	})
	assert.Contains(t, showOutput, "Default account: work (work@example.com)")

	setOutput := captureAllStdout(t, func() {
		setDefaultAccountCmd("personal")
	})
	assert.Contains(t, setOutput, "Default account set to 'personal'")
	defaultAlias, err := auth.GetDefaultAnthropicAccount()
	require.NoError(t, err)
	assert.Equal(t, "personal", defaultAlias)

	renameOutput := captureAllStdout(t, func() {
		renameAccountCmd("personal", "home")
	})
	assert.Contains(t, renameOutput, "Account 'personal' renamed to 'home'")
	defaultAlias, err = auth.GetDefaultAnthropicAccount()
	require.NoError(t, err)
	assert.Equal(t, "home", defaultAlias)

	removeOutput := captureAllStdout(t, func() {
		removeAccountCmd("home")
	})
	assert.Contains(t, removeOutput, "Account 'home' removed successfully")
	assert.Contains(t, removeOutput, "Default account changed to 'work'")
	accounts, err := auth.ListAnthropicAccounts()
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "work", accounts[0].Alias)
}

func TestShowDefaultAccountCmdWithoutDefault(t *testing.T) {
	setupAnthropicAccountsTestHome(t)

	output := captureAllStdout(t, func() {
		showDefaultAccountCmd()
	})

	assert.Contains(t, output, "No default account set")
}

func TestAccountsUsageFormatting(t *testing.T) {
	assert.Equal(t, "✓ allowed", formatStatus("allowed"))
	assert.Equal(t, "⚠ limited", formatStatus("limited"))
	assert.Equal(t, "other", formatStatus("other"))

	assert.Equal(t, "unknown", formatResetTime(time.Time{}))
	assert.Contains(t, formatResetTime(time.Now().Add(-time.Minute)), "(passed)")
	future := formatResetTime(time.Now().Add(25*time.Hour + 2*time.Minute))
	assert.Contains(t, future, "(in 1d 1h")

	output := captureStdout(t, func() {
		outputJSONError("failed")
	})
	var payload map[string]string
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	assert.Equal(t, "failed", payload["error"])
}

func setupAnthropicAccountsTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func saveTestAnthropicAccount(t *testing.T, alias, email string, expiresAt time.Time) {
	t.Helper()

	_, err := auth.SaveAnthropicCredentialsWithAlias(alias, &auth.AnthropicCredentials{
		Email:        email,
		Scope:        "user",
		AccessToken:  "access-" + alias,
		RefreshToken: "refresh-" + alias,
		ExpiresAt:    expiresAt.Unix(),
	})
	require.NoError(t, err)
}

func captureAllStdout(t *testing.T, f func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	stdoutFD := int(os.Stdout.Fd())
	savedStdoutFD, err := syscall.Dup(stdoutFD)
	require.NoError(t, err)
	defer syscall.Close(savedStdoutFD)

	require.NoError(t, syscall.Dup2(int(w.Fd()), stdoutFD))

	output := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output <- buf.String()
	}()

	f()

	require.NoError(t, syscall.Dup2(savedStdoutFD, stdoutFD))
	require.NoError(t, w.Close())

	return <-output
}
