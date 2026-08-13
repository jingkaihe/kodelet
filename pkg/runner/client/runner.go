package client

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/jingkaihe/kodelet/pkg/version"
	pkgerrors "github.com/pkg/errors"
)

const (
	defaultReconnectMin         = time.Second
	defaultReconnectMax         = 30 * time.Second
	defaultManifestInterval     = 30 * time.Second
	defaultManifestProbeTimeout = 10 * time.Second
	connectionShutdownPeriod    = 5 * time.Second
)

// RunnerConfig configures one long-running workspace-bound runner process.
type RunnerConfig struct {
	Server               string
	AuthToken            string
	Workspace            string
	DisplayName          string
	ServiceOptions       ServiceOptions
	Store                *localstate.Store
	ReconnectMin         time.Duration
	ReconnectMax         time.Duration
	ManifestInterval     time.Duration
	ManifestProbeTimeout time.Duration
	OnRegistered         func(protocol.RegisterResult)
	OnRetry              func(error, time.Duration)
}

// Runner maintains one workspace lock, stable identity, and reconnecting control connection.
type Runner struct {
	config       RunnerConfig
	store        *localstate.Store
	workspace    string
	server       string
	websocketURL string
	credential   *localstate.Credential
	host         protocol.Host
	lockMu       sync.Mutex
	lock         *localstate.WorkspaceLock
	service      *Service
}

// NewRunner validates configuration and acquires no external resources.
func NewRunner(ctx context.Context, config RunnerConfig) (*Runner, error) {
	server, websocketURL, err := normalizeServerURL(config.Server)
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
	var credential *localstate.Credential
	if strings.TrimSpace(config.AuthToken) == "" {
		storedCredential, found, err := store.LoadCredential(server, workspace)
		if err != nil {
			return nil, pkgerrors.Wrap(err, "failed to load enrolled runner credential; re-enroll this runner with `kodelet runner enroll --replace`")
		}
		if found {
			credential = &storedCredential
		}
	}
	identity, err := store.LoadOrCreateHostIdentity()
	if err != nil {
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return nil, pkgerrors.Wrap(err, "failed to determine runner hostname")
	}
	if config.ReconnectMin <= 0 {
		config.ReconnectMin = defaultReconnectMin
	}
	if config.ReconnectMax <= 0 {
		config.ReconnectMax = defaultReconnectMax
	}
	if config.ReconnectMax < config.ReconnectMin {
		return nil, pkgerrors.New("runner reconnect maximum must not be shorter than its minimum")
	}
	if config.ManifestInterval <= 0 {
		config.ManifestInterval = defaultManifestInterval
	}
	if config.ManifestProbeTimeout <= 0 {
		config.ManifestProbeTimeout = defaultManifestProbeTimeout
	}
	config.Server = server
	config.Workspace = workspace
	config.DisplayName = strings.TrimSpace(config.DisplayName)
	service, err := NewService(ctx, workspace, config.ServiceOptions)
	if err != nil {
		return nil, err
	}
	return &Runner{
		config:       config,
		store:        store,
		workspace:    workspace,
		server:       server,
		websocketURL: websocketURL,
		credential:   credential,
		host: protocol.Host{
			InstanceID: identity.InstanceID,
			Hostname:   hostname,
			OS:         runtime.GOOS,
			Arch:       runtime.GOARCH,
			PID:        os.Getpid(),
		},
		service: service,
	}, nil
}

// Commands discovers workspace and extension slash commands for an optional environment profile.
func (r *Runner) Commands(ctx context.Context, environmentProfile string) ([]slashcommands.Command, error) {
	if r == nil || r.service == nil {
		return nil, pkgerrors.New("runner is not initialized")
	}
	manifest, err := r.service.ProbeManifest(ctx, environmentProfile)
	if err != nil {
		return nil, err
	}
	return slices.Clone(manifest.Commands), nil
}

