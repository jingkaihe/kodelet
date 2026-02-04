package main

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management commands",
	Long:  `Commands for managing the kodelet database (migrations, status, etc.)`,
}

var dbStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show database migration status",
	Long:  `Shows the current database migration status, including applied and pending migrations.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		applied, err := db.GetMigrationStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get migration status: %w", err)
		}

		appliedMap := make(map[int64]bool)
		for _, v := range applied {
			appliedMap[v] = true
		}

		allMigrations := migrations.All()

		fmt.Println("Database Migration Status")
		fmt.Println("=========================")
		fmt.Printf("Database: %s\n\n", getDatabasePath())

		appliedCount := 0
		for _, m := range allMigrations {
			status := "[ ]"
			if appliedMap[m.Version] {
				status = "[✓]"
				appliedCount++
			}
			fmt.Printf("%s %d - %s\n", status, m.Version, m.Description)
		}

		fmt.Printf("\nApplied: %d/%d migrations\n", appliedCount, len(allMigrations))

		return nil
	},
}

var dbRollbackCmd = &cobra.Command{
	Use:   "rollback",
	Short: "Rollback the last database migration",
	Long:  `Rolls back the most recently applied database migration. Useful for testing or downgrading kodelet.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		// Show current status first
		applied, err := db.GetMigrationStatus(ctx)
		if err != nil {
			return fmt.Errorf("failed to get migration status: %w", err)
		}

		if len(applied) == 0 {
			presenter.Warning("No migrations to rollback")
			return nil
		}

		lastVersion := applied[len(applied)-1]

		// Find the migration description
		var description string
		for _, m := range migrations.All() {
			if m.Version == lastVersion {
				description = m.Description
				break
			}
		}

		presenter.Info(fmt.Sprintf("Rolling back migration %d: %s", lastVersion, description))

		if err := db.RollbackMigration(ctx, migrations.All()); err != nil {
			return fmt.Errorf("failed to rollback migration: %w", err)
		}

		presenter.Success(fmt.Sprintf("Successfully rolled back migration %d", lastVersion))

		return nil
	},
}

func getDatabasePath() string {
	path, err := db.DefaultDBPath()
	if err != nil {
		return "unknown"
	}
	return path
}

func init() {
	dbCmd.AddCommand(dbStatusCmd)
	dbCmd.AddCommand(dbRollbackCmd)
}
