package acp

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/acp/acptypes"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerLocalSessionsUseInjectedDatabase(t *testing.T) {
	restoreViperExtensionsForTest(t)
	basePath := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", basePath)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	database := dbtest.OpenTemp(t)
	server := NewServer(
		WithOutput(bytes.NewBuffer(nil)),
		WithContext(context.Background()),
		WithConfig(&ServerConfig{Provider: "anthropic", Model: "claude-test", NoSkills: true, NoExtensions: true}),
		WithDatabase(database),
	)
	t.Cleanup(server.Shutdown)

	session, err := server.sessionManager.NewSession(t.Context(), acptypes.NewSessionRequest{CWD: t.TempDir()})
	require.NoError(t, err)
	require.True(t, session.Thread.IsPersisted())
	require.NoError(t, session.Thread.SaveConversation(t.Context()))
	_, err = conversations.NewConversationStore(database).Load(t.Context(), string(session.ID))
	require.NoError(t, err, "local sessions must persist through the injected database")
	require.NoError(t, server.SendUpdate(session.ID, map[string]any{"sessionUpdate": "agent_message_chunk"}))
	assert.NoFileExists(t, filepath.Join(basePath, "storage.db"), "the ACP server must not open a database of its own")
}

func TestServerLocalSessionsRequireDatabaseToPersist(t *testing.T) {
	restoreViperExtensionsForTest(t)
	basePath := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KODELET_BASE_PATH", basePath)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	server := NewServer(
		WithOutput(bytes.NewBuffer(nil)),
		WithContext(context.Background()),
		WithConfig(&ServerConfig{Provider: "anthropic", Model: "claude-test", NoSkills: true, NoExtensions: true}),
	)
	t.Cleanup(server.Shutdown)

	_, err := server.sessionManager.NewSession(t.Context(), acptypes.NewSessionRequest{CWD: t.TempDir()})
	require.ErrorContains(t, err, "conversation storage is unavailable")
	assert.Nil(t, server.sessionStorage)
	assert.NoFileExists(t, filepath.Join(basePath, "storage.db"), "the ACP server must not open a database of its own")
}