// AcquireWorkspaceLock claims this host workspace before it is registered.
func (r *Runner) AcquireWorkspaceLock() error {
	if r == nil || r.service == nil {
		return pkgerrors.New("runner is not initialized")
	}
	r.lockMu.Lock()
	defer r.lockMu.Unlock()
	if r.lock != nil {
		return nil
	}
	lock, err := r.store.AcquireWorkspaceLock(r.workspace, localstate.LockMetadata{
		PID:         r.host.PID,
		Hostname:    r.host.Hostname,
		Workspace:   r.workspace,
		Server:      r.server,
		DisplayName: r.config.DisplayName,
		StartedAt:   time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	r.lock = lock
	return nil
}

// Run acquires the workspace lock and keeps the runner connected until cancellation.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.AcquireWorkspaceLock(); err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			logger.G(ctx).WithError(err).Warn("failed to close runner")
		}
	}()

	// The first snapshot may need to cold-start extensions. It is bounded by the
	// runner lifetime rather than the short periodic-refresh timeout.
	initialDigest, err := r.service.ProbeManifestDigest(ctx)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to discover initial runner manifest")
	}

	backoff := r.config.ReconnectMin
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		connected, connectionErr := r.runConnection(ctx, initialDigest)
		err = connectionErr
		if ctx.Err() != nil {
			return nil
		}
		if connected {
			backoff = r.config.ReconnectMin
		}
		if isPermanentConnectionError(err) {
			return err
		}
		if r.config.OnRetry != nil {
			r.config.OnRetry(err, backoff)
		} else {
			logger.G(ctx).WithError(err).WithField("retry_in", backoff).Warn("runner connection lost; retrying")
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = min(backoff*2, r.config.ReconnectMax)
		if digest, probeErr := r.probeManifestDigest(ctx); probeErr == nil {
			initialDigest = digest
		}
	}
}

// Close releases runner-owned local resources. It must not race with Run.
func (r *Runner) Close() error {
	if r == nil {
		return nil
	}
	r.lockMu.Lock()
	lock := r.lock
	r.lock = nil
	r.lockMu.Unlock()
	serviceErr := r.service.Close()
	lockErr := lock.Close()
	if serviceErr != nil {
		return serviceErr
	}
	return lockErr
}

