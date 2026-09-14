// Package browser manages optional, externally installed Chrome processes for runner workspaces.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

const (
	maxSessions        = 4
	defaultIdleTimeout = 15 * time.Minute
	startupTimeout     = 20 * time.Second
	commandTimeout     = 30 * time.Second
	connectionTimeout  = 10 * time.Second
	processGracePeriod = time.Second
	processKillWait    = 2 * time.Second
	maxStderrBytes     = 32 * 1024
	maxCDPMessageBytes = 16 * 1024 * 1024
	maxTargetListBytes = 1024 * 1024
	maxAssetChunkBytes = 128 * 1024
	maxAssetBytes      = 16 * 1024 * 1024
)

// Config is trusted runner configuration, not workspace- or page-provided input.
type Config struct {
	// Executable is an installed Chrome/Chromium executable or PATH name. Blank disables launching.
	Executable string
	// DevToolsDir optionally contains a compiled frontend compatible with the installed browser version.
	// These trusted assets are never downloaded or embedded by this package.
	DevToolsDir string
	// IdleTimeout applies only to sessions without attachments. Nonpositive values use fifteen minutes.
	IdleTimeout time.Duration
}

// Scope isolates a conversation's browser in one canonical working directory.
type Scope struct {
	ConversationID string
	CWD            string
}

// Info identifies one immutable browser session. DevTools indicates a configured frontend directory.
type Info struct {
	SessionID      string `json:"sessionId"`
	ConversationID string `json:"conversationId"`
	CWD            string `json:"cwd"`
	DevTools       bool   `json:"devTools"`
}

// AssetChunk is a bounded portion of one trusted DevTools frontend file.
type AssetChunk struct {
	Data        []byte `json:"data"`
	ContentType string `json:"contentType"`
	EOF         bool   `json:"eof"`
}

// Manager owns at most four conversation sessions, independently of individual agent runs.
// Call Close at runner shutdown. Connections must be released even if their socket has already closed.
type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	config    Config
	mu        sync.Mutex
	sessions  map[Scope]*session
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

type session struct {
	info   Info
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	done   chan struct{}

	// Startup publishes these fields by closing ready; cleanup publishes closeErr through done.
	wsURL       string
	startErr    error
	closeErr    error
	profile     string
	cmd         *exec.Cmd
	processDone chan struct{}
	processErr  error
	stderr      stderrTail

	// Protected by Manager.mu, including connections that are still dialing.
	attachments int
	lastUsed    time.Time
	connections map[*websocket.Conn]struct{}
}

// NewManager creates a lazy manager. It does not launch a browser or inspect a user profile.
func NewManager(ctx context.Context, config Config) *Manager {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	config.Executable = strings.TrimSpace(config.Executable)
	config.DevToolsDir = strings.TrimSpace(config.DevToolsDir)
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = defaultIdleTimeout
	}
	m := &Manager{ctx: ctx, cancel: cancel, config: config, sessions: make(map[Scope]*session)}
	go func() {
		<-ctx.Done()
		_ = m.Close()
	}()
	return m
}

// Enabled reports whether an executable is configured, not whether it has successfully launched.
func (m *Manager) Enabled() bool {
	return m.config.Executable != ""
}

