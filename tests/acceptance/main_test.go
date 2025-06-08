package acceptance

import (
	"os"
	"os/exec"
	"testing"
)

// TestMain runs setup and teardown for acceptance tests
func TestMain(m *testing.M) {
	// Build the kodelet binary for testing
	if err := buildKodelet(); err != nil {
		panic("Failed to build kodelet binary: " + err.Error())
	}
	
	// Run tests
	code := m.Run()
	
	// Cleanup if needed
	// (In production we might want to clean up test data)
	
	os.Exit(code)
}

// buildKodelet builds the kodelet binary for testing
func buildKodelet() error {
	cmd := exec.Command("make", "build")
	cmd.Dir = "../../" // Go back to project root
	return cmd.Run()
}

// Helper function to check if binary exists
func binaryExists() bool {
	_, err := os.Stat("../../bin/kodelet")
	return err == nil
}