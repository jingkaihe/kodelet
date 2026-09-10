package sqlite

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/artifacts"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactConversationLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	setupTestDB(t, dbPath)
	store, err := NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	defer store.Close()
	images, err := artifacts.Open(t.Context(), dbPath)
	require.NoError(t, err)
	defer images.Close()
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	parent := convtypes.NewConversationRecord("parent")
	parent.Provider = "openai"
	attachment, err := images.Put(t.Context(), parent.ID, "generate", tooltypes.ToolAttachment{Type: "image", Filename: "generated.png"}, bytes.NewReader(imageData.Bytes()))
	require.NoError(t, err)
	attachment.ViewURL = "https://old.example/i/" + attachment.ShortCode

	// A checkpoint without the completed result must retain the uploaded reference.
	require.NoError(t, store.Save(t.Context(), parent))
	_, path, err := images.Get(t.Context(), parent.ID, attachment.ArtifactID)
	require.NoError(t, err)
	parent.ToolResults["generate"] = tooltypes.StructuredToolResult{ToolName: "generate", Success: true, Attachments: []tooltypes.ToolAttachment{attachment}}
	require.NoError(t, store.Save(t.Context(), parent))
	loaded, err := store.Load(t.Context(), parent.ID)
	require.NoError(t, err)
	assert.Empty(t, loaded.ToolResults["generate"].Attachments[0].ViewURL)
	assert.Equal(t, attachment.ShortCode, loaded.ToolResults["generate"].Attachments[0].ShortCode)
	assert.Equal(t, attachment.ViewURL, parent.ToolResults["generate"].Attachments[0].ViewURL)

	storedFork := convtypes.ForkConversationRecord(loaded)
	require.NoError(t, store.SaveConversationFork(t.Context(), parent.ID, storedFork))
	liveFork := convtypes.ForkConversationRecord(parent)
	require.NoError(t, store.Save(t.Context(), liveFork))
	for _, child := range []string{storedFork.ID, liveFork.ID} {
		_, childPath, err := images.Get(t.Context(), child, attachment.ArtifactID)
		require.NoError(t, err)
		assert.Equal(t, path, childPath)
	}
	var count int
	require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM image_artifacts`))
	assert.Equal(t, 1, count)

	// The original short link and file survive deletion of their source conversation.
	require.NoError(t, store.Delete(t.Context(), parent.ID))
	_, _, err = images.Get(t.Context(), parent.ID, attachment.ArtifactID)
	assert.ErrorIs(t, err, artifacts.ErrNotFound)
	_, _, err = images.GetByShortCode(t.Context(), attachment.ShortCode)
	require.NoError(t, err)
	assert.FileExists(t, path)
	require.NoError(t, store.Delete(t.Context(), storedFork.ID))
	assert.FileExists(t, path)
	require.NoError(t, store.Delete(t.Context(), liveFork.ID))
	assert.NoFileExists(t, path)
	_, _, err = images.GetByShortCode(t.Context(), attachment.ShortCode)
	assert.ErrorIs(t, err, artifacts.ErrNotFound)
	require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM image_artifacts`))
	assert.Zero(t, count)
	require.NoError(t, store.db.Get(&count, `SELECT COUNT(*) FROM conversation_artifacts`))
	assert.Zero(t, count)
}

func TestArtifactReferencesRollbackWithConversation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "storage.db")
	setupTestDB(t, dbPath)
	store, err := NewStore(t.Context(), dbPath)
	require.NoError(t, err)
	defer store.Close()
	images, err := artifacts.Open(t.Context(), dbPath)
	require.NoError(t, err)
	defer images.Close()
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	attachment, err := images.Put(t.Context(), "parent", "call", tooltypes.ToolAttachment{Type: "image"}, &encoded)
	require.NoError(t, err)
	_, path, err := images.Get(t.Context(), "parent", attachment.ArtifactID)
	require.NoError(t, err)
	parent := convtypes.NewConversationRecord("parent")
	parent.Provider = "openai"
	parent.ToolResults["call"] = tooltypes.StructuredToolResult{Success: true, Attachments: []tooltypes.ToolAttachment{attachment}}
	require.NoError(t, store.Save(t.Context(), parent))

	_, err = store.db.Exec(`CREATE TRIGGER prevent_conversation_delete BEFORE DELETE ON conversations BEGIN SELECT RAISE(ABORT, 'forced failure'); END`)
	require.NoError(t, err)
	require.ErrorContains(t, store.Delete(t.Context(), "parent"), "forced failure")
	assert.FileExists(t, path)
	_, _, err = images.GetByShortCode(t.Context(), attachment.ShortCode)
	require.NoError(t, err)

	child := convtypes.ForkConversationRecord(parent)
	child.ToolResults["missing"] = tooltypes.StructuredToolResult{Attachments: []tooltypes.ToolAttachment{{Type: "image", ArtifactID: "missing"}}}
	require.Error(t, store.Save(t.Context(), child))
	_, err = store.Load(t.Context(), child.ID)
	assert.ErrorIs(t, err, convtypes.ErrConversationNotFound)
	_, _, err = images.Get(t.Context(), child.ID, attachment.ArtifactID)
	assert.ErrorIs(t, err, artifacts.ErrNotFound)

	_, err = store.db.Exec(`DROP TRIGGER prevent_conversation_delete`)
	require.NoError(t, err)
	require.NoError(t, store.Delete(t.Context(), "parent"))
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))

	// Removing an upload before any conversation checkpoint also releases its file.
	encoded.Reset()
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	pending, err := images.Put(t.Context(), "never-saved", "call", tooltypes.ToolAttachment{Type: "image"}, &encoded)
	require.NoError(t, err)
	_, path, err = images.Get(t.Context(), "never-saved", pending.ArtifactID)
	require.NoError(t, err)
	require.NoError(t, store.Delete(t.Context(), "never-saved"))
	assert.NoFileExists(t, path)
}
