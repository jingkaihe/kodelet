package payload

import tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"

const (
	// MethodArtifactUpload authorizes a bounded HTTP upload for an active tool.
	MethodArtifactUpload = "artifact.upload.begin"
	// MethodArtifactResolve resolves an image in the active tool's conversation.
	MethodArtifactResolve = "artifact.resolve"
	// ArtifactUploadPath receives bytes using a short-lived upload ticket.
	ArtifactUploadPath = "/api/runner/v1/artifacts/upload"
	// MaxToolAttachments limits image outputs per completed tool call.
	MaxToolAttachments = 8
	// MaxArtifactBytes bounds streamed image uploads.
	MaxArtifactBytes = 32 * 1024 * 1024
)

// ArtifactRequest identifies an operation owned by one active runner tool call.
type ArtifactRequest struct {
	RunID      string                   `json:"runId"`
	ToolCallID string                   `json:"toolCallId"`
	ArtifactID string                   `json:"artifactId,omitempty"`
	Attachment tooltypes.ToolAttachment `json:"attachment,omitempty"`
}

// ArtifactUploadGrant is a one-use upload capability, not an image viewing link.
type ArtifactUploadGrant struct {
	Token string `json:"token"`
}
