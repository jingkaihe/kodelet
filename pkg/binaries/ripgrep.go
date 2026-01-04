package binaries

import (
	"context"
	"fmt"
)

const (
	// RipgrepVersion is the version of ripgrep to download and use
	RipgrepVersion = "15.1.0"
	ripgrepBaseURL = "https://github.com/BurntSushi/ripgrep/releases/download"
)

var ripgrepCache BinaryPathCache

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
	return ripgrepCache.Get(func() (string, error) {
		return EnsureBinaryWithFallback(ctx, RipgrepSpec())
	})
}

// GetRipgrepPath returns the cached ripgrep path without ensuring installation.
// Returns empty string if ripgrep hasn't been ensured yet.
func GetRipgrepPath() string {
	return ripgrepCache.path
}

func getRipgrepDownloadURL(version, goos, goarch string) (string, error) {
	platform, err := GetPlatformString(goos, goarch)
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
