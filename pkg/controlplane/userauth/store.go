package userauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/pkg/errors"
)

const (
	stateVersion      = 1
	maxStateFileBytes = 1024 * 1024
)

// Store owns local non-browser user credentials and pending login state.
type Store struct {
	root    string
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// Credential is one active Kodelet-issued bearer credential for a control plane.
type Credential struct {
	Version      int               `json:"version"`
	Server       string            `json:"server"`
	CredentialID string            `json:"credentialId"`
	BearerToken  string            `json:"bearerToken"`
	Principal    PrincipalSnapshot `json:"principal"`
	CreatedAt    time.Time         `json:"createdAt"`
	ExpiresAt    time.Time         `json:"expiresAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

// PendingLogin is one uncompleted device login and its start-only secrets.
type PendingLogin struct {
	Version                 int       `json:"version"`
	Server                  string    `json:"server"`
	AuthorizationID         string    `json:"authorizationId"`
	DeviceCode              string    `json:"deviceCode"`
	UserCode                string    `json:"userCode"`
	VerificationURL         string    `json:"verificationUrl"`
	VerificationURLComplete string    `json:"verificationUrlComplete,omitempty"`
	BearerToken             string    `json:"bearerToken"`
	ExpiresAt               time.Time `json:"expiresAt"`
	PollIntervalMS          int64     `json:"pollIntervalMs"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// NewStore opens the default local control-plane user-auth directory.
func NewStore() (*Store, error) {
	basePath, err := convtypes.GetDefaultBasePath()
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve Kodelet state directory")
	}
	return NewStoreAt(filepath.Join(basePath, "control-plane-auth"))
}

// NewStoreAt opens a local user-auth directory at an explicit path.
func NewStoreAt(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("user authentication state directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve user authentication state directory")
	}
	root = filepath.Clean(absolute)
	for _, directory := range []string{root, filepath.Join(root, "credentials"), filepath.Join(root, "logins")} {
		if err := osutil.EnsurePrivateDir(directory); err != nil {
			return nil, errors.Wrapf(err, "failed to secure user authentication state directory %s", directory)
		}
	}
	return &Store{root: root, locks: make(map[string]*sync.Mutex)}, nil
}

// Root returns the local user-auth state directory.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// LoadCredential returns the active credential for a canonical server identity.
func (s *Store) LoadCredential(server string) (Credential, bool, error) {
	if s == nil {
		return Credential{}, false, errors.New("user authentication store is required")
	}
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return Credential{}, false, err
	}
	var credential Credential
	var found bool
	err = s.withStateLock("credentials", server, func() error {
		var loadErr error
		credential, found, loadErr = s.loadCredentialUnlocked(server)
		return loadErr
	})
	return credential, found, err
}

// SaveCredential atomically persists an active credential.
func (s *Store) SaveCredential(credential Credential) error {
	if s == nil {
		return errors.New("user authentication store is required")
	}
	_, err := s.saveCredentialAt(credential, time.Now().UTC())
	return err
}

// DeleteCredential removes an active credential only when its ID still matches.
func (s *Store) DeleteCredential(server, expectedCredentialID string) (bool, error) {
	if s == nil {
		return false, errors.New("user authentication store is required")
	}
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return false, err
	}
	if err := validateText("expected credential id", expectedCredentialID, true); err != nil {
		return false, err
	}
	var removed bool
	err = s.withStateLock("credentials", server, func() error {
		credential, found, loadErr := s.loadCredentialUnlocked(server)
		if loadErr != nil || !found {
			return loadErr
		}
		if credential.CredentialID != expectedCredentialID {
			return nil
		}
		if removeErr := os.Remove(s.credentialPath(server)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Wrap(removeErr, "failed to delete user credential")
		}
		removed = true
		return nil
	})
	return removed, err
}

// LoadPendingLogin returns the pending device login for a canonical server identity.
func (s *Store) LoadPendingLogin(server string) (PendingLogin, bool, error) {
	if s == nil {
		return PendingLogin{}, false, errors.New("user authentication store is required")
	}
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return PendingLogin{}, false, err
	}
	var login PendingLogin
	var found bool
	err = s.withStateLock("logins", server, func() error {
		var loadErr error
		login, found, loadErr = s.loadPendingLoginUnlocked(server)
		return loadErr
	})
	return login, found, err
}

// SavePendingLogin atomically persists a pending device login.
func (s *Store) SavePendingLogin(login PendingLogin) error {
	if s == nil {
		return errors.New("user authentication store is required")
	}
	prepared, err := preparePendingLogin(login, time.Now().UTC())
	if err != nil {
		return err
	}
	return s.withStateLock("logins", prepared.Server, func() error {
		return writeJSONAtomic(s.pendingLoginPath(prepared.Server), prepared)
	})
}

