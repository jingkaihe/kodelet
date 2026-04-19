package webui

import (
	"context"
	"encoding/json"
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

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

const (
	defaultTerminalRows = 28
	defaultTerminalCols = 100
	maxTerminalRows     = 400
	maxTerminalCols     = 400
	terminalWriteWait   = 10 * time.Second
	terminalPongWait    = 30 * time.Second
	terminalPingPeriod  = 20 * time.Second
	terminalReadLimit   = 64 * 1024
)

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return terminalOriginAllowed(r)
	},
}

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Code int    `json:"code,omitempty"`
	CWD  string `json:"cwd,omitempty"`
	Name string `json:"name,omitempty"`
	Git  bool   `json:"git,omitempty"`
	PID  int    `json:"pid,omitempty"`
	Text string `json:"text,omitempty"`
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
	resolvedCWD, err := s.resolveRequestedCWD(r.URL.Query().Get("cwd"))
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "invalid cwd", err)
		return
	}

	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.G(r.Context()).WithError(err).Warn("failed to upgrade terminal websocket")
		return
	}

	ctx, cancel := context.WithCancel(s.chatExecutionContext(r.Context()))
	defer cancel()

	rows := boundedTerminalRows(parseTerminalDimension(r.URL.Query().Get("rows")))
	cols := boundedTerminalCols(parseTerminalDimension(r.URL.Query().Get("cols")))

	shell, shellName := resolveTerminalShell()
	cmd := exec.CommandContext(ctx, shell)
	cmd.Dir = resolvedCWD
	cmd.Env = terminalEnv(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		_ = conn.Close()
		logger.G(r.Context()).WithError(err).Warn("failed to start terminal pty")
		return
	}
	defer func() { _ = ptmx.Close() }()

	writer := &websocketWriter{conn: conn}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(terminalReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalPongWait))
	})

	gitRepo := false
	if _, gitErr := resolveGitRoot(r.Context(), resolvedCWD); gitErr == nil {
		gitRepo = true
	}

	if err := writer.writeJSON(terminalMessage{
		Type: "ready",
		CWD:  resolvedCWD,
		Name: shellName,
		Git:  gitRepo,
		PID:  cmd.Process.Pid,
	}); err != nil {
		return
	}

	errCh := make(chan error, 2)
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			cancel()
			_ = ptmx.Close()
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
			}
		})
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if writeErr := writer.Write(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					errCh <- writeErr
					return
				}
			}
			if readErr != nil {
				errCh <- readErr
				return
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(terminalPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writer.Write(websocket.PingMessage, nil); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	go func() {
		waitErr := cmd.Wait()
		code := 0
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				logger.G(r.Context()).WithError(waitErr).Warn("terminal process ended with error")
			}
		}
		_ = writer.writeJSON(terminalMessage{Type: "exit", Code: code})
		errCh <- waitErr
	}()

	for {
		select {
		case asyncErr := <-errCh:
			closeAll()
			if asyncErr != nil && !errors.Is(asyncErr, os.ErrClosed) {
				var closeErr *websocket.CloseError
				if !errors.As(asyncErr, &closeErr) {
					logger.G(r.Context()).WithError(asyncErr).Debug("terminal session closed")
				}
			}
			return
		default:
		}

		messageType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			closeAll()
			return
		}

		switch messageType {
		case websocket.BinaryMessage:
			if _, err := ptmx.Write(payload); err != nil {
				closeAll()
				return
			}
		case websocket.TextMessage:
			var message terminalMessage
			if err := json.Unmarshal(payload, &message); err != nil {
				continue
			}

			switch message.Type {
			case "input":
				if message.Data == "" {
					continue
				}
				if _, err := ptmx.Write([]byte(message.Data)); err != nil {
					closeAll()
					return
				}
			case "resize":
				if err := pty.Setsize(ptmx, &pty.Winsize{
					Rows: uint16(boundedTerminalRows(message.Rows)),
					Cols: uint16(boundedTerminalCols(message.Cols)),
				}); err != nil {
					logger.G(r.Context()).WithError(err).Warn("failed to resize terminal pty")
				}
			case "signal":
				if cmd.Process == nil {
					continue
				}
				if sig, ok := parseTerminalSignal(message.Name); ok {
					_ = syscall.Kill(-cmd.Process.Pid, sig)
				}
			}
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
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}

	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}

	originHost := normalizedHost(originURL.Host)
	requestHost := normalizedHost(r.Host)
	if originHost == "" || requestHost == "" {
		return false
	}

	if originHost == requestHost {
		return true
	}

	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func normalizedHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		return strings.ToLower(strings.Trim(host, "[]"))
	}

	if strings.Contains(err.Error(), "missing port in address") {
		return strings.ToLower(strings.Trim(trimmed, "[]"))
	}

	return strings.ToLower(strings.Trim(trimmed, "[]"))
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
