package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	runnerregistry "github.com/jingkaihe/kodelet/pkg/runner/registry"
)

type artifactUploadTicket struct {
	identity runnerregistry.UIRequestIdentity
	request  runnerpayload.ArtifactRequest
	owner    context.Context
	expires  time.Time
	used     bool
}

type artifactUploadManager struct {
	mu      sync.Mutex
	tickets map[string]*artifactUploadTicket
}

// HandleRunnerArtifactRequest is reachable only through a registered runner session.
func (s *Server) HandleRunnerArtifactRequest(ctx context.Context, identity runnerregistry.UIRequestIdentity, method string, raw json.RawMessage) (any, *protocol.RPCError) {
	if s.artifacts == nil || s.runnerRegistry == nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "artifact storage is unavailable"}
	}
	var request runnerpayload.ArtifactRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "invalid artifact request"}
	}
	owner, conversationID, err := s.runnerRegistry.ArtifactToolContext(identity, request.RunID, request.ToolCallID)
	if err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeStale, Message: err.Error()}
	}
	if method == runnerpayload.MethodArtifactResolve {
		attachment, _, err := s.artifacts.Get(ctx, conversationID, request.ArtifactID)
		if err != nil {
			return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "image artifact is not available in this conversation"}
		}
		attachment.ViewURL = s.imageViewURL(attachment.ShortCode)
		return attachment, nil
	}
	if method != runnerpayload.MethodArtifactUpload || request.Attachment.Type != "image" || request.Attachment.ArtifactID != "" {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "expected a new image attachment"}
	}
	if len(request.Attachment.Alt) > 4096 || len(request.Attachment.Filename) > 255 {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInvalidParams, Message: "image attachment metadata is too large"}
	}
	manager := &s.artifactUploads
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.tickets == nil {
		manager.tickets = make(map[string]*artifactUploadTicket)
	}
	now := time.Now()
	count := 0
	for token, ticket := range manager.tickets {
		if ticket.owner.Err() != nil || now.After(ticket.expires) {
			delete(manager.tickets, token)
			continue
		}
		if ticket.request.RunID == request.RunID && ticket.request.ToolCallID == request.ToolCallID {
			count++
		}
	}
	if count >= runnerpayload.MaxToolAttachments || len(manager.tickets) >= 1024 {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeUnavailable, Message: "image upload limit reached"}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, &protocol.RPCError{Code: protocol.ErrorCodeInternal, Message: "failed to create image upload ticket"}
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	manager.tickets[token] = &artifactUploadTicket{identity: identity, request: request, owner: owner, expires: now.Add(2 * time.Minute)}
	return runnerpayload.ArtifactUploadGrant{Token: token}, nil
}

func (s *Server) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	// This endpoint has its own single-use capability authentication. It does not
	// accept browser cookies, administrative tokens, or arbitrary runner credentials.
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	manager := &s.artifactUploads
	manager.mu.Lock()
	ticket := manager.tickets[token]
	valid := ok && scheme == "Bearer" && ticket != nil && !ticket.used && ticket.owner.Err() == nil && time.Now().Before(ticket.expires)
	if valid {
		ticket.used = true
	}
	manager.mu.Unlock()
	if !valid || s.artifacts == nil || s.runnerRegistry == nil {
		s.writeErrorResponse(w, http.StatusUnauthorized, "invalid or expired image upload ticket", nil)
		return
	}
	owner, conversationID, err := s.runnerRegistry.ArtifactToolContext(ticket.identity, ticket.request.RunID, ticket.request.ToolCallID)
	if err != nil {
		s.writeErrorResponse(w, http.StatusUnauthorized, "image upload owner is no longer active", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Until(ticket.expires))
	stop := context.AfterFunc(owner, cancel)
	defer stop()
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, runnerpayload.MaxArtifactBytes)
	// Body.Close cannot interrupt a blocked HTTP/1 Read. A socket/stream deadline
	// bounds stalled uploads and advances immediately when the owning tool ends.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(ticket.expires); err != nil {
		s.writeErrorResponse(w, http.StatusInternalServerError, "image upload deadlines are unavailable", err)
		return
	}
	readStopped := make(chan struct{})
	stopRead := context.AfterFunc(ctx, func() {
		_ = controller.SetReadDeadline(time.Now())
		close(readStopped)
	})
	defer func() {
		if !stopRead() {
			<-readStopped // Do not use the response writer after this handler returns.
		}
	}()
	attachment, err := s.artifacts.Put(ctx, conversationID, ticket.request.ToolCallID, ticket.request.Attachment, r.Body)
	if err != nil {
		s.writeErrorResponse(w, http.StatusBadRequest, "image upload could not be stored", err)
		return
	}
	attachment.ViewURL = s.imageViewURL(attachment.ShortCode)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(attachment)
}
