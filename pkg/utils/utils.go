// Package utils provides common utility functions for kodelet including
// content formatting with line numbers, process management, language detection,
// domain filtering, and various helper functions used across the application.
package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/pkg/errors"
)

// ContentWithLineNumber formats a slice of strings by prefixing each line with its line number
// starting from the given offset, with appropriate padding for alignment.
func ContentWithLineNumber(lines []string, offset int) string {
	var result string
	maxLineWidth := 1

	if len(lines) > 0 {
		maxLineNum := offset + len(lines) - 1
		maxLineWidth = len(strconv.Itoa(maxLineNum))
	}

	// Format lines with appropriate padding
	for i, line := range lines {
		lineNum := offset + i
		paddedLineNum := fmt.Sprintf("%*d", maxLineWidth, lineNum)
		result += fmt.Sprintf("%s: %s\n", paddedLineNum, line)
	}

	return result
}

// IsBinaryFile checks if a file is binary by reading the first 512 bytes
// and looking for NULL bytes which indicate binary content
func IsBinaryFile(filePath string) bool {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	// Read the first 512 bytes
	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil {
		return false
	}
	buf = buf[:n]

	// Check for NULL bytes which indicate binary content
	for _, b := range buf {
		if b == 0 {
			return true
		}
	}

	return false
}

// OpenBrowser attempts to open the default browser with the given URL
func OpenBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return errors.New("unsupported operating system")
	}

	return exec.Command(cmd, args...).Start()
}