// Open opens or reuses a conversation session. Concurrent opens within a scope share one startup.
// Canceling the initiating request during startup tears down that startup; after success,
// request cancellation does not affect the browser lifetime.
func (m *Manager) Open(ctx context.Context, scope Scope) (Info, error) {
	if !m.Enabled() {
		return Info{}, errors.New("browser is disabled: configure an external Chrome executable")
	}
	scope, err := canonicalScope(scope)
	if err != nil {
		return Info{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	for {
		m.mu.Lock()
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return Info{}, errors.Wrap(err, "browser open canceled")
		}
		if m.closed || m.ctx.Err() != nil {
			m.mu.Unlock()
			return Info{}, errors.New("browser manager is closed")
		}
		s := m.sessions[scope]
		if s != nil && s.ctx.Err() != nil {
			m.mu.Unlock()
			select {
			case <-s.done:
				continue
			case <-ctx.Done():
				return Info{}, errors.Wrap(ctx.Err(), "waiting for previous browser to stop")
			}
		}
		created := s == nil
		if created {
			if len(m.sessions) >= maxSessions {
				m.mu.Unlock()
				return Info{}, errors.New("browser session limit reached (four conversation sessions); stop an unused session")
			}
			sessionCtx, sessionCancel := context.WithCancel(m.ctx)
			s = &session{
				info: Info{SessionID: rand.Text(), ConversationID: scope.ConversationID, CWD: scope.CWD, DevTools: m.config.DevToolsDir != ""},
				ctx:  sessionCtx, cancel: sessionCancel,
				ready: make(chan struct{}), done: make(chan struct{}),
				connections: make(map[*websocket.Conn]struct{}), lastUsed: time.Now(),
			}
			m.sessions[scope] = s
			go m.run(ctx, s)
		}
		s.lastUsed = time.Now()
		m.mu.Unlock()
		select {
		case <-s.ready:
			if s.startErr != nil {
				return Info{}, s.startErr
			}
			if err := ctx.Err(); err != nil {
				if created {
					s.cancel()
					<-s.done
				}
				return Info{}, errors.Wrap(err, "browser open canceled")
			}
			if s.ctx.Err() != nil {
				return Info{}, errors.New("browser session stopped while opening")
			}
			return s.info, nil
		case <-ctx.Done():
			if created {
				s.cancel()
				<-s.done
				if s.startErr != nil {
					return Info{}, s.startErr
				}
			}
			return Info{}, errors.Wrap(ctx.Err(), "browser open canceled")
		}
	}
}

// Connect attaches to an existing page target, never creating a session for a stale ID.
// The idempotent release function closes the socket and releases its idle-timeout lease.
// Canceling ctx, stopping the session, or closing the manager also closes the socket.
func (m *Manager) Connect(ctx context.Context, scope Scope, sessionID string) (*websocket.Conn, func(), error) {
	scope, err := canonicalScope(scope)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	s := m.sessions[scope]
	if m.closed || s == nil || sessionID == "" || s.info.SessionID != sessionID || s.ctx.Err() != nil {
		m.mu.Unlock()
		return nil, nil, errors.New("browser session is unavailable or stale")
	}
	select {
	case <-s.ready:
	default:
		m.mu.Unlock()
		return nil, nil, errors.New("browser session is still starting")
	}
	s.attachments++
	m.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
	stopCancel := context.AfterFunc(s.ctx, cancel)
	stopHandshake := func() bool { return true }
	dialer := websocket.Dialer{
		HandshakeTimeout: connectionTimeout, // Never proxy a private CDP endpoint.
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err == nil {
				// Gorilla applies the deadline to the upgrade, but cancellation after
				// TCP connection establishment must also interrupt a stalled upgrade.
				stopHandshake = context.AfterFunc(dialCtx, func() { _ = conn.Close() })
			}
			return conn, err
		},
	}
	conn, response, err := dialer.DialContext(dialCtx, s.wsURL, nil)
	stopHandshake()
	stopCancel()
	cancel()
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	m.mu.Lock()
	if err == nil && (s.ctx.Err() != nil || ctx.Err() != nil) {
		_ = conn.Close()
		err = errors.New("browser attachment canceled")
	}
	if err != nil {
		s.attachments--
		s.lastUsed = time.Now()
		m.mu.Unlock()
		return nil, nil, errors.Wrap(err, "failed to connect to browser page")
	}
	conn.SetReadLimit(maxCDPMessageBytes)
	s.connections[conn] = struct{}{}
	m.mu.Unlock()
	released := make(chan struct{})
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = conn.Close()
			m.mu.Lock()
			delete(s.connections, conn)
			s.attachments--
			s.lastUsed = time.Now()
			m.mu.Unlock()
			close(released)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			release()
		case <-s.ctx.Done():
			release()
		case <-released:
		}
	}()
	return conn, release, nil
}

