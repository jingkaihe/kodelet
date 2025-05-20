package tools

import (
	"context"
	"strings"
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
	if err != nil {
		t.Errorf("SetFileLastAccessed returned an error: %v", err)
	}

	lastAccessed, err := s.GetFileLastAccessed(path)
	if err != nil {
		t.Errorf("GetFileLastAccessed returned an error: %v", err)
	}

	if !lastAccessed.Equal(now) {
		t.Errorf("Expected lastAccessed to be %v, got %v", now, lastAccessed)
	}

	// Test getting a non-existent file
	nonExistentPath := "non/existent/file.txt"
	lastAccessed, err = s.GetFileLastAccessed(nonExistentPath)
	if err == nil {
		t.Errorf("Expected error for non-existent file, got nil")
	}
	if !lastAccessed.IsZero() {
		t.Errorf("Expected zero time for non-existent file, got %v", lastAccessed)
	}

	// Test tools
	tools := s.Tools()
	if len(tools) != len(MainTools) {
		t.Errorf("Expected %d tools, got %d", len(MainTools), len(tools))
	}
	for i, tool := range tools {
		if tool.Name() != MainTools[i].Name() {
			t.Errorf("Expected tool %d to be %s, got %s", i, MainTools[i].Name(), tool.Name())
		}
	}

	subAgentTools := NewBasicState(context.TODO(), WithSubAgentTools())
	if len(subAgentTools.Tools()) != len(SubAgentTools) {
		t.Errorf("Expected %d tools, got %d", len(SubAgentTools), len(subAgentTools.Tools()))
	}
	for i, tool := range subAgentTools.Tools() {
		if tool.Name() != SubAgentTools[i].Name() {
			t.Errorf("Expected tool %d to be %s, got %s", i, SubAgentTools[i].Name(), tool.Name())
		}
	}
}

func TestClearFileLastAccessed(t *testing.T) {
	s := NewBasicState(context.TODO())

	// Set a file's last modified time
	path := "test/file.txt"
	now := time.Now()

	err := s.SetFileLastAccessed(path, now)
	if err != nil {
		t.Errorf("SetFileLastAccessed returned an error: %v", err)
	}

	// Verify it was set
	lastAccessed, err := s.GetFileLastAccessed(path)
	if err != nil {
		t.Errorf("GetFileLastAccessed returned an error: %v", err)
	}
	if !lastAccessed.Equal(now) {
		t.Errorf("Expected lastAccessed to be %v, got %v", now, lastAccessed)
	}

	// Clear the file's last modified time
	err = s.ClearFileLastAccessed(path)
	if err != nil {
		t.Errorf("ClearFileLastAccessed returned an error: %v", err)
	}

	// Verify it was cleared - we now expect an error
	lastAccessed, err = s.GetFileLastAccessed(path)
	if err == nil {
		t.Errorf("Expected an error after clearing, got nil")
	} else if !strings.Contains(err.Error(), "has not been read yet") {
		t.Errorf("Unexpected error message: %v", err)
	}
	if !lastAccessed.IsZero() {
		t.Errorf("Expected lastAccessed to be zero after clearing, got %v", lastAccessed)
	}
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
}

func TestBasicState_MCPTools(t *testing.T) {
	config := goldenMCPServersConfig
	manager, err := NewMCPManager(config)
	assert.NoError(t, err)

	err = manager.Initialize(context.Background())
	assert.NoError(t, err)

	s := NewBasicState(context.TODO(), WithMCPTools(manager))

	tools := s.MCPTools()
	assert.NotNil(t, tools)
	assert.Equal(t, len(tools), 3)
}
