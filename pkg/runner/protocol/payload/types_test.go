package payload

import (
	"encoding/json"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestDigestPinsSessionExtensionsWithoutTools(t *testing.T) {
	manifest := Manifest{SessionExtensionIDs: []string{"inline-1"}}
	digest, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	manifest.RunID = "another-run"
	reattached, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.Equal(t, digest, reattached)
	manifest.SessionExtensionIDs = nil
	withoutCallbacks, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	assert.NotEqual(t, digest, withoutCallbacks)
}

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

func TestExtensionCountWireCompatibilityAndDigestStability(t *testing.T) {
	for _, test := range []struct {
		name  string
		wire  string
		count *int
	}{
		{name: "legacy unknown", wire: `{}`},
		{name: "known zero", wire: `{"extensionCount":0}`, count: new(0)},
		{name: "initialized", wire: `{"extensionCount":3}`, count: new(3)},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := Manifest{WorkingDirectory: "/workspace", Tools: []ToolDefinition{{Name: "file_read"}}}
			fullDigest, err := ComputeManifestDigest(manifest)
			require.NoError(t, err)
			discoveryDigest, err := ComputeDiscoveryDigest(manifest)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal([]byte(test.wire), &manifest))
			withCount, err := ComputeManifestDigest(manifest)
			require.NoError(t, err)
			assert.Equal(t, fullDigest, withCount)
			withCount, err = ComputeDiscoveryDigest(manifest)
			require.NoError(t, err)
			assert.Equal(t, discoveryDigest, withCount)
			assert.Equal(t, test.count, manifest.ExtensionCount, "hashing must not mutate advisory metadata")

			var discovery protocol.WorkspaceDiscoverResult
			require.NoError(t, json.Unmarshal([]byte(test.wire), &discovery))
			assert.Equal(t, test.count, discovery.ExtensionCount)
			for _, value := range []any{manifest, discovery} {
				data, err := json.Marshal(value)
				require.NoError(t, err)
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(data, &fields))
				if test.count == nil {
					assert.NotContains(t, fields, "extensionCount")
				} else {
					require.Contains(t, fields, "extensionCount", "known zero must survive the wire round trip")
					var count int
					require.NoError(t, json.Unmarshal(fields["extensionCount"], &count))
					assert.Equal(t, *test.count, count)
				}
			}
		})
	}
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

func TestDiscoveryDigestIgnoresToolAvailabilityButPinsPolicyAndRegistrations(t *testing.T) {
	manifest := Manifest{
		WorkingDirectory: "/workspace",
		Config:           EnvironmentConfig{Options: &llmtypes.ExecutionOptions{NoSkills: new(true)}},
		Shortcuts:        []protocol.ShortcutDescriptor{{Key: "ctrl+alt+r", ExtensionID: "dictate", Description: "Start dictation", Generation: 1}},
		Tools:            []ToolDefinition{{Name: "view_image", Description: "Model-specific image tool"}},
	}
	digest, err := ComputeDiscoveryDigest(manifest)
	require.NoError(t, err)
	fullDigest, err := ComputeManifestDigest(manifest)
	require.NoError(t, err)
	execution := manifest
	execution.Tools = []ToolDefinition{{Name: "view_image", Description: "Original detail supported"}, {Name: "spawn_agent"}}
	execution.Shortcuts = []protocol.ShortcutDescriptor{{Key: "ctrl+alt+r", ExtensionID: "dictate", Description: "Start dictation", Generation: 2}}
	executionDigest, err := ComputeDiscoveryDigest(execution)
	require.NoError(t, err)
	assert.Equal(t, digest, executionDigest)
	executionFullDigest, err := ComputeManifestDigest(execution)
	require.NoError(t, err)
	assert.NotEqual(t, fullDigest, executionFullDigest)
	require.Len(t, manifest.Tools, 1, "computing the discovery digest must not mutate the snapshot")
	assert.Equal(t, uint64(1), manifest.Shortcuts[0].Generation)

	for name, change := range map[string]func(*Manifest){
		"working directory": func(m *Manifest) { m.WorkingDirectory = "/other" },
		"tool permissions": func(m *Manifest) {
			m.Config.Options = &llmtypes.ExecutionOptions{NoSkills: new(true), AllowedTools: &[]string{}}
		},
		"command permissions": func(m *Manifest) { m.Config.AllowedCommands = []string{"git status"} },
		"disabled extensions": func(m *Manifest) {
			m.Config.Options = &llmtypes.ExecutionOptions{NoSkills: new(true), NoExtensions: new(true)}
		},
		"disabled tools": func(m *Manifest) {
			m.Config.Options = &llmtypes.ExecutionOptions{NoSkills: new(true), NoTools: new(true)}
		},
		"skills": func(m *Manifest) { m.Config.Options = &llmtypes.ExecutionOptions{} },
		"shortcut owner": func(m *Manifest) {
			m.Shortcuts = []protocol.ShortcutDescriptor{{Key: "ctrl+alt+r", ExtensionID: "replacement", Description: "Start dictation"}}
		},
		"shortcut key": func(m *Manifest) {
			m.Shortcuts = []protocol.ShortcutDescriptor{{Key: "ctrl+r", ExtensionID: "dictate", Description: "Start dictation"}}
		},
		"shortcut description": func(m *Manifest) {
			m.Shortcuts = []protocol.ShortcutDescriptor{{Key: "ctrl+alt+r", ExtensionID: "dictate", Description: "Changed action"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := manifest
			change(&changed)
			changedDigest, err := ComputeDiscoveryDigest(changed)
			require.NoError(t, err)
			assert.NotEqual(t, digest, changedDigest)
		})
	}
}
