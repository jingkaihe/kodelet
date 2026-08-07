// Package localstate stores runner identity, registrations, and workspace locks outside source repositories.
package localstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

const stateVersion = 1

// Store owns runner-local state rooted below the Kodelet user-state directory.
type Store struct {
	root string
}

// HostIdentity is the stable, non-secret identifier for one local Kodelet installation.
type HostIdentity struct {
	Version    int       `json:"version"`
	InstanceID string    `json:"instanceId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Registration caches one control-plane-assigned runner identity for a host workspace.
type Registration struct {
	Version     int       `json:"version"`
	Server      string    `json:"server"`
	Workspace   string    `json:"workspace"`
	RunnerID    string    `json:"runnerId"`
	DisplayName string    `json:"displayName,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewStore opens the default runner-local state directory.
func NewStore() (*Store, error) {
	basePath, err := convtypes.GetDefaultBasePath()
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve Kodelet state directory")
	}
	return NewStoreAt(filepath.Join(basePath, "runners"))
}

// NewStoreAt opens a runner-local state directory at an explicit path.
func NewStoreAt(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("runner state directory is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve runner state directory")
	}
	root = filepath.Clean(root)
	for _, directory := range []string{root, filepath.Join(root, "registrations"), filepath.Join(root, "locks")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, errors.Wrapf(err, "failed to create runner state directory %s", directory)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return nil, errors.Wrapf(err, "failed to secure runner state directory %s", directory)
		}
	}
	return &Store{root: root}, nil
}

// Root returns the runner-local state directory.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// LoadOrCreateHostIdentity returns the stable host instance ID, creating it atomically when absent.
func (s *Store) LoadOrCreateHostIdentity() (HostIdentity, error) {
	if s == nil {
		return HostIdentity{}, errors.New("runner state store is required")
	}
	path := filepath.Join(s.root, "host.json")
	identity, err := readJSONFile[HostIdentity](path)
	if err == nil {
		if identity.Version != stateVersion || strings.TrimSpace(identity.InstanceID) == "" {
			return HostIdentity{}, errors.New("runner host identity is invalid")
		}
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return HostIdentity{}, errors.Wrap(err, "failed to read runner host identity")
	}

	instanceID, err := randomID("host")
	if err != nil {
		return HostIdentity{}, errors.Wrap(err, "failed to generate runner host identity")
	}
	identity = HostIdentity{Version: stateVersion, InstanceID: instanceID, CreatedAt: time.Now().UTC()}
	if err := writeJSONExclusive(path, identity); err != nil {
		if errors.Is(err, os.ErrExist) {
			for range 20 {
				if existing, readErr := readJSONFile[HostIdentity](path); readErr == nil && existing.InstanceID != "" {
					return existing, nil
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		return HostIdentity{}, errors.Wrap(err, "failed to save runner host identity")
	}
	return identity, nil
}

// LoadRegistration returns a cached stable runner ID for a server and canonical workspace.
func (s *Store) LoadRegistration(server, workspace string) (Registration, bool, error) {
	if s == nil {
		return Registration{}, false, errors.New("runner state store is required")
	}
	normalizedServer, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return Registration{}, false, err
	}
	server = normalizedServer
	workspace = strings.TrimSpace(workspace)
	path := s.registrationPath(server, workspace)
	registration, err := readJSONFile[Registration](path)
	if errors.Is(err, os.ErrNotExist) {
		return Registration{}, false, nil
	}
	if err != nil {
		return Registration{}, false, errors.Wrap(err, "failed to read cached runner registration")
	}
	if registration.Version != stateVersion || strings.TrimSpace(registration.RunnerID) == "" {
		return Registration{}, false, errors.New("cached runner registration is invalid")
	}
	return registration, true, nil
}

// SaveRegistration atomically updates a cached stable runner ID.
func (s *Store) SaveRegistration(registration Registration) error {
	if s == nil {
		return errors.New("runner state store is required")
	}
	registration.Server = strings.TrimSpace(registration.Server)
	registration.Workspace = strings.TrimSpace(registration.Workspace)
	registration.RunnerID = strings.TrimSpace(registration.RunnerID)
	if registration.Server == "" || registration.Workspace == "" || registration.RunnerID == "" {
		return errors.New("server, workspace, and runner id are required")
	}
	server, err := controlplaneurl.NormalizeBase(registration.Server)
	if err != nil {
		return err
	}
	registration.Server = server
	registration.Version = stateVersion
	registration.UpdatedAt = time.Now().UTC()
	return s.withRegistrationLock(registration.Server, registration.Workspace, func() error {
		return writeJSONAtomic(s.registrationPath(registration.Server, registration.Workspace), registration)
	})
}

// DeleteRegistration removes a cached registration only when it still contains the expected runner ID.
func (s *Store) DeleteRegistration(server, workspace, expectedRunnerID string) (bool, error) {
	if s == nil {
		return false, errors.New("runner state store is required")
	}
	server = strings.TrimSpace(server)
	workspace = strings.TrimSpace(workspace)
	expectedRunnerID = strings.TrimSpace(expectedRunnerID)
	if server == "" || workspace == "" || expectedRunnerID == "" {
		return false, errors.New("server, workspace, and expected runner id are required")
	}
	normalizedServer, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return false, err
	}
	server = normalizedServer
	removed := false
	err = s.withRegistrationLock(server, workspace, func() error {
		path := s.registrationPath(server, workspace)
		registration, err := readJSONFile[Registration](path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "failed to read cached runner registration before deletion")
		}
		if strings.TrimSpace(registration.RunnerID) != expectedRunnerID {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "failed to delete cached runner registration")
		}
		removed = true
		return nil
	})
	return removed, err
}

