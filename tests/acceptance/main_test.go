package acceptance

import (
	"os"
	"testing"
)

// TestMain runs setup and teardown for acceptance tests
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
