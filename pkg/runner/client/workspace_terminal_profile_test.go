package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceTerminalEnvProfile(t *testing.T) {
	for _, profile := range []workspaceTerminalProfileConfig{
		{term: "xterm-ghostty", terminfoDir: "/kodelet/terminfo"},
		{term: "xterm-256color"},
	} {
		t.Run(profile.term, func(t *testing.T) {
			previous := workspaceTerminalProfile
			workspaceTerminalProfile = func() workspaceTerminalProfileConfig { return profile }
			t.Cleanup(func() { workspaceTerminalProfile = previous })

			t.Setenv("TERM", "dumb")
			t.Setenv("TERM_PROGRAM", "iTerm.app")
			t.Setenv("COLORTERM", "launcher-color")
			t.Setenv("SHELL", "/bin/zsh")
			t.Setenv("TERMINFO", "/launcher/terminfo")
			t.Setenv("TERMINFO_DIRS", "/launcher/other:/launcher/terminfo")
			metadata := []string{
				"TERM_PROGRAM_VERSION", "LC_TERMINAL", "LC_TERMINAL_VERSION",
				"TERM_SESSION_ID", "ITERM_SESSION_ID", "ITERM_PROFILE",
				"GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR", "GHOSTTY_SHELL_FEATURES",
				"KITTY_WINDOW_ID", "KITTY_PID", "KITTY_PUBLIC_KEY", "KITTY_INSTALLATION_DIR", "KITTY_LISTEN_ON",
				"WEZTERM_PANE", "WEZTERM_UNIX_SOCKET", "WEZTERM_EXECUTABLE", "WEZTERM_EXECUTABLE_DIR",
			}
			for _, key := range metadata {
				t.Setenv(key, "launcher-value")
			}
			preserved := []string{
				"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GHOSTTY_TEST_TOKEN",
				"KITTY_TEST_TOKEN", "WEZTERM_TEST_TOKEN", "KODELET_TEST_KEEP",
			}
			for _, key := range preserved {
				t.Setenv(key, "test-value")
			}

			env := make(map[string]string)
			for _, entry := range workspaceTerminalEnv("/bin/bash") {
				key, value, ok := strings.Cut(entry, "=")
				require.True(t, ok)
				require.NotContains(t, env, key, "duplicate environment entry")
				env[key] = value
			}
			assert.Equal(t, profile.term, env["TERM"])
			assert.Equal(t, "ghostty-web", env["TERM_PROGRAM"])
			assert.Equal(t, "truecolor", env["COLORTERM"])
			assert.Equal(t, "/bin/bash", env["SHELL"])
			assert.NotContains(t, env, "TERMINFO")
			wantDirs := "/launcher/terminfo:/launcher/other:"
			if profile.terminfoDir != "" {
				wantDirs = profile.terminfoDir + ":" + wantDirs
			}
			assert.Equal(t, wantDirs, env["TERMINFO_DIRS"])
			for _, key := range metadata {
				assert.NotContains(t, env, key)
			}
			for _, key := range preserved {
				assert.Equal(t, "test-value", env[key], key)
			}
			assert.Equal(t, os.Getenv("PATH"), env["PATH"])
		})
	}
}

func TestWorkspaceTerminalTerminfoDirs(t *testing.T) {
	for _, test := range []struct {
		name, profile, inherited, dirs, want string
	}{
		{name: "system defaults"},
		{name: "profile", profile: "/profile", want: "/profile:"},
		{name: "inherited terminfo", inherited: "/inherited", want: "/inherited:"},
		{name: "inherited directories", dirs: "/one:/two", want: "/one:/two:"},
		{name: "preserve system entry", dirs: ":/one:", want: ":/one"},
		{name: "deduplicate", profile: "/profile", inherited: "/one", dirs: "/one:/profile:/two", want: "/profile:/one:/two:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, workspaceTerminalTerminfoDirs(test.profile, test.inherited, test.dirs))
		})
	}
}

