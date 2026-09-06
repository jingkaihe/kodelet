package extensions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/delegation"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionProfilesResolveAndSnapshotRunnerFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "search.md"), []byte("runner prompt"), 0o600))
	p := &Process{Extension: Extension{ID: "search", Dir: dir}}
	source := &processExtensionUISource{process: p, owner: UIExtensionOwner{ExtensionID: "search", Generation: 7}}
	p.uiSource = source
	profile := delegation.Profile{Name: "code_search", SystemPromptPath: "search.md", Options: &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read"})}}
	require.NoError(t, p.setProfiles(source, []delegation.Profile{profile}))
	runtime := &Runtime{processes: []*Process{p}}
	snapshot := runtime.Profiles()
	require.Len(t, snapshot, 1)
	assert.Equal(t, "runner prompt", snapshot[0].SystemPrompt)
	assert.Empty(t, snapshot[0].SystemPromptPath)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "search.md"), []byte("new prompt"), 0o600))
	(*profile.Options.AllowedTools)[0] = "bash"
	assert.Equal(t, []string{"file_read"}, *snapshot[0].Options.AllowedTools)
	require.NoError(t, p.setProfiles(source, []delegation.Profile{profile}))
	assert.Equal(t, "runner prompt", snapshot[0].SystemPrompt, "active child snapshots must not change")
	assert.Equal(t, "new prompt", runtime.Profiles()[0].SystemPrompt)
	require.Error(t, p.setProfiles(source, []delegation.Profile{profile, profile}))
	p.uiSource = &processExtensionUISource{owner: UIExtensionOwner{ExtensionID: "search", Generation: 8}}
	assert.Empty(t, runtime.Profiles(), "old registrations cannot survive process replacement")
	require.Error(t, p.setProfiles(source, []delegation.Profile{profile}))
	p.closed = true
	assert.Empty(t, runtime.Profiles())
}

func TestExecutionProfilesAreExtensionScoped(t *testing.T) {
	runtime := &Runtime{}
	for _, id := range []string{"one", "two"} {
		p := &Process{Extension: Extension{ID: id, Dir: t.TempDir()}}
		source := &processExtensionUISource{process: p, owner: UIExtensionOwner{ExtensionID: id, Generation: 1}}
		p.uiSource = source
		require.NoError(t, p.setProfiles(source, []delegation.Profile{{Name: "code_search", SystemPrompt: id}}))
		runtime.processes = append(runtime.processes, p)
	}
	profiles := runtime.Profiles()
	require.Len(t, profiles, 2)
	for _, profile := range profiles {
		assert.Equal(t, profile.ExtensionID, profile.SystemPrompt)
	}
}
