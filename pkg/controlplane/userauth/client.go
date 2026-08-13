package userauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/pkg/errors"
)

const (
	maxHTTPResponseBytes    = 1024 * 1024
	defaultRetryInterval    = time.Second
	defaultUserClientName   = "kodelet"
	maxAPIErrorMessageRunes = 512
)

var bearerTokenPattern = regexp.MustCompile(`kltu_[A-Za-z0-9_-]+`)

// LoginConfig configures one user device-login operation.
type LoginConfig struct {
	Server     string
	Store      *Store
	HTTPClient *http.Client
	// OnPending runs after pending state is securely persisted and before polling.
	OnPending func(LoginInfo)
}

// LoginInfo contains only display-safe metadata for a pending login.
type LoginInfo struct {
	Server                  string
	UserCode                string
	VerificationURL         string
	VerificationURLComplete string
	ExpiresAt               time.Time
	PollInterval            time.Duration
	Resumed                 bool
}

// APIError is a non-successful response returned by a user-auth endpoint.
type APIError struct {
	Operation  string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "user authentication API request failed"
	}
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "request"
	}
	if strings.TrimSpace(e.Message) == "" {
		return fmt.Sprintf("user authentication %s failed with HTTP %d", operation, e.StatusCode)
	}
	return fmt.Sprintf("user authentication %s failed with HTTP %d: %s", operation, e.StatusCode, e.Message)
}

type loginDependencies struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

type loginClient struct {
	server     string
	startURL   string
	pollURL    string
	store      *Store
	httpClient *http.Client
	onPending  func(LoginInfo)
	deps       loginDependencies
}

type authHTTPResponse struct {
	statusCode int
	header     http.Header
	body       []byte
}

// Login starts or resumes one device flow for the canonical server and polls for approval.
func Login(ctx context.Context, config LoginConfig) (Credential, error) {
	return login(ctx, config, loginDependencies{now: time.Now, sleep: sleepWithContext})
}

func login(ctx context.Context, config LoginConfig, deps loginDependencies) (Credential, error) {
	if ctx == nil {
		return Credential{}, errors.New("user login context is required")
	}
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	client, err := newLoginClient(config, deps)
	if err != nil {
		return Credential{}, err
	}
	pending, resumed, err := client.loadOrStart(ctx)
	if err != nil {
		return Credential{}, err
	}
	info, err := loginInfo(pending, resumed)
	if err != nil {
		return Credential{}, err
	}
	if client.onPending != nil {
		client.onPending(info)
	}
	return client.poll(ctx, pending)
}

