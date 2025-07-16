package feedback

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/pkg/errors"
)

const (
	lockTimeout      = 30 * time.Second // Maximum time to wait for lock
	lockRetryDelay   = 50 * time.Millisecond
	lockRetryJitter  = 50 * time.Millisecond // Add up to 50ms of jitter
)

// getJitteredDelay returns the base delay plus some random jitter to prevent thundering herd
func getJitteredDelay() time.Duration {
	jitter := time.Duration(rand.Int63n(int64(lockRetryJitter)))
	return lockRetryDelay + jitter
}

// fileLock represents a file-based lock
type fileLock struct {
	lockPath string
	lockFile *os.File
}

// acquireLock attempts to acquire a file lock with timeout and PID tracking
func acquireLock(filePath string) (*fileLock, error) {
	lockPath := filePath + ".lock"
	startTime := time.Now()

	for {
		// Try to create the lock file
		lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			// Successfully acquired lock - write PID for debugging
			pid := os.Getpid()
			lockFile.WriteString(fmt.Sprintf("%d\n", pid))
			
			return &fileLock{
				lockPath: lockPath,
				lockFile: lockFile,
			}, nil
		}

		if !os.IsExist(err) {
			// Some other error occurred
			return nil, errors.Wrap(err, "failed to create lock file")
		}

		// Lock file exists, check timeout
		if time.Since(startTime) > lockTimeout {
			return nil, errors.New("timeout waiting for lock")
		}

		// Wait before retrying with jitter to prevent thundering herd
		time.Sleep(getJitteredDelay())
	}
}

// release releases the file lock
func (fl *fileLock) release() error {
	if fl.lockFile != nil {
		fl.lockFile.Close()
		fl.lockFile = nil
	}
	
	if fl.lockPath != "" {
		err := os.Remove(fl.lockPath)
		fl.lockPath = ""
		return err
	}
	
	return nil
}

// withLock executes a function while holding a file lock
func withLock(filePath string, fn func() error) error {
	lock, err := acquireLock(filePath)
	if err != nil {
		return errors.Wrap(err, "failed to acquire lock")
	}
	
	defer func() {
		if releaseErr := lock.release(); releaseErr != nil {
			// Log but don't override the main error
			// logger.G(nil).WithError(releaseErr).Warn("failed to release lock")
		}
	}()
	
	return fn()
}