func (r *Runner) runConnection(ctx context.Context, initialDigest string) (bool, error) {
	headers, keyAuthenticated, err := r.connectionHeaders()
	if err != nil {
		return false, err
	}
	dialer := websocket.Dialer{Subprotocols: []string{protocol.Subprotocol}}
	conn, response, err := dialer.DialContext(ctx, r.websocketURL, headers)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			if keyAuthenticated {
				return false, &permanentConnectionError{err: pkgerrors.Errorf("runner credential authentication failed with HTTP %d; the credential may be unknown or revoked, so re-enroll this runner", response.StatusCode)}
			}
			return false, &permanentConnectionError{err: pkgerrors.Errorf("runner authentication failed with HTTP %d", response.StatusCode)}
		}
		return false, pkgerrors.Wrap(err, "failed to connect runner websocket")
	}
	if conn.Subprotocol() != protocol.Subprotocol {
		_ = conn.Close()
		return false, &permanentConnectionError{err: pkgerrors.New("control plane did not negotiate the runner websocket subprotocol")}
	}

	peer, err := protocol.NewPeer(conn, protocol.PeerConfig{
		RequestPrefix: "runner",
		Handler:       r.service,
		Notifications: r.service,
	})
	if err != nil {
		_ = conn.Close()
		return false, err
	}
	r.service.Attach(peer)
	if err := peer.Start(ctx); err != nil {
		_ = peer.Close()
		return false, err
	}
	defer func() {
		_ = peer.Close()
		if abortErr := r.service.AbortActiveRun(context.Background()); abortErr != nil {
			logger.G(ctx).WithError(abortErr).Warn("failed to abort runner environment after disconnect")
		}
	}()

	cached, found, err := r.store.LoadRegistration(r.server, r.workspace)
	if err != nil {
		return false, err
	}
	params := protocol.RegisterParams{
		ProtocolVersions: []int{protocol.Version},
		Capabilities: protocol.RunnerCapabilities{
			ConcurrentRuns: true,
		},
		DisplayName: r.config.DisplayName,
		Host:        r.host,
		Workspace: protocol.Workspace{
			Path: r.workspace,
			Name: filepath.Base(r.workspace),
		},
		KodeletVersion: version.Get().Version,
		ManifestDigest: initialDigest,
	}
	if found {
		params.RunnerID = cached.RunnerID
	}
	registration, err := registerRunner(ctx, peer, params, !keyAuthenticated)
	if err != nil {
		return false, runnerRegistrationError(err, strings.TrimSpace(r.config.AuthToken) != "")
	}
	if err := r.service.SetRegistration(registration); err != nil {
		return false, err
	}
	connected := true
	if err := r.store.SaveRegistration(localstate.Registration{
		Server:      r.server,
		Workspace:   r.workspace,
		RunnerID:    registration.RunnerID,
		DisplayName: r.config.DisplayName,
	}); err != nil {
		return connected, err
	}
	metadata := r.lock.Metadata()
	metadata.RunnerID = registration.RunnerID
	if err := r.lock.WriteMetadata(metadata); err != nil {
		return connected, err
	}
	if r.config.OnRegistered != nil {
		r.config.OnRegistered(registration)
	}

	heartbeatInterval := time.Duration(registration.HeartbeatIntervalMS) * time.Millisecond
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	if err := r.sendHeartbeat(ctx, peer, registration); err != nil {
		return connected, err
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	manifestTicker := time.NewTicker(r.config.ManifestInterval)
	defer heartbeatTicker.Stop()
	defer manifestTicker.Stop()
	lastAdvertisedDigest := initialDigest
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	type manifestProbeResult struct {
		digest string
		err    error
	}
	probeResults := make(chan manifestProbeResult, 1)
	probeInFlight := false

	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), connectionShutdownPeriod)
			_ = peer.Notify(shutdownCtx, protocol.MethodRunnerGoodbye, protocol.GoodbyeParams{
				RunnerID:   registration.RunnerID,
				Generation: registration.Generation,
				Reason:     "runner process stopped",
			})
			_ = peer.Shutdown(shutdownCtx, websocket.CloseNormalClosure, "runner process stopped")
			cancel()
			return connected, nil
		case <-peer.TransportDone():
			return connected, peer.Err()
		case <-heartbeatTicker.C:
			if err := r.sendHeartbeat(ctx, peer, registration); err != nil {
				return connected, err
			}
		case <-manifestTicker.C:
			if probeInFlight {
				continue
			}
			probeInFlight = true
			go func() {
				digest, probeErr := r.probeManifestDigest(connectionCtx)
				select {
				case probeResults <- manifestProbeResult{digest: digest, err: probeErr}:
				case <-connectionCtx.Done():
				}
			}()
		case result := <-probeResults:
			probeInFlight = false
			if result.err != nil {
				logger.G(ctx).WithError(result.err).Warn("failed to refresh runner manifest digest")
				continue
			}
			if result.digest != lastAdvertisedDigest {
				if err := peer.Notify(ctx, protocol.MethodRunnerManifestChanged, protocol.ManifestChangedParams{
					RunnerID:       registration.RunnerID,
					Generation:     registration.Generation,
					ManifestDigest: result.digest,
				}); err != nil {
					return connected, err
				}
				lastAdvertisedDigest = result.digest
			}
		}
	}
}

