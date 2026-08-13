package localstate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

// LockMetadata is diagnostic process information stored in the advisory lock file.
type LockMetadata struct {
	Version     int        `json:"version"`
	PID         int        `json:"pid"`
	Hostname    string     `json:"hostname"`
	Workspace   string     `json:"workspace"`
	Server      string     `json:"server"`
	RunnerID    string     `json:"runnerId,omitempty"`
	DisplayName string     `json:"displayName,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	StoppedAt   *time.Time `json:"stoppedAt,omitempty"`
}

// WorkspaceLock holds the OS-level advisory lock for one canonical workspace.
type WorkspaceLock struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	metadata LockMetadata
	closed   bool
}

// LockHeldError reports diagnostics from an already-running workspace runner.
type LockHeldError struct {
	Path     string
	Metadata LockMetadata
}

func (e *LockHeldError) Error() string {
	if e == nil {
		return "runner workspace lock is held"
	}
	details := make([]string, 0, 6)
	if workspace := strings.TrimSpace(e.Metadata.Workspace); workspace != "" {
		details = append(details, fmt.Sprintf("workspace %q", workspace))
	}
	if e.Metadata.PID > 0 {
		details = append(details, fmt.Sprintf("pid %d", e.Metadata.PID))
	}
	if runnerID := strings.TrimSpace(e.Metadata.RunnerID); runnerID != "" {
		details = append(details, fmt.Sprintf("runner %q", runnerID))
	}
	if server := strings.TrimSpace(e.Metadata.Server); server != "" {
		details = append(details, fmt.Sprintf("server %q", server))
	}
	if !e.Metadata.StartedAt.IsZero() {
		details = append(details, fmt.Sprintf("started %s", e.Metadata.StartedAt.UTC().Format(time.RFC3339)))
	}
	if path := strings.TrimSpace(e.Path); path != "" {
		details = append(details, fmt.Sprintf("lock %q", path))
	}
	if len(details) == 0 {
		return "workspace already has a runner process"
	}
	return fmt.Sprintf("workspace already has a runner process (%s)", strings.Join(details, ", "))
}

// AcquireWorkspaceLock acquires the non-blocking advisory lock for a canonical workspace.
func (s *Store) AcquireWorkspaceLock(workspace string, metadata LockMetadata) (*WorkspaceLock, error) {
	if s == nil {
		return nil, errors.New("runner state store is required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("runner workspace is required")
	}
	path := filepath.Join(s.root, "locks", stateKey(workspace)+".lock")
	file, held, err := openWorkspaceLockFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to lock runner workspace")
	}
	if held {
		existing, _ := readLockMetadata(path)
		existing.Workspace = workspace
		return nil, &LockHeldError{Path: path, Metadata: existing}
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, errors.Wrap(err, "failed to secure runner workspace lock")
	}
	lock := &WorkspaceLock{file: file, path: path}
	metadata.Version = stateVersion
	metadata.Workspace = workspace
	metadata.StoppedAt = nil
	if metadata.StartedAt.IsZero() {
		metadata.StartedAt = time.Now().UTC()
	}
	if err := lock.WriteMetadata(metadata); err != nil {
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

// WorkspaceLockPath returns the diagnostic lock-file path for a canonical workspace.
func (s *Store) WorkspaceLockPath(workspace string) string {
	if s == nil || strings.TrimSpace(workspace) == "" {
		return ""
	}
	return filepath.Join(s.root, "locks", stateKey(strings.TrimSpace(workspace))+".lock")
}

// ReadWorkspaceLockMetadata reads the latest diagnostic lock metadata without acquiring the lock.
func (s *Store) ReadWorkspaceLockMetadata(workspace string) (LockMetadata, bool, error) {
	path := s.WorkspaceLockPath(workspace)
	if path == "" {
		return LockMetadata{}, false, errors.New("runner workspace is required")
	}
	metadata, err := readLockMetadata(path)
	if errors.Is(err, os.ErrNotExist) {
		return LockMetadata{}, false, nil
	}
	if err != nil {
		return LockMetadata{}, false, err
	}
	return metadata, true, nil
}

// WorkspaceLockHeld reports whether another open file description currently owns
// the advisory lock for a workspace. A stale diagnostic file without a live lock
// returns false.
func (s *Store) WorkspaceLockHeld(workspace string) (bool, error) {
	path := s.WorkspaceLockPath(workspace)
	if path == "" {
		return false, errors.New("runner workspace is required")
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to open runner workspace lock")
	}
	file, held, err := openWorkspaceLockFile(path)
	if err != nil {
		return false, errors.Wrap(err, "failed to inspect runner workspace lock")
	}
	if held {
		return true, nil
	}
	_ = file.Close()
	return false, nil
}

// Path returns the diagnostic lock-file path.
func (l *WorkspaceLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Metadata returns the latest metadata written by this process.
func (l *WorkspaceLock) Metadata() LockMetadata {
	if l == nil {
		return LockMetadata{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.metadata
}

// WriteMetadata updates the diagnostic JSON while retaining the advisory lock.
func (l *WorkspaceLock) WriteMetadata(metadata LockMetadata) error {
	if l == nil || l.file == nil {
		return errors.New("runner workspace lock is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("runner workspace lock is closed")
	}
	metadata.Version = stateVersion
	payload, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to encode runner lock metadata")
	}
	payload = append(payload, '\n')
	if err := l.file.Truncate(0); err != nil {
		return errors.Wrap(err, "failed to truncate runner lock metadata")
	}
	if _, err := l.file.Seek(0, io.SeekStart); err != nil {
		return errors.Wrap(err, "failed to seek runner lock metadata")
	}
	if _, err := l.file.Write(payload); err != nil {
		return errors.Wrap(err, "failed to write runner lock metadata")
	}
	if err := l.file.Sync(); err != nil {
		return errors.Wrap(err, "failed to sync runner lock metadata")
	}
	l.metadata = metadata
	return nil
}

// Close records a stop time and releases the advisory lock.
func (l *WorkspaceLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	stoppedAt := time.Now().UTC()
	metadata := l.metadata
	metadata.StoppedAt = &stoppedAt
	l.mu.Unlock()
	writeErr := l.WriteMetadata(metadata)

	l.mu.Lock()
	l.closed = true
	file := l.file
	l.mu.Unlock()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func openWorkspaceLockFile(path string) (*os.File, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err == nil {
		return file, false, nil
	}
	closeErr := file.Close()
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		if closeErr != nil {
			return nil, false, errors.Wrap(closeErr, "failed to close runner workspace lock probe")
		}
		return nil, true, nil
	}
	if closeErr != nil {
		return nil, false, errors.Wrapf(err, "failed to lock runner workspace; closing lock file also failed: %v", closeErr)
	}
	return nil, false, &os.PathError{Op: "flock", Path: path, Err: err}
}

func readLockMetadata(path string) (LockMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return LockMetadata{}, err
	}
	defer file.Close()
	return readLockMetadataFile(file)
}

func readLockMetadataFile(file io.ReadSeeker) (LockMetadata, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return LockMetadata{}, err
	}
	payload, err := io.ReadAll(file)
	if err != nil {
		return LockMetadata{}, err
	}
	var metadata LockMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return LockMetadata{}, err
	}
	return metadata, nil
}