// DeletePendingLogin removes pending state only when its authorization ID still matches.
func (s *Store) DeletePendingLogin(server, expectedAuthorizationID string) (bool, error) {
	if s == nil {
		return false, errors.New("user authentication store is required")
	}
	server, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return false, err
	}
	if err := validateText("expected authorization id", expectedAuthorizationID, true); err != nil {
		return false, err
	}
	var removed bool
	err = s.withStateLock("logins", server, func() error {
		var deleteErr error
		removed, deleteErr = s.deletePendingLoginUnlocked(server, expectedAuthorizationID)
		return deleteErr
	})
	return removed, err
}

func (s *Store) saveCredentialAt(credential Credential, now time.Time) (Credential, error) {
	prepared, err := prepareCredential(credential, now)
	if err != nil {
		return Credential{}, err
	}
	err = s.withStateLock("credentials", prepared.Server, func() error {
		return writeJSONAtomic(s.credentialPath(prepared.Server), prepared)
	})
	if err != nil {
		return Credential{}, errors.Wrap(err, "failed to save user credential")
	}
	return prepared, nil
}

func (s *Store) loadCredentialUnlocked(server string) (Credential, bool, error) {
	credential, err := readJSONFile[Credential](s.credentialPath(server))
	if errors.Is(err, os.ErrNotExist) {
		return Credential{}, false, nil
	}
	if err != nil {
		return Credential{}, false, errors.Wrap(err, "failed to read user credential")
	}
	if err := validateStoredCredential(credential, server); err != nil {
		return Credential{}, false, errors.Wrap(err, "stored user credential is invalid")
	}
	credential.CreatedAt = credential.CreatedAt.UTC()
	credential.ExpiresAt = credential.ExpiresAt.UTC()
	credential.UpdatedAt = credential.UpdatedAt.UTC()
	return credential, true, nil
}

func (s *Store) loadPendingLoginUnlocked(server string) (PendingLogin, bool, error) {
	login, err := readJSONFile[PendingLogin](s.pendingLoginPath(server))
	if errors.Is(err, os.ErrNotExist) {
		return PendingLogin{}, false, nil
	}
	if err != nil {
		return PendingLogin{}, false, errors.Wrap(err, "failed to read pending user login")
	}
	if err := validateStoredPendingLogin(login, server); err != nil {
		return PendingLogin{}, false, errors.Wrap(err, "stored pending user login is invalid")
	}
	login.ExpiresAt = login.ExpiresAt.UTC()
	login.CreatedAt = login.CreatedAt.UTC()
	login.UpdatedAt = login.UpdatedAt.UTC()
	return login, true, nil
}

func (s *Store) deletePendingLoginUnlocked(server, expectedAuthorizationID string) (bool, error) {
	login, found, err := s.loadPendingLoginUnlocked(server)
	if err != nil || !found {
		return false, err
	}
	if login.AuthorizationID != expectedAuthorizationID {
		return false, nil
	}
	if err := os.Remove(s.pendingLoginPath(server)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, errors.Wrap(err, "failed to delete pending user login")
	}
	return true, nil
}

func prepareCredential(credential Credential, now time.Time) (Credential, error) {
	server, err := controlplaneurl.NormalizeBase(credential.Server)
	if err != nil {
		return Credential{}, err
	}
	credential.Server = server
	credential.Version = stateVersion
	credential.CreatedAt = credential.CreatedAt.UTC()
	credential.ExpiresAt = credential.ExpiresAt.UTC()
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now.UTC()
	}
	credential.UpdatedAt = now.UTC()
	if credential.UpdatedAt.Before(credential.CreatedAt) {
		credential.UpdatedAt = credential.CreatedAt
	}
	if err := validateCredentialFields(credential); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func preparePendingLogin(login PendingLogin, now time.Time) (PendingLogin, error) {
	server, err := controlplaneurl.NormalizeBase(login.Server)
	if err != nil {
		return PendingLogin{}, err
	}
	login.Server = server
	login.Version = stateVersion
	login.ExpiresAt = login.ExpiresAt.UTC()
	login.CreatedAt = login.CreatedAt.UTC()
	if login.CreatedAt.IsZero() {
		login.CreatedAt = now.UTC()
	}
	login.UpdatedAt = now.UTC()
	if login.UpdatedAt.Before(login.CreatedAt) {
		login.UpdatedAt = login.CreatedAt
	}
	if err := validatePendingLoginFields(login); err != nil {
		return PendingLogin{}, err
	}
	return login, nil
}

func validateStoredCredential(credential Credential, server string) error {
	if credential.Version != stateVersion {
		return errors.New("unsupported state version")
	}
	if credential.Server != server {
		return errors.New("stored server does not match credential key")
	}
	return validateCredentialFields(credential)
}

