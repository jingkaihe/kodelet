// Package localstate stores runner identity, registrations, and workspace locks outside source repositories.
package localstate

import (
	"bytes"
	"crypto/ed25519"
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
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
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

// Credential is one active server-issued credential bound to a runner-local Ed25519 key pair.
// PrivateKey and PublicKey are encoded as unpadded base64url only in the on-disk representation.
type Credential struct {
	Version      int                `json:"version"`
	Server       string             `json:"server"`
	Workspace    string             `json:"workspace"`
	CredentialID string             `json:"credentialId"`
	AccessToken  string             `json:"-"`
	Fingerprint  string             `json:"fingerprint"`
	PublicKey    ed25519.PublicKey  `json:"-"`
	PrivateKey   ed25519.PrivateKey `json:"-"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

// PendingEnrollment is one unapproved device-enrollment flow and its runner-local key pair.
type PendingEnrollment struct {
	Version                 int                `json:"version"`
	Server                  string             `json:"server"`
	Workspace               string             `json:"workspace"`
	EnrollmentID            string             `json:"enrollmentId"`
	DeviceCode              string             `json:"deviceCode"`
	UserCode                string             `json:"userCode,omitempty"`
	VerificationURL         string             `json:"verificationUrl,omitempty"`
	VerificationURLComplete string             `json:"verificationUrlComplete,omitempty"`
	Fingerprint             string             `json:"fingerprint"`
	PublicKey               ed25519.PublicKey  `json:"-"`
	PrivateKey              ed25519.PrivateKey `json:"-"`
	ExpiresAt               time.Time          `json:"expiresAt"`
	PollIntervalMS          int64              `json:"pollIntervalMs,omitempty"`
	CreatedAt               time.Time          `json:"createdAt"`
	UpdatedAt               time.Time          `json:"updatedAt"`
}

type credentialFile struct {
	Version      int       `json:"version"`
	Server       string    `json:"server"`
	Workspace    string    `json:"workspace"`
	CredentialID string    `json:"credentialId"`
	AccessToken  string    `json:"accessToken"`
	Fingerprint  string    `json:"fingerprint"`
	PublicKey    string    `json:"publicKey"`
	PrivateKey   string    `json:"privateKey"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type enrollmentFile struct {
	Version                 int       `json:"version"`
	Server                  string    `json:"server"`
	Workspace               string    `json:"workspace"`
	EnrollmentID            string    `json:"enrollmentId"`
	DeviceCode              string    `json:"deviceCode"`
	UserCode                string    `json:"userCode,omitempty"`
	VerificationURL         string    `json:"verificationUrl,omitempty"`
	VerificationURLComplete string    `json:"verificationUrlComplete,omitempty"`
	Fingerprint             string    `json:"fingerprint"`
	PublicKey               string    `json:"publicKey"`
	PrivateKey              string    `json:"privateKey"`
	ExpiresAt               time.Time `json:"expiresAt"`
	PollIntervalMS          int64     `json:"pollIntervalMs,omitempty"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
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
	for _, directory := range []string{
		root,
		filepath.Join(root, "registrations"),
		filepath.Join(root, "credentials"),
		filepath.Join(root, "enrollments"),
		filepath.Join(root, "locks"),
	} {
		if err := osutil.EnsurePrivateDir(directory); err != nil {
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

// LoadCredential returns the active key-bound credential for a server and canonical workspace.
func (s *Store) LoadCredential(server, workspace string) (Credential, bool, error) {
	if s == nil {
		return Credential{}, false, errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(server, workspace)
	if err != nil {
		return Credential{}, false, err
	}
	stored, err := readJSONFile[credentialFile](s.credentialPath(server, workspace))
	if errors.Is(err, os.ErrNotExist) {
		return Credential{}, false, nil
	}
	if err != nil {
		return Credential{}, false, errors.Wrap(err, "failed to read runner credential")
	}
	credential, err := decodeCredentialFile(stored, server, workspace)
	if err != nil {
		return Credential{}, false, errors.Wrap(err, "runner credential is invalid")
	}
	return credential, true, nil
}

// SaveCredential atomically persists an active key-bound credential.
func (s *Store) SaveCredential(credential Credential) error {
	if s == nil {
		return errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(credential.Server, credential.Workspace)
	if err != nil {
		return err
	}
	credential.Server = server
	credential.Workspace = workspace
	credential.CredentialID = strings.TrimSpace(credential.CredentialID)
	credential.Fingerprint = strings.TrimSpace(credential.Fingerprint)
	if credential.CredentialID == "" {
		return errors.New("credential id is required")
	}
	if err := protocol.ValidateRunnerAccessToken(credential.AccessToken); err != nil {
		return err
	}
	if err := validateCredentialKeyPair(credential.PublicKey, credential.PrivateKey, credential.Fingerprint); err != nil {
		return err
	}
	now := time.Now().UTC()
	credential.Version = stateVersion
	if credential.CreatedAt.IsZero() {
		credential.CreatedAt = now
	} else {
		credential.CreatedAt = credential.CreatedAt.UTC()
	}
	credential.UpdatedAt = now
	stored, err := encodeCredentialFile(credential)
	if err != nil {
		return err
	}
	return s.withKeyedStateLock("credentials", "runner credential", server, workspace, func() error {
		return writeJSONAtomic(s.credentialPath(server, workspace), stored)
	})
}

// DeleteCredential atomically removes the active credential for a server and canonical workspace.
func (s *Store) DeleteCredential(server, workspace string) (bool, error) {
	if s == nil {
		return false, errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(server, workspace)
	if err != nil {
		return false, err
	}
	removed := false
	err = s.withKeyedStateLock("credentials", "runner credential", server, workspace, func() error {
		if err := os.Remove(s.credentialPath(server, workspace)); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return errors.Wrap(err, "failed to delete runner credential")
		}
		removed = true
		return nil
	})
	return removed, err
}

// LoadPendingEnrollment returns the pending enrollment for a server and canonical workspace.
func (s *Store) LoadPendingEnrollment(server, workspace string) (PendingEnrollment, bool, error) {
	if s == nil {
		return PendingEnrollment{}, false, errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(server, workspace)
	if err != nil {
		return PendingEnrollment{}, false, err
	}
	stored, err := readJSONFile[enrollmentFile](s.enrollmentPath(server, workspace))
	if errors.Is(err, os.ErrNotExist) {
		return PendingEnrollment{}, false, nil
	}
	if err != nil {
		return PendingEnrollment{}, false, errors.Wrap(err, "failed to read pending runner enrollment")
	}
	enrollment, err := decodeEnrollmentFile(stored, server, workspace)
	if err != nil {
		return PendingEnrollment{}, false, errors.Wrap(err, "pending runner enrollment is invalid")
	}
	return enrollment, true, nil
}

// SavePendingEnrollment atomically persists a pending device-enrollment flow.
func (s *Store) SavePendingEnrollment(enrollment PendingEnrollment) error {
	if s == nil {
		return errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(enrollment.Server, enrollment.Workspace)
	if err != nil {
		return err
	}
	enrollment.Server = server
	enrollment.Workspace = workspace
	enrollment.EnrollmentID = strings.TrimSpace(enrollment.EnrollmentID)
	enrollment.DeviceCode = strings.TrimSpace(enrollment.DeviceCode)
	enrollment.UserCode = strings.TrimSpace(enrollment.UserCode)
	enrollment.VerificationURL = strings.TrimSpace(enrollment.VerificationURL)
	enrollment.VerificationURLComplete = strings.TrimSpace(enrollment.VerificationURLComplete)
	enrollment.Fingerprint = strings.TrimSpace(enrollment.Fingerprint)
	if enrollment.EnrollmentID == "" || enrollment.DeviceCode == "" {
		return errors.New("enrollment id and device code are required")
	}
	if err := protocol.ValidateRunnerAccessToken(enrollment.DeviceCode); err != nil {
		return err
	}
	if enrollment.ExpiresAt.IsZero() {
		return errors.New("enrollment expiration is required")
	}
	if enrollment.PollIntervalMS < 0 {
		return errors.New("enrollment poll interval must not be negative")
	}
	if err := validateCredentialKeyPair(enrollment.PublicKey, enrollment.PrivateKey, enrollment.Fingerprint); err != nil {
		return err
	}
	now := time.Now().UTC()
	enrollment.Version = stateVersion
	enrollment.ExpiresAt = enrollment.ExpiresAt.UTC()
	if enrollment.CreatedAt.IsZero() {
		enrollment.CreatedAt = now
	} else {
		enrollment.CreatedAt = enrollment.CreatedAt.UTC()
	}
	enrollment.UpdatedAt = now
	stored, err := encodeEnrollmentFile(enrollment)
	if err != nil {
		return err
	}
	return s.withKeyedStateLock("enrollments", "pending runner enrollment", server, workspace, func() error {
		return writeJSONAtomic(s.enrollmentPath(server, workspace), stored)
	})
}

// DeletePendingEnrollment atomically removes the pending enrollment for a server and canonical workspace.
func (s *Store) DeletePendingEnrollment(server, workspace string) (bool, error) {
	if s == nil {
		return false, errors.New("runner state store is required")
	}
	server, workspace, err := normalizeCredentialLocation(server, workspace)
	if err != nil {
		return false, err
	}
	removed := false
	err = s.withKeyedStateLock("enrollments", "pending runner enrollment", server, workspace, func() error {
		if err := os.Remove(s.enrollmentPath(server, workspace)); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return errors.Wrap(err, "failed to delete pending runner enrollment")
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

func (s *Store) credentialPath(server, workspace string) string {
	return filepath.Join(s.root, "credentials", stateKey(server, workspace)+".json")
}

func (s *Store) enrollmentPath(server, workspace string) string {
	return filepath.Join(s.root, "enrollments", stateKey(server, workspace)+".json")
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

func (s *Store) withKeyedStateLock(directory, description, server, workspace string, operation func() error) error {
	lockPath := filepath.Join(s.root, directory, stateKey(server, workspace)+".lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return errors.Wrapf(err, "failed to open %s lock", description)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return errors.Wrapf(err, "failed to secure %s lock", description)
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
		return errors.Wrapf(lockErr, "failed to lock %s", description)
	}
	defer func() {
		_ = unlockFile(file)
		_ = file.Close()
	}()
	return operation()
}

func normalizeCredentialLocation(server, workspace string) (string, string, error) {
	normalizedServer, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return "", "", err
	}
	canonicalWorkspace, err := CanonicalWorkspace(workspace)
	if err != nil {
		return "", "", err
	}
	return normalizedServer, canonicalWorkspace, nil
}

func encodeCredentialFile(credential Credential) (credentialFile, error) {
	publicKey, err := protocol.EncodePublicKey(credential.PublicKey)
	if err != nil {
		return credentialFile{}, err
	}
	return credentialFile{
		Version:      credential.Version,
		Server:       credential.Server,
		Workspace:    credential.Workspace,
		CredentialID: credential.CredentialID,
		AccessToken:  credential.AccessToken,
		Fingerprint:  credential.Fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   base64.RawURLEncoding.EncodeToString(credential.PrivateKey),
		CreatedAt:    credential.CreatedAt,
		UpdatedAt:    credential.UpdatedAt,
	}, nil
}

func decodeCredentialFile(stored credentialFile, server, workspace string) (Credential, error) {
	if stored.Version != stateVersion {
		return Credential{}, errors.New("unsupported state version")
	}
	if err := validateStoredCredentialLocation(stored.Server, stored.Workspace, server, workspace); err != nil {
		return Credential{}, err
	}
	credentialID := strings.TrimSpace(stored.CredentialID)
	if credentialID == "" || credentialID != stored.CredentialID {
		return Credential{}, errors.New("credential id is invalid")
	}
	if err := protocol.ValidateRunnerAccessToken(stored.AccessToken); err != nil {
		return Credential{}, err
	}
	publicKey, err := protocol.DecodePublicKey(stored.PublicKey)
	if err != nil {
		return Credential{}, err
	}
	privateKey, err := decodePrivateKey(stored.PrivateKey)
	if err != nil {
		return Credential{}, err
	}
	if err := validateCredentialKeyPair(publicKey, privateKey, stored.Fingerprint); err != nil {
		return Credential{}, err
	}
	return Credential{
		Version:      stored.Version,
		Server:       server,
		Workspace:    workspace,
		CredentialID: credentialID,
		AccessToken:  stored.AccessToken,
		Fingerprint:  stored.Fingerprint,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		CreatedAt:    stored.CreatedAt,
		UpdatedAt:    stored.UpdatedAt,
	}, nil
}

func encodeEnrollmentFile(enrollment PendingEnrollment) (enrollmentFile, error) {
	publicKey, err := protocol.EncodePublicKey(enrollment.PublicKey)
	if err != nil {
		return enrollmentFile{}, err
	}
	return enrollmentFile{
		Version:                 enrollment.Version,
		Server:                  enrollment.Server,
		Workspace:               enrollment.Workspace,
		EnrollmentID:            enrollment.EnrollmentID,
		DeviceCode:              enrollment.DeviceCode,
		UserCode:                enrollment.UserCode,
		VerificationURL:         enrollment.VerificationURL,
		VerificationURLComplete: enrollment.VerificationURLComplete,
		Fingerprint:             enrollment.Fingerprint,
		PublicKey:               publicKey,
		PrivateKey:              base64.RawURLEncoding.EncodeToString(enrollment.PrivateKey),
		ExpiresAt:               enrollment.ExpiresAt,
		PollIntervalMS:          enrollment.PollIntervalMS,
		CreatedAt:               enrollment.CreatedAt,
		UpdatedAt:               enrollment.UpdatedAt,
	}, nil
}

func decodeEnrollmentFile(stored enrollmentFile, server, workspace string) (PendingEnrollment, error) {
	if stored.Version != stateVersion {
		return PendingEnrollment{}, errors.New("unsupported state version")
	}
	if err := validateStoredCredentialLocation(stored.Server, stored.Workspace, server, workspace); err != nil {
		return PendingEnrollment{}, err
	}
	enrollmentID := strings.TrimSpace(stored.EnrollmentID)
	deviceCode := strings.TrimSpace(stored.DeviceCode)
	if enrollmentID == "" || enrollmentID != stored.EnrollmentID || deviceCode == "" || deviceCode != stored.DeviceCode {
		return PendingEnrollment{}, errors.New("enrollment id or device code is invalid")
	}
	if err := protocol.ValidateRunnerAccessToken(deviceCode); err != nil {
		return PendingEnrollment{}, err
	}
	if stored.ExpiresAt.IsZero() {
		return PendingEnrollment{}, errors.New("enrollment expiration is required")
	}
	if stored.PollIntervalMS < 0 {
		return PendingEnrollment{}, errors.New("enrollment poll interval must not be negative")
	}
	publicKey, err := protocol.DecodePublicKey(stored.PublicKey)
	if err != nil {
		return PendingEnrollment{}, err
	}
	privateKey, err := decodePrivateKey(stored.PrivateKey)
	if err != nil {
		return PendingEnrollment{}, err
	}
	if err := validateCredentialKeyPair(publicKey, privateKey, stored.Fingerprint); err != nil {
		return PendingEnrollment{}, err
	}
	return PendingEnrollment{
		Version:                 stored.Version,
		Server:                  server,
		Workspace:               workspace,
		EnrollmentID:            enrollmentID,
		DeviceCode:              deviceCode,
		UserCode:                stored.UserCode,
		VerificationURL:         stored.VerificationURL,
		VerificationURLComplete: stored.VerificationURLComplete,
		Fingerprint:             stored.Fingerprint,
		PublicKey:               publicKey,
		PrivateKey:              privateKey,
		ExpiresAt:               stored.ExpiresAt,
		PollIntervalMS:          stored.PollIntervalMS,
		CreatedAt:               stored.CreatedAt,
		UpdatedAt:               stored.UpdatedAt,
	}, nil
}

func validateStoredCredentialLocation(storedServer, storedWorkspace, server, workspace string) error {
	normalizedServer, err := controlplaneurl.NormalizeBase(storedServer)
	if err != nil || normalizedServer != server {
		return errors.New("stored server does not match credential key")
	}
	canonicalWorkspace, err := CanonicalWorkspace(storedWorkspace)
	if err != nil || canonicalWorkspace != workspace {
		return errors.New("stored workspace does not match credential key")
	}
	return nil
}

func validateCredentialKeyPair(publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, fingerprint string) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return errors.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return errors.Errorf("Ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	derivedPrivateKey := ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])
	if !bytes.Equal(privateKey, derivedPrivateKey) {
		return errors.New("Ed25519 private key does not contain its derived public key")
	}
	derivedPublicKey := derivedPrivateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, derivedPublicKey) {
		return errors.New("Ed25519 public key does not match private key")
	}
	expectedFingerprint, err := protocol.CredentialFingerprint(derivedPublicKey)
	if err != nil {
		return err
	}
	if fingerprint == "" {
		return errors.New("runner credential fingerprint is required")
	}
	if fingerprint != strings.TrimSpace(fingerprint) || fingerprint != expectedFingerprint {
		return errors.New("runner credential fingerprint does not match public key")
	}
	return nil
}

func decodePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	if encoded == "" {
		return nil, errors.New("Ed25519 private key is required")
	}
	privateKey, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode Ed25519 private key")
	}
	if base64.RawURLEncoding.EncodeToString(privateKey) != encoded {
		return nil, errors.New("Ed25519 private key must use canonical unpadded base64url")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.Errorf("Ed25519 private key must decode to %d bytes", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(privateKey), nil
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
	if err := osutil.EnsurePrivateFile(path); err != nil {
		return value, err
	}
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
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return osutil.EnsurePrivateFile(path)
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
	if err := file.Close(); err != nil {
		return err
	}
	return osutil.EnsurePrivateFile(path)
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
