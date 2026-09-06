package client

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/pkg/errors"
)

type childToolKey struct{}

// ChildRequest binds extension requests to the host-observed process generation
// and active tool or retained lease. Callers cannot supply their own identity.
func (s *Service) ChildRequest(ctx context.Context, source extensions.UIExtensionSource, method string, raw json.RawMessage) (any, error) {
	owner, err := runnerBackgroundTaskOwner(source)
	if err != nil {
		return nil, err
	}
	var input struct {
		delegation.Request
		ChildID    string `json:"childId,omitempty"`
		ChildRunID string `json:"childRunId,omitempty"`
		After      uint64 `json:"after,omitempty"`
	}
	if err := delegation.Decode(raw, &input); err != nil {
		return nil, errors.Wrap(err, "invalid child request")
	}
	params := delegation.Params{
		RunID: runnerRunIDFromContext(ctx), ExtensionID: owner.ExtensionID, Generation: owner.Generation,
		Request: input.Request, ChildID: input.ChildID, ChildRunID: input.ChildRunID, LeaseID: input.LeaseID, After: input.After,
		Message: input.Message, RequestID: input.RequestID,
	}
	params.ToolCallID, _ = ctx.Value(childToolKey{}).(string)
	s.mu.Lock()
	var cwd string
	if params.LeaseID != "" {
		resources := s.backgroundLeases[params.LeaseID]
		if resources == nil || resources.leases[params.LeaseID].owner != owner {
			s.mu.Unlock()
			return nil, errors.New("child background lease is absent or owned by another extension")
		}
		if resources.leases[params.LeaseID].openingRunID != "" {
			s.mu.Unlock()
			return nil, errors.New("child background lease is provisional until run.open succeeds")
		}
		lease := resources.leases[params.LeaseID]
		if lease.releasing {
			s.mu.Unlock()
			return nil, errors.New("child background lease is being released")
		}
		if _, ok := resources.runIDs[params.RunID]; !ok {
			s.mu.Unlock()
			return nil, errors.New("child lease belongs to another run")
		}
		cwd = resources.workingDirectory
		// Includes uncertain starts: release must drain even if the caller
		// never received a child identity. Set this before dropping s.mu.
		lease.childAuthority = true
		resources.leases[params.LeaseID] = lease
	} else if run := s.runs[params.RunID]; run != nil && !run.opening && !run.closing && params.ToolCallID != "" {
		cwd = run.manifest.WorkingDirectory
	} else {
		s.mu.Unlock()
		return nil, errors.New("child requires an active tool or explicit background lease")
	}
	s.mu.Unlock()
	if method == delegation.StartMethod {
		if err := input.Validate(); err != nil {
			return nil, err
		}
		if input.CWD != "" {
			path := input.CWD
			if !filepath.IsAbs(path) {
				path = filepath.Join(cwd, path)
			}
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				return nil, errors.Wrap(err, "invalid child working directory")
			}
			rel, err := filepath.Rel(cwd, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, errors.New("child working directory exceeds parent workspace")
			}
			params.Request.CWD = path
		} else if input.Resume == "" {
			params.Request.CWD = cwd
		}
	}
	peer := s.currentPeer()
	if peer == nil {
		return nil, errors.New("central child execution is unavailable")
	}
	if params.LeaseID != "" {
		s.mu.Lock()
		resources := s.backgroundLeases[params.LeaseID]
		if resources == nil || resources.leases[params.LeaseID].owner != owner || resources.leases[params.LeaseID].releasing {
			s.mu.Unlock()
			return nil, errors.New("child lease was released before dispatch")
		}
		lease := resources.leases[params.LeaseID]
		if method == delegation.StartMethod && lease.childAuthorityRunID == "" {
			// Pin even an uncertain first send. A later reattachment's pending
			// handler is not the authority that created this retained grant.
			lease.childAuthorityRunID = params.RunID
			resources.leases[params.LeaseID] = lease
		}
		if lease.childAuthorityRunID != "" && (method != delegation.StartMethod || input.ContextMode != "fork") {
			if params.RunID != lease.childAuthorityRunID {
				params.ToolCallID = ""
			}
			params.RunID = lease.childAuthorityRunID
		}
		s.mu.Unlock()
	}
	if method == delegation.SteerMethod {
		var result delegation.SteerResult
		if err := peer.Call(ctx, method, params, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	var result delegation.Result
	if err := peer.Call(ctx, method, params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) releaseChildLease(runID, leaseID string, owner extensions.UIExtensionOwner) {
	// Never wait for a reverse RPC while holding the runner or process mutex.
	go func() {
		peer := s.currentPeer()
		if peer == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = peer.Call(ctx, delegation.ReleaseMethod, delegation.Params{RunID: runID, LeaseID: leaseID, ExtensionID: owner.ExtensionID, Generation: owner.Generation}, nil)
	}()
}
