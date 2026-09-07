package registry

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	runnerpayload "github.com/jingkaihe/kodelet/pkg/runner/protocol/payload"
	"github.com/pkg/errors"
)

// ValidateSessionExtensionFrame fences a frame to the attachment pinned by run.open.
// Canceled/failed leases remain routable only until run.close finishes, allowing
// cancellation and bounded session.end replies through the ordinary RPC client.
func (r *Registry) ValidateSessionExtensionFrame(identity UIRequestIdentity, frame protocol.ExtensionFrame) (Run, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	run, _, err := r.sessionExtensionTargetLocked(identity, frame)
	if err != nil {
		return Run{}, err
	}
	return run.Run, nil
}

// DeliverSessionExtensionFrame delivers a client frame to precisely the runner
// connection that opened its channel, never to a reconnected generation.
func (r *Registry) DeliverSessionExtensionFrame(ctx context.Context, identity UIRequestIdentity, frame protocol.ExtensionFrame) error {
	r.mu.RLock()
	_, link, err := r.sessionExtensionTargetLocked(identity, frame)
	r.mu.RUnlock()
	if err != nil {
		return err
	}
	return link.Call(ctx, protocol.MethodSessionExtensionFrame, frame, new(struct{}))
}

func (r *Registry) sessionExtensionTargetLocked(identity UIRequestIdentity, frame protocol.ExtensionFrame) (*runEntry, Link, error) {
	run := r.runs[frame.RunID]
	if run == nil || run.RunnerID != identity.RunnerID || run.connectionID != identity.ConnectionID || run.generation != identity.Generation {
		return nil, nil, errors.New("session extension frame belongs to another runner generation or run")
	}
	attachment := run.sessionExtensions
	if attachment == nil || attachment.ID != frame.AttachmentID || !slices.Contains(attachment.ExtensionIDs, frame.ExtensionID) {
		return nil, nil, errors.New("session extension is not attached to this run")
	}
	// Transport closure is asynchronous; a final close notice can follow the
	// run.close acknowledgement. It conveys no execution or reverse-call authority.
	if frame.Close {
		runner, err := r.currentRunnerLocked(identity.RunnerID, identity.ConnectionID, identity.Generation)
		if err != nil {
			return nil, nil, err
		}
		return run, runner.link, nil
	}
	link, err := r.activeRunLinkLocked(frame.RunID, true)
	return run, link, err
}

// RequiredSessionExtensions returns the callback identities from the most recent
// pinned manifest. Live transports and attachment credentials are never persisted.
func (r *Registry) RequiredSessionExtensions(conversationID string) ([]string, error) {
	if store, ok := r.persistence.(interface {
		SessionExtensionRequirements(context.Context, string) ([]string, error)
	}); ok {
		return store.SessionExtensionRequirements(r.ctx, conversationID)
	}
	r.mu.RLock()
	var latest *runEntry
	for _, run := range r.runs {
		if run.ConversationID == conversationID && run.ManifestJSON != "" && (latest == nil || run.CreatedAt.After(latest.CreatedAt)) {
			latest = run
		}
	}
	var snapshot string
	if latest != nil {
		snapshot = latest.ManifestJSON
	}
	r.mu.RUnlock()
	if snapshot == "" {
		return nil, nil
	}
	var manifest runnerpayload.Manifest
	if err := json.Unmarshal([]byte(snapshot), &manifest); err != nil {
		return nil, errors.Wrap(err, "failed to read the saved session extension requirements")
	}
	return manifest.SessionExtensionIDs, nil
}
