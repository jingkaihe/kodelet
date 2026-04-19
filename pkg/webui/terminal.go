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

type terminalSocketRead struct {
	MessageType int
	Payload     []byte
	Err         error
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
			_ = writer.writeJSON(terminalMessage{Type: "exit", Code: code})
			return
		case asyncErr := <-attachment.errCh:
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
