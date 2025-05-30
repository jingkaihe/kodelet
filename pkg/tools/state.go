package tools

import (
	"context"
	"fmt"
	"os"
	"path"
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
	basicTools   []tooltypes.Tool
	mcpTools     []tooltypes.Tool
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

	if len(state.basicTools) == 0 {
		state.basicTools = MainTools
	}

	return state
}

func WithSubAgentTools() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		s.basicTools = SubAgentTools
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
			s.mcpTools = append(s.mcpTools, &tool)
		}
		return nil
	}
}

func WithExtraMCPTools(tools []tooltypes.Tool) BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		s.mcpTools = append(s.mcpTools, tools...)
		return nil
	}
}

func (s *BasicState) TodoFilePath() (string, error) {
	if s.todoFilePath != "" {
		return s.todoFilePath, nil
	}
	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	todoFilePath := path.Join(pwd, ".kodelet", fmt.Sprintf("kodelet-todos-%s.json", s.sessionID))
	return todoFilePath, nil
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

func (s *BasicState) BasicTools() []tooltypes.Tool {
	return s.basicTools
}

func (s *BasicState) MCPTools() []tooltypes.Tool {
	return s.mcpTools
}

func (s *BasicState) Tools() []tooltypes.Tool {
	tools := make([]tooltypes.Tool, 0, len(s.basicTools)+len(s.mcpTools))
	tools = append(tools, s.basicTools...)
	tools = append(tools, s.mcpTools...)
	return tools
}