func validateCredentialFields(credential Credential) error {
	if err := validateText("credential id", credential.CredentialID, true); err != nil {
		return err
	}
	if err := ValidateBearerToken(credential.BearerToken); err != nil {
		return err
	}
	if err := credential.Principal.Validate(); err != nil {
		return errors.Wrap(err, "credential principal is invalid")
	}
	if credential.CreatedAt.IsZero() || credential.ExpiresAt.IsZero() || credential.UpdatedAt.IsZero() {
		return errors.New("credential timestamps are required")
	}
	if !credential.ExpiresAt.After(credential.CreatedAt) {
		return errors.New("credential expiration must be after creation")
	}
	if credential.UpdatedAt.Before(credential.CreatedAt) {
		return errors.New("credential update time must not precede creation")
	}
	return nil
}

func validateStoredPendingLogin(login PendingLogin, server string) error {
	if login.Version != stateVersion {
		return errors.New("unsupported state version")
	}
	if login.Server != server {
		return errors.New("stored server does not match login key")
	}
	return validatePendingLoginFields(login)
}

func validatePendingLoginFields(login PendingLogin) error {
	if err := (DevicePollRequest{AuthorizationID: login.AuthorizationID, DeviceCode: login.DeviceCode}).Validate(); err != nil {
		return err
	}
	if err := validateText("user code", login.UserCode, true); err != nil {
		return err
	}
	if err := validateVerificationURL("verification URL", login.VerificationURL, true); err != nil {
		return err
	}
	if err := validateVerificationURL("complete verification URL", login.VerificationURLComplete, false); err != nil {
		return err
	}
	if err := ValidateBearerToken(login.BearerToken); err != nil {
		return err
	}
	if _, err := durationFromMilliseconds(login.PollIntervalMS); err != nil {
		return errors.Wrap(err, "pending login poll interval is invalid")
	}
	if login.ExpiresAt.IsZero() || login.CreatedAt.IsZero() || login.UpdatedAt.IsZero() {
		return errors.New("pending login timestamps are required")
	}
	if !login.ExpiresAt.After(login.CreatedAt) {
		return errors.New("pending login expiration must be after creation")
	}
	if login.UpdatedAt.Before(login.CreatedAt) {
		return errors.New("pending login update time must not precede creation")
	}
	return nil
}

func (s *Store) credentialPath(server string) string {
	return filepath.Join(s.root, "credentials", stateKey(server)+".json")
}

func (s *Store) pendingLoginPath(server string) string {
	return filepath.Join(s.root, "logins", stateKey(server)+".json")
}

func stateKey(server string) string {
	digest := sha256.Sum256([]byte(server))
	return hex.EncodeToString(digest[:])
}

func (s *Store) withStateLock(directory, server string, operation func() error) error {
	if s == nil {
		return errors.New("user authentication store is required")
	}
	key := directory + "/" + stateKey(server)
	localLock := s.localLock(key)
	localLock.Lock()
	defer localLock.Unlock()

	lockPath := filepath.Join(s.root, directory, stateKey(server)+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Wrap(err, "failed to open user authentication state lock")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.Wrap(err, "failed to secure user authentication state lock")
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
		return errors.Wrap(lockErr, "failed to lock user authentication state")
	}
	defer func() {
		_ = unlockFile(file)
		_ = file.Close()
	}()
	return operation()
}

func (s *Store) localLock(key string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	lock := s.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	return lock
}

func readJSONFile[T any](path string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	if err := osutil.EnsurePrivateFile(path); err != nil {
		return value, errors.Wrap(err, "failed to secure JSON state")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxStateFileBytes+1))
	if err != nil {
		return value, errors.Wrap(err, "failed to read JSON state")
	}
	if len(payload) > maxStateFileBytes {
		return value, errors.Errorf("JSON state exceeds %d bytes", maxStateFileBytes)
	}
	if err := decodeStrictJSON(payload, &value); err != nil {
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".userauth-*.tmp")
	if err != nil {
		return errors.Wrap(err, "failed to create temporary user authentication state")
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.Wrap(err, "failed to secure temporary user authentication state")
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return errors.Wrap(err, "failed to write temporary user authentication state")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errors.Wrap(err, "failed to sync temporary user authentication state")
	}
	if err := temporary.Close(); err != nil {
		return errors.Wrap(err, "failed to close temporary user authentication state")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.Wrap(err, "failed to replace user authentication state")
	}
	if err := osutil.EnsurePrivateFile(path); err != nil {
		return errors.Wrap(err, "failed to secure user authentication state")
	}
	return nil
}

func decodeStrictJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains multiple values")
		}
		return errors.Wrap(err, "JSON contains trailing data")
	}
	return nil
}
