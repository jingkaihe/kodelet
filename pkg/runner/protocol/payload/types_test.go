package payload

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeManifestDigestIgnoresExistingDigest(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion:     protocol.Version,
		RunnerID:            "runner-one",
		RunID:               "run-one",
		Generation:          1,
		ExtensionGeneration: 1,
		Tools: []ToolDefinition{{
			Name:        "bash",
			Description: "execute a command",
			InputSchema: map[string]any{"type": "object"},
			Placement:   "environment",
		}},
	}

	first, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	manifest.Digest = "stale"
	manifest.RunnerID = "runner-two"
	manifest.RunID = "run-two"
	manifest.Generation = 2
	manifest.ExtensionGeneration = 99
	second, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Regexp(t, `^sha256:[0-9a-f]{64}$`, first)
}

func TestShortcutManifestDigestIgnoresGenerationWithoutMutatingSnapshot(t *testing.T) {
	manifest := Manifest{Shortcuts: []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "review", Generation: 11}}}
	digest, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, uint64(11), manifest.Shortcuts[0].Generation)
	manifest.Shortcuts[0].Generation++
	reopened, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, digest, reopened, "isolated probe and lease processes share a stable registration digest")
	manifest.Shortcuts[0].ExtensionID = "replacement"
	replacement, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.NotEqual(t, digest, replacement, "same key from another extension is not the same registration")
}

func TestComputeManifestDigestIgnoresRunIdentityAndDetectsContentChanges(t *testing.T) {
	manifest := Manifest{
		ProtocolVersion:  1,
		RunnerID:         "runner-one",
		RunID:            "run-one",
		Generation:       3,
		Digest:           "sha256:previous",
		WorkingDirectory: "/work/project",
		ContextFiles:     []ContextFile{{Path: "AGENTS.md", Content: "rules", Digest: "sha256:rules"}},
		Tools:            []ToolDefinition{{Name: "file_read", Description: "Read files", InputSchema: map[string]any{"type": "object"}, Placement: "environment"}},
		Config: EnvironmentConfig{
			AllowedCommands: []string{"go test ./..."},
			SystemInformation: &llmtypes.SystemInformation{
				IsGitRepo: true,
				Platform:  "darwin",
				OSVersion: "macOS 26.0",
				Date:      "2026-08-09",
			},
		},
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

	systemChange := manifest
	systemChange.Config.SystemInformation = manifest.Config.SystemInformation.Clone()
	systemChange.Config.SystemInformation.Platform = "linux"
	systemDigest, err := ComputeManifestDigest(systemChange)
	require.NoError(t, err)
	assert.Equal(t, digest, systemDigest)
}

func TestComputeManifestDigestReportsUnserializableManifest(t *testing.T) {
	_, err := ComputeManifestDigest(Manifest{Tools: []ToolDefinition{{
		Name:        "invalid",
		InputSchema: map[string]any{"callback": func() {}},
	}}})

	require.ErrorContains(t, err, "failed to encode runner manifest")
}
