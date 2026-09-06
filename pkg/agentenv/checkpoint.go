package agentenv

import "context"

// RunCheckpoint persists admitted input after runner validation but before
// extension startup. It is process-local authority, never a runner credential.
type RunCheckpoint func(ctx context.Context, canonicalCWD string) error

type runCheckpointKey struct{}

// ContextWithRunCheckpoint installs the central checkpoint for one ordinary turn.
func ContextWithRunCheckpoint(ctx context.Context, checkpoint RunCheckpoint) context.Context {
	return context.WithValue(ctx, runCheckpointKey{}, checkpoint)
}

// RunCheckpointFromContext returns the central checkpoint, if this is an admitted turn.
func RunCheckpointFromContext(ctx context.Context) RunCheckpoint {
	checkpoint, _ := ctx.Value(runCheckpointKey{}).(RunCheckpoint)
	return checkpoint
}
