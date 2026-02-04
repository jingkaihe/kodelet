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
		Migration20260204163000CreateConversations(),
		Migration20260204163001AddPerformanceIndexes(),
		Migration20260204163002AddProviderToSummaries(),
		Migration20260204163003AddBackgroundProcesses(),
		Migration20260204163004CreateACPSessionUpdates(),
	}
}
