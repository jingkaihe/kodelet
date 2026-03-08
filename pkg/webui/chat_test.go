package webui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/tools"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildChatState_BindsTodoPathToConversationID(t *testing.T) {
	customManager, err := tools.NewCustomToolManager()
	require.NoError(t, err)

	state, err := buildChatState(
		context.Background(),
		llmtypes.Config{
			DisableSubagent: true,
		},
		"conv-web-123",
		nil,
		customManager,
	)
	require.NoError(t, err)

	todoPath, err := state.TodoFilePath()
	require.NoError(t, err)

	assert.Equal(t, "conv-web-123.json", filepath.Base(todoPath))
	assert.Equal(t, "todos", filepath.Base(filepath.Dir(todoPath)))
}
