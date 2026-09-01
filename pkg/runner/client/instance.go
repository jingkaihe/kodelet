package client

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/pkg/errors"
)

// ExecutionInstanceSpec identifies one run or idle manifest probe that needs runner-side resources.
type ExecutionInstanceSpec struct {
	RunID          string
	ConversationID string
	CWD            string
	Probe          bool
}

// ExecutionInstance is the concrete filesystem/process/network backing for one runner environment.
// The direct-workspace implementation is not isolated; future providers can provision ephemeral backing.
type ExecutionInstance interface {
	WorkingDirectory() string
	Close(ctx context.Context) error
}

// ExecutionInstanceProvider resolves and creates the backing used before runner configuration, extensions, and tools are discovered.
// Create must return an instance whose working directory exactly matches the canonical path returned by ResolveWorkingDirectory.
// ResolveWorkingDirectory should wrap ErrInvalidWorkingDirectory when the requested path is invalid.
type ExecutionInstanceProvider interface {
	ResolveWorkingDirectory(ctx context.Context, requestedCWD string) (string, error)
	Create(ctx context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error)
}

// ErrInvalidWorkingDirectory identifies a caller-provided working directory that cannot be used.
var ErrInvalidWorkingDirectory = errors.New("invalid runner working directory")

type invalidWorkingDirectoryError struct {
	err error
}

func (e *invalidWorkingDirectoryError) Error() string {
	if e == nil || e.err == nil {
		return "invalid runner working directory"
	}
	return e.err.Error()
}

func (e *invalidWorkingDirectoryError) Unwrap() error { return e.err }

func (*invalidWorkingDirectoryError) Is(target error) bool {
	return target == ErrInvalidWorkingDirectory
}

func invalidWorkingDirectory(err error) error {
	if err == nil {
		return nil
	}
	return &invalidWorkingDirectoryError{err: err}
}

// DirectWorkspaceInstanceProvider creates per-run handles backed by directories on the runner host.
// The registered workspace is the default; no filesystem, process, network, or port isolation is provided.
type DirectWorkspaceInstanceProvider struct {
	workspace string
}

// NewDirectWorkspaceInstanceProvider creates the initial non-isolating execution-instance provider.
func NewDirectWorkspaceInstanceProvider(workspace string) (*DirectWorkspaceInstanceProvider, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("runner workspace is required")
	}
	var err error
	workspace, err = conversations.NormalizeCWD(workspace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to resolve runner workspace")
	}
	return &DirectWorkspaceInstanceProvider{workspace: workspace}, nil
}

// Create returns a fresh lifecycle handle rooted at the requested host directory.
func (p *DirectWorkspaceInstanceProvider) Create(ctx context.Context, spec ExecutionInstanceSpec) (ExecutionInstance, error) {
	workingDirectory, err := p.ResolveWorkingDirectory(ctx, spec.CWD)
	if err != nil {
		return nil, err
	}
	return &directWorkspaceInstance{workspace: workingDirectory}, nil
}

// ResolveWorkingDirectory resolves a request using runner-host path semantics.
func (p *DirectWorkspaceInstanceProvider) ResolveWorkingDirectory(ctx context.Context, requestedCWD string) (string, error) {
	if p == nil || strings.TrimSpace(p.workspace) == "" {
		return "", errors.New("direct workspace instance provider is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	workingDirectory, err := resolveDirectWorkingDirectory(p.workspace, requestedCWD)
	if err != nil {
		return "", err
	}
	return workingDirectory, nil
}

func resolveDirectWorkingDirectory(workspace, requestedCWD string) (string, error) {
	requestedCWD = strings.TrimSpace(requestedCWD)
	if requestedCWD == "" {
		requestedCWD = workspace
	} else if strings.HasPrefix(requestedCWD, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", invalidWorkingDirectory(errors.Wrap(err, "failed to resolve runner home directory"))
		}
		switch {
		case requestedCWD == "~":
			requestedCWD = homeDir
		case strings.HasPrefix(requestedCWD, "~/"):
			requestedCWD = filepath.Join(homeDir, strings.TrimPrefix(requestedCWD, "~/"))
		default:
			return "", invalidWorkingDirectory(errors.New("runner working directory supports only ~ or ~/ paths"))
		}
	}
	if !filepath.IsAbs(requestedCWD) {
		requestedCWD = filepath.Join(workspace, requestedCWD)
	}
	workingDirectory, err := conversations.NormalizeCWD(requestedCWD)
	if err != nil {
		return "", invalidWorkingDirectory(errors.Wrap(err, "failed to resolve runner working directory"))
	}
	return workingDirectory, nil
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
