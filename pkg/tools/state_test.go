package tools

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBasicState(t *testing.T) {
	// Create a new BasicState using the constructor
	s := NewBasicState(context.TODO())

	// Test setting and getting a file's last modified time
	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err, "SetFileLastAccessed should not return an error")

	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err, "GetFileLastAccessed should not return an error")
	assert.True(t, lastAccessed.Equal(now), "lastAccessed should equal the time that was set")

	// Test getting a non-existent file
	nonExistentPath := "non/existent/file.txt"
	lastAccessed, err = s.GetFileLastAccessed(nonExistentPath)
	assert.Error(t, err, "Expected error for non-existent file")
	assert.True(t, lastAccessed.IsZero(), "Time for non-existent file should be zero")

	// Test tools
	tools := s.Tools()
	assert.Equal(t, len(MainTools), len(tools), "Should have the correct number of tools")
	for i, tool := range tools {
		assert.Equal(t, MainTools[i].Name(), tool.Name(), "Tool names should match")
	}

	subAgentTools := NewBasicState(context.TODO(), WithSubAgentTools())
	assert.Equal(t, len(SubAgentTools), len(subAgentTools.Tools()), "Should have the correct number of subagent tools")
	for i, tool := range subAgentTools.Tools() {
		assert.Equal(t, SubAgentTools[i].Name(), tool.Name(), "Subagent tool names should match")
	}
}

func TestClearFileLastAccessed(t *testing.T) {
	s := NewBasicState(context.TODO())

	// Set a file's last modified time
	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	assert.NoError(t, err, "SetFileLastAccessed should not return an error")

	// Verify it was set
	lastAccessed, err := s.GetFileLastAccessed(path)
	assert.NoError(t, err, "GetFileLastAccessed should not return an error")
	assert.True(t, lastAccessed.Equal(now), "lastAccessed should equal the time that was set")

	// Clear the file's last modified time
	err = s.ClearFileLastAccessed(path)
	assert.NoError(t, err, "ClearFileLastAccessed should not return an error")

	// Verify it was cleared - we now expect an error
	lastAccessed, err = s.GetFileLastAccessed(path)
	assert.Error(t, err, "Should get an error after clearing access time")
	assert.Contains(t, err.Error(), "has not been read yet", "Error message should indicate the file hasn't been read")
	assert.True(t, lastAccessed.IsZero(), "Time should be zero after clearing")
}

func TestConcurrentAccess(t *testing.T) {
	s := NewBasicState(context.TODO())

	const numGoroutines = 100
	const operationsPerGoroutine = 10

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			path := "test/file.txt"
			for j := 0; j < operationsPerGoroutine; j++ {
				now := time.Now()
				_ = s.SetFileLastAccessed(path, now)
				_, _ = s.GetFileLastAccessed(path)
				if j%2 == 0 {
					_ = s.ClearFileLastAccessed(path)
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// If we got here without deadlock or panic, the test passes
	assert.True(t, true, "Concurrent access test completed without deadlock or panic")
}

func TestBasicState_MCPTools(t *testing.T) {
	if os.Getenv("SKIP_DOCKER_TEST") == "true" {
		t.Skip("Skipping docker test")
	}
	config := goldenMCPConfig
	manager, err := NewMCPManager(config)
	assert.NoError(t, err)

	err = manager.Initialize(context.Background())
	assert.NoError(t, err)

	s := NewBasicState(context.TODO(), WithMCPTools(manager))

	tools := s.MCPTools()
	assert.NotNil(t, tools)
	assert.Equal(t, len(tools), 3)
}
