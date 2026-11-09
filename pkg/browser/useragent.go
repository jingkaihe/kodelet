package browser

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

const versionProbeTimeout = 5 * time.Second

var chromeVersionPattern = regexp.MustCompile(`\b(\d+)\.\d+\.\d+\.\d+\b`)

// reducedUserAgent returns the User-Agent of a desktop Chrome, which only varies by major version and platform.
func reducedUserAgent(goos string, major int) (string, error) {
	var platform string
	switch goos {
	case "linux":
		platform = "X11; Linux x86_64"
	case "darwin":
		platform = "Macintosh; Intel Mac OS X 10_15_7"
	default:
		return "", errors.Errorf("unsupported browser platform %q", goos)
	}
	return fmt.Sprintf("Mozilla/5.0 (%s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", platform, major), nil
}

func parseChromeMajorVersion(output string) (int, error) {
	match := chromeVersionPattern.FindStringSubmatch(output)
	if match == nil {
		return 0, errors.Errorf("no Chrome version found in %q", output)
	}
	major, err := strconv.Atoi(match[1])
	if err != nil || major <= 0 {
		return 0, errors.Errorf("invalid Chrome major version %q", match[1])
	}
	return major, nil
}

func detectUserAgent(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--version")
	cmd.WaitDelay = processGracePeriod
	output, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "failed to read Chrome version")
	}
	major, err := parseChromeMajorVersion(string(output))
	if err != nil {
		return "", err
	}
	return reducedUserAgent(runtime.GOOS, major)
}

// launchUserAgent returns the cached user agent, or "" to keep Chrome's default. Canceled probes are not cached.
func (m *Manager) launchUserAgent(ctx context.Context, executable string) string {
	m.userAgentMu.Lock()
	defer m.userAgentMu.Unlock()
	if m.userAgentProbed {
		return m.userAgent
	}
	userAgent, err := detectUserAgent(ctx, executable)
	if ctx.Err() != nil {
		return ""
	}
	if err != nil {
		logger.G(ctx).WithError(err).Warn("could not determine browser version; keeping the default user agent")
	}
	m.userAgent, m.userAgentProbed = userAgent, true
	return userAgent
}
