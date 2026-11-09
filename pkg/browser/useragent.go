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

// versionProbeTimeout bounds Chrome's --version; tests shorten it.
var versionProbeTimeout = 5 * time.Second

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
	probeCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, executable, "--version")
	cmd.WaitDelay = processGracePeriod
	output, err := cmd.Output()
	if probeErr := probeCtx.Err(); probeErr != nil {
		// A killed probe reports only its exit status, so surface why it was killed.
		return "", errors.Wrap(probeErr, "Chrome version probe did not finish")
	}
	if err != nil {
		return "", errors.Wrap(err, "failed to read Chrome version")
	}
	major, err := parseChromeMajorVersion(string(output))
	if err != nil {
		return "", err
	}
	return reducedUserAgent(runtime.GOOS, major)
}

// launchUserAgent returns the cached user agent, or "" to keep Chrome's default. Canceled and timed-out probes are not cached.
func (m *Manager) launchUserAgent(ctx context.Context, executable string) string {
	// A canceled launch stops waiting for another launch's probe instead of blocking until it finishes.
	if err := m.userAgentGate.Acquire(ctx, 1); err != nil {
		return ""
	}
	defer m.userAgentGate.Release(1)
	if m.userAgentProbed {
		return m.userAgent
	}
	userAgent, err := detectUserAgent(ctx, executable)
	if ctx.Err() != nil {
		return ""
	}
	if err != nil {
		logger.G(ctx).WithError(err).Warn("could not determine browser version; keeping the default user agent")
		if errors.Is(err, context.DeadlineExceeded) {
			// A slow cold start may answer in time on the next launch.
			return ""
		}
	}
	m.userAgent, m.userAgentProbed = userAgent, true
	return userAgent
}