// Stop closes one exact session and all of its attached sockets. Stale IDs cannot stop replacements.
func (m *Manager) Stop(scope Scope, sessionID string) error {
	scope, err := canonicalScope(scope)
	if err != nil {
		return err
	}
	m.mu.Lock()
	s := m.sessions[scope]
	if s == nil || sessionID == "" || s.info.SessionID != sessionID {
		m.mu.Unlock()
		return errors.New("browser session is unavailable or stale")
	}
	s.cancel()
	m.mu.Unlock()
	<-s.done
	return s.closeErr
}

// Close stops all sessions and waits for subprocess and temporary-profile cleanup.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		sessions := make([]*session, 0, len(m.sessions))
		for _, s := range m.sessions {
			s.cancel()
			sessions = append(sessions, s)
		}
		m.mu.Unlock()
		for _, s := range sessions {
			<-s.done
			m.closeErr = stderrors.Join(m.closeErr, s.closeErr)
		}
	})
	return m.closeErr
}

// Command opens or reuses a session and sends one CDP command over its own page connection.
// It returns the command's result (not the envelope), ignoring events and unrelated response IDs.
// Methods are not allowlisted: authorization belongs to the caller, as it does for Connect.
func (m *Manager) Command(ctx context.Context, scope Scope, method string, params json.RawMessage) (json.RawMessage, error) {
	domain, name, found := strings.Cut(method, ".")
	if !found || domain == "" || name == "" || strings.ContainsAny(method, " \t\n\r") {
		return nil, errors.New("browser command requires a CDP Domain.method")
	}
	if len(params) > maxCDPMessageBytes {
		return nil, errors.New("browser command parameters exceed the size limit")
	}
	if len(params) > 0 && (!json.Valid(params) || !strings.HasPrefix(strings.TrimSpace(string(params)), "{")) {
		return nil, errors.New("browser command parameters must be a JSON object")
	}
	request := struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params,omitempty"`
	}{ID: 1, Method: method, Params: params}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to encode browser command")
	}
	if len(payload) > maxCDPMessageBytes {
		return nil, errors.New("browser command exceeds the size limit")
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	info, err := m.Open(ctx, scope)
	if err != nil {
		return nil, err
	}
	conn, release, err := m.Connect(ctx, scope, info.SessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	deadline, _ := ctx.Deadline()
	_ = conn.SetWriteDeadline(deadline)
	_ = conn.SetReadDeadline(deadline)
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return nil, errors.Wrap(err, "failed to send browser command")
	}
	for {
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := conn.ReadJSON(&response); err != nil {
			if ctx.Err() != nil {
				return nil, errors.Wrap(ctx.Err(), "browser command canceled")
			}
			return nil, errors.Wrap(err, "failed to read browser command response")
		}
		if response.ID != request.ID {
			continue
		}
		if response.Error != nil {
			return nil, errors.Errorf("browser CDP %s failed (%d): %s", method, response.Error.Code, response.Error.Message)
		}
		if len(response.Result) == 0 {
			return nil, errors.New("browser command response is missing its result")
		}
		return response.Result, nil
	}
}

func (m *Manager) run(openCtx context.Context, s *session) {
	started := false
	defer func() {
		s.cancel()
		m.mu.Lock()
		for conn := range s.connections {
			_ = conn.Close()
		}
		m.mu.Unlock()
		s.closeErr = s.cleanup()
		if !started {
			s.startErr = errors.Wrapf(s.startErr, "failed to start Chrome; stderr: %s", s.stderr.String())
		}
		m.mu.Lock()
		delete(m.sessions, Scope{ConversationID: s.info.ConversationID, CWD: s.info.CWD})
		m.mu.Unlock()
		if !started {
			close(s.ready)
		}
		close(s.done)
	}()
	ctx, cancel := context.WithTimeout(openCtx, startupTimeout)
	stopCancel := context.AfterFunc(s.ctx, cancel)
	s.startErr = s.start(ctx, m.config.Executable)
	if s.startErr == nil {
		s.startErr = ctx.Err()
	}
	stopCancel()
	cancel()
	if s.startErr != nil {
		return
	}
	m.mu.Lock()
	s.lastUsed = time.Now()
	m.mu.Unlock()
	started = true
	close(s.ready)
	ticker := time.NewTicker(min(m.config.IdleTimeout, time.Minute))
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.processDone:
			return
		case <-ticker.C:
			m.mu.Lock()
			idle := s.attachments == 0 && time.Since(s.lastUsed) >= m.config.IdleTimeout
			if idle {
				s.cancel()
			}
			m.mu.Unlock()
			if idle {
				return
			}
		}
	}
}

