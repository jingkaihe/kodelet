// Package binaries provides functionality for managing external binary dependencies
// that kodelet requires, such as ripgrep and fd. It can resolve binaries from a
// packaged libexec directory, a managed per-user install location, or the system PATH.
package binaries

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	binDir           = ".kodelet/bin"
	libexecBinDir    = "/usr/libexec/kodelet"
	downloadTimeout  = 5 * time.Minute
	httpClientTimout = 30 * time.Second
)

var libexecDir = libexecBinDir

// BinarySpec defines the specification for an external binary
type BinarySpec struct {
	Name            string
	Version         string
	BinaryName      string
	SystemNames     []string
	GetDownloadURL  func(version, goos, goarch string) (string, error)
	GetChecksumURL  func(version, goos, goarch string) (string, error) // Optional if GetChecksum is provided
	GetChecksum     func(version, goos, goarch string) (string, error) // Optional: returns embedded checksum
	GetArchiveEntry func(version, goos, goarch string) string
	GetVersionCmd   func(binaryPath string) (args []string, parseVersion func(output string) string) // Returns command args and version parser
}

// EnsureDepsInstalled ensures all required binaries are installed
func EnsureDepsInstalled(ctx context.Context) {
	if _, err := EnsureRipgrep(ctx); err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to ensure ripgrep is installed, grep_tool may not work")
	}
	if _, err := EnsureFd(ctx); err != nil {
		logger.G(ctx).WithError(err).Warn("Failed to ensure fd is installed, glob_tool may not work")
	}
}

// BinaryPathCache provides thread-safe caching for binary paths
type BinaryPathCache struct {
	path string
	err  error
	once sync.Once
}

// Get returns the cached path, computing it once via the provided function
func (c *BinaryPathCache) Get(fn func() (string, error)) (string, error) {
	c.once.Do(func() {
		c.path, c.err = fn()
	})
	return c.path, c.err
}

// ResolveBinary resolves a binary using the following precedence:
// 1. Packaged libexec binary (e.g. /usr/libexec/kodelet/rg)
// 2. Managed user binary in ~/.kodelet/bin (if already installed with the expected version)
// 3. Managed user install attempt into ~/.kodelet/bin
// 4. System PATH lookup (including alternate names such as Debian's fdfind)
func ResolveBinary(ctx context.Context, spec BinarySpec) (string, error) {
	if path, ok := resolveLibexecBinary(ctx, spec); ok {
		return path, nil
	}

	if path, ok := resolveManagedBinary(ctx, spec); ok {
		return path, nil
	}

	path, err := EnsureBinary(ctx, spec)
	if err == nil {
		return path, nil
	}

	logger.G(ctx).WithError(err).Debugf("Failed to ensure managed %s, falling back to system PATH", spec.Name)

	if systemPath, ok := resolveSystemBinary(ctx, spec); ok {
		return systemPath, nil
	}

	return "", errors.Wrapf(err, "failed to resolve %s (libexec missing, managed install failed, and no system binary found)", spec.Name)
}

// GetPlatformString returns the platform-specific string for common rust-style releases
func GetPlatformString(goos, goarch string) (string, error) {
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

// GetBinDir returns the path to the kodelet bin directory
func GetBinDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get home directory")
	}
	return filepath.Join(homeDir, binDir), nil
}

// GetLibexecBinDir returns the path to the packaged libexec directory.
func GetLibexecBinDir() string {
	return libexecDir
}

// GetBinaryPath returns the full path to a binary in the kodelet bin directory
func GetBinaryPath(name string) (string, error) {
	binDir, err := GetBinDir()
	if err != nil {
		return "", err
	}
	binaryName := name
	if runtime.GOOS == "windows" {
		binaryName = name + ".exe"
	}
	return filepath.Join(binDir, binaryName), nil
}

// GetLibexecBinaryPath returns the full path to a packaged binary in the libexec directory.
func GetLibexecBinaryPath(name string) string {
	binaryName := name
	if runtime.GOOS == "windows" {
		binaryName = name + ".exe"
	}
	return filepath.Join(GetLibexecBinDir(), binaryName)
}

