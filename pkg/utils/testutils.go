package utils

import (
	"os"
	"strings"
	"time"
)

// WaitForCondition polls a condition function until it returns true or times out.
// It returns true if the condition was met, false if it timed out.
//
// Parameters:
// - timeout: maximum time to wait
// - interval: how often to check the condition
// - condition: function that returns true when the desired state is reached
//
// Example usage:
//   success := WaitForCondition(5*time.Second, 100*time.Millisecond, func() bool {
//       _, err := os.Stat(expectedFile)
//       return err == nil  // file exists
//   })
func WaitForCondition(timeout, interval time.Duration, condition func() bool) bool {
	if timeout <= 0 {
		return condition()
	}

	deadline := time.Now().Add(timeout)
	
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(interval)
	}
	
	// Final check after timeout
	return condition()
}

// WaitForFiles waits for multiple files to exist within the timeout period.
// Returns true if all files exist, false if timeout reached.
func WaitForFiles(timeout, interval time.Duration, filePaths ...string) bool {
	return WaitForCondition(timeout, interval, func() bool {
		for _, path := range filePaths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return false
			}
		}
		return true
	})
}

// WaitForFileContent waits for a file to exist and contain specific content.
// Returns true if file contains all expected content, false if timeout reached.
func WaitForFileContent(timeout, interval time.Duration, filePath string, expectedContent []string) bool {
	return WaitForCondition(timeout, interval, func() bool {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return false
		}
		
		contentStr := string(content)
		for _, expected := range expectedContent {
			if !strings.Contains(contentStr, expected) {
				return false
			}
		}
		return true
	})
}