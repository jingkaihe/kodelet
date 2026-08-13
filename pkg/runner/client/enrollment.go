package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

const (
	maxEnrollmentResponseBytes = 1024 * 1024
	defaultSlowDownInterval    = time.Second
	maxTimeDuration            = time.Duration(1<<63 - 1)
)

var (
	// ErrActiveLocalCredential indicates that a new enrollment was refused because the workspace already has a credential and replacement was not requested.
	ErrActiveLocalCredential = errors.New("runner already has an active local credential")
	// ErrEnrollmentDenied indicates that the control plane denied the pending enrollment.
	ErrEnrollmentDenied = errors.New("runner enrollment was denied")
	// ErrEnrollmentExpired indicates that the pending enrollment expired before approval.
	ErrEnrollmentExpired = errors.New("runner enrollment expired")
)

// EnrollmentConfig configures one runner device-enrollment operation.
type EnrollmentConfig struct {
	Server                 string
	Workspace              string
	DisplayName            string
	Store                  *localstate.Store
	HTTPClient             *http.Client
	ReplaceLocalCredential bool
	// OnPending is called after pending state is securely persisted and before polling begins.
	// It is also called when an unexpired enrollment is resumed from local state.
	OnPending func(EnrollmentInfo)
}

// EnrollmentInfo is the non-secret enrollment metadata suitable for display to a user.
type EnrollmentInfo struct {
	Server                  string
	Workspace               string
	UserCode                string
	VerificationURL         string
	VerificationURLComplete string
	Fingerprint             string
	ExpiresAt               time.Time
	PollInterval            time.Duration
	Resumed                 bool
}

// EnrollmentResult identifies the approved credential and runner registration saved locally.
type EnrollmentResult struct {
	Info         EnrollmentInfo
	CredentialID string
	RunnerID     string
	Fingerprint  string
}

// EnrollmentAPIError is a non-successful response returned by a runner enrollment endpoint.
type EnrollmentAPIError struct {
	Operation  string
	StatusCode int
	Message    string
}

func (e *EnrollmentAPIError) Error() string {
	if e == nil {
		return "runner enrollment API request failed"
	}
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "request"
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("runner enrollment %s failed with HTTP %d", operation, e.StatusCode)
	}
	return fmt.Sprintf("runner enrollment %s failed with HTTP %d: %s", operation, e.StatusCode, e.Message)
}

type enrollmentDependencies struct {
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	random         io.Reader
	hostname       func() (string, error)
	kodeletVersion func() string
}

type enrollmentClient struct {
	server       string
	workspace    string
	displayName  string
	startURL     string
	pollURL      string
	store        *localstate.Store
	httpClient   *http.Client
	onPending    func(EnrollmentInfo)
	replaceLocal bool
	deps         enrollmentDependencies
}

type enrollmentHTTPResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

// EnrollRunner starts or resumes device enrollment and polls until it reaches a terminal state.
func EnrollRunner(ctx context.Context, config EnrollmentConfig) (EnrollmentResult, error) {
	return enrollRunner(ctx, config, defaultEnrollmentDependencies())
}

func defaultEnrollmentDependencies() enrollmentDependencies {
	return enrollmentDependencies{
		now:      time.Now,
		sleep:    sleepWithContext,
		random:   rand.Reader,
		hostname: os.Hostname,
		kodeletVersion: func() string {
			return version.Get().Version
		},
	}
}

func enrollRunner(ctx context.Context, config EnrollmentConfig, deps enrollmentDependencies) (EnrollmentResult, error) {
	if ctx == nil {
		return EnrollmentResult{}, errors.New("runner enrollment context is required")
	}
	if err := ctx.Err(); err != nil {
		return EnrollmentResult{}, err
	}
	client, err := newEnrollmentClient(config, deps)
	if err != nil {
		return EnrollmentResult{}, err
	}

	pending, found, err := client.store.LoadPendingEnrollment(client.server, client.workspace)
	if err != nil {
		return EnrollmentResult{}, err
	}

	if !found {
		_, active, err := client.store.LoadCredential(client.server, client.workspace)
		if err != nil && !client.replaceLocal {
			return EnrollmentResult{}, err
		}
		if active && !client.replaceLocal {
			return EnrollmentResult{}, errors.Wrap(ErrActiveLocalCredential, "refusing to start runner enrollment without replacement opt-in")
		}
		pending, err = client.start(ctx)
		if err != nil {
			return EnrollmentResult{}, err
		}
	}

	info, err := client.enrollmentInfo(pending, found)
	if err != nil {
		return EnrollmentResult{}, err
	}
	if client.onPending != nil {
		client.onPending(info)
	}

	result := EnrollmentResult{Info: info}
	approved, err := client.poll(ctx, pending)
	if err != nil {
		return result, err
	}
	result.CredentialID = approved.CredentialID
	result.RunnerID = approved.RunnerID
	result.Fingerprint = approved.Fingerprint
	return result, nil
}

