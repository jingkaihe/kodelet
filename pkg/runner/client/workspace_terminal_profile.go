package client

import (
	"context"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	workspaceGhosttyTerm = "xterm-ghostty"
	// Bump the cache version when the bundled terminfo changes.
	workspaceGhosttyTerminfoVersion = "xterm-ghostty-v1"
	workspaceTerminfoInstallTimeout = 3 * time.Second
)

// The bundled source includes provenance and the upstream MIT license notice.
//
//go:embed terminfo/xterm-ghostty.terminfo
var workspaceGhosttyTerminfo string

type workspaceTerminalProfileConfig struct {
	term        string
	terminfoDir string
}

// Cache failures too: an unavailable tic/cache should not delay every new PTY.
var workspaceTerminalProfile = sync.OnceValue(loadWorkspaceTerminalProfile)

func loadWorkspaceTerminalProfile() workspaceTerminalProfileConfig {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceTerminfoInstallTimeout)
	defer cancel()
	dir, err := installWorkspaceGhosttyTerminfo(ctx)
	if err != nil {
		logger.G(ctx).WithError(err).Warn("Ghostty terminfo unavailable; using xterm-256color for workspace terminals")
		return workspaceTerminalProfileConfig{term: "xterm-256color"}
	}
	return workspaceTerminalProfileConfig{term: workspaceGhosttyTerm, terminfoDir: dir}
}

func installWorkspaceGhosttyTerminfo(ctx context.Context) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", errors.Wrap(err, "find terminal cache directory")
	}
	dir := filepath.Join(cacheDir, "kodelet", "terminfo", workspaceGhosttyTerminfoVersion)
	if workspaceGhosttyTerminfoInstalled(dir) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errors.Wrap(err, "create terminal terminfo cache")
	}
	cmd := exec.CommandContext(ctx, "tic", "-x", "-o", dir, "-")
	cmd.Stdin = strings.NewReader(workspaceGhosttyTerminfo)
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", errors.Wrapf(err, "install Ghostty terminfo: %s", strings.TrimSpace(string(output)))
	}
	if !workspaceGhosttyTerminfoInstalled(dir) {
		return "", errors.New("install Ghostty terminfo: compiled entry was not created")
	}
	return dir, nil
}

func workspaceGhosttyTerminfoInstalled(dir string) bool {
	// ncurses uses a character directory on Linux and a hex directory on macOS.
	for _, prefix := range []string{"x", "78"} {
		info, err := os.Stat(filepath.Join(dir, prefix, workspaceGhosttyTerm))
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func workspaceTerminalTerminfoDirs(profileDir, inheritedTerminfo, inheritedDirs string) string {
	dirs := make([]string, 0, 4)
	seen := make(map[string]bool)
	appendDir := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	if profileDir != "" {
		appendDir(profileDir)
	}
	if inheritedTerminfo != "" {
		appendDir(inheritedTerminfo)
	}
	if inheritedDirs != "" {
		for _, dir := range filepath.SplitList(inheritedDirs) {
			appendDir(dir)
		}
	}
	// An empty entry preserves ncurses' compiled-in system search paths, even
	// when the launcher provided only custom directories or installation failed.
	appendDir("")
	return strings.Join(dirs, string(os.PathListSeparator))
}

func isWorkspaceTerminalMetadata(key string) bool {
	// Only replace known emulator metadata. In particular, do not filter whole
	// prefixes: the PTY shares the runner's trusted environment and credentials.
	switch key {
	case "TERM", "TERMINFO", "TERMINFO_DIRS", "TERM_PROGRAM", "TERM_PROGRAM_VERSION",
		"COLORTERM", "SHELL", "LC_TERMINAL", "LC_TERMINAL_VERSION",
		"TERM_SESSION_ID", "ITERM_SESSION_ID", "ITERM_PROFILE",
		"GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR", "GHOSTTY_SHELL_FEATURES",
		"KITTY_WINDOW_ID", "KITTY_PID", "KITTY_PUBLIC_KEY", "KITTY_INSTALLATION_DIR", "KITTY_LISTEN_ON",
		"WEZTERM_PANE", "WEZTERM_UNIX_SOCKET", "WEZTERM_EXECUTABLE", "WEZTERM_EXECUTABLE_DIR":
		return true
	}
	return false
}
