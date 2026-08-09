package payload

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeManifestDigestIgnoresRunIdentityAndDetectsContentChanges(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion:     1,
		RunnerID:            "runner-one",
		RunID:               "run-one",
		Generation:          3,
		Digest:              "sha256:previous",
		WorkingDirectory:    "/work/project",
		ContextFiles:        []ContextFile{{Path: "AGENTS.md", Content: "rules", Digest: "sha256:rules"}},
		Tools:               []ToolDefinition{{Name: "file_read", Description: "Read files", InputSchema: map[string]any{"type": "object"}, Placement: "environment"}},
		Config:              EnvironmentConfig{AllowedCommands: []string{"go test ./..."}},
		ExtensionGeneration: 9,
		Capabilities:        EnvironmentCapabilities{ToolUpdates: true, Commands: true},
	}

	digest, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, digest)

	identityChange := manifest
	identityChange.RunnerID = "runner-two"
	identityChange.RunID = "run-two"
	identityChange.Generation = 10
	identityChange.Digest = "sha256:other"
	identityChange.ExtensionGeneration = 42
	identityDigest, err := ComputeManifestDigest(identityChange)
	require.NoError(t, err)
	assert.Equal(t, digest, identityDigest)

	contentChange := manifest
	contentChange.ContextFiles = []ContextFile{{Path: "AGENTS.md", Content: "new rules", Digest: "sha256:new-rules"}}
	contentDigest, err := ComputeManifestDigest(contentChange)
	require.NoError(t, err)
	assert.NotEqual(t, digest, contentDigest)
}

func TestComputeManifestDigestReportsUnserializableManifest(t *testing.T) {
	_, err := ComputeManifestDigest(Manifest{Tools: []ToolDefinition{{
		Name:        "invalid",
		InputSchema: map[string]any{"callback": func() {}},
	}}})

	require.ErrorContains(t, err, "failed to encode runner manifest")
}