func newEnrollmentClient(config EnrollmentConfig, deps enrollmentDependencies) (*enrollmentClient, error) {
	server, err := controlplaneurl.NormalizeBase(config.Server)
	if err != nil {
		return nil, err
	}
	workspace, err := localstate.CanonicalWorkspace(config.Workspace)
	if err != nil {
		return nil, err
	}
	store := config.Store
	if store == nil {
		store, err = localstate.NewStore()
		if err != nil {
			return nil, err
		}
	}
	startURL, err := endpointFromProtocolPath(server, protocol.EnrollmentStartPath)
	if err != nil {
		return nil, err
	}
	pollURL, err := endpointFromProtocolPath(server, protocol.EnrollmentPollPath)
	if err != nil {
		return nil, err
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.sleep == nil {
		deps.sleep = sleepWithContext
	}
	if deps.random == nil {
		deps.random = rand.Reader
	}
	if deps.hostname == nil {
		deps.hostname = os.Hostname
	}
	if deps.kodeletVersion == nil {
		deps.kodeletVersion = func() string { return version.Get().Version }
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &enrollmentClient{
		server:       server,
		workspace:    workspace,
		displayName:  strings.TrimSpace(config.DisplayName),
		startURL:     startURL,
		pollURL:      pollURL,
		store:        store,
		httpClient:   httpClient,
		onPending:    config.OnPending,
		replaceLocal: config.ReplaceLocalCredential,
		deps:         deps,
	}, nil
}

func (c *enrollmentClient) start(ctx context.Context) (localstate.PendingEnrollment, error) {
	identity, err := c.store.LoadOrCreateHostIdentity()
	if err != nil {
		return localstate.PendingEnrollment{}, err
	}
	hostname, err := c.deps.hostname()
	if err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "failed to determine runner hostname")
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return localstate.PendingEnrollment{}, errors.New("runner hostname is required")
	}
	kodeletVersion := strings.TrimSpace(c.deps.kodeletVersion())
	if kodeletVersion == "" {
		return localstate.PendingEnrollment{}, errors.New("Kodelet version is required for runner enrollment")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(c.deps.random)
	if err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "failed to generate runner enrollment key")
	}
	encodedPublicKey, err := protocol.EncodePublicKey(publicKey)
	if err != nil {
		return localstate.PendingEnrollment{}, err
	}
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	if err != nil {
		return localstate.PendingEnrollment{}, err
	}
	request := protocol.EnrollmentStartRequest{
		ProtocolVersions: []int{protocol.Version},
		PublicKey:        encodedPublicKey,
		Fingerprint:      fingerprint,
		Host: protocol.Host{
			InstanceID: identity.InstanceID,
			Hostname:   hostname,
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
		},
		Workspace: protocol.Workspace{
			Path: c.workspace,
			Name: filepath.Base(c.workspace),
		},
		DisplayName:    c.displayName,
		KodeletVersion: kodeletVersion,
	}
	if err := request.Validate(); err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "runner enrollment request is invalid")
	}

	response, err := c.postJSON(ctx, c.startURL, request, "start")
	if err != nil {
		return localstate.PendingEnrollment{}, err
	}
	privateKeySecret := base64.RawURLEncoding.EncodeToString(privateKey)
	if !isHTTPSuccess(response.statusCode) {
		return localstate.PendingEnrollment{}, newEnrollmentAPIError("start", response, privateKeySecret)
	}
	var started protocol.EnrollmentStartResponse
	if err := json.Unmarshal(response.body, &started); err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "failed to decode runner enrollment start response")
	}
	if err := validateEnrollmentStartResponse(started, c.now()); err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "control plane returned an invalid runner enrollment")
	}

	now := c.now()
	pending := localstate.PendingEnrollment{
		Server:                  c.server,
		Workspace:               c.workspace,
		EnrollmentID:            started.EnrollmentID,
		DeviceCode:              started.DeviceCode,
		UserCode:                started.UserCode,
		VerificationURL:         started.VerificationURL,
		VerificationURLComplete: started.VerificationURLComplete,
		Fingerprint:             fingerprint,
		PublicKey:               publicKey,
		PrivateKey:              privateKey,
		ExpiresAt:               started.ExpiresAt,
		PollIntervalMS:          started.PollIntervalMS,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := c.store.SavePendingEnrollment(pending); err != nil {
		return localstate.PendingEnrollment{}, errors.Wrap(err, "failed to save pending runner enrollment")
	}
	return pending, nil
}