func newLoginClient(config LoginConfig, deps loginDependencies) (*loginClient, error) {
	server, err := controlplaneurl.NormalizeBase(config.Server)
	if err != nil {
		return nil, err
	}
	store := config.Store
	if store == nil {
		store, err = NewStore()
		if err != nil {
			return nil, err
		}
	}
	startURL, err := endpointFromPath(server, DeviceStartPath)
	if err != nil {
		return nil, err
	}
	pollURL, err := endpointFromPath(server, DevicePollPath)
	if err != nil {
		return nil, err
	}
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.sleep == nil {
		deps.sleep = sleepWithContext
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &loginClient{
		server:     server,
		startURL:   startURL,
		pollURL:    pollURL,
		store:      store,
		httpClient: httpClient,
		onPending:  config.OnPending,
		deps:       deps,
	}, nil
}

func (c *loginClient) loadOrStart(ctx context.Context) (PendingLogin, bool, error) {
	var pending PendingLogin
	var resumed bool
	err := c.store.withStateLock("logins", c.server, func() error {
		stored, found, err := c.store.loadPendingLoginUnlocked(c.server)
		if err != nil {
			return err
		}
		if found && stored.ExpiresAt.After(c.now()) {
			pending = stored
			resumed = true
			return nil
		}
		if found {
			if _, err := c.store.deletePendingLoginUnlocked(c.server, stored.AuthorizationID); err != nil {
				return errors.Wrap(err, "failed to delete expired pending user login")
			}
		}
		started, err := c.start(ctx)
		if err != nil {
			return err
		}
		pending = started
		return nil
	})
	return pending, resumed, err
}

func (c *loginClient) start(ctx context.Context) (PendingLogin, error) {
	request := DeviceStartRequest{
		ClientName:     defaultUserClientName,
		ClientOS:       runtime.GOOS,
		ClientArch:     runtime.GOARCH,
		KodeletVersion: version.Get().Version,
	}
	if err := request.Validate(); err != nil {
		return PendingLogin{}, errors.Wrap(err, "user login start request is invalid")
	}
	response, err := postJSON(ctx, c.httpClient, c.startURL, request, "start")
	if err != nil {
		return PendingLogin{}, err
	}
	if !isHTTPSuccess(response.statusCode) {
		return PendingLogin{}, newAPIError("start", response)
	}
	var started DeviceStartResponse
	if err := decodeStrictJSON(response.body, &started); err != nil {
		return PendingLogin{}, errors.Wrap(err, "failed to decode user login start response")
	}
	if err := started.ValidateAt(c.now()); err != nil {
		return PendingLogin{}, errors.Wrap(err, "control plane returned an invalid user login start response")
	}
	pending, err := preparePendingLogin(PendingLogin{
		Server:                  c.server,
		AuthorizationID:         started.AuthorizationID,
		DeviceCode:              started.DeviceCode,
		UserCode:                started.UserCode,
		VerificationURL:         started.VerificationURL,
		VerificationURLComplete: started.VerificationURLComplete,
		BearerToken:             started.BearerToken,
		ExpiresAt:               started.ExpiresAt,
		PollIntervalMS:          started.PollIntervalMS,
		CreatedAt:               c.now(),
	}, c.now())
	if err != nil {
		return PendingLogin{}, errors.Wrap(err, "failed to prepare pending user login")
	}
	if err := writeJSONAtomic(c.store.pendingLoginPath(c.server), pending); err != nil {
		return PendingLogin{}, errors.Wrap(err, "failed to save pending user login")
	}
	return pending, nil
}

func (c *loginClient) poll(ctx context.Context, pending PendingLogin) (Credential, error) {
	baseInterval, err := durationFromMilliseconds(pending.PollIntervalMS)
	if err != nil {
		return Credential{}, errors.Wrap(err, "pending user login has an invalid poll interval")
	}
	nextInterval := baseInterval
	for {
		deadlineReached, err := c.waitForPoll(ctx, nextInterval, pending.ExpiresAt)
		if err != nil {
			return Credential{}, err
		}
		response, err := postJSON(ctx, c.httpClient, c.pollURL, DevicePollRequest{
			AuthorizationID: pending.AuthorizationID,
			DeviceCode:      pending.DeviceCode,
		}, "poll", pending.DeviceCode, pending.BearerToken)
		if err != nil {
			return Credential{}, err
		}
		if response.statusCode == http.StatusTooManyRequests {
			if deadlineReached {
				return Credential{}, ErrLoginExpired
			}
			nextInterval = max(baseInterval, retryInterval(response, c.now()))
			if nextInterval <= 0 {
				nextInterval = defaultRetryInterval
			}
			continue
		}
		if !isHTTPSuccess(response.statusCode) {
			if response.statusCode == http.StatusNotFound {
				return Credential{}, c.finishTerminal(pending.AuthorizationID, ErrLoginExpired)
			}
			return Credential{}, newAPIError("poll", response, pending.DeviceCode, pending.BearerToken)
		}
		var polled DevicePollResponse
		if err := decodeStrictJSON(response.body, &polled); err != nil {
			return Credential{}, errors.Wrap(err, "failed to decode user login poll response")
		}
		if err := polled.ValidateAt(c.now()); err != nil {
			return Credential{}, errors.Wrap(err, "control plane returned an invalid user login poll response")
		}
		switch polled.Status {
		case DeviceStatusPending:
			if deadlineReached {
				return Credential{}, ErrLoginExpired
			}
			bodyInterval, _ := durationFromMilliseconds(polled.RetryAfterMS)
			nextInterval = max(baseInterval, bodyInterval)
			if headerInterval, ok := parseRetryAfter(response.header.Get("Retry-After"), c.now()); ok {
				nextInterval = max(nextInterval, headerInterval)
			}
		case DeviceStatusApproved:
			credential, err := c.store.saveCredentialAt(Credential{
				Server:       c.server,
				CredentialID: polled.CredentialID,
				BearerToken:  pending.BearerToken,
				Principal:    polled.Principal,
				CreatedAt:    c.now(),
				ExpiresAt:    polled.ExpiresAt,
			}, c.now())
			if err != nil {
				return Credential{}, errors.Wrap(err, "failed to save approved user credential")
			}
			if _, err := c.store.DeletePendingLogin(c.server, pending.AuthorizationID); err != nil {
				return Credential{}, errors.Wrap(err, "failed to delete approved pending user login")
			}
			return credential, nil
		case DeviceStatusDenied:
			return Credential{}, c.finishTerminal(pending.AuthorizationID, ErrLoginDenied)
		case DeviceStatusExpired:
			return Credential{}, c.finishTerminal(pending.AuthorizationID, ErrLoginExpired)
		}
	}
}

func (c *loginClient) finishTerminal(authorizationID string, terminal error) error {
	if _, err := c.store.DeletePendingLogin(c.server, authorizationID); err != nil {
		return errors.Wrap(err, "failed to delete terminal pending user login")
	}
	return terminal
}

func (c *loginClient) waitForPoll(ctx context.Context, interval time.Duration, expiresAt time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := c.now()
	if expiresAt.After(now) {
		interval = min(interval, expiresAt.Sub(now))
	} else {
		interval = 0
	}
	if interval > 0 {
		if err := c.deps.sleep(ctx, interval); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, ctxErr
			}
			return false, errors.Wrap(err, "failed while waiting to poll user login")
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return !expiresAt.After(c.now()), nil
}

func (c *loginClient) now() time.Time {
	return c.deps.now().UTC()
}

func loginInfo(pending PendingLogin, resumed bool) (LoginInfo, error) {
	pollInterval, err := durationFromMilliseconds(pending.PollIntervalMS)
	if err != nil {
		return LoginInfo{}, errors.Wrap(err, "pending user login has an invalid poll interval")
	}
	return LoginInfo{
		Server:                  pending.Server,
		UserCode:                pending.UserCode,
		VerificationURL:         pending.VerificationURL,
		VerificationURLComplete: pending.VerificationURLComplete,
		ExpiresAt:               pending.ExpiresAt,
		PollInterval:            pollInterval,
		Resumed:                 resumed,
	}, nil
}

// ValidateCredential verifies a bearer and returns its current principal snapshot.
func ValidateCredential(ctx context.Context, server, bearer string, client *http.Client) (PrincipalSnapshot, error) {
	if ctx == nil {
		return PrincipalSnapshot{}, errors.New("credential validation context is required")
	}
	if err := ValidateBearerToken(bearer); err != nil {
		return PrincipalSnapshot{}, err
	}
	endpoint, httpClient, err := authenticatedEndpoint(server, MePath, client)
	if err != nil {
		return PrincipalSnapshot{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PrincipalSnapshot{}, errors.Wrap(err, "failed to create credential validation request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := doRequest(httpClient, request, "validate credential", bearer)
	if err != nil {
		return PrincipalSnapshot{}, err
	}
	if !isHTTPSuccess(response.statusCode) {
		return PrincipalSnapshot{}, newAPIError("validate credential", response, bearer)
	}
	var principal PrincipalSnapshot
	if err := decodeStrictJSON(response.body, &principal); err != nil {
		return PrincipalSnapshot{}, errors.Wrap(err, "failed to decode credential validation response")
	}
	if err := principal.Validate(); err != nil {
		return PrincipalSnapshot{}, errors.Wrap(err, "control plane returned an invalid principal")
	}
	return principal, nil
}

// RevokeCredential revokes the bearer credential used for the request.
func RevokeCredential(ctx context.Context, server, bearer string, client *http.Client) error {
	if ctx == nil {
		return errors.New("credential revocation context is required")
	}
	if err := ValidateBearerToken(bearer); err != nil {
		return err
	}
	endpoint, httpClient, err := authenticatedEndpoint(server, CurrentCredentialPath, client)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return errors.Wrap(err, "failed to create credential revocation request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := doRequest(httpClient, request, "revoke credential", bearer)
	if err != nil {
		return err
	}
	if !isHTTPSuccess(response.statusCode) {
		return newAPIError("revoke credential", response, bearer)
	}
	return nil
}

func authenticatedEndpoint(server, path string, client *http.Client) (string, *http.Client, error) {
	normalized, err := controlplaneurl.NormalizeBase(server)
	if err != nil {
		return "", nil, err
	}
	endpoint, err := endpointFromPath(normalized, path)
	if err != nil {
		return "", nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return endpoint, client, nil
}

func endpointFromPath(server, path string) (string, error) {
	return controlplaneurl.Endpoint(server, strings.Split(strings.Trim(path, "/"), "/")...)
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, value any, operation string, secrets ...string) (authHTTPResponse, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return authHTTPResponse{}, errors.Wrapf(err, "failed to encode user authentication %s request", operation)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return authHTTPResponse{}, errors.Wrapf(err, "failed to create user authentication %s request", operation)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	return doRequest(client, request, operation, secrets...)
}

func doRequest(client *http.Client, request *http.Request, operation string, secrets ...string) (authHTTPResponse, error) {
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return authHTTPResponse{}, ctxErr
		}
		message := redactSecrets(err.Error(), secrets...)
		return authHTTPResponse{}, errors.Errorf("failed to send user authentication %s request: %s", operation, message)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
	if err != nil {
		message := redactSecrets(err.Error(), secrets...)
		return authHTTPResponse{}, errors.Errorf("failed to read user authentication %s response: %s", operation, message)
	}
	if len(body) > maxHTTPResponseBytes {
		return authHTTPResponse{}, errors.Errorf("user authentication %s response exceeds %d bytes", operation, maxHTTPResponseBytes)
	}
	return authHTTPResponse{statusCode: response.StatusCode, header: response.Header.Clone(), body: body}, nil
}

func retryInterval(response authHTTPResponse, now time.Time) time.Duration {
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
		const maxDuration = time.Duration(1<<63 - 1)
		if seconds < 0 || seconds > int64(maxDuration/time.Second) {
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

func isHTTPSuccess(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func newAPIError(operation string, response authHTTPResponse, secrets ...string) error {
	message := apiErrorMessage(response.body)
	message = compactErrorMessage(redactSecrets(message, secrets...))
	if message == "" {
		message = http.StatusText(response.statusCode)
	}
	return &APIError{Operation: operation, StatusCode: response.statusCode, Message: message}
}

func apiErrorMessage(body []byte) string {
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

func compactErrorMessage(message string) string {
	message = strings.ToValidUTF8(message, "")
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > maxAPIErrorMessageRunes {
		message = string(runes[:maxAPIErrorMessageRunes]) + "…"
	}
	return message
}

func redactSecrets(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return bearerTokenPattern.ReplaceAllString(message, "[REDACTED]")
}
