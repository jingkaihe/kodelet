package binaries

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRipgrepSpec(t *testing.T) {
	spec := RipgrepSpec()

	assert.Equal(t, "ripgrep", spec.Name)
	assert.Equal(t, RipgrepVersion, spec.Version)
	assert.Equal(t, "rg", spec.BinaryName)
	assert.NotNil(t, spec.GetDownloadURL)
	assert.NotNil(t, spec.GetChecksumURL)
	assert.NotNil(t, spec.GetArchiveEntry)
}

func TestGetRipgrepDownloadURL(t *testing.T) {
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
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-x86_64-apple-darwin.tar.gz",
		},
		{
			name:    "darwin arm64",
			goos:    "darwin",
			goarch:  "arm64",
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-aarch64-apple-darwin.tar.gz",
		},
		{
			name:    "linux amd64",
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-x86_64-unknown-linux-musl.tar.gz",
		},
		{
			name:    "linux arm64",
			goos:    "linux",
			goarch:  "arm64",
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-aarch64-unknown-linux-gnu.tar.gz",
		},
		{
			name:    "windows amd64",
			goos:    "windows",
			goarch:  "amd64",
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-x86_64-pc-windows-msvc.zip",
		},
		{
			name:    "windows arm64",
			goos:    "windows",
			goarch:  "arm64",
			wantURL: "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-aarch64-pc-windows-msvc.zip",
		},
		{
			name:      "unsupported platform",
			goos:      "freebsd",
			goarch:    "amd64",
			wantError: true,
		},
		{
			name:      "unsupported arch linux arm",
			goos:      "linux",
			goarch:    "arm",
			wantError: true,
		},
		{
			name:      "unsupported arch windows 386",
			goos:      "windows",
			goarch:    "386",
			wantError: true,
		},
		{
			name:      "unsupported arch",
			goos:      "linux",
			goarch:    "mips",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := getRipgrepDownloadURL(RipgrepVersion, tt.goos, tt.goarch)
			if tt.wantError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, url)
		})
	}
}

func TestGetRipgrepChecksumURL(t *testing.T) {
	url, err := getRipgrepChecksumURL(RipgrepVersion, "linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/BurntSushi/ripgrep/releases/download/15.1.0/ripgrep-15.1.0-x86_64-unknown-linux-musl.tar.gz.sha256", url)
}

func TestGetRipgrepArchiveEntry(t *testing.T) {
	assert.Equal(t, "rg", getRipgrepArchiveEntry(RipgrepVersion, "linux", "amd64"))
	assert.Equal(t, "rg", getRipgrepArchiveEntry(RipgrepVersion, "darwin", "arm64"))
	assert.Equal(t, "rg.exe", getRipgrepArchiveEntry(RipgrepVersion, "windows", "amd64"))
}

func TestGetRipgrepPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		goarch   string
		expected string
		wantErr  bool
	}{
		{"darwin", "amd64", "x86_64-apple-darwin", false},
		{"darwin", "arm64", "aarch64-apple-darwin", false},
		{"linux", "amd64", "x86_64-unknown-linux-musl", false},
		{"linux", "arm64", "aarch64-unknown-linux-gnu", false},
		{"windows", "amd64", "x86_64-pc-windows-msvc", false},
		{"windows", "arm64", "aarch64-pc-windows-msvc", false},
		{"freebsd", "amd64", "", true},
		{"linux", "arm", "", true},
		{"windows", "386", "", true},
		{"linux", "riscv64", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			platform, err := getRipgrepPlatform(tt.goos, tt.goarch)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, platform)
		})
	}
}
