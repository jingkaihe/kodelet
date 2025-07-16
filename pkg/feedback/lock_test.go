package feedback

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireLockWithPID(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "feedback-lock-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.json")

	// Test successful lock acquisition
	lock, err := acquireLock(testFile)
	require.NoError(t, err)
	assert.NotNil(t, lock)
	assert.NotEmpty(t, lock.lockPath)
	assert.NotNil(t, lock.lockFile)

	// Verify PID was written to lock file
	lockContent, err := os.ReadFile(lock.lockPath)
	require.NoError(t, err)
	expectedPID := fmt.Sprintf("%d\n", os.Getpid())
	assert.Equal(t, expectedPID, string(lockContent))

	// Release the lock
	err = lock.release()
	require.NoError(t, err)

	// Verify lock file is removed
	_, err = os.Stat(lock.lockPath)
	assert.True(t, os.IsNotExist(err))
}

func TestWithLock(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "feedback-lock-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.json")
	var executed bool

	// Test successful execution with lock
	err = withLock(testFile, func() error {
		executed = true
		return nil
	})

	require.NoError(t, err)
	assert.True(t, executed)

	// Verify lock file is cleaned up
	lockPath := testFile + ".lock"
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err))
}

func TestLockTimeout(t *testing.T) {
	// This test would take too long with the actual timeout (30 seconds)
	// So we'll skip it in normal testing, but it demonstrates the concept
	t.Skip("Skipping timeout test to avoid long test execution")

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "feedback-lock-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.json")
	lockPath := testFile + ".lock"

	// Create a persistent lock file (not stale)
	persistentLockFile, err := os.Create(lockPath)
	require.NoError(t, err)
	persistentLockFile.Close()

	// Try to acquire lock - should fail after timeout
	lock, err := acquireLock(testFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout waiting for lock")
	assert.Nil(t, lock)

	// Cleanup
	os.Remove(lockPath)
}

func TestGetJitteredDelay(t *testing.T) {
	// Test that jittered delay is within expected range
	for i := 0; i < 100; i++ {
		delay := getJitteredDelay()
		
		// Should be at least the base delay
		assert.GreaterOrEqual(t, delay, lockRetryDelay)
		
		// Should be at most base + jitter
		assert.LessOrEqual(t, delay, lockRetryDelay+lockRetryJitter)
	}
	
	// Test that multiple calls produce different values (randomness)
	delays := make(map[time.Duration]bool)
	for i := 0; i < 50; i++ {
		delay := getJitteredDelay()
		delays[delay] = true
	}
	
	// Should have some variation (not all the same)
	// With 50ms jitter, we expect at least a few different values
	assert.Greater(t, len(delays), 1, "jittered delay should produce some variation")
}

func TestConcurrentLockAccess(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "feedback-lock-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.json")
	
	// Counter to track sequential access
	counter := 0
	maxConcurrent := 5
	done := make(chan bool, maxConcurrent)

	// Start multiple goroutines trying to acquire the same lock
	for i := 0; i < maxConcurrent; i++ {
		go func(id int) {
			err := withLock(testFile, func() error {
				// Simulate some work
				currentValue := counter
				time.Sleep(10 * time.Millisecond)
				counter = currentValue + 1
				return nil
			})
			assert.NoError(t, err)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < maxConcurrent; i++ {
		<-done
	}

	// Verify that all increments were successful (no race conditions)
	assert.Equal(t, maxConcurrent, counter)
}