// EnsureBinary ensures the binary is installed with the correct version.
// Returns the path to the binary.
func EnsureBinary(ctx context.Context, spec BinarySpec) (string, error) {
	binDir, err := GetBinDir()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", errors.Wrap(err, "failed to create bin directory")
	}

	binaryName := spec.BinaryName
	if runtime.GOOS == "windows" {
		binaryName = spec.BinaryName + ".exe"
	}
	binaryPath := filepath.Join(binDir, binaryName)

	installedVersion := getInstalledVersion(binaryPath, spec)
	if installedVersion == spec.Version {
		logger.G(ctx).WithField("binary", spec.Name).WithField("version", spec.Version).Debug("Binary already installed")
		return binaryPath, nil
	}

	logger.G(ctx).WithField("binary", spec.Name).WithField("version", spec.Version).Info("Installing binary")

	downloadURL, err := spec.GetDownloadURL(spec.Version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", errors.Wrap(err, "failed to get download URL")
	}

	var expectedChecksum string
	if spec.GetChecksum != nil {
		expectedChecksum, err = spec.GetChecksum(spec.Version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return "", errors.Wrap(err, "failed to get embedded checksum")
		}
	} else if spec.GetChecksumURL != nil {
		checksumURL, err := spec.GetChecksumURL(spec.Version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return "", errors.Wrap(err, "failed to get checksum URL")
		}
		expectedChecksum, err = fetchChecksum(ctx, checksumURL)
		if err != nil {
			return "", errors.Wrap(err, "failed to fetch checksum")
		}
	} else {
		return "", errors.New("no checksum method provided")
	}

	archivePath := filepath.Join(binDir, filepath.Base(downloadURL))
	defer os.Remove(archivePath)

	if err := downloadFile(ctx, downloadURL, archivePath); err != nil {
		return "", errors.Wrap(err, "failed to download binary archive")
	}

	actualChecksum, err := calculateFileChecksum(archivePath)
	if err != nil {
		return "", errors.Wrap(err, "failed to calculate checksum")
	}

	if actualChecksum != expectedChecksum {
		return "", errors.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	archiveEntry := spec.GetArchiveEntry(spec.Version, runtime.GOOS, runtime.GOARCH)
	if err := extractBinary(archivePath, archiveEntry, binaryPath); err != nil {
		return "", errors.Wrap(err, "failed to extract binary")
	}

	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return "", errors.Wrap(err, "failed to set binary permissions")
	}

	logger.G(ctx).WithField("binary", spec.Name).WithField("version", spec.Version).Info("Binary installed successfully")
	return binaryPath, nil
}

func resolveLibexecBinary(ctx context.Context, spec BinarySpec) (string, bool) {
	binaryPath := GetLibexecBinaryPath(spec.BinaryName)
	if getInstalledVersion(binaryPath, spec) == spec.Version {
		logger.G(ctx).WithField("path", binaryPath).Infof("Using packaged %s from libexec", spec.BinaryName)
		return binaryPath, true
	}

	return "", false
}

func resolveManagedBinary(ctx context.Context, spec BinarySpec) (string, bool) {
	binaryPath, err := GetBinaryPath(spec.BinaryName)
	if err != nil {
		return "", false
	}

	if getInstalledVersion(binaryPath, spec) == spec.Version {
		logger.G(ctx).WithField("path", binaryPath).Debugf("Using managed %s from user bin dir", spec.BinaryName)
		return binaryPath, true
	}

	return "", false
}

func resolveSystemBinary(ctx context.Context, spec BinarySpec) (string, bool) {
	names := spec.SystemNames
	if len(names) == 0 {
		names = []string{spec.BinaryName}
	}

	for _, name := range names {
		systemPath, err := exec.LookPath(name)
		if err == nil {
			logger.G(ctx).WithField("path", systemPath).Infof("Using system-installed %s", name)
			return systemPath, true
		}
	}

	return "", false
}

func getInstalledVersion(binaryPath string, spec BinarySpec) string {
	if !fileExists(binaryPath) {
		return ""
	}

	if spec.GetVersionCmd == nil {
		return ""
	}

	args, parseVersion := spec.GetVersionCmd(binaryPath)
	if len(args) == 0 {
		return ""
	}

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return parseVersion(string(output))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fetchChecksum(ctx context.Context, url string) (string, error) {
	client := &http.Client{Timeout: httpClientTimout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("failed to fetch checksum: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	parts := strings.Fields(string(data))
	if len(parts) == 0 {
		return "", errors.New("empty checksum file")
	}
	return parts[0], nil
}

func downloadFile(ctx context.Context, url, destPath string) error {
	client := &http.Client{Timeout: downloadTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("failed to download: HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func calculateFileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func extractBinary(archivePath, entryName, destPath string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, entryName, destPath)
	}
	if strings.HasSuffix(archivePath, ".tar.gz") || strings.HasSuffix(archivePath, ".tgz") {
		return extractFromTarGz(archivePath, entryName, destPath)
	}
	return errors.Errorf("unsupported archive format: %s", archivePath)
}

func extractFromZip(archivePath, entryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.HasSuffix(f.Name, "/"+entryName) || filepath.Base(f.Name) == entryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, rc)
			return err
		}
	}
	return errors.Errorf("entry %s not found in archive", entryName)
}

func extractFromTarGz(archivePath, entryName, destPath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if strings.HasSuffix(header.Name, "/"+entryName) || filepath.Base(header.Name) == entryName {
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, tr)
			return err
		}
	}
	return errors.Errorf("entry %s not found in archive", entryName)
}