func isolateWorkspaceTerminalProfile(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	previous := workspaceTerminalProfile
	workspaceTerminalProfile = sync.OnceValue(loadWorkspaceTerminalProfile)
	t.Cleanup(func() { workspaceTerminalProfile = previous })
	cacheDir, err := os.UserCacheDir()
	require.NoError(t, err)
	return filepath.Join(cacheDir, "kodelet", "terminfo", workspaceGhosttyTerminfoVersion)
}

func TestWorkspaceTerminalProfileInstallsAndReusesGhosttyTerminfo(t *testing.T) {
	if _, err := exec.LookPath("tic"); err != nil {
		t.Skip("tic is not installed")
	}
	dir := isolateWorkspaceTerminalProfile(t)
	t.Setenv("TERMINFO", "")
	t.Setenv("TERMINFO_DIRS", "")
	profile := workspaceTerminalProfile()
	require.Equal(t, workspaceTerminalProfileConfig{term: "xterm-ghostty", terminfoDir: dir}, profile)
	require.True(t, workspaceGhosttyTerminfoInstalled(dir))

	if infocmp, err := exec.LookPath("infocmp"); err == nil {
		for _, term := range []string{"xterm-ghostty", "xterm-256color"} {
			cmd := exec.CommandContext(t.Context(), infocmp, "-x", term)
			cmd.Env = workspaceTerminalEnv("/bin/bash")
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
			assert.Contains(t, string(output), term)
		}
	}

	// The on-disk cache remains usable without tic, including after a restart.
	t.Setenv("PATH", t.TempDir())
	assert.Equal(t, profile, loadWorkspaceTerminalProfile())
}

func TestWorkspaceTerminalProfileFallback(t *testing.T) {
	for _, reason := range []string{"missing tic", "unusable cache", "missing cache directory"} {
		t.Run(reason, func(t *testing.T) {
			dir := isolateWorkspaceTerminalProfile(t)
			switch reason {
			case "missing tic":
				t.Setenv("PATH", t.TempDir())
			case "unusable cache":
				require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o700))
				require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o600))
			case "missing cache directory":
				t.Setenv("HOME", "")
				t.Setenv("XDG_CACHE_HOME", "")
			}
			profile := workspaceTerminalProfile()
			assert.Equal(t, workspaceTerminalProfileConfig{term: "xterm-256color"}, profile)
		})
	}
}

func TestWorkspaceGhosttyTerminfoInstallErrors(t *testing.T) {
	for _, test := range []struct {
		name, script, wantError string
	}{
		{
			name: "tic failure",
			script: `#!/bin/sh
echo broken >&2
exit 1
`,
			wantError: "broken",
		},
		{
			name: "missing compiled entry",
			script: `#!/bin/sh
exit 0
`,
			wantError: "compiled entry was not created",
		},
		{
			name: "timeout",
			script: `#!/bin/sh
exec /bin/sleep 10
`,
			wantError: "install Ghostty terminfo",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateWorkspaceTerminalProfile(t)
			binDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(binDir, "tic"), []byte(test.script), 0o700))
			t.Setenv("PATH", binDir)
			timeout := workspaceTerminfoInstallTimeout
			if test.name == "timeout" {
				timeout = 100 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			dir, err := installWorkspaceGhosttyTerminfo(ctx)
			require.ErrorContains(t, err, test.wantError)
			assert.Empty(t, dir)
			if test.name == "timeout" {
				assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
			}
		})
	}
}

func TestWorkspaceGhosttyTerminfoInstalledLayouts(t *testing.T) {
	for _, prefix := range []string{"x", "78"} {
		t.Run(prefix, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, prefix, workspaceGhosttyTerm)
			assert.False(t, workspaceGhosttyTerminfoInstalled(dir))
			require.NoError(t, os.MkdirAll(entry, 0o700))
			assert.False(t, workspaceGhosttyTerminfoInstalled(dir))
			require.NoError(t, os.Remove(entry))
			require.NoError(t, os.WriteFile(entry, nil, 0o600))
			assert.False(t, workspaceGhosttyTerminfoInstalled(dir))
			require.NoError(t, os.WriteFile(entry, []byte("compiled terminfo"), 0o600))
			assert.True(t, workspaceGhosttyTerminfoInstalled(dir))
		})
	}
}
