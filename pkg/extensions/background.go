package extensions

import (
	"context"
)

const (
	// BackgroundTaskAcquireMethod acquires a host-owned lifetime lease for work
	// that must continue after the originating extension request returns.
	BackgroundTaskAcquireMethod = "kodelet.runtime.background.acquire"
	// BackgroundTaskReleaseMethod releases a previously acquired lifetime lease.
	BackgroundTaskReleaseMethod = "kodelet.runtime.background.release"
)

// BackgroundTaskAcquireRequest describes one extension-owned background task.
type BackgroundTaskAcquireRequest struct {
	Description string `json:"description,omitempty"`
}

// BackgroundTaskAcquireResponse identifies an explicit host lifetime lease.
// An empty lease ID means the host already provides a persistent lifetime.
type BackgroundTaskAcquireResponse struct {
	LeaseID string `json:"leaseId,omitempty"`
}

// BackgroundTaskReleaseRequest identifies a host lifetime lease to release.
type BackgroundTaskReleaseRequest struct {
	LeaseID string `json:"leaseId"`
}

// BackgroundTaskReleaseResponse reports whether the lease was still active.
type BackgroundTaskReleaseResponse struct {
	Released bool `json:"released"`
	// AfterResponse defers final host cleanup until the release response has been written.
	AfterResponse func() `json:"-"`
}

func (r BackgroundTaskReleaseResponse) afterRPCResponse() {
	if r.AfterResponse != nil {
		r.AfterResponse()
	}
}

// BackgroundTaskHost keeps extension runtime resources alive while leases exist.
// CleanupBackgroundTasks releases leases owned by a failed or closed extension
// process generation.
type BackgroundTaskHost interface {
	AcquireBackgroundTask(ctx context.Context, source UIExtensionSource, request BackgroundTaskAcquireRequest) (BackgroundTaskAcquireResponse, error)
	ReleaseBackgroundTask(ctx context.Context, source UIExtensionSource, request BackgroundTaskReleaseRequest) (BackgroundTaskReleaseResponse, error)
	CleanupBackgroundTasks(owner UIExtensionOwner)
}

type backgroundTaskHostContextKey struct{}

// ContextWithBackgroundTaskHost attaches host-owned background lifetime support.
func ContextWithBackgroundTaskHost(ctx context.Context, host BackgroundTaskHost) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if host == nil {
		return ctx
	}
	return context.WithValue(ctx, backgroundTaskHostContextKey{}, host)
}

// BackgroundTaskHostFromContext returns the attached background lifetime host.
func BackgroundTaskHostFromContext(ctx context.Context) (BackgroundTaskHost, bool) {
	if ctx == nil {
		return nil, false
	}
	host, ok := ctx.Value(backgroundTaskHostContextKey{}).(BackgroundTaskHost)
	return host, ok && host != nil
}