func (r *Runner) connectionHeaders() (http.Header, bool, error) {
	headers := http.Header{}
	if token := strings.TrimSpace(r.config.AuthToken); token != "" {
		headers.Set("Authorization", "Bearer "+token)
		return headers, false, nil
	}
	if r.credential == nil {
		return headers, false, nil
	}

	proof, err := protocol.SignDPoPProof(r.credential.PrivateKey, protocol.DPoPProofOptions{
		Method:      http.MethodGet,
		TargetURL:   r.websocketURL,
		AccessToken: r.credential.AccessToken,
	})
	if err != nil {
		return nil, true, &permanentConnectionError{err: pkgerrors.Wrap(err, "stored runner credential cannot sign a DPoP proof; re-enroll this runner")}
	}
	headers.Set("Authorization", protocol.DPoPAuthorizationScheme+" "+r.credential.AccessToken)
	headers.Set(protocol.DPoPHeader, proof)
	return headers, true, nil
}

func (r *Runner) probeManifestDigest(ctx context.Context) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, r.config.ManifestProbeTimeout)
	defer cancel()
	return r.service.ProbeManifestDigest(probeCtx)
}

func (r *Runner) sendHeartbeat(ctx context.Context, peer *protocol.Peer, registration protocol.RegisterResult) error {
	state, activeRunIDs, manifestDigest := r.service.HeartbeatSnapshotRuns()
	activeRunID := ""
	if len(activeRunIDs) == 1 {
		activeRunID = activeRunIDs[0]
	}
	return peer.Notify(ctx, protocol.MethodRunnerHeartbeat, protocol.HeartbeatParams{
		RunnerID:       registration.RunnerID,
		Generation:     registration.Generation,
		State:          state,
		ActiveRunID:    activeRunID,
		ActiveRunIDs:   activeRunIDs,
		ManifestDigest: manifestDigest,
	})
}

func registerRunner(ctx context.Context, peer *protocol.Peer, params protocol.RegisterParams, allowStaleRunnerIDFallback bool) (protocol.RegisterResult, error) {
	var result protocol.RegisterResult
	err := peer.Call(ctx, protocol.MethodRunnerRegister, params, &result)
	if err == nil {
		return result, nil
	}
	var rpcErr *protocol.RPCError
	if !allowStaleRunnerIDFallback || params.RunnerID == "" || !errors.As(err, &rpcErr) || rpcErr.Code != protocol.ErrorCodeStale || (rpcErr.Reason() != "" && rpcErr.Reason() != protocol.ErrorReasonRunnerNotFound) {
		return protocol.RegisterResult{}, err
	}
	params.RunnerID = ""
	if err := peer.Call(ctx, protocol.MethodRunnerRegister, params, &result); err != nil {
		return protocol.RegisterResult{}, err
	}
	return result, nil
}

func runnerRegistrationError(err error, explicitLegacyToken bool) error {
	if err == nil || !explicitLegacyToken {
		return err
	}
	var rpcErr *protocol.RPCError
	if errors.As(err, &rpcErr) && rpcErr.Reason() == protocol.ErrorReasonLegacyAuthKeyEnrolled {
		return &permanentConnectionError{err: pkgerrors.New("runner has an enrolled key credential but an explicit legacy runner token took precedence; remove --auth-token or KODELET_RUNNER_AUTH_TOKEN")}
	}
	return err
}

func normalizeServerURL(raw string) (string, string, error) {
	server, err := controlplaneurl.NormalizeBase(raw)
	if err != nil {
		return "", "", err
	}
	websocketURL, err := controlplaneurl.WebSocketEndpoint(server, "api", "runner", "v1", "connect")
	if err != nil {
		return "", "", err
	}
	return server, websocketURL, nil
}

func isLoopbackHostname(hostname string) bool {
	return controlplaneurl.IsLoopbackHostname(hostname)
}

type permanentConnectionError struct {
	err error
}

func (e *permanentConnectionError) Error() string { return e.err.Error() }
func (e *permanentConnectionError) Unwrap() error { return e.err }

func isPermanentConnectionError(err error) bool {
	var permanent *permanentConnectionError
	return errors.As(err, &permanent)
}
