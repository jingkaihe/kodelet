package chat

import (
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/dbtest"
	"github.com/jingkaihe/kodelet/pkg/llm"
	"github.com/jingkaihe/kodelet/pkg/steer"
	"github.com/stretchr/testify/require"
)

// openTestStorage returns thread storage on the migrated database under
// KODELET_BASE_PATH, so the code under test and the test's own fixtures share
// it. Tests must point KODELET_BASE_PATH at a temporary directory first.
func openTestStorage(t *testing.T) llm.Storage {
	t.Helper()
	require.NotEmpty(t, os.Getenv("KODELET_BASE_PATH"), "tests must not open the user's database")
	path, err := db.DefaultDBPath()
	require.NoError(t, err)
	database := dbtest.Open(t, path)
	return llm.Storage{
		Conversations: conversations.NewConversationStore(database),
		Steering:      steer.NewStore(database),
	}
}
