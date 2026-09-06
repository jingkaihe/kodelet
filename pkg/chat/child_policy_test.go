package chat

import (
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/agentenv"
	"github.com/jingkaihe/kodelet/pkg/delegation"
	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type childPolicyThread struct {
	*fakeMetadataThread
	config llmtypes.Config
}

func (t *childPolicyThread) GetConfig() llmtypes.Config { return t.config }

func TestChildPolicyUsesExplicitCeilingsNotPresentation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	readTools := []string{"file_read", "grep_tool", "glob_tool"}
	for _, tt := range []struct {
		name     string
		policy   *llmtypes.ExecutionOptions
		legacy   []string
		patch    any
		override *llmtypes.ExecutionOptions
		outside  bool
		wantErr  string
	}{
		{name: "default patch and search off"},
		{name: "permitted explicit tools", policy: &llmtypes.ExecutionOptions{AllowedTools: &readTools}},
		{name: "legacy allowlist", legacy: []string{"grep_tool", "glob_tool"}, wantErr: "child tools exceed parent policy: file_read"},
		{name: "legacy no tools", legacy: []string{tools.NoToolsMarker}, wantErr: "child tools exceed parent policy"},
		{name: "typed empty", policy: &llmtypes.ExecutionOptions{AllowedTools: new([]string{})}, wantErr: "child tools exceed parent policy"},
		{name: "typed disabled search", policy: &llmtypes.ExecutionOptions{EnableFSSearchTools: new(false)}, wantErr: "parent-disabled filesystem search"},
		{name: "no tools relaxation", policy: &llmtypes.ExecutionOptions{NoTools: new(true)}, override: &llmtypes.ExecutionOptions{NoTools: new(false)}, wantErr: "cannot relax parent noTools"},
		{name: "no skills relaxation", override: &llmtypes.ExecutionOptions{NoSkills: new(false)}, wantErr: "cannot relax parent noSkills"},
		{name: "no extensions relaxation", override: &llmtypes.ExecutionOptions{NoExtensions: new(false)}, wantErr: "cannot relax parent noExtensions"},
		{name: "turn limit relaxation", policy: &llmtypes.ExecutionOptions{MaxTurns: new(2)}, override: &llmtypes.ExecutionOptions{MaxTurns: new(3)}, wantErr: "maxTurns exceeds parent ceiling"},
		{name: "agent init permits", patch: readTools},
		{name: "late agent init disables", patch: []string{"grep_tool", "glob_tool"}, wantErr: "child tools exceed parent policy: file_read"},
		{name: "late agent init empty", patch: []any{}, wantErr: "child tools exceed parent policy"},
		{name: "late agent init nil slice", patch: []string(nil), wantErr: "child tools exceed parent policy"},
		{name: "late agent init malformed", patch: map[string]any{"file_read": true}, wantErr: "invalid parent agent.init tool policy"},
		{name: "late agent init malformed member", patch: []any{42}, wantErr: "invalid parent agent.init tool policy"},
		{name: "patch cannot widen typed policy", policy: &llmtypes.ExecutionOptions{AllowedTools: new([]string{})}, patch: readTools, wantErr: "child tools exceed parent policy"},
		{name: "outside workspace", outside: true, wantErr: "working directory must be inside the parent workspace"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := llmtypes.Config{Provider: "openai", Model: "gpt-4o", ReasoningEffort: "medium", ToolMode: llmtypes.ToolModePatch, AllowedTools: tt.legacy, ExecutionOptions: tt.policy}
			parent := &childPolicyThread{fakeMetadataThread: &fakeMetadataThread{metadata: map[string]any{}}, config: config}
			environment := agentenv.NewLocalEnvironment(workspace, nil)
			manifest, err := environment.Open(t.Context(), agentenv.RunSpec{Config: config})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, environment.Close(t.Context())) })
			if tt.policy == nil && tt.legacy == nil {
				assert.NotContains(t, manifest.ToolNames(), "file_read")
				assert.NotContains(t, manifest.ToolNames(), "grep_tool")
				assert.NotContains(t, manifest.ToolNames(), "glob_tool")
			}
			ctx := contextWithCentralChildren(t.Context(), parent, environment, ChatRequest{RunnerID: "runner"}, nil)
			// The closure exists before agent.init supplies its per-turn patch.
			parent.SetMetadataValue("allowed_tools", tt.patch)
			request := delegation.Request{Profile: "search", Message: "find implementation", CWD: workspace, Options: tt.override}
			if tt.outside {
				request.CWD = filepath.Dir(workspace)
			}
			preset := delegation.Preset{Profile: delegation.Profile{Name: "search", Options: &llmtypes.ExecutionOptions{AllowedTools: &readTools, EnableFSSearchTools: new(true), NoSkills: new(true), NoExtensions: new(true)}}}
			run, err := delegation.PrepareFromContext(ctx)(ctx, request, preset, delegation.Identity{})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, run)
			} else {
				require.NoError(t, err)
				require.NotNil(t, run)
			}
			assert.Equal(t, config, parent.config, "child admission must not mutate parent policy")
			assert.Zero(t, parent.sendCalls, "preparation does not execute a provider turn")
		})
	}
}

func TestChildPolicyRemainsNarrowForDescendants(t *testing.T) {
	parent := llmtypes.Config{Provider: "openai", Model: "gpt-4o", ReasoningEffort: "medium", ToolMode: llmtypes.ToolModePatch}
	preset := &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read", "grep_tool", "glob_tool"}), EnableFSSearchTools: new(true), NoSkills: new(true), NoExtensions: new(true)}
	child, err := childConfiguration(parent, preset, &llmtypes.ExecutionOptions{MaxTurns: new(5)})
	require.NoError(t, err)
	assert.Equal(t, new(5), child.ExecutionOptions.MaxTurns)
	for _, widening := range []*llmtypes.ExecutionOptions{
		{AllowedTools: new([]string{"bash"})}, {NoExtensions: new(false)}, {NoSkills: new(false)}, {MaxTurns: new(6)},
	} {
		_, err := childConfiguration(child, widening, nil)
		require.Error(t, err)
	}
	narrowed, err := childConfiguration(child, &llmtypes.ExecutionOptions{AllowedTools: new([]string{"file_read"}), MaxTurns: new(1)}, nil)
	require.NoError(t, err)
	assert.Equal(t, new([]string{"file_read"}), narrowed.ExecutionOptions.AllowedTools)
}
