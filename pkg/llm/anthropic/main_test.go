package anthropic

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/db"
	"github.com/jingkaihe/kodelet/pkg/db/migrations"
)

func TestMain(m *testing.M) {
	os.Exit(runTestsWithMigratedDatabase(m))
}

func runTestsWithMigratedDatabase(m *testing.M) int {
	basePath, err := os.MkdirTemp("", "kodelet-anthropic-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create test base path: %v\n", err)
		return 1
	}
	defer os.RemoveAll(basePath)

	if err := os.Setenv("KODELET_BASE_PATH", basePath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure test base path: %v\n", err)
		return 1
	}
	if err := db.RunMigrations(context.Background(), migrations.All()); err != nil {
		fmt.Fprintf(os.Stderr, "failed to migrate test database: %v\n", err)
		return 1
	}

	return m.Run()
}
