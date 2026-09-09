package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/controlplane"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localServerURLTestCommand mirrors the flags registered on `kodelet server url`.
func localServerURLTestCommand() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	command := &cobra.Command{Use: "url", RunE: localServerURLCommand}
	command.Flags().Bool("open", false, "")
	command.Flags().Bool("no-token", false, "")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	return command, stdout, stderr
}

func publishHealthyLocalServer(t *testing.T, directory string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer private-token", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(localServerStatus{
			APIReady:       true,
			InstanceID:     "same",
			EmbeddedRunner: controlplane.EmbeddedRunnerStatus{Enabled: true, Ready: true},
		})
	}))
	t.Cleanup(server.Close)
	lock, err := tryLocalServerLock(directory, "server.lock")
	require.NoError(t, err)
	require.NotNil(t, lock)
	t.Cleanup(func() { _ = lock.Close() })
	require.NoError(t, publishLocalServer(directory, server.URL, "private-token", "same", true))
	return server.URL
}

func TestLocalServerURLCommandPrintsTokenURL(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	endpoint := publishHealthyLocalServer(t, directory)

	command, stdout, stderr := localServerURLTestCommand()
	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(t, endpoint+"?token=private-token\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestLocalServerURLCommandOmitsTokenOnRequest(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	endpoint := publishHealthyLocalServer(t, directory)

	command, stdout, _ := localServerURLTestCommand()
	require.NoError(t, command.Flags().Set("no-token", "true"))
	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(t, endpoint+"\n", stdout.String())
}

func TestLocalServerURLCommandOpensBrowserWithoutLeakingToken(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	endpoint := publishHealthyLocalServer(t, directory)

	previous := localServerOpenBrowser
	t.Cleanup(func() { localServerOpenBrowser = previous })
	var opened string
	localServerOpenBrowser = func(target string) error {
		opened = target
		return nil
	}

	command, stdout, _ := localServerURLTestCommand()
	require.NoError(t, command.Flags().Set("open", "true"))
	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(t, endpoint+"?token=private-token", opened)
	assert.Equal(t, "Opened "+endpoint+"\n", stdout.String())
	assert.NotContains(t, stdout.String(), "private-token")
}

func TestLocalServerURLCommandFallsBackWhenBrowserFails(t *testing.T) {
	directory := localServerTestState(t)
	forbidLocalServerSpawn(t)
	endpoint := publishHealthyLocalServer(t, directory)

	previous := localServerOpenBrowser
	t.Cleanup(func() { localServerOpenBrowser = previous })
	localServerOpenBrowser = func(string) error { return errors.New("no browser") }

	command, stdout, stderr := localServerURLTestCommand()
	require.NoError(t, command.Flags().Set("open", "true"))
	require.NoError(t, command.ExecuteContext(t.Context()))
	assert.Equal(t, endpoint+"?token=private-token\n", stdout.String())
	assert.Contains(t, stderr.String(), "Could not open the browser automatically")
}