func (c *enrollmentClient) poll(ctx context.Context, pending localstate.PendingEnrollment) (protocol.EnrollmentPollResponse, error) {
	baseInterval, err := durationFromMilliseconds(pending.PollIntervalMS)
	if err != nil {
		return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "pending runner enrollment has an invalid poll interval")
	}
	nextInterval := baseInterval
	privateKeySecret := base64.RawURLEncoding.EncodeToString(pending.PrivateKey)

	for {
		deadlineReached, err := c.waitForPoll(ctx, nextInterval, pending.ExpiresAt)
		if err != nil {
			return protocol.EnrollmentPollResponse{}, err
		}

		response, err := c.postJSON(ctx, c.pollURL, protocol.EnrollmentPollRequest{
			EnrollmentID: pending.EnrollmentID,
			DeviceCode:   pending.DeviceCode,
		}, "poll")
		if err != nil {
			return protocol.EnrollmentPollResponse{}, err
		}
		if response.statusCode == http.StatusTooManyRequests {
			if deadlineReached {
				return protocol.EnrollmentPollResponse{}, ErrEnrollmentExpired
			}
			nextInterval = max(baseInterval, slowDownInterval(response, c.now()))
			if nextInterval <= 0 {
				nextInterval = defaultSlowDownInterval
			}
			continue
		}
		if !isHTTPSuccess(response.statusCode) {
			if response.statusCode == http.StatusNotFound {
				return protocol.EnrollmentPollResponse{}, c.finishTerminalEnrollment(ErrEnrollmentExpired)
			}
			return protocol.EnrollmentPollResponse{}, newEnrollmentAPIError("poll", response, pending.DeviceCode, privateKeySecret)
		}

		var polled protocol.EnrollmentPollResponse
		if err := json.Unmarshal(response.body, &polled); err != nil {
			return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "failed to decode runner enrollment poll response")
		}
		switch polled.Status {
		case protocol.EnrollmentStatusPending:
			if deadlineReached {
				return protocol.EnrollmentPollResponse{}, ErrEnrollmentExpired
			}
			retryInterval, err := durationFromMilliseconds(polled.RetryAfterMS)
			if err != nil {
				return protocol.EnrollmentPollResponse{}, errors.Wrap(err, "control plane returned an invalid runner enrollment retry interval")
			}
			nextInterval = max(baseInterval, retryInterval)
		case protocol.EnrollmentStatusApproved:
			if err := c.finishApprovedEnrollment(pending, polled); err != nil {
				return protocol.EnrollmentPollResponse{}, err
			}
			return polled, nil
		case protocol.EnrollmentStatusDenied:
			return protocol.EnrollmentPollResponse{}, c.finishTerminalEnrollment(ErrEnrollmentDenied)
		case protocol.EnrollmentStatusExpired:
			return protocol.EnrollmentPollResponse{}, c.finishTerminalEnrollment(ErrEnrollmentExpired)
		default:
			return protocol.EnrollmentPollResponse{}, errors.Errorf("control plane returned unknown runner enrollment status %q", polled.Status)
		}
	}
}

