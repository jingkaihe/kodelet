package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	runnerclient "github.com/jingkaihe/kodelet/pkg/runner/client"
	"github.com/jingkaihe/kodelet/pkg/runner/localstate"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

// EmbeddedRunnerConfig composes the normal workspace runner into the daemon.
// A nil configuration leaves embedding disabled during the opt-in migration.
type EmbeddedRunnerConfig struct {
	Workspace      string
	Settings       map[string]any
	Store          *localstate.Store
	ServiceOptions runnerclient.ServiceOptions
}

// EmbeddedRunnerStatus separates API availability from workspace readiness.
type EmbeddedRunnerStatus struct {
	Enabled  bool   `json:"enabled"`
	RunnerID string `json:"runnerId,omitempty"`
	Ready    bool   `json:"ready"`
	Error    string `json:"error,omitempty"`
}

// EmbeddedRunnerStatus returns current normal-registry readiness, not merely a
// successful dial. The default identity stays pinned while offline or busy.
func (s *Server) EmbeddedRunnerStatus() EmbeddedRunnerStatus {
	s.embeddedMu.Lock()
	status := s.embeddedStatus
	s.embeddedMu.Unlock()
	status.Enabled = s.config != nil && s.config.EmbeddedRunner != nil
	s.activeChatsMu.Lock()
	stopping := s.stopping
	s.activeChatsMu.Unlock()
	if !stopping && status.RunnerID != "" && s.runnerRegistry != nil && status.Error == "" {
		runner, ok := s.runnerRegistry.Runner(status.RunnerID)
		status.Ready = ok && runner.Connected && (runner.Status == runnerregistry.RunnerStatusIdle || runner.Status == runnerregistry.RunnerStatusBusy && runner.ConcurrentRuns)
	}
	return status
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.activeChatsMu.Lock()
	stopping := s.stopping
	s.activeChatsMu.Unlock()
	s.writeJSONResponse(w, map[string]any{
		"apiReady":       !stopping,
		"embeddedRunner": s.EmbeddedRunnerStatus(),
	})
}

func (s *Server) embeddedRunnerError(err error) {
	s.embeddedMu.Lock()
	s.embeddedStatus.Ready = false
	if err != nil {
		s.embeddedStatus.Error = err.Error()
	}
	s.embeddedMu.Unlock()
	if err != nil {
		logger.G(s.runCtx).WithError(err).Error("embedded runner unavailable; control-plane API remains available")
	}
}

// Serve accepts an already-bound listener so service hosts can report its actual
// address (including port zero) before starting optional embedded execution.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	s.server = &http.Server{Addr: listener.Addr().String(), Handler: s.router}
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.server.Serve(listener) }()
	runnerCtx, stopRunner := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRunner()
	runnerDone := make(chan struct{})
	go func() {
		defer close(runnerDone)
		if s.config.EmbeddedRunner == nil {
			return
		}
		endpoint, err := embeddedLoopbackEndpoint(listener.Addr())
		if err != nil {
			s.embeddedRunnerError(err)
			return
		}
		runner, err := s.newEmbeddedRunner(runnerCtx, endpoint)
		if err != nil {
			s.embeddedRunnerError(err)
			return
		}
		if err := runner.Run(runnerCtx); err != nil {
			s.embeddedRunnerError(err)
		}
	}()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveDone:
	}
	// Stop admission and cancel/drain agent work while runner transport is usable.
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), s.httpShutdownTimeout())
	drainErr := s.drainExecutions(drainCtx)
	cancelDrain()
	stopRunner()
	var runnerErr error
	select {
	case <-runnerDone:
	case <-time.After(s.httpShutdownTimeout()):
		runnerErr = errors.New("embedded runner shutdown timed out")
	}
	shutdownErr := s.shutdownHTTPServer()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return errors.Wrap(serveErr, "control-plane listener failed")
	}
	if drainErr != nil {
		return drainErr
	}
	if runnerErr != nil {
		return runnerErr
	}
	return shutdownErr
}

func (s *Server) drainExecutions(ctx context.Context) error {
	s.activeChatsMu.Lock()
	s.stopping = true
	runs := make([]*activeChatRun, 0, len(s.activeChats))
	for _, run := range s.activeChats {
		runs = append(runs, run)
		run.cancel()
	}
	s.activeChatsMu.Unlock()
	for _, run := range runs {
		select {
		case <-run.done:
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "active executions did not stop before daemon shutdown")
		}
	}
	return nil
}

func embeddedLoopbackEndpoint(address net.Addr) (string, error) {
	tcp, ok := address.(*net.TCPAddr)
	if !ok || tcp.Port <= 0 {
		return "", errors.New("embedded runner requires a bound TCP listener")
	}
	ip := tcp.IP
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			ip = net.IPv4(127, 0, 0, 1)
		} else {
			ip = net.IPv6loopback
		}
	}
	if !ip.IsLoopback() {
		return "", errors.New("embedded runner requires a loopback or wildcard listener; use --host=0.0.0.0 or disable embedding")
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(tcp.Port)), nil
}

