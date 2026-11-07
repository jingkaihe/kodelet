package conversations

import (
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations/sqlite"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	convtypes "github.com/jingkaihe/kodelet/pkg/types/conversations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConversationStoreUsesCallersDatabase(t *testing.T) {
	database := dbtest.OpenTemp(t)
	store := NewConversationStore(database)
	require.IsType(t, &sqlite.Store{}, store)
	require.NoError(t, store.Save(t.Context(), convtypes.NewConversationRecord("shared")))

	// The caller owns the handle: closing the store must leave it usable.
	require.NoError(t, store.Close())
	var count int
	require.NoError(t, database.Reader.GetContext(t.Context(), &count, "SELECT COUNT(*) FROM conversations"))
	assert.Equal(t, 1, count)
}
