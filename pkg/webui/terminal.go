package webui

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
	"github.com/pkg/errors"
)

const (
	defaultTerminalRows            = 28
	defaultTerminalCols            = 100
	maxTerminalRows                = 400
	maxTerminalCols                = 400
	terminalWriteWait              = 10 * time.Second
	terminalPongWait               = 30 * time.Second
	terminalPingPeriod             = 20 * time.Second
	terminalReadLimit              = 64 * 1024
	remoteTerminalReadBytes        = 64 * 1024
	remoteTerminalReadWaitMS       = 20_000
	remoteTerminalOpenTimeout      = 15 * time.Second
	remoteTerminalOperationTimeout = 10 * time.Second
	maxRemoteTerminalAttachments   = 8
)

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     terminalOriginAllowed,
}

func (s *Server) terminalUpgrader() websocket.Upgrader {
	upgrader := terminalUpgrader
	upgrader.CheckOrigin = s.terminalOriginAllowed
	return upgrader
}

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Code *int   `json:"code,omitempty"`
	CWD  string `json:"cwd,omitempty"`
	Name string `json:"name,omitempty"`
	Git  bool   `json:"git,omitempty"`
	PID  int    `json:"pid,omitempty"`
	Text string `json:"text,omitempty"`
}

type terminalSocketRead struct {
	MessageType int
	Payload     []byte
	Err         error
}

type remoteTerminalRead struct {
	Result protocol.WorkspaceTerminalReadResult
	Err    error
}

type websocketWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *websocketWriter) Write(messageType int, payload []byte) error {
	if w == nil || w.conn == nil {
		return errors.New("websocket writer is not initialized")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.conn.SetWriteDeadline(time.Now().Add(terminalWriteWait)); err != nil {
		return err
	}

	return w.conn.WriteMessage(messageType, payload)
}

func (w *websocketWriter) writeJSON(message terminalMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return errors.Wrap(err, "failed to encode terminal message")
	}

	return w.Write(websocket.TextMessage, payload)
}

func (s *Server) handleTerminalWebsocket(w http.ResponseWriter, r *http.Request) {
	target, targetErr := s.resolveWorkspaceRunnerTarget(r)
	if targetErr != nil {
		s.writeWorkspaceRunnerTargetError(w, targetErr)
		return
	}
	if target != nil {
		if err := s.runnerRegistry.ValidateRunnerCall(target.Runner.ID, target.Runner.Generation, protocol.MethodWorkspaceTerminalOpen); err != nil {
			if errors.Is(err, runnerregistry.ErrRunnerCapabilityUnsupported) {
				s.writeErrorResponse(w, http.StatusNotImplemented, "runner does not support workspace terminal", nil)
				return
			}
			s.writeErrorResponse(w, http.StatusServiceUnavailable, "runner terminal is unavailable", err)
			return
		}
		s.handleRemoteTerminalWebsocket(w, r, target.Runner.ID, target.Runner.Generation)
		return
	}
	if !s.requireControlPlaneWorkspace(w) {
		return
	}
	resolvedCWD, err := s.resolveRequestedCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid cwd", err)
		return
	}

	upgrader := s.terminalUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("failed to upgrade terminal websocket")
		return
	}

	ctx, cancel := context.WithCancel(s.chatExecutionContext(r.Context()))
	defer cancel()

	rows := boundedTerminalRows(parseTerminalDimension(r.URL.Query().Get("rows")))
	cols := boundedTerminalCols(parseTerminalDimension(r.URL.Query().Get("cols")))

	session, err := s.terminalSessionManager().getOrCreate(r.Context(), terminalSessionKey(resolvedCWD), resolvedCWD, rows, cols)
	if err != nil {
		_ = conn.Close()
		logger.G(r.Context()).WithError(err).Warn("failed to get terminal session")
		return
	}

	writer := &websocketWriter{conn: conn}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(terminalReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	})

	attachment, replay, err := session.attach()
	if err != nil {
		logger.G(r.Context()).WithError(err).Debug("terminal session ended before websocket attach")
		return
	}
	defer session.detach(attachment)

	if err := session.resize(rows, cols); err != nil && !errors.Is(err, errTerminalSessionClosed) {
		logger.G(r.Context()).WithError(err).Warn("failed to resize terminal pty")
	}

	if err := writer.writeJSON(session.readyMessage()); err != nil {
		return
	}
	if len(replay) > 0 {
		if err := writer.Write(websocket.BinaryMessage, replay); err != nil {
			return
		}
	}
	if err := writer.writeJSON(terminalMessage{Type: "replay-complete"}); err != nil {
		return
	}

	go func() {
		ticker := time.NewTicker(terminalPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writer.Write(websocket.PingMessage, nil); err != nil {
					attachment.notify(err)
					return
				}
			}
		}
	}()

	readCh := make(chan terminalSocketRead, 1)
	go readTerminalWebsocket(ctx, conn, readCh)

	for {
		select {
		case output := <-attachment.outputCh:
			if err := writer.Write(websocket.BinaryMessage, output); err != nil {
				return
			}
		case code := <-attachment.exitCh:
			_ = writer.writeJSON(terminalMessage{Type: "exit", Code: terminalExitCode(code)})
			return
		case asyncErr := <-attachment.errCh:
			if !terminalAttachmentErrorCloses(asyncErr) {
				continue
			}
			if asyncErr != nil && !errors.Is(asyncErr, errTerminalSessionClosed) {
				var closeErr *websocket.CloseError
				if !errors.As(asyncErr, &closeErr) {
					logger.G(r.Context()).WithError(asyncErr).Debug("terminal session closed")
				}
			}
			return
		case socketRead := <-readCh:
			if socketRead.Err != nil {
				return
			}

			switch socketRead.MessageType {
			case websocket.BinaryMessage:
				if err := session.writeInput(socketRead.Payload); err != nil {
					return
				}
			case websocket.TextMessage:
				var message terminalMessage
				if err := json.Unmarshal(socketRead.Payload, &message); err != nil {
					continue
				}

				switch message.Type {
				case "input":
					if message.Data == "" {
						continue
					}
					if err := session.writeInput([]byte(message.Data)); err != nil {
						return
					}
				case "resize":
					if err := session.resize(message.Rows, message.Cols); err != nil && !errors.Is(err, errTerminalSessionClosed) {
						logger.G(r.Context()).WithError(err).Warn("failed to resize terminal pty")
					}
				case "signal":
					if sig, ok := parseTerminalSignal(message.Name); ok {
						_ = session.signal(sig)
					}
				}
			}
		}
	}
}

