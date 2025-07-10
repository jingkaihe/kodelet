package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWaitForCondition(t *testing.T) {
	t.Run("condition met immediately", func(t *testing.T) {
		result := WaitForCondition(1*time.Second, 10*time.Millisecond, func() bool {
			return true
		})
		assert.True(t, result, "Expected condition to be met immediately")
	})

	t.Run("condition met after delay", func(t *testing.T) {
		start := time.Now()
		counter := 0
		result := WaitForCondition(1*time.Second, 10*time.Millisecond, func() bool {
			counter++
			return counter >= 3 // Will be true on the 3rd call
		})
		elapsed := time.Since(start)

		assert.True(t, result, "Expected condition to be met after delay")
		assert.GreaterOrEqual(t, elapsed, 20*time.Millisecond, "Expected at least 20ms delay")
		assert.GreaterOrEqual(t, counter, 3, "Expected at least 3 calls")
	})

	t.Run("condition times out", func(t *testing.T) {
		start := time.Now()
		result := WaitForCondition(50*time.Millisecond, 10*time.Millisecond, func() bool {
			return false
		})
		elapsed := time.Since(start)

		assert.False(t, result, "Expected condition to timeout")
		assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "Expected at least 50ms delay")
	})

	t.Run("zero timeout runs once", func(t *testing.T) {
		callCount := 0
		result := WaitForCondition(0, 10*time.Millisecond, func() bool {
			callCount++
			return false
		})

		assert.False(t, result, "Expected condition to fail with zero timeout")
		assert.Equal(t, 1, callCount, "Expected exactly 1 call with zero timeout")
	})
}

func TestWaitForFiles(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "file1.txt")
	file2 := filepath.Join(tempDir, "file2.txt")

	t.Run("wait for files that exist", func(t *testing.T) {
		// Create the files
		os.WriteFile(file1, []byte("content1"), 0644)
		os.WriteFile(file2, []byte("content2"), 0644)

		result := WaitForFiles(1*time.Second, 10*time.Millisecond, file1, file2)
		assert.True(t, result, "Expected files to be found")
	})

	t.Run("wait for files that don't exist", func(t *testing.T) {
		nonExistentFile := filepath.Join(tempDir, "nonexistent.txt")
		result := WaitForFiles(50*time.Millisecond, 10*time.Millisecond, nonExistentFile)
		assert.False(t, result, "Expected files to not be found")
	})

	t.Run("wait for files created during wait", func(t *testing.T) {
		delayedFile := filepath.Join(tempDir, "delayed.txt")
		
		// Create file after a short delay
		go func() {
			time.Sleep(30 * time.Millisecond)
			os.WriteFile(delayedFile, []byte("delayed content"), 0644)
		}()

		result := WaitForFiles(100*time.Millisecond, 10*time.Millisecond, delayedFile)
		assert.True(t, result, "Expected delayed file to be found")
	})
}

func TestWaitForFileContent(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "content_test.txt")

	t.Run("wait for content in existing file", func(t *testing.T) {
		content := "Hello World\nThis is a test file\n"
		os.WriteFile(testFile, []byte(content), 0644)

		result := WaitForFileContent(1*time.Second, 10*time.Millisecond, testFile, []string{"Hello", "test file"})
		assert.True(t, result, "Expected content to be found")
	})

	t.Run("wait for content not in file", func(t *testing.T) {
		content := "Hello World\n"
		os.WriteFile(testFile, []byte(content), 0644)

		result := WaitForFileContent(50*time.Millisecond, 10*time.Millisecond, testFile, []string{"missing content"})
		assert.False(t, result, "Expected content to not be found")
	})

	t.Run("wait for content in file that doesn't exist", func(t *testing.T) {
		nonExistentFile := filepath.Join(tempDir, "missing.txt")
		result := WaitForFileContent(50*time.Millisecond, 10*time.Millisecond, nonExistentFile, []string{"any content"})
		assert.False(t, result, "Expected file to not exist")
	})

	t.Run("wait for content added during wait", func(t *testing.T) {
		dynamicFile := filepath.Join(tempDir, "dynamic.txt")
		os.WriteFile(dynamicFile, []byte("initial content\n"), 0644)

		// Add content after a delay
		go func() {
			time.Sleep(30 * time.Millisecond)
			file, _ := os.OpenFile(dynamicFile, os.O_APPEND|os.O_WRONLY, 0644)
			file.WriteString("added content\n")
			file.Close()
		}()

		result := WaitForFileContent(100*time.Millisecond, 10*time.Millisecond, dynamicFile, []string{"initial", "added content"})
		assert.True(t, result, "Expected dynamic content to be found")
	})

	t.Run("empty expected content list", func(t *testing.T) {
		os.WriteFile(testFile, []byte("any content"), 0644)
		result := WaitForFileContent(50*time.Millisecond, 10*time.Millisecond, testFile, []string{})
		assert.True(t, result, "Expected empty content list to always match")
	})
}