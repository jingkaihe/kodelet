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
	return publishLocalServerWithRunner(t, directory, controlplane.EmbeddedRunnerStatus{Enabled: true, Ready: true})
}

func publishLocalServerWithRunner(t *testing.T, directory string, runner controlplane.EmbeddedRunnerStatus) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer private-token", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(localServerStatus{
			APIReady:       true,
			InstanceID:     "same",
			EmbeddedRunner: runner,
		})
	}))
	t.Cleanup(server.Close)
	lock, err := tryLocalServerLock(directory, "server.lock")
	require.NoError(t, err)
	require.NotNil(t, lock)
	t.Cleanup(func() { _ = lock.Close() })
	require.NoError(t, publishLocalServer(directory, server.URL, "private-token", "same", true, ""))
	return server.URL
}

func TestLocalServerURLCommandPrintsURL(t *testing.T) {
	for _, test := range []struct {
		name, webURL string
		noToken      bool
	}{
		{name: "token"},
		{name: "no token", noToken: true},
		{name: "OIDC", webURL: "https://kodelet.example.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			endpoint := publishHealthyLocalServer(t, directory)
			expected := endpoint + "?token=private-token"
			if test.webURL != "" {
				require.NoError(t, publishLocalServer(directory, endpoint, "private-token", "same", true, test.webURL))
				expected = test.webURL
			} else if test.noToken {
				expected = endpoint
			}
			command, stdout, stderr := localServerURLTestCommand()
			if test.noToken {
				require.NoError(t, command.Flags().Set("no-token", "true"))
			}
			require.NoError(t, command.ExecuteContext(t.Context()))
			assert.Equal(t, expected+"\n", stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestLocalServerURLCommandWorksWithoutEmbeddedRunner(t *testing.T) {
	for _, test := range []struct {
		name   string
		runner controlplane.EmbeddedRunnerStatus
	}{
		{"disabled", controlplane.EmbeddedRunnerStatus{Enabled: false}},
		{"failed", controlplane.EmbeddedRunnerStatus{Enabled: true, Error: "workspace locked"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			endpoint := publishLocalServerWithRunner(t, directory, test.runner)

			command, stdout, _ := localServerURLTestCommand()
			require.NoError(t, command.ExecuteContext(t.Context()))
			assert.Equal(t, endpoint+"?token=private-token\n", stdout.String())
		})
	}
}

func TestLocalServerURLCommandOpensBrowser(t *testing.T) {
	for _, test := range []struct {
		name, webURL string
		confirmed    bool
		launchErr    error
	}{
		{name: "token", confirmed: true},
		{name: "OIDC", webURL: "https://kodelet.example.com", confirmed: true},
		{name: "browser failure", launchErr: errors.New("no browser")},
		{name: "unconfirmed launch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := localServerTestState(t)
			forbidLocalServerSpawn(t)
			endpoint := publishHealthyLocalServer(t, directory)
			displayURL, target := endpoint, endpoint+"?token=private-token"
			if test.webURL != "" {
				require.NoError(t, publishLocalServer(directory, endpoint, "private-token", "same", true, test.webURL))
				displayURL, target = test.webURL, test.webURL
			}
			previous := localServerOpenBrowser
			t.Cleanup(func() { localServerOpenBrowser = previous })
			var opened string
			localServerOpenBrowser = func(url string) (bool, error) {
				opened = url
				return test.confirmed, test.launchErr
			}
			command, stdout, stderr := localServerURLTestCommand()
			require.NoError(t, command.Flags().Set("open", "true"))
			require.NoError(t, command.ExecuteContext(t.Context()))
			assert.Equal(t, target, opened)
			if test.confirmed {
				assert.Equal(t, "Opened "+displayURL+"\n", stdout.String())
				assert.NotContains(t, stdout.String(), "private-token")
			} else {
				assert.Equal(t, target+"\n", stdout.String())
			}
			if test.launchErr != nil {
				assert.Contains(t, stderr.String(), "Could not open the browser automatically")
			} else {
				assert.Empty(t, stderr.String())
			}
		})
	}
}
