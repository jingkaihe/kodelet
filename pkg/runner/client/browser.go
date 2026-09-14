package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jingkaihe/kodelet/pkg/browser"
	"github.com/jingkaihe/kodelet/pkg/runner/controlplaneurl"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
)

const maxBrowserRelays = 8

type webBrowserRelay struct {
	cancel context.CancelFunc
	once   sync.Once
}

// BrowserManager returns the runner-owned browser resource, independent of agent runs.
func (s *Service) BrowserManager() *browser.Manager { return s.browserManager }

func (s *Service) handleBrowserRequest(ctx context.Context, method string, raw json.RawMessage) (any, *protocol.RPCError) {
	if s.browserManager == nil || !s.browserManager.Enabled() {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "browser support is not enabled on this runner"}
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return rpcResult(nil, errors.New("runner service is closed"))
	}
	if method == protocol.MethodWorkspaceBrowserAsset {
		params, rpcErr := decodeParams[protocol.WorkspaceBrowserAssetParams](raw)
		if rpcErr != nil {
			return nil, rpcErr
		}
		chunk, err := s.browserManager.ReadAsset(params.Path, params.Offset)
		return rpcResult(chunk, err)
	}
	if method == protocol.MethodWorkspaceBrowserConnect {
		params, rpcErr := decodeParams[protocol.WorkspaceBrowserConnectParams](raw)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return rpcResult(struct{}{}, s.connectBrowser(ctx, params))
	}
	params, rpcErr := decodeParams[protocol.WorkspaceBrowserParams](raw)
	if rpcErr != nil {
		return nil, rpcErr
	}
	cwd, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return rpcResult(nil, err)
	}
	if method == protocol.MethodWorkspaceBrowserStop {
		if strings.TrimSpace(params.SessionID) == "" {
			return rpcResult(nil, errors.New("browser session ID is required"))
		}
		return rpcResult(struct{}{}, s.browserManager.Stop(cwd, params.SessionID))
	}
	info, err := s.browserManager.Open(ctx, cwd)
	return rpcResult(info, err)
}

func (s *Service) connectBrowser(ctx context.Context, params protocol.WorkspaceBrowserConnectParams) error {
	if params.SessionID == "" || len(params.RelayToken) < 32 || len(params.RelayToken) > 256 {
		return errors.New("browser session ID and relay ticket are required")
	}
	endpoint, err := controlplaneurl.WebSocketEndpoint(s.artifactBaseURL, "api", "browser", "relay")
	if err != nil {
		return errors.Wrap(err, "invalid browser relay server")
	}
	cwd, err := s.instanceProvider.ResolveWorkingDirectory(ctx, params.CWD)
	if err != nil {
		return err
	}
	// Reserve before dialing. A disconnect cancels both pending and active attachments.
	relayCtx, cancel := context.WithCancel(s.ctx)
	relay := &webBrowserRelay{cancel: cancel}
	s.mu.Lock()
	if s.closed || s.peer == nil || len(s.browserRelays) >= maxBrowserRelays {
		s.mu.Unlock()
		cancel()
		return errors.New("browser relay unavailable or attachment limit reached")
	}
	s.browserRelays[relay] = struct{}{}
	s.mu.Unlock()
	succeeded := false
	defer func() {
		if !succeeded {
			s.releaseBrowserRelay(relay)
		}
	}()
	connectCtx, cancelConnect := context.WithTimeout(ctx, 20*time.Second)
	defer cancelConnect()
	stopCancellation := context.AfterFunc(relayCtx, cancelConnect)
	defer stopCancellation()
	stopRequestCancellation := context.AfterFunc(connectCtx, cancel)
	defer stopRequestCancellation()
	cdp, release, err := s.browserManager.Connect(relayCtx, cwd, params.SessionID)
	if err != nil {
		return err
	}
	proxy, response, err := websocket.DefaultDialer.DialContext(connectCtx, endpoint, http.Header{
		"Authorization": {"Bearer " + params.RelayToken},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		release()
		return errors.Wrap(err, "failed to connect browser relay")
	}
	if !stopRequestCancellation() || relayCtx.Err() != nil || connectCtx.Err() != nil {
		_ = proxy.Close()
		release()
		return errors.New("browser relay connection was canceled")
	}
	succeeded = true
	go func() {
		defer s.releaseBrowserRelay(relay)
		defer release()
		proxyBrowserSockets(relayCtx, proxy, cdp)
	}()
	return nil
}

func (s *Service) releaseBrowserRelay(relay *webBrowserRelay) {
	relay.once.Do(func() {
		relay.cancel()
		s.mu.Lock()
		delete(s.browserRelays, relay)
		s.mu.Unlock()
	})
}

func (s *Service) closeBrowserRelays() {
	s.mu.Lock()
	for relay := range s.browserRelays {
		relay.cancel()
	}
	s.mu.Unlock()
}

func proxyBrowserSockets(ctx context.Context, left, right *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer left.Close()
	defer right.Close()
	stop := context.AfterFunc(ctx, func() {
		_ = left.Close()
		_ = right.Close()
	})
	defer stop()
	left.SetReadLimit(16 * 1024 * 1024)
	right.SetReadLimit(16 * 1024 * 1024)
	var copies sync.WaitGroup
	copies.Add(2)
	copyMessages := func(dst, src *websocket.Conn) {
		defer copies.Done()
		defer cancel()
		for {
			kind, payload, err := src.ReadMessage()
			if err != nil || kind != websocket.TextMessage {
				return
			}
			if err := dst.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				return
			}
			if err := dst.WriteMessage(kind, payload); err != nil {
				return
			}
		}
	}
	go copyMessages(left, right)
	go copyMessages(right, left)
	copies.Wait()
}