func canonicalScope(scope Scope) (Scope, error) {
	scope.ConversationID = strings.TrimSpace(scope.ConversationID)
	if scope.ConversationID == "" {
		return Scope{}, errors.New("browser conversation ID is required")
	}
	cwd, err := canonicalCWD(scope.CWD)
	if err != nil {
		return Scope{}, err
	}
	scope.CWD = cwd
	return scope, nil
}

func canonicalCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("browser workspace directory is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve browser workspace")
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve browser workspace symlinks")
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", errors.Wrap(err, "failed to inspect browser workspace")
	}
	if !info.IsDir() {
		return "", errors.New("browser workspace must be a directory")
	}
	return canonical, nil
}

func (s *session) start(ctx context.Context, executable string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := exec.LookPath(executable)
	if err != nil {
		return errors.Wrap(err, "external Chrome executable was not found")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return errors.Wrap(err, "failed to resolve Chrome executable")
	}
	s.profile, err = os.MkdirTemp("", "kodelet-browser-")
	if err != nil {
		return errors.Wrap(err, "failed to create isolated browser profile")
	}
	s.cmd = exec.Command(executable,
		"--headless", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir="+s.profile, "--no-first-run", "--no-default-browser-check", "about:blank")
	s.cmd.Dir = s.info.CWD
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s.cmd.Stderr = &s.stderr
	s.cmd.WaitDelay = processGracePeriod
	if err := s.cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to launch external Chrome executable")
	}
	s.processDone = make(chan struct{})
	go func() {
		s.processErr = s.cmd.Wait()
		close(s.processDone)
	}()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("browser target discovery must not redirect")
		},
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return errors.Wrapf(ctx.Err(), "waiting for Chrome page target (last discovery error: %v)", lastErr)
		case <-s.processDone:
			return errors.Errorf("Chrome exited before its page target was ready: %v", s.processErr)
		default:
		}
		s.wsURL, lastErr = discoverPage(ctx, client, s.profile)
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
		case <-s.processDone:
		case <-ticker.C:
		}
	}
}

