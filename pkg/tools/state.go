package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

var (
	_ tooltypes.State = &BasicState{}
)

type BasicState struct {
	lastAccessed map[string]time.Time
	mu           sync.RWMutex
	sessionID    string
	todoFilePath string
	tools        []tooltypes.Tool
}

type BasicStateOption func(ctx context.Context, s *BasicState) error

// NewBasicState creates a new instance of BasicState with initialized map
func NewBasicState(ctx context.Context, opts ...BasicStateOption) *BasicState {
	state := &BasicState{
		lastAccessed: make(map[string]time.Time),
		sessionID:    uuid.New().String(),
		todoFilePath: "",
	}

	for _, opt := range opts {
		opt(ctx, state)
	}

	if len(state.tools) == 0 {
		state.tools = MainTools
	}

	return state
}

func WithSubAgentTools() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		s.tools = SubAgentTools
		return nil
	}
}

func WithMCPTools(mcpManager *MCPManager) BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		tools, err := mcpManager.ListMCPTools(ctx)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			s.tools = append(s.tools, &tool)
		}
		return nil
	}
}

func (s *BasicState) TodoFilePath() string {
	if s.todoFilePath != "" {
		return s.todoFilePath
	}
	return fmt.Sprintf("kodelet-todos-%s.json", s.sessionID)
}

func (s *BasicState) SetTodoFilePath(path string) {
	s.todoFilePath = path
}

func (s *BasicState) SetFileLastAccessed(path string, lastAccessed time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccessed[path] = lastAccessed
	return nil
}

func (s *BasicState) FileLastAccess() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAccessed
}

func (s *BasicState) SetFileLastAccess(fileLastAccess map[string]time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccessed = fileLastAccess
}

func (s *BasicState) GetFileLastAccessed(path string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lastAccessed, ok := s.lastAccessed[path]
	if !ok {
		return time.Time{}, fmt.Errorf("file %s has not been read yet", path)
	}
	return lastAccessed, nil
}

func (s *BasicState) ClearFileLastAccessed(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lastAccessed, path)
	return nil
}

func (s *BasicState) Tools() []tooltypes.Tool {
	return s.tools
}