func (s *Server) handleRemoteTerminalWebsocket(w http.ResponseWriter, r *http.Request, runnerID string, generation int64) {
	if !s.acquireRemoteTerminalAttachment(runnerID) {
		s.writeErrorResponse(w, http.StatusTooManyRequests, "too many terminal attachments for runner", nil)
		return
	}
	defer s.releaseRemoteTerminalAttachment(runnerID)

	upgrader := s.terminalUpgrader()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("failed to upgrade remote terminal websocket")
		return
	}

	ctx, cancel := context.WithCancel(s.chatExecutionContext(r.Context()))
	defer cancel()
	defer func() { _ = conn.Close() }()

	rows := boundedTerminalRows(parseTerminalDimension(r.URL.Query().Get("rows")))
	cols := boundedTerminalCols(parseTerminalDimension(r.URL.Query().Get("cols")))
	writer := &websocketWriter{conn: conn}
	var opened protocol.WorkspaceTerminalOpenResult
	openCtx, cancelOpen := context.WithTimeout(ctx, remoteTerminalOpenTimeout)
	err = s.runnerRegistry.CallRunner(openCtx, runnerID, generation, protocol.MethodWorkspaceTerminalOpen, protocol.WorkspaceTerminalOpenParams{Rows: rows, Cols: cols}, &opened)
	cancelOpen()
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("failed to open runner terminal")
		_ = writer.writeJSON(terminalMessage{Type: "info", Text: "Failed to open runner terminal."})
		return
	}
	if strings.TrimSpace(opened.SessionID) == "" {
		logger.G(r.Context()).Warn("runner terminal returned an empty session id")
		return
	}
	if opened.ReplayCursor > opened.WriteCursor {
		logger.G(r.Context()).Warn("runner terminal returned invalid replay cursors")
		_ = writer.writeJSON(terminalMessage{Type: "info", Text: "Runner terminal returned invalid replay state."})
		return
	}

	conn.SetReadLimit(terminalReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	})

	if err := writer.writeJSON(terminalMessage{
		Type: "ready",
		CWD:  opened.CWD,
		Name: opened.Name,
		Git:  opened.Git,
		PID:  opened.PID,
	}); err != nil {
		return
	}
	replayComplete := opened.ReplayCursor >= opened.WriteCursor
	if replayComplete {
		if err := writer.writeJSON(terminalMessage{Type: "replay-complete"}); err != nil {
			return
		}
	}

	go func() {
		ticker := time.NewTicker(terminalPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writer.Write(websocket.PingMessage, nil); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	socketReads := make(chan terminalSocketRead, 1)
	go readTerminalWebsocket(ctx, conn, socketReads)
	remoteReads := make(chan remoteTerminalRead, 1)
	go s.readRemoteTerminal(ctx, runnerID, generation, opened.SessionID, opened.ReplayCursor, remoteReads)

	for {
		select {
		case <-ctx.Done():
			return
		case remoteRead := <-remoteReads:
			if remoteRead.Err != nil {
				if ctx.Err() == nil {
					logger.G(r.Context()).WithError(remoteRead.Err).Debug("runner terminal output stream ended")
				}
				return
			}
			result := remoteRead.Result
			if result.Exited && !replayComplete && result.NextCursor < opened.WriteCursor {
				logger.G(r.Context()).Warn("runner terminal exited before replay reached the open cursor")
				return
			}
			if result.Truncated {
				if err := writer.writeJSON(terminalMessage{Type: "info", Text: "[earlier terminal output was truncated]"}); err != nil {
					return
				}
			}
			if !replayComplete {
				chunkStart := result.NextCursor - uint64(len(result.Data))
				replayBytes := len(result.Data)
				if chunkStart >= opened.WriteCursor {
					replayBytes = 0
				} else if result.NextCursor > opened.WriteCursor {
					replayBytes = int(opened.WriteCursor - chunkStart)
				}
				if replayBytes > 0 {
					if err := writer.Write(websocket.BinaryMessage, result.Data[:replayBytes]); err != nil {
						return
					}
				}
				if result.NextCursor >= opened.WriteCursor {
					replayComplete = true
					if err := writer.writeJSON(terminalMessage{Type: "replay-complete"}); err != nil {
						return
					}
				}
				if replayBytes < len(result.Data) {
					if err := writer.Write(websocket.BinaryMessage, result.Data[replayBytes:]); err != nil {
						return
					}
				}
			} else if len(result.Data) > 0 {
				if err := writer.Write(websocket.BinaryMessage, result.Data); err != nil {
					return
				}
			}
			if result.Exited {
				_ = writer.writeJSON(terminalMessage{Type: "exit", Code: terminalExitCode(result.ExitCode)})
				return
			}
		case socketRead := <-socketReads:
			if socketRead.Err != nil {
				return
			}
			switch socketRead.MessageType {
			case websocket.BinaryMessage:
				if err := s.callRemoteTerminal(ctx, runnerID, generation, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{SessionID: opened.SessionID, Data: socketRead.Payload}); err != nil {
					return
				}
			case websocket.TextMessage:
				var message terminalMessage
				if err := json.Unmarshal(socketRead.Payload, &message); err != nil {
					continue
				}
				switch message.Type {
				case "input":
					if message.Data == "" {
						continue
					}
					if err := s.callRemoteTerminal(ctx, runnerID, generation, protocol.MethodWorkspaceTerminalInput, protocol.WorkspaceTerminalInputParams{SessionID: opened.SessionID, Data: []byte(message.Data)}); err != nil {
						return
					}
				case "resize":
					if err := s.callRemoteTerminal(ctx, runnerID, generation, protocol.MethodWorkspaceTerminalResize, protocol.WorkspaceTerminalResizeParams{SessionID: opened.SessionID, Rows: message.Rows, Cols: message.Cols}); err != nil {
						return
					}
				case "signal":
					if _, ok := parseTerminalSignal(message.Name); !ok {
						continue
					}
					if err := s.callRemoteTerminal(ctx, runnerID, generation, protocol.MethodWorkspaceTerminalSignal, protocol.WorkspaceTerminalSignalParams{SessionID: opened.SessionID, Name: message.Name}); err != nil {
						return
					}
				}
			}
		}
	}
}

func (s *Server) readRemoteTerminal(ctx context.Context, runnerID string, generation int64, sessionID string, cursor uint64, reads chan<- remoteTerminalRead) {
	for {
		var result protocol.WorkspaceTerminalReadResult
		requestedCursor := cursor
		err := s.runnerRegistry.CallRunner(ctx, runnerID, generation, protocol.MethodWorkspaceTerminalRead, protocol.WorkspaceTerminalReadParams{
			SessionID: sessionID,
			Cursor:    cursor,
			MaxBytes:  remoteTerminalReadBytes,
			WaitMS:    remoteTerminalReadWaitMS,
		}, &result)
		if err == nil {
			err = validateRemoteTerminalRead(requestedCursor, result)
		}
		if err == nil {
			cursor = result.NextCursor
		}
		if err != nil || len(result.Data) > 0 || result.Truncated || result.Exited {
			select {
			case reads <- remoteTerminalRead{Result: result, Err: err}:
			case <-ctx.Done():
				return
			}
		}
		if err != nil || result.Exited {
			return
		}
	}
}

func validateRemoteTerminalRead(requestedCursor uint64, result protocol.WorkspaceTerminalReadResult) error {
	dataLength := uint64(len(result.Data))
	if dataLength > remoteTerminalReadBytes {
		return errors.New("runner terminal output exceeds the requested chunk size")
	}
	if dataLength > result.NextCursor {
		return errors.New("runner terminal returned an invalid output cursor")
	}
	if result.NextCursor < requestedCursor {
		return errors.New("runner terminal cursor moved backwards")
	}
	chunkStart := result.NextCursor - dataLength
	if result.Truncated {
		if result.NextCursor == requestedCursor {
			return errors.New("runner terminal truncated output without advancing the cursor")
		}
		if chunkStart < requestedCursor {
			return errors.New("runner terminal truncated output before the requested cursor")
		}
		return nil
	}
	if chunkStart != requestedCursor {
		return errors.New("runner terminal output is not contiguous with the requested cursor")
	}
	return nil
}

func (s *Server) acquireRemoteTerminalAttachment(runnerID string) bool {
	if s == nil {
		return false
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return false
	}
	s.remoteTerminalsMu.Lock()
	defer s.remoteTerminalsMu.Unlock()
	if s.remoteTerminals == nil {
		s.remoteTerminals = make(map[string]int)
	}
	if s.remoteTerminals[runnerID] >= maxRemoteTerminalAttachments {
		return false
	}
	s.remoteTerminals[runnerID]++
	return true
}

func (s *Server) releaseRemoteTerminalAttachment(runnerID string) {
	if s == nil {
		return
	}
	runnerID = strings.TrimSpace(runnerID)
	s.remoteTerminalsMu.Lock()
	defer s.remoteTerminalsMu.Unlock()
	count := s.remoteTerminals[runnerID]
	if count <= 1 {
		delete(s.remoteTerminals, runnerID)
		return
	}
	s.remoteTerminals[runnerID] = count - 1
}

func (s *Server) callRemoteTerminal(ctx context.Context, runnerID string, generation int64, method string, params any) error {
	operationCtx, cancel := context.WithTimeout(ctx, remoteTerminalOperationTimeout)
	defer cancel()
	if err := s.runnerRegistry.CallRunner(operationCtx, runnerID, generation, method, params, nil); err != nil {
		logger.G(ctx).WithError(err).WithField("runner_id", runnerID).Debug("runner terminal operation failed")
		return err
	}
	return nil
}

func terminalAttachmentErrorCloses(err error) bool {
	return err == nil || !errors.Is(err, errTerminalClientSlow)
}

func terminalExitCode(code int) *int {
	return &code
}

func readTerminalWebsocket(ctx context.Context, conn *websocket.Conn, readCh chan<- terminalSocketRead) {
	for {
		messageType, payload, readErr := conn.ReadMessage()
		select {
		case readCh <- terminalSocketRead{MessageType: messageType, Payload: payload, Err: readErr}:
		case <-ctx.Done():
			return
		}
		if readErr != nil {
			return
		}
	}
}

func parseTerminalDimension(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}

	return value
}

