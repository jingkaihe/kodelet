package tools

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
)

type BasicState struct {
	lastAccessed map[string]time.Time
	mu           sync.RWMutex
	sessionID    string
	todoFilePath string
	tools        []tooltypes.Tool
}

type BasicStateOption func(*BasicState)

// NewBasicState creates a new instance of BasicState with initialized map
func NewBasicState(opts ...BasicStateOption) *BasicState {
	state := &BasicState{
		lastAccessed: make(map[string]time.Time),
		sessionID:    uuid.New().String(),
		todoFilePath: "",
	}

	for _, opt := range opts {
		opt(state)
	}

	if len(state.tools) == 0 {
		state.tools = MainTools
	}

	return state
}

func WithSubAgentTools() BasicStateOption {
	return func(s *BasicState) {
		s.tools = SubAgentTools
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
