package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseChromeMajorVersion(t *testing.T) {
	for output, want := range map[string]int{
		"Google Chrome 156.0.7500.100 \n":                                               156,
		"Chromium 156.0.8078.73 Built from source for Fedora release 44 (Forty Four)\n": 156,
		"Google Chrome for Testing 131.0.6778.204\n":                                    131,
		"HeadlessChrome 99.1.2.3":                                                       99,
	} {
		got, err := parseChromeMajorVersion(output)
		require.NoError(t, err, output)
		assert.Equal(t, want, got, output)
	}
	for _, output := range []string{"", "Chromium", "Chromium 156", "Chromium 156.0.1", "Chromium 0.0.0.0", "Chromium 99999999999999999999.0.0.0"} {
		_, err := parseChromeMajorVersion(output)
		assert.Error(t, err, output)
	}
}

func TestReducedUserAgent(t *testing.T) {
	linux, err := reducedUserAgent("linux", 156)
	require.NoError(t, err)
	assert.Equal(t, "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36", linux)
	darwin, err := reducedUserAgent("darwin", 156)
	require.NoError(t, err)
	assert.Equal(t, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36", darwin)
	assert.NotContains(t, linux+darwin, "Headless")
	_, err = reducedUserAgent("windows", 156)
	assert.ErrorContains(t, err, "unsupported browser platform")
}

func wrapFakeBrowser(t *testing.T, config Config, script string) Config {
	t.Helper()
	wrapper := filepath.Join(t.TempDir(), "wrapped-chrome")
	quoted := "'" + strings.ReplaceAll(config.Executable, "'", "'\"'\"'") + "'"
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\n"+script+"\nexec "+quoted+" \"$@\"\n"), 0o700))
	config.Executable = wrapper
	return config
}

func userAgentArgs(args []string) []string {
	var found []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "--user-agent=") {
			found = append(found, arg)
		}
	}
	return found
}

func TestHeadlessLaunchSendsNormalChromeUserAgent(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	info, err := m.Open(t.Context(), Scope{ConversationID: "conversation", CWD: t.TempDir()})
	require.NoError(t, err)
	s := openedSession(t, m, info)
	want, err := reducedUserAgent(runtime.GOOS, 156)
	require.NoError(t, err)
	assert.Contains(t, s.cmd.Args, "--headless")
	assert.Equal(t, []string{"--user-agent=" + want}, userAgentArgs(s.cmd.Args))
	assert.Equal(t, "about:blank", s.cmd.Args[len(s.cmd.Args)-1], "the start page must remain the last argument")
}

func TestUserAgentVersionIsProbedOncePerManager(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	counter := filepath.Join(t.TempDir(), "version-probes")
	config = wrapFakeBrowser(t, config, `if [ "$1" = "--version" ]; then echo probe >> '`+counter+`'; fi`)
	m := testManager(t, config)
	for _, conversation := range []string{"first", "second", "third"} {
		_, err := m.Open(t.Context(), Scope{ConversationID: conversation, CWD: t.TempDir()})
		require.NoError(t, err)
	}
	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "probe"))
}

func TestUndeterminedBrowserVersionKeepsDefaultUserAgentAndStillLaunches(t *testing.T) {
	for name, script := range map[string]string{
		"version fails":    `if [ "$1" = "--version" ]; then exit 1; fi`,
		"version is junk":  `if [ "$1" = "--version" ]; then echo 'not a version'; exit 0; fi`,
		"version is empty": `if [ "$1" = "--version" ]; then exit 0; fi`,
	} {
		t.Run(name, func(t *testing.T) {
			config, _ := fakeBrowserConfig(t)
			m := testManager(t, wrapFakeBrowser(t, config, script))
			info, err := m.Open(t.Context(), Scope{ConversationID: "conversation", CWD: t.TempDir()})
			require.NoError(t, err)
			s := openedSession(t, m, info)
			assert.Empty(t, userAgentArgs(s.cmd.Args))
			assert.Contains(t, s.cmd.Args, "--headless")
			assert.Equal(t, "about:blank", s.cmd.Args[len(s.cmd.Args)-1])
		})
	}
}

func TestCanceledVersionProbeIsNotCached(t *testing.T) {
	config, _ := fakeBrowserConfig(t)
	m := testManager(t, config)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Empty(t, m.launchUserAgent(canceled, config.Executable))
	want, err := reducedUserAgent(runtime.GOOS, 156)
	require.NoError(t, err)
	assert.Equal(t, want, m.launchUserAgent(t.Context(), config.Executable))
}
