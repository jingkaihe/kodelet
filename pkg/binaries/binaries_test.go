package binaries

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBinDir(t *testing.T) {
	binDir, err := GetBinDir()
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	expected := filepath.Join(homeDir, ".kodelet", "bin")
	assert.Equal(t, expected, binDir)
}

func TestGetBinaryPath(t *testing.T) {
	path, err := GetBinaryPath("rg")
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	binaryName := "rg"
	if runtime.GOOS == "windows" {
		binaryName = "rg.exe"
	}
	expected := filepath.Join(homeDir, ".kodelet", "bin", binaryName)
	assert.Equal(t, expected, path)
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	existingFile := filepath.Join(tmpDir, "exists.txt")
	err := os.WriteFile(existingFile, []byte("test"), 0o644)
	require.NoError(t, err)

	assert.True(t, fileExists(existingFile))
	assert.False(t, fileExists(filepath.Join(tmpDir, "nonexistent.txt")))
}

func TestCalculateFileChecksum(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello world")

	err := os.WriteFile(testFile, content, 0o644)
	require.NoError(t, err)

	checksum, err := calculateFileChecksum(testFile)
	require.NoError(t, err)

	hasher := sha256.New()
	hasher.Write(content)
	expected := hex.EncodeToString(hasher.Sum(nil))

	assert.Equal(t, expected, checksum)
}

func TestFetchChecksum(t *testing.T) {
	expectedChecksum := "abc123def456"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  filename.tar.gz\n", expectedChecksum)
	}))
	defer server.Close()

	checksum, err := fetchChecksum(context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, expectedChecksum, checksum)
}

func TestFetchChecksumError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := fetchChecksum(context.Background(), server.URL)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestDownloadFile(t *testing.T) {
	content := []byte("test file content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.txt")

	err := downloadFile(context.Background(), server.URL, destPath)
	require.NoError(t, err)

	downloaded, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded)
}

func TestExtractFromTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted")
	binaryContent := []byte("#!/bin/bash\necho hello")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "test-1.0.0/rg",
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)
	_, err = tw.Write(binaryContent)
	require.NoError(t, err)

	tw.Close()
	gzw.Close()
	file.Close()

	err = extractFromTarGz(archivePath, "rg", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, extracted)
}

func TestExtractFromZip(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.zip")
	destPath := filepath.Join(tmpDir, "extracted")
	binaryContent := []byte("binary content here")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	zw := zip.NewWriter(file)
	w, err := zw.Create("test-1.0.0/rg.exe")
	require.NoError(t, err)
	_, err = w.Write(binaryContent)
	require.NoError(t, err)

	zw.Close()
	file.Close()

	err = extractFromZip(archivePath, "rg.exe", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, extracted)
}

func TestExtractBinaryUnsupportedFormat(t *testing.T) {
	err := extractBinary("test.rar", "entry", "dest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported archive format")
}

func TestExtractFromTarGzEntryNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "other-file.txt",
		Mode: 0o644,
		Size: 4,
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)
	_, err = tw.Write([]byte("test"))
	require.NoError(t, err)

	tw.Close()
	gzw.Close()
	file.Close()

	err = extractFromTarGz(archivePath, "rg", destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")
}

func TestExtractFromZipEntryNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.zip")
	destPath := filepath.Join(tmpDir, "extracted")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	zw := zip.NewWriter(file)
	w, err := zw.Create("other-file.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("test"))
	require.NoError(t, err)

	zw.Close()
	file.Close()

	err = extractFromZip(archivePath, "rg.exe", destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in archive")
}

func TestExtractFromTarGzWithSimilarSuffixFiles(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted")
	completionContent := []byte("#compdef rg\n# zsh completion")
	binaryContent := []byte("#!/bin/bash\necho actual binary")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "ripgrep-1.0.0/complete/_rg",
		Mode: 0o644,
		Size: int64(len(completionContent)),
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)
	_, err = tw.Write(completionContent)
	require.NoError(t, err)

	hdr = &tar.Header{
		Name: "ripgrep-1.0.0/rg",
		Mode: 0o755,
		Size: int64(len(binaryContent)),
	}
	err = tw.WriteHeader(hdr)
	require.NoError(t, err)
	_, err = tw.Write(binaryContent)
	require.NoError(t, err)

	tw.Close()
	gzw.Close()
	file.Close()

	err = extractFromTarGz(archivePath, "rg", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, extracted, "should extract the actual binary, not the completion file ending with _rg")
}

func TestExtractFromZipWithSimilarSuffixFiles(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.zip")
	destPath := filepath.Join(tmpDir, "extracted")
	completionContent := []byte("#compdef rg\n# zsh completion")
	binaryContent := []byte("binary content here")

	file, err := os.Create(archivePath)
	require.NoError(t, err)

	zw := zip.NewWriter(file)

	w, err := zw.Create("ripgrep-1.0.0/complete/_rg.exe")
	require.NoError(t, err)
	_, err = w.Write(completionContent)
	require.NoError(t, err)

	w, err = zw.Create("ripgrep-1.0.0/rg.exe")
	require.NoError(t, err)
	_, err = w.Write(binaryContent)
	require.NoError(t, err)

	zw.Close()
	file.Close()

	err = extractFromZip(archivePath, "rg.exe", destPath)
	require.NoError(t, err)

	extracted, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, extracted, "should extract the actual binary, not the completion file ending with _rg.exe")
}

func TestParseRipgrepVersion(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "standard output",
			output:   "ripgrep 15.1.0\n-SIMD -AVX (compiled)\n+SIMD +AVX (runtime)\n",
			expected: "15.1.0",
		},
		{
			name:     "single line",
			output:   "ripgrep 14.0.0",
			expected: "14.0.0",
		},
		{
			name:     "empty output",
			output:   "",
			expected: "",
		},
		{
			name:     "malformed output",
			output:   "something else",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseRipgrepVersion(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseFdVersion(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name:     "standard output",
			output:   "fd 10.3.0\n",
			expected: "10.3.0",
		},
		{
			name:     "single line no newline",
			output:   "fd 9.0.0",
			expected: "9.0.0",
		},
		{
			name:     "empty output",
			output:   "",
			expected: "",
		},
		{
			name:     "malformed output",
			output:   "something else",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseFdVersion(tt.output)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetInstalledVersion(t *testing.T) {
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "test-binary")

	spec := BinarySpec{
		Name:       "test",
		Version:    "1.0.0",
		BinaryName: "test-binary",
	}

	assert.Empty(t, getInstalledVersion(binaryPath, spec), "should return empty for non-existent binary")

	err := os.WriteFile(binaryPath, []byte("test"), 0o755)
	require.NoError(t, err)

	assert.Empty(t, getInstalledVersion(binaryPath, spec), "should return empty when GetVersionCmd is nil")

	spec.GetVersionCmd = func(_ string) ([]string, func(string) string) {
		return []string{}, func(_ string) string { return "1.0.0" }
	}
	assert.Empty(t, getInstalledVersion(binaryPath, spec), "should return empty when args are empty")
}
