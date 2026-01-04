package binaries

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

const (
	// FdVersion is the version of fd to download and use
	FdVersion = "10.3.0"
	fdBaseURL = "https://github.com/sharkdp/fd/releases/download"
)

var fdChecksums = map[string]string{
	"darwin/amd64":  "50d30f13fe3d5914b14c4fff5abcbd4d0cdab4b855970a6956f4f006c17117a3",
	"darwin/arm64":  "0570263812089120bc2a5d84f9e65cd0c25e4a4d724c80075c357239c74ae904",
	"linux/amd64":   "2b6bfaae8c48f12050813c2ffe1884c61ea26e750d803df9c9114550a314cd14",
	"linux/arm64":   "66f297e404400a3358e9a0c0b2f3f4725956e7e4435427a9ae56e22adbe73a68",
	"windows/amd64": "318aa2a6fa664325933e81fda60d523fff29444129e91ebf0726b5b3bcd8b059",
	"windows/arm64": "bf9b1e31bcac71c1e95d49c56f0d872f525b95d03854e94b1d4dd6786f825cc5",
}

var fdCache BinaryPathCache

// FdSpec returns the BinarySpec for fd
func FdSpec() BinarySpec {
	return BinarySpec{
		Name:            "fd",
		Version:         FdVersion,
		BinaryName:      "fd",
		GetDownloadURL:  getFdDownloadURL,
		GetChecksum:     getFdChecksum,
		GetArchiveEntry: getFdArchiveEntry,
		GetVersionCmd:   getFdVersionCmd,
	}
}

// EnsureFd ensures fd is installed and returns its path.
// It first tries to use the managed binary, then falls back to system fd.
// This is cached after the first successful call.
func EnsureFd(ctx context.Context) (string, error) {
	return fdCache.Get(func() (string, error) {
		return EnsureBinaryWithFallback(ctx, FdSpec())
	})
}

// GetFdPath returns the cached fd path without ensuring installation.
// Returns empty string if fd hasn't been ensured yet.
func GetFdPath() string {
	return fdCache.path
}

func getFdDownloadURL(version, goos, goarch string) (string, error) {
	platform, err := GetPlatformString(goos, goarch)
	if err != nil {
		return "", err
	}

	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}

	return fmt.Sprintf("%s/v%s/fd-v%s-%s.%s", fdBaseURL, version, version, platform, ext), nil
}

func getFdChecksum(_, goos, goarch string) (string, error) {
	key := fmt.Sprintf("%s/%s", goos, goarch)
	checksum, ok := fdChecksums[key]
	if !ok {
		return "", errors.Errorf("no checksum available for platform: %s", key)
	}
	return checksum, nil
}

func getFdArchiveEntry(_, goos, _ string) string {
	if goos == "windows" {
		return "fd.exe"
	}
	return "fd"
}

func getFdVersionCmd(binaryPath string) ([]string, func(string) string) {
	return []string{binaryPath, "--version"}, parseFdVersion
}

func parseFdVersion(output string) string {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return ""
	}
	parts := strings.Fields(lines[0])
	if len(parts) >= 2 && parts[0] == "fd" {
		return parts[1]
	}
	return ""
}
