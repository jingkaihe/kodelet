package tools

import "context"

// ToolAttachment declares a runner-local output or references an ingested artifact.
// Path is consumed during ingestion; ArtifactID and ShortCode are assigned by the
// control plane. ViewURL is presentation-only and is not persisted with history.
type ToolAttachment struct {
	Type       string `json:"type"`
	Path       string `json:"path,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	ShortCode  string `json:"shortCode,omitempty"`
	ViewURL    string `json:"viewUrl,omitempty"`
	Filename   string `json:"filename,omitempty"`
	MimeType   string `json:"mimeType,omitempty"`
	Alt        string `json:"alt,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ArtifactResolver authorizes an image reference against the current tool's conversation.
// It returns metadata only; image bytes are materialized by the control plane.
type ArtifactResolver func(context.Context, string) (ToolAttachment, error)

type artifactResolverKey struct{}

// ContextWithArtifactResolver binds artifact access to a tool execution.
func ContextWithArtifactResolver(ctx context.Context, resolver ArtifactResolver) context.Context {
	return context.WithValue(ctx, artifactResolverKey{}, resolver)
}

// ArtifactResolverFromContext returns the resolver for this tool execution, if available.
func ArtifactResolverFromContext(ctx context.Context) ArtifactResolver {
	resolver, _ := ctx.Value(artifactResolverKey{}).(ArtifactResolver)
	return resolver
}
