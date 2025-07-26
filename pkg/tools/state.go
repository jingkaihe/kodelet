package tools

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jingkaihe/kodelet/pkg/tools/browser"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/jingkaihe/kodelet/pkg/utils"
	"github.com/pkg/errors"
)

var (
	_ tooltypes.State = &BasicState{}
)

type BasicState struct {
	lastAccessed        map[string]time.Time
	backgroundProcesses []tooltypes.BackgroundProcess
	browserManager      tooltypes.BrowserManager
	mu                  sync.RWMutex
	sessionID           string
	todoFilePath        string
	tools               []tooltypes.Tool
	mcpTools            []tooltypes.Tool
	llmConfig           llmtypes.Config
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
		var allowedTools []string
		if state.llmConfig.AllowedTools != nil {
			allowedTools = state.llmConfig.AllowedTools
		}
		state.tools = GetMainTools(allowedTools, false) // Default without browser tools
		// Configure tools with LLM config parameters
		state.configureTools()
	}

	return state
}

func WithSubAgentTools(config interface{}) BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		config, ok := config.(llmtypes.Config)
		if !ok {
			return errors.New("invalid config type")
		}
		var allowedTools []string
		if config.SubAgent != nil && config.SubAgent.AllowedTools != nil {
			allowedTools = config.SubAgent.AllowedTools
		}
		s.tools = GetSubAgentTools(allowedTools, false) // Default without browser tools
		s.configureTools()
		return nil
	}
}

func WithMainToolsAndBrowser() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		var allowedTools []string
		if s.llmConfig.AllowedTools != nil {
			allowedTools = s.llmConfig.AllowedTools
		}
		s.tools = GetMainTools(allowedTools, true) // Main tools with browser support
		s.configureTools()
		return nil
	}
}

func WithSubAgentToolsAndBrowser() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		var allowedTools []string
		if s.llmConfig.SubAgent != nil && s.llmConfig.SubAgent.AllowedTools != nil {
			allowedTools = s.llmConfig.SubAgent.AllowedTools
		}
		s.tools = GetSubAgentTools(allowedTools, true) // Sub-agent tools with browser support
		s.configureTools()
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

func WithLLMConfig(config llmtypes.Config) BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		s.llmConfig = config
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
		return time.Time{}, errors.Errorf("file %s has not been read yet", path)
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
	return s.tools
}

func (s *BasicState) MCPTools() []tooltypes.Tool {
	return s.mcpTools
}

func (s *BasicState) Tools() []tooltypes.Tool {
	tools := make([]tooltypes.Tool, 0, len(s.tools)+len(s.mcpTools))
	tools = append(tools, s.tools...)
	tools = append(tools, s.mcpTools...)
	return tools
}

func (s *BasicState) AddBackgroundProcess(process tooltypes.BackgroundProcess) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backgroundProcesses = append(s.backgroundProcesses, process)
	return nil
}

func (s *BasicState) GetBackgroundProcesses() []tooltypes.BackgroundProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy to avoid race conditions
	processes := make([]tooltypes.BackgroundProcess, len(s.backgroundProcesses))
	copy(processes, s.backgroundProcesses)
	return processes
}

func (s *BasicState) RemoveBackgroundProcess(pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, process := range s.backgroundProcesses {
		if process.PID == pid {
			s.backgroundProcesses = append(s.backgroundProcesses[:i], s.backgroundProcesses[i+1:]...)
			return nil
		}
	}
	return errors.Errorf("background process with PID %d not found", pid)
}

func (s *BasicState) GetBrowserManager() tooltypes.BrowserManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.browserManager
}

func (s *BasicState) SetBrowserManager(manager tooltypes.BrowserManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.browserManager = manager
}

func (s *BasicState) GetLLMConfig() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.llmConfig
}

// configureTools configures tools with LLM config parameters
func (s *BasicState) configureTools() {
	var domainFilter *utils.DomainFilter
	if s.llmConfig.AllowedDomainsFile != "" {
		domainFilter = utils.NewDomainFilter(s.llmConfig.AllowedDomainsFile)
	}

	for i, tool := range s.tools {
		switch tool.Name() {
		case "bash":
			s.tools[i] = NewBashTool(s.llmConfig.AllowedCommands)
		case "web_fetch":
			s.tools[i] = NewWebFetchTool(s.llmConfig.AllowedDomainsFile)
		case "browser_navigate":
			s.tools[i] = browser.NewNavigateTool(domainFilter)
		}
	}
}
