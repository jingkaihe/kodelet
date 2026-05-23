package binaries

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFdSpec(t *testing.T) {
	spec := FdSpec()

	assert.Equal(t, "fd", spec.Name)
	assert.Equal(t, FdVersion, spec.Version)
	assert.Equal(t, "fd", spec.BinaryName)
	assert.Equal(t, []string{"fd", "fdfind"}, spec.SystemNames)
	assert.NotNil(t, spec.GetDownloadURL)
	assert.NotNil(t, spec.GetChecksum)
	assert.NotNil(t, spec.GetArchiveEntry)
	assert.NotNil(t, spec.GetVersionCmd)
}

func TestGetFdDownloadURL(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		goarch    string
		wantURL   string
		wantError bool
	}{
		{
			name:    "darwin amd64",
			goos:    "darwin",
			goarch:  "amd64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-x86_64-apple-darwin.tar.gz",
		},
		{
			name:    "darwin arm64",
			goos:    "darwin",
			goarch:  "arm64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-aarch64-apple-darwin.tar.gz",
		},
		{
			name:    "linux amd64",
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-x86_64-unknown-linux-musl.tar.gz",
		},
		{
			name:    "linux arm64",
			goos:    "linux",
			goarch:  "arm64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-aarch64-unknown-linux-gnu.tar.gz",
		},
		{
			name:    "windows amd64",
			goos:    "windows",
			goarch:  "amd64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-x86_64-pc-windows-msvc.zip",
		},
		{
			name:    "windows arm64",
			goos:    "windows",
			goarch:  "arm64",
			wantURL: "https://github.com/sharkdp/fd/releases/download/v10.3.0/fd-v10.3.0-aarch64-pc-windows-msvc.zip",
		},
		{
			name:      "unsupported platform",
			goos:      "freebsd",
			goarch:    "amd64",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := getFdDownloadURL(FdVersion, tt.goos, tt.goarch)
			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
		})
	}
}

func TestGetFdChecksum(t *testing.T) {
	checksum, err := getFdChecksum(FdVersion, "linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, fdChecksums["linux/amd64"], checksum)

	_, err = getFdChecksum(FdVersion, "freebsd", "amd64")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no checksum available for platform: freebsd/amd64")
}

func TestGetFdArchiveEntry(t *testing.T) {
	assert.Equal(t, "fd", getFdArchiveEntry(FdVersion, "linux", "amd64"))
	assert.Equal(t, "fd", getFdArchiveEntry(FdVersion, "darwin", "arm64"))
	assert.Equal(t, "fd.exe", getFdArchiveEntry(FdVersion, "windows", "amd64"))
}

func TestGetFdVersionCmd(t *testing.T) {
	args, parser := getFdVersionCmd("/tmp/fd")
	assert.Equal(t, []string{"/tmp/fd", "--version"}, args)
	assert.Equal(t, "10.3.0", parser("fd 10.3.0\n"))
}

func TestBinaryPathCacheGetCachesResult(t *testing.T) {
	cache := BinaryPathCache{}
	calls := 0

	path, err := cache.Get(func() (string, error) {
		calls++
		return "/tmp/fd", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/fd", path)

	path, err = cache.Get(func() (string, error) {
		calls++
		return "", assert.AnError
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/fd", path)
	assert.Equal(t, 1, calls)
}

func TestEnsureFdAndRipgrepUseLibexecAndCachePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix-style executable scripts")
	}

	oldLibexecDir := libexecDir
	oldFdCache := fdCache
	oldRipgrepCache := ripgrepCache
	libexecDir = filepath.Join(t.TempDir(), "libexec")
	fdCache = &BinaryPathCache{}
	ripgrepCache = &BinaryPathCache{}
	t.Cleanup(func() {
		libexecDir = oldLibexecDir
		fdCache = oldFdCache
		ripgrepCache = oldRipgrepCache
	})

	require.NoError(t, os.MkdirAll(libexecDir, 0o755))
	writeVersionedTestBinary(t, GetLibexecBinaryPath("fd"), "fd 10.3.0")
	writeVersionedTestBinary(t, GetLibexecBinaryPath("rg"), "ripgrep 15.1.0")

	fdPath, err := EnsureFd(context.Background())
	require.NoError(t, err)
	assert.Equal(t, GetLibexecBinaryPath("fd"), fdPath)
	assert.Equal(t, fdPath, GetFdPath())

	rgPath, err := EnsureRipgrep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, GetLibexecBinaryPath("rg"), rgPath)
	assert.Equal(t, rgPath, GetRipgrepPath())

	// Remove the files after resolution to prove subsequent Ensure* calls are cached.
	require.NoError(t, os.Remove(fdPath))
	require.NoError(t, os.Remove(rgPath))
	fdPathAgain, err := EnsureFd(context.Background())
	require.NoError(t, err)
	assert.Equal(t, fdPath, fdPathAgain)
	rgPathAgain, err := EnsureRipgrep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, rgPath, rgPathAgain)
}