// Registrations lists every locally cached control-plane registration.
func (s *Store) Registrations() ([]Registration, error) {
	if s == nil {
		return nil, errors.New("runner state store is required")
	}
	directory := filepath.Join(s.root, "registrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list cached runner registrations")
	}
	registrations := make([]Registration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		registration, err := readJSONFile[Registration](filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read cached runner registration %s", entry.Name())
		}
		registration.Server, err = controlplaneurl.NormalizeBase(registration.Server)
		if err != nil {
			return nil, errors.Wrapf(err, "cached runner registration %s has an invalid server", entry.Name())
		}
		registrations = append(registrations, registration)
	}
	sort.Slice(registrations, func(i, j int) bool {
		if registrations[i].Server == registrations[j].Server {
			return registrations[i].Workspace < registrations[j].Workspace
		}
		return registrations[i].Server < registrations[j].Server
	})
	return registrations, nil
}

func (s *Store) registrationPath(server, workspace string) string {
	return filepath.Join(s.root, "registrations", stateKey(server, workspace)+".json")
}

func (s *Store) withRegistrationLock(server, workspace string, operation func() error) error {
	lockPath := filepath.Join(s.root, "registrations", stateKey(server, workspace)+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Wrap(err, "failed to open cached runner registration lock")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.Wrap(err, "failed to secure cached runner registration lock")
	}

	var lockErr error
	for range 100 {
		lockErr = tryLockFile(file)
		if lockErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lockErr != nil {
		_ = file.Close()
		return errors.Wrap(lockErr, "failed to lock cached runner registration")
	}
	defer func() {
		_ = unlockFile(file)
		_ = file.Close()
	}()
	return operation()
}

func stateKey(values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return fmt.Sprintf("%x", digest[:])
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value), nil
}

func readJSONFile[T any](path string) (T, error) {
	var value T
	payload, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, errors.Wrap(err, "failed to decode JSON state")
	}
	return value, nil
}

func writeJSONAtomic(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to encode JSON state")
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".runner-state-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func writeJSONExclusive(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to encode JSON state")
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

// CanonicalWorkspace resolves an existing workspace to a stable absolute path.
func CanonicalWorkspace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("workspace path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve workspace path")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve workspace symlinks")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", errors.Wrap(err, "failed to stat workspace")
	}
	if !info.IsDir() {
		return "", errors.New("runner workspace must be a directory")
	}
	return osutil.CanonicalizePath(resolved), nil
}