func (s *Server) newEmbeddedRunner(ctx context.Context, endpoint string) (*runnerclient.Runner, error) {
	config := s.config.EmbeddedRunner
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
	options := config.ServiceOptions
	if options.ConfigLoader == nil && options.WorkspaceConfigLoader == nil {
		options.WorkspaceConfigLoader, err = runnerclient.NewWorkspaceConfigLoader(config.Settings)
		if err != nil {
			return nil, err
		}
	}
	runner, err := runnerclient.NewRunner(ctx, runnerclient.RunnerConfig{
		Server: endpoint, AuthToken: s.config.RunnerAuthToken,
		Workspace: workspace, DisplayName: "Embedded runner", Store: store,
		ServiceOptions: options,
		OnRegistered: func(result protocol.RegisterResult) {
			s.embeddedMu.Lock()
			s.embeddedStatus.RunnerID = result.RunnerID
			s.embeddedStatus.Error = ""
			s.embeddedMu.Unlock()
		},
		OnRetry: func(err error, _ time.Duration) { s.embeddedRunnerError(err) },
	})
	if err != nil {
		return nil, err
	}
	if err := runner.AcquireWorkspaceLock(); err != nil {
		_ = runner.Close()
		var held *localstate.LockHeldError
		if errors.As(err, &held) {
			return nil, errors.Wrapf(err, "embedded runner workspace is already owned by runner %q at %q; stop its owner or disable --embedded-runner and explicitly select that runner", held.Metadata.RunnerID, held.Metadata.Server)
		}
		return nil, err
	}
	if s.config.resolvedRunnerAuthMode() == RunnerAuthModeEnrollment {
		if err := s.provisionEmbeddedCredential(ctx, store, endpoint, workspace); err != nil {
			_ = runner.Close()
			return nil, err
		}
	}
	return runner, nil
}

// Provisioning uses the existing approval/credential store. The daemon's trusted
// configuration authorizes the local service identity, not a reusable user token.
func (s *Server) provisionEmbeddedCredential(ctx context.Context, store *localstate.Store, endpoint, workspace string) error {
	credential, found, err := store.LoadCredential(endpoint, workspace)
	if err != nil {
		return err
	}
	if found {
		proof, err := protocol.SignDPoPProof(credential.PrivateKey, protocol.DPoPProofOptions{Method: http.MethodGet, TargetURL: endpoint + protocol.Endpoint, AccessToken: credential.AccessToken})
		if err != nil {
			return err
		}
		identity, err := s.authStore.VerifyRunnerDPoP(ctx, credential.AccessToken, proof, http.MethodGet, endpoint+protocol.Endpoint)
		if err != nil {
			return errors.Wrap(err, "embedded runner credential is invalid; re-enroll the runner explicitly rather than replacing a revoked identity automatically")
		}
		return store.SaveRegistration(localstate.Registration{Server: endpoint, Workspace: workspace, RunnerID: identity.RunnerID})
	}
	identity, err := store.LoadOrCreateHostIdentity()
	if err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.Wrap(err, "failed to generate embedded runner key")
	}
	encodedKey, err := protocol.EncodePublicKey(publicKey)
	if err != nil {
		return err
	}
	fingerprint, err := protocol.CredentialFingerprint(publicKey)
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return errors.Wrap(err, "failed to determine embedded runner hostname")
	}
	request := protocol.EnrollmentStartRequest{
		ProtocolVersions: []int{protocol.Version}, PublicKey: encodedKey, Fingerprint: fingerprint,
		Host:      protocol.Host{InstanceID: identity.InstanceID, Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH, PID: os.Getpid()},
		Workspace: protocol.Workspace{Path: workspace, Name: filepath.Base(workspace)}, DisplayName: "Embedded runner",
	}
	enrollment, err := s.authStore.StartRunnerEnrollment(ctx, request, endpoint+"/runner/enroll")
	if err != nil {
		return err
	}
	runner, err := s.runnerRegistry.CommitEnrollmentRegistration(request, false, func(runner runnerregistry.Runner) error {
		_, err := s.authStore.ApproveRunnerEnrollment(ctx, enrollment.UserCode, "daemon:embedded-runner", runner, false)
		return err
	})
	if err != nil {
		return errors.Wrap(err, "failed to approve embedded runner identity; stop or explicitly re-enroll an existing owner")
	}
	approved, err := s.authStore.PollRunnerEnrollment(ctx, protocol.EnrollmentPollRequest{EnrollmentID: enrollment.EnrollmentID, DeviceCode: enrollment.DeviceCode})
	if err != nil {
		return err
	}
	if err := store.SaveCredential(localstate.Credential{Server: endpoint, Workspace: workspace, CredentialID: approved.CredentialID, AccessToken: approved.AccessToken, PublicKey: publicKey, PrivateKey: privateKey, Fingerprint: fingerprint}); err != nil {
		return err
	}
	return store.SaveRegistration(localstate.Registration{Server: endpoint, Workspace: workspace, RunnerID: runner.ID, DisplayName: request.DisplayName})
}
