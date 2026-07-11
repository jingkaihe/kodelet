package tools

import (
	"context"
	"os"
	"testing"

	"github.com/jingkaihe/kodelet/pkg/binaries"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	// Ensure binaries are available before running tests
	_, _ = binaries.EnsureRipgrep(ctx)
	_, _ = binaries.EnsureFd(ctx)
	os.Exit(m.Run())
}