func (c *enrollmentClient) finishApprovedEnrollment(pending localstate.PendingEnrollment, response protocol.EnrollmentPollResponse) error {
	credentialID, err := validateOpaqueID("credential id", response.CredentialID)
	if err != nil {
		return errors.Wrap(err, "control plane returned an invalid approved runner enrollment")
	}
	runnerID, err := validateOpaqueID("runner id", response.RunnerID)
	if err != nil {
		return errors.Wrap(err, "control plane returned an invalid approved runner enrollment")
	}
	expectedFingerprint, err := protocol.CredentialFingerprint(pending.PublicKey)
	if err != nil {
		return errors.Wrap(err, "failed to verify approved runner enrollment key")
	}
	if response.Fingerprint != strings.TrimSpace(response.Fingerprint) || response.Fingerprint != expectedFingerprint {
		return errors.New("control plane returned an approved runner fingerprint that does not match the generated key")
	}
	if response.TokenType != protocol.DPoPAuthorizationScheme {
		return errors.New("control plane returned an approved runner credential with an unsupported token type")
	}
	if err := protocol.ValidateRunnerAccessToken(response.AccessToken); err != nil {
		return errors.Wrap(err, "control plane returned an invalid approved runner access token")
	}
	if response.AccessToken != pending.DeviceCode {
		return errors.New("control plane returned an approved runner access token that does not match the enrollment secret")
	}
	now := c.now()
	if err := c.store.SaveCredential(localstate.Credential{
		Server:       c.server,
		Workspace:    c.workspace,
		CredentialID: credentialID,
		AccessToken:  response.AccessToken,
		Fingerprint:  expectedFingerprint,
		PublicKey:    pending.PublicKey,
		PrivateKey:   pending.PrivateKey,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return errors.Wrap(err, "failed to save approved runner credential")
	}
	if err := c.store.SaveRegistration(localstate.Registration{
		Server:      c.server,
		Workspace:   c.workspace,
		RunnerID:    runnerID,
		DisplayName: c.displayName,
		UpdatedAt:   now,
	}); err != nil {
		return errors.Wrap(err, "failed to save approved runner registration")
	}
	if err := c.deletePendingEnrollment(); err != nil {
		return errors.Wrap(err, "failed to delete approved pending runner enrollment")
	}
	return nil
}

func (c *enrollmentClient) finishTerminalEnrollment(terminal error) error {
	if err := c.deletePendingEnrollment(); err != nil {
		return errors.Wrap(err, "failed to delete terminal pending runner enrollment")
	}
	return terminal
}

func (c *enrollmentClient) deletePendingEnrollment() error {
	_, err := c.store.DeletePendingEnrollment(c.server, c.workspace)
	return err
}

func (c *enrollmentClient) enrollmentInfo(pending localstate.PendingEnrollment, resumed bool) (EnrollmentInfo, error) {
	pollInterval, err := durationFromMilliseconds(pending.PollIntervalMS)
	if err != nil {
		return EnrollmentInfo{}, errors.Wrap(err, "pending runner enrollment has an invalid poll interval")
	}
	return EnrollmentInfo{
		Server:                  c.server,
		Workspace:               c.workspace,
		UserCode:                pending.UserCode,
		VerificationURL:         pending.VerificationURL,
		VerificationURLComplete: pending.VerificationURLComplete,
		Fingerprint:             pending.Fingerprint,
		ExpiresAt:               pending.ExpiresAt,
		PollInterval:            pollInterval,
		Resumed:                 resumed,
	}, nil
}

func (c *enrollmentClient) waitForPoll(ctx context.Context, interval time.Duration, expiresAt time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := c.now()
	if expiresAt.After(now) {
		remaining := expiresAt.Sub(now)
		if interval > remaining {
			interval = remaining
		}
	} else {
		interval = 0
	}
	if interval > 0 {
		if err := c.deps.sleep(ctx, interval); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, ctxErr
			}
			return false, errors.Wrap(err, "failed while waiting to poll runner enrollment")
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return !expiresAt.After(c.now()), nil
}

func (c *enrollmentClient) postJSON(ctx context.Context, endpoint string, value any, operation string) (enrollmentHTTPResponse, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return enrollmentHTTPResponse{}, errors.Wrapf(err, "failed to encode runner enrollment %s request", operation)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return enrollmentHTTPResponse{}, errors.Wrapf(err, "failed to create runner enrollment %s request", operation)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return enrollmentHTTPResponse{}, ctxErr
		}
		return enrollmentHTTPResponse{}, errors.Wrapf(err, "failed to send runner enrollment %s request", operation)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxEnrollmentResponseBytes+1))
	if err != nil {
		return enrollmentHTTPResponse{}, errors.Wrapf(err, "failed to read runner enrollment %s response", operation)
	}
	if len(body) > maxEnrollmentResponseBytes {
		return enrollmentHTTPResponse{}, errors.Errorf("runner enrollment %s response exceeds %d bytes", operation, maxEnrollmentResponseBytes)
	}
	return enrollmentHTTPResponse{
		statusCode: response.StatusCode,
		header:     response.Header.Clone(),
		body:       body,
	}, nil
}