func boundedTerminalRows(value int) int {
	if value <= 0 {
		return defaultTerminalRows
	}
	if value > maxTerminalRows {
		return maxTerminalRows
	}
	return value
}

func boundedTerminalCols(value int) int {
	if value <= 0 {
		return defaultTerminalCols
	}
	if value > maxTerminalCols {
		return maxTerminalCols
	}
	return value
}

func resolveTerminalShell() (string, string) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/bash"
	}

	name := filepath.Base(shell)
	if name == "." || name == string(os.PathSeparator) || name == "" {
		name = shell
	}

	return shell, name
}

func terminalEnv(shell string) []string {
	env := os.Environ()
	hasTerm := false
	hasShell := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=") {
			hasTerm = true
		}
		if strings.HasPrefix(entry, "SHELL=") {
			hasShell = true
		}
	}

	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasShell {
		env = append(env, "SHELL="+shell)
	}

	return env
}

func parseTerminalSignal(name string) (syscall.Signal, bool) {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "INT", "SIGINT":
		return syscall.SIGINT, true
	case "TERM", "SIGTERM":
		return syscall.SIGTERM, true
	case "HUP", "SIGHUP":
		return syscall.SIGHUP, true
	case "QUIT", "SIGQUIT":
		return syscall.SIGQUIT, true
	default:
		return 0, false
	}
}

func terminalOriginAllowed(r *http.Request) bool {
	return serverTerminalOriginAllowed(nil, r)
}

func (s *Server) terminalOriginAllowed(r *http.Request) bool {
	return serverTerminalOriginAllowed(s, r)
}

func serverTerminalOriginAllowed(s *Server, r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	if s != nil && s.corsOriginAllowed(origin) {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}

	originHost := normalizedHostPort(originURL.Host)
	requestHost := normalizedHostPort(r.Host)
	if originHost == "" || requestHost == "" {
		return false
	}

	return originHost == requestHost
}

func normalizedHostPort(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(trimmed)
	if err == nil {
		return net.JoinHostPort(normalizedHostname(host), port)
	}

	if ip := net.ParseIP(strings.Trim(trimmed, "[]")); ip != nil {
		return strings.ToLower(ip.String())
	}

	return normalizedHostname(trimmed)
}

func normalizedHostname(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "[]"))
}