func discoverPage(ctx context.Context, client *http.Client, profile string) (string, error) {
	file, err := os.Open(filepath.Join(profile, "DevToolsActivePort"))
	if err != nil {
		return "", errors.Wrap(err, "reading Chrome debugging port")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return "", errors.Wrap(err, "reading Chrome debugging port")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(data) > 4096 || len(lines) != 2 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return "", errors.New("invalid Chrome DevToolsActivePort file")
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || port <= 0 || port > 65535 {
		return "", errors.New("invalid Chrome debugging port")
	}
	host := "127.0.0.1:" + strconv.Itoa(port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/json/list", nil)
	if err != nil {
		return "", errors.Wrap(err, "creating Chrome discovery request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "querying Chrome page targets")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("Chrome target discovery returned HTTP %d", resp.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxTargetListBytes+1))
	if err != nil {
		return "", errors.Wrap(err, "reading Chrome page targets")
	}
	if len(data) > maxTargetListBytes {
		return "", errors.New("Chrome target list exceeds the size limit")
	}
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(data, &targets); err != nil {
		return "", errors.Wrap(err, "decoding Chrome page targets")
	}
	for _, target := range targets {
		if target.Type != "page" {
			continue
		}
		u, err := url.Parse(target.URL)
		if err != nil || u.Scheme != "ws" || u.Host != host || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
			return "", errors.New("Chrome page target must use the same loopback debugging endpoint")
		}
		id, ok := strings.CutPrefix(u.Path, "/devtools/page/")
		if !ok || id == "" || strings.Contains(id, "/") {
			return "", errors.New("Chrome target is not a page WebSocket")
		}
		return u.String(), nil
	}
	return "", errors.New("Chrome has no page target")
}

func (s *session) cleanup() error {
	var cleanupErr error
	if s.cmd != nil && s.cmd.Process != nil {
		pid := s.cmd.Process.Pid
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			cleanupErr = errors.Wrap(err, "failed to terminate Chrome process group")
		}
		timer := time.NewTimer(processGracePeriod)
		select {
		case <-s.processDone:
		case <-timer.C:
		}
		timer.Stop()
		// Kill remaining descendants even if the main browser has already exited.
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			cleanupErr = stderrors.Join(cleanupErr, errors.Wrap(err, "failed to kill Chrome process group"))
		}
		timer = time.NewTimer(processKillWait)
		select {
		case <-s.processDone:
		case <-timer.C:
			cleanupErr = stderrors.Join(cleanupErr, errors.New("Chrome process did not exit before the cleanup deadline"))
		}
		timer.Stop()
	}
	if s.profile != "" {
		cleanupErr = stderrors.Join(cleanupErr, errors.Wrap(os.RemoveAll(s.profile), "failed to remove isolated Chrome profile"))
	}
	return cleanupErr
}

type stderrTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *stderrTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) >= maxStderrBytes {
		b.data = append(b.data[:0], p[len(p)-maxStderrBytes:]...)
	} else {
		if excess := len(b.data) + len(p) - maxStderrBytes; excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *stderrTail) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}

var assetContentTypes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8", ".json": "application/json", ".map": "application/json",
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon",
	".woff2": "font/woff2", ".woff": "font/woff", ".ttf": "font/ttf", ".wasm": "application/wasm",
}

// ReadAsset reads at most 128 KiB from a relative frontend path. Files larger than 16 MiB,
// non-regular files, unsupported extensions, and paths/symlinks escaping DevToolsDir are rejected.
func (m *Manager) ReadAsset(path string, offset int64) (AssetChunk, error) {
	if m.ctx.Err() != nil {
		return AssetChunk{}, errors.New("browser manager is closed")
	}
	if m.config.DevToolsDir == "" {
		return AssetChunk{}, errors.New("DevTools frontend directory is not configured")
	}
	contentType, allowed := assetContentTypes[strings.ToLower(filepath.Ext(path))]
	if !allowed || !filepath.IsLocal(path) || offset < 0 {
		return AssetChunk{}, errors.New("invalid DevTools asset path, extension, or offset")
	}
	root, err := os.OpenRoot(m.config.DevToolsDir)
	if err != nil {
		return AssetChunk{}, errors.Wrap(err, "opening DevTools frontend directory")
	}
	defer root.Close()
	// O_NONBLOCK prevents a named pipe with an allowed suffix from blocking before Stat.
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return AssetChunk{}, errors.Wrap(err, "opening confined DevTools asset")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return AssetChunk{}, errors.Wrap(err, "inspecting DevTools asset")
	}
	if !info.Mode().IsRegular() || info.Size() > maxAssetBytes || offset > info.Size() {
		return AssetChunk{}, errors.New("DevTools asset must be a regular file of at most 16 MiB with a valid offset")
	}
	data := make([]byte, min(int64(maxAssetChunkBytes), info.Size()-offset))
	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return AssetChunk{}, errors.Wrap(err, "reading DevTools asset chunk")
	}
	return AssetChunk{Data: data[:n], ContentType: contentType, EOF: offset+int64(n) >= info.Size() || errors.Is(err, io.EOF)}, nil
}
