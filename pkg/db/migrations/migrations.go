// Package migrations contains all database migrations for kodelet.
// Migrations use Rails-style timestamp versioning (YYYYMMDDHHmmss).
package migrations

import (
	"github.com/jingkaihe/kodelet/pkg/db"
)

// All returns all registered migrations in the correct order.
// New migrations should be added to this list.
func All() []db.Migration {
	return []db.Migration{
		// Conversations migrations (originally versions 1-4)
		Migration20240101000001CreateConversations(),
		Migration20240101000002AddPerformanceIndexes(),
		Migration20240101000003AddProviderToSummaries(),
		Migration20240101000004AddBackgroundProcesses(),
		// ACP migrations
		Migration20240204000001CreateACPSessionUpdates(),
	}
}
