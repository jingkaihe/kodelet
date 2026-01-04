package binaries

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	// RipgrepVersion is the version of ripgrep to download and use
	RipgrepVersion = "15.1.0"
	ripgrepBaseURL = "https://github.com/BurntSushi/ripgrep/releases/download"
)

var (
	ripgrepPath     string
	ripgrepPathOnce sync.Once
	ripgrepPathErr  error
)

// RipgrepSpec returns the BinarySpec for ripgrep
func RipgrepSpec() BinarySpec {
	return BinarySpec{
		Name:            "ripgrep",
		Version:         RipgrepVersion,
		BinaryName:      "rg",
		GetDownloadURL:  getRipgrepDownloadURL,
		GetChecksumURL:  getRipgrepChecksumURL,
		GetArchiveEntry: getRipgrepArchiveEntry,
	}
}

// EnsureRipgrep ensures ripgrep is installed and returns its path.
// It first tries to use the managed binary, then falls back to system ripgrep.
// This is cached after the first successful call.
func EnsureRipgrep(ctx context.Context) (string, error) {
	ripgrepPathOnce.Do(func() {
		ripgrepPath, ripgrepPathErr = ensureRipgrepWithFallback(ctx)
	})
	return ripgrepPath, ripgrepPathErr
}

func ensureRipgrepWithFallback(ctx context.Context) (string, error) {
	path, err := EnsureBinary(ctx, RipgrepSpec())
	if err == nil {
		return path, nil
	}

	logger.G(ctx).WithError(err).Debug("Failed to ensure managed ripgrep, falling back to system ripgrep")

	systemPath, lookErr := exec.LookPath("rg")
	if lookErr == nil {
		logger.G(ctx).WithField("path", systemPath).Info("Using system-installed ripgrep")
		return systemPath, nil
	}

	return "", errors.Wrap(err, "failed to ensure ripgrep (managed download failed and no system ripgrep found)")
}

// GetRipgrepPath returns the cached ripgrep path without ensuring installation.
// Returns empty string if ripgrep hasn't been ensured yet.
func GetRipgrepPath() string {
	return ripgrepPath
}

func getRipgrepDownloadURL(version, goos, goarch string) (string, error) {
	platform, err := getRipgrepPlatform(goos, goarch)
	if err != nil {
		return "", err
	}

	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}

	return fmt.Sprintf("%s/%s/ripgrep-%s-%s.%s", ripgrepBaseURL, version, version, platform, ext), nil
}

func getRipgrepChecksumURL(version, goos, goarch string) (string, error) {
	downloadURL, err := getRipgrepDownloadURL(version, goos, goarch)
	if err != nil {
		return "", err
	}
	return downloadURL + ".sha256", nil
}

func getRipgrepArchiveEntry(_, goos, _ string) string {
	if goos == "windows" {
		return "rg.exe"
	}
	return "rg"
}

func getRipgrepPlatform(goos, goarch string) (string, error) {
	switch goos {
	case "darwin":
		switch goarch {
		case "amd64":
			return "x86_64-apple-darwin", nil
		case "arm64":
			return "aarch64-apple-darwin", nil
		}
	case "linux":
		switch goarch {
		case "amd64":
			return "x86_64-unknown-linux-musl", nil
		case "arm64":
			return "aarch64-unknown-linux-gnu", nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return "x86_64-pc-windows-msvc", nil
		case "arm64":
			return "aarch64-pc-windows-msvc", nil
		}
	}
	return "", errors.Errorf("unsupported platform: %s/%s", goos, goarch)
}
