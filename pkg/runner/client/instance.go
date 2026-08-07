package client

import (
	"context"
	"strings"

	"github.com/pkg/errors"
)

// ExecutionInstanceSpec identifies one run or idle manifest probe that needs runner-side resources.
type ExecutionInstanceSpec struct {
	RunID          string
	ConversationID string
	Probe          bool
}

// ExecutionInstance is the concrete filesystem/process/network backing for one runner environment.
// The direct-workspace implementation is not isolated; future providers can provision ephemeral backing.
type ExecutionInstance interface {
	WorkingDirectory() string
	Close(ctx context.Context) error
}

// ExecutionInstanceProvider creates the backing used before runner configuration, extensions, and tools are discovered.
type ExecutionInstanceProvider interface {
	Create(ctx context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error)
}

// DirectWorkspaceInstanceProvider creates per-run handles backed by the runner's registered workspace.
// It provides lifecycle symmetry for future providers but no filesystem, process, network, or port isolation.
type DirectWorkspaceInstanceProvider struct {
	workspace string
}

// NewDirectWorkspaceInstanceProvider creates the initial non-isolating execution-instance provider.
func NewDirectWorkspaceInstanceProvider(workspace string) (*DirectWorkspaceInstanceProvider, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("runner workspace is required")
	}
	return &DirectWorkspaceInstanceProvider{workspace: workspace}, nil
}

// Create returns a fresh lifecycle handle to the same direct workspace.
func (p *DirectWorkspaceInstanceProvider) Create(ctx context.Context, _ ExecutionInstanceSpec) (ExecutionInstance, error) {
	if p == nil || strings.TrimSpace(p.workspace) == "" {
		return nil, errors.New("direct workspace instance provider is not configured")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &directWorkspaceInstance{workspace: p.workspace}, nil
}

type directWorkspaceInstance struct {
	workspace string
}

func (i *directWorkspaceInstance) WorkingDirectory() string {
	if i == nil {
		return ""
	}
	return i.workspace
}

func (*directWorkspaceInstance) Close(context.Context) error { return nil }

var (
	_ ExecutionInstanceProvider = (*DirectWorkspaceInstanceProvider)(nil)
	_ ExecutionInstance         = (*directWorkspaceInstance)(nil)
)
