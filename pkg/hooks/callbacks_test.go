package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type stubThread struct {
	config llmtypes.Config
	state  tooltypes.State
}

func (t *stubThread) SetState(s tooltypes.State) { t.state = s }
func (t *stubThread) GetState() tooltypes.State  { return t.state }
func (t *stubThread) AddUserMessage(_ context.Context, _ string, _ ...string) {
}

func (t *stubThread) SendMessage(_ context.Context, _ string, handler llmtypes.MessageHandler, _ llmtypes.MessageOpt) (string, error) {
	if handler != nil {
		handler.HandleText("ok")
		handler.HandleDone()
	}
	return "ok", nil
}
func (t *stubThread) GetUsage() llmtypes.Usage { return llmtypes.Usage{} }
func (t *stubThread) GetConversationID() string {
	return "callback-test"
}
func (t *stubThread) SetConversationID(_ string) {}
func (t *stubThread) SaveConversation(_ context.Context, _ bool) error {
	return nil
}
func (t *stubThread) IsPersisted() bool { return false }
func (t *stubThread) EnablePersistence(_ context.Context, _ bool) {
}
func (t *stubThread) Provider() string { return "stub" }
func (t *stubThread) GetMessages() ([]llmtypes.Message, error) {
	return nil, nil
}
func (t *stubThread) GetConfig() llmtypes.Config { return t.config }
func (t *stubThread) NewSubAgent(_ context.Context, config llmtypes.Config) llmtypes.Thread {
	return &stubThread{config: config}
}
func (t *stubThread) AggregateSubagentUsage(_ llmtypes.Usage) {}
func (t *stubThread) SetInvokedRecipe(_ string)               {}
func (t *stubThread) SetCallbackRegistry(_ interface{})       {}

func TestCallbackRegistry_ExecuteRecipe_AppliesAllowedCommands(t *testing.T) {
	tempDir := t.TempDir()
	recipePath := filepath.Join(tempDir, "test-recipe.md")
	recipeContent := `---
name: Test Recipe
allowed_tools:
  - bash
allowed_commands:
  - "ls *"
  - "echo *"
---
Do the thing.
`
	require.NoError(t, os.WriteFile(recipePath, []byte(recipeContent), 0o644))

	processor, err := fragments.NewFragmentProcessor(fragments.WithFragmentDirs(tempDir))
	require.NoError(t, err)

	var gotConfig llmtypes.Config
	registry := NewCallbackRegistry(processor, func(_ context.Context, config llmtypes.Config) (llmtypes.Thread, error) {
		gotConfig = config
		return &stubThread{config: config}, nil
	}, llmtypes.Config{
		AllowedCommands: []string{"pwd"},
	})

	result, err := registry.Execute(context.Background(), "test-recipe", nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []string{"ls *", "echo *"}, gotConfig.AllowedCommands)
	assert.Equal(t, []string{"bash"}, gotConfig.AllowedTools)
}