func (c *enrollmentClient) now() time.Time {
	return c.deps.now().UTC()
}

func endpointFromProtocolPath(server, path string) (string, error) {
	return controlplaneurl.Endpoint(server, strings.Split(strings.Trim(path, "/"), "/")...)
}

func validateEnrollmentStartResponse(response protocol.EnrollmentStartResponse, now time.Time) error {
	if _, err := validateOpaqueID("enrollment id", response.EnrollmentID); err != nil {
		return err
	}
	if _, err := validateOpaqueID("device code", response.DeviceCode); err != nil {
		return err
	}
	if _, err := validateOpaqueID("user code", response.UserCode); err != nil {
		return err
	}
	if err := validateEnrollmentURL("verification URL", response.VerificationURL, true); err != nil {
		return err
	}
	if err := validateEnrollmentURL("complete verification URL", response.VerificationURLComplete, false); err != nil {
		return err
	}
	if response.ExpiresAt.IsZero() {
		return errors.New("enrollment expiration is required")
	}
	if !response.ExpiresAt.After(now) {
		return ErrEnrollmentExpired
	}
	_, err := durationFromMilliseconds(response.PollIntervalMS)
	return err
}

func validateOpaqueID(name, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.Errorf("%s is required", name)
	}
	if trimmed != value {
		return "", errors.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	return value, nil
}

func validateEnrollmentURL(name, value string, required bool) error {
	if strings.TrimSpace(value) == "" {
		if required {
			return errors.Errorf("%s is required", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value {
		return errors.Errorf("%s must not contain leading or trailing whitespace", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.Errorf("%s must be an absolute http or https URL", name)
	}
	return nil
}

func durationFromMilliseconds(milliseconds int64) (time.Duration, error) {
	if milliseconds < 0 {
		return 0, errors.New("poll interval must not be negative")
	}
	if milliseconds > int64(maxTimeDuration/time.Millisecond) {
		return 0, errors.New("poll interval is too large")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func slowDownInterval(response enrollmentHTTPResponse, now time.Time) time.Duration {
	var interval time.Duration
	if headerInterval, ok := parseRetryAfter(response.header.Get("Retry-After"), now); ok {
		interval = headerInterval
	}
	var payload struct {
		RetryAfterMS int64 `json:"retryAfterMs"`
	}
	if json.Unmarshal(response.body, &payload) == nil {
		if bodyInterval, err := durationFromMilliseconds(payload.RetryAfterMS); err == nil {
			interval = max(interval, bodyInterval)
		}
	}
	return interval
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64(maxTimeDuration/time.Second) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	duration := retryAt.Sub(now)
	if duration < 0 {
		duration = 0
	}
	return duration, true
}

func isHTTPSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func newEnrollmentAPIError(operation string, response enrollmentHTTPResponse, secrets ...string) error {
	message := enrollmentAPIErrorMessage(response.body)
	message = redactEnrollmentSecrets(message, secrets...)
	message = compactEnrollmentErrorMessage(message)
	if message == "" {
		message = http.StatusText(response.statusCode)
	}
	return &EnrollmentAPIError{
		Operation:  operation,
		StatusCode: response.statusCode,
		Message:    message,
	}
}

func enrollmentAPIErrorMessage(body []byte) string {
	var envelope struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		var message string
		if len(envelope.Error) > 0 && json.Unmarshal(envelope.Error, &message) == nil && strings.TrimSpace(message) != "" {
			return message
		}
		var nested struct {
			Message string `json:"message"`
		}
		if len(envelope.Error) > 0 && json.Unmarshal(envelope.Error, &nested) == nil && strings.TrimSpace(nested.Message) != "" {
			return nested.Message
		}
		if strings.TrimSpace(envelope.Message) != "" {
			return envelope.Message
		}
	}
	return string(body)
}

func compactEnrollmentErrorMessage(message string) string {
	message = strings.ToValidUTF8(message, "")
	message = strings.Join(strings.Fields(message), " ")
	const maxRunes = 512
	runes := []rune(message)
	if len(runes) > maxRunes {
		message = string(runes[:maxRunes]) + "…"
	}
	return message
}

func redactEnrollmentSecrets(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	return message
}
