package tools

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/skills"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
)

var _ tooltypes.State = &BasicState{}

type contextInfo struct {
	Content      string
	Path         string
	LastModified time.Time
}

// BasicState implements the State interface with basic functionality
type BasicState struct {
	lastAccessed        map[string]time.Time
	backgroundProcesses []tooltypes.BackgroundProcess
	mu                  sync.RWMutex
	sessionID           string
	todoFilePath        string
	tools               []tooltypes.Tool
	mcpTools            []tooltypes.Tool
	customTools         []tooltypes.Tool
	llmConfig           llmtypes.Config

	// Context discovery fields
	contextCache     map[string]*contextInfo
	contextDiscovery *ContextDiscovery

	// Per-file locking for atomic file operations
	fileLocks   map[string]*sync.Mutex
	fileLocksMu sync.Mutex
}

// ContextDiscovery tracks context discovery results
type ContextDiscovery struct {
	workingDir      string
	homeDir         string
	contextPatterns []string // ["AGENTS.md"]
}

// BasicStateOption is a function that configures a BasicState
type BasicStateOption func(ctx context.Context, s *BasicState) error

// NewBasicState creates a new BasicState with the given options
func NewBasicState(ctx context.Context, opts ...BasicStateOption) *BasicState {
	// Get working directory - this is critical for proper context discovery
	workingDir, err := os.Getwd()
	if err != nil {
		logger.G(ctx).WithError(err).Fatal("Failed to get current working directory. Context discovery requires a valid working directory.")
	}

	// Get home directory with fallback
	homeDir, err := os.UserHomeDir()
	var kodeletHomeDir string
	if err != nil {
		logger.G(ctx).WithError(err).Warning("Failed to get user home directory, home context discovery will be disabled")
		kodeletHomeDir = "" // Empty string disables home context discovery
	} else {
		kodeletHomeDir = filepath.Join(homeDir, ".kodelet")
	}

	state := &BasicState{
		lastAccessed: make(map[string]time.Time),
		sessionID:    uuid.New().String(),
		todoFilePath: "",
		contextCache: make(map[string]*contextInfo),
		contextDiscovery: &ContextDiscovery{
			workingDir:      workingDir,
			homeDir:         kodeletHomeDir,
			contextPatterns: llmtypes.DefaultContextPatterns(),
		},
		fileLocks: make(map[string]*sync.Mutex),
	}

	for _, opt := range opts {
		opt(ctx, state)
	}

	// Update context patterns from config if specified
	if state.llmConfig.Context != nil && len(state.llmConfig.Context.Patterns) > 0 {
		state.contextDiscovery.contextPatterns = state.llmConfig.Context.Patterns
	}

	if len(state.tools) == 0 {
		var allowedTools []string
		if state.llmConfig.AllowedTools != nil {
			allowedTools = state.llmConfig.AllowedTools
		}
		allowedTools = enforceApplyPatchMode(allowedTools, state.llmConfig.ApplyPatchEnabled, defaultMainTools)
		state.tools = GetMainTools(ctx, allowedTools, state.llmConfig.EnableTodos)
		state.tools = enforceApplyPatchModeOnResolvedTools(state.tools, allowedTools, state.llmConfig.ApplyPatchEnabled)
		state.configureTools()
	}

	return state
}

// WithSubAgentToolsFromConfig returns an option that configures sub-agent tools using the state's llmConfig
// This is used when running kodelet with --as-subagent flag
func WithSubAgentToolsFromConfig() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		var allowedTools []string
		if s.llmConfig.AllowedTools != nil {
			allowedTools = s.llmConfig.AllowedTools
		}
		allowedTools = enforceApplyPatchMode(allowedTools, s.llmConfig.ApplyPatchEnabled, defaultSubAgentTools)
		s.tools = GetSubAgentTools(ctx, allowedTools)
		s.tools = enforceApplyPatchModeOnResolvedTools(s.tools, allowedTools, s.llmConfig.ApplyPatchEnabled)
		s.configureTools()
		return nil
	}
}

// WithMainTools returns an option that configures main tools
func WithMainTools() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		var allowedTools []string
		if s.llmConfig.AllowedTools != nil {
			allowedTools = s.llmConfig.AllowedTools
		}
		allowedTools = enforceApplyPatchMode(allowedTools, s.llmConfig.ApplyPatchEnabled, defaultMainTools)
		s.tools = GetMainTools(ctx, allowedTools, s.llmConfig.EnableTodos)
		s.tools = enforceApplyPatchModeOnResolvedTools(s.tools, allowedTools, s.llmConfig.ApplyPatchEnabled)
		if s.llmConfig.DisableSubagent {
			s.tools = filterOutSubagent(s.tools)
		}
		s.configureTools()
		return nil
	}
}

// WithMCPTools returns an option that configures MCP tools
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

// WithExtraMCPTools returns an option that adds extra MCP tools
func WithExtraMCPTools(tools []tooltypes.Tool) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		s.mcpTools = append(s.mcpTools, tools...)
		return nil
	}
}

// WithCustomTools returns an option that configures custom tools
func WithCustomTools(customManager *CustomToolManager) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		tools := customManager.ListTools()
		s.customTools = append(s.customTools, tools...)
		return nil
	}
}

// WithLLMConfig returns an option that sets the LLM configuration
func WithLLMConfig(config llmtypes.Config) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		s.llmConfig = config
		return nil
	}
}

// WithSkillTool returns an option that configures the skill tool with discovered skills
func WithSkillTool() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		discoveredSkills := discoverSkills(ctx, s.llmConfig)
		skillTool := NewSkillTool(discoveredSkills, len(discoveredSkills) > 0)
		for i, tool := range s.tools {
			if tool.Name() == "skill" {
				s.tools[i] = skillTool
				return nil
			}
		}
		s.tools = append(s.tools, skillTool)
		return nil
	}
}

// discoverSkills discovers available skills based on configuration
func discoverSkills(ctx context.Context, llmConfig llmtypes.Config) map[string]*skills.Skill {
	// Check if skills are disabled via config
	if llmConfig.Skills != nil && !llmConfig.Skills.Enabled {
		return nil
	}

	discovery, err := skills.NewDiscovery()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to create skill discovery")
		return nil
	}

	allSkills, err := discovery.DiscoverSkills()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to discover skills")
		return nil
	}

	if llmConfig.Skills != nil && len(llmConfig.Skills.Allowed) > 0 {
		allSkills = skills.FilterByAllowlist(allSkills, llmConfig.Skills.Allowed)
	}

	logger.G(ctx).WithField("count", len(allSkills)).Debug("Discovered skills")
	return allSkills
}

// WithSubAgentTool returns an option that configures the subagent tool with discovered workflows
func WithSubAgentTool() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		discoveredWorkflows := discoverWorkflows(ctx)
		subagentTool := NewSubAgentTool(discoveredWorkflows, len(discoveredWorkflows) > 0)
		for i, tool := range s.tools {
			if tool.Name() == "subagent" {
				s.tools[i] = subagentTool
				return nil
			}
		}
		s.tools = append(s.tools, subagentTool)
		return nil
	}
}

// discoverWorkflows discovers available workflow fragments for the subagent tool
func discoverWorkflows(ctx context.Context) map[string]*fragments.Fragment {
	processor, err := fragments.NewFragmentProcessor()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to create fragment processor for workflow discovery")
		return nil
	}

	frags, err := processor.ListFragmentsWithMetadata()
	if err != nil {
		logger.G(ctx).WithError(err).Debug("Failed to list fragments for workflow discovery")
		return nil
	}

	workflows := make(map[string]*fragments.Fragment)
	for _, frag := range frags {
		// Only include fragments explicitly marked as workflows
		if frag.Metadata.Workflow {
			workflows[frag.ID] = frag
		}
	}

	logger.G(ctx).WithField("count", len(workflows)).Debug("Discovered workflows for subagent")
	return workflows
}

func enforceApplyPatchMode(allowedTools []string, applyPatchEnabled bool, defaultTools []string) []string {
	if !applyPatchEnabled {
		return allowedTools
	}
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return allowedTools
	}

	baseTools := allowedTools
	if len(baseTools) == 0 {
		baseTools = append([]string{}, defaultTools...)
	}

	filteredTools := make([]string, 0, len(baseTools))
	seen := make(map[string]struct{})
	for _, tool := range baseTools {
		if tool == "file_write" || tool == "file_edit" {
			continue
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		filteredTools = append(filteredTools, tool)
	}

	if _, exists := seen["apply_patch"]; !exists {
		filteredTools = append(filteredTools, "apply_patch")
	}

	return filteredTools
}

func enforceApplyPatchModeOnResolvedTools(tools []tooltypes.Tool, allowedTools []string, applyPatchEnabled bool) []tooltypes.Tool {
	if !applyPatchEnabled {
		return tools
	}
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return tools
	}

	filteredTools := make([]tooltypes.Tool, 0, len(tools))
	hasApplyPatch := false
	for _, tool := range tools {
		switch tool.Name() {
		case "file_write", "file_edit":
			continue
		case "apply_patch":
			hasApplyPatch = true
		}
		filteredTools = append(filteredTools, tool)
	}

	if !hasApplyPatch {
		if applyPatchTool, exists := toolRegistry["apply_patch"]; exists {
			filteredTools = append(filteredTools, applyPatchTool)
		}
	}

	return filteredTools
}

// TodoFilePath returns the path to the todo file
func (s *BasicState) TodoFilePath() (string, error) {
	s.mu.RLock()
	todoPath := s.todoFilePath
	s.mu.RUnlock()

	if todoPath != "" {
		return todoPath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	todoFilePath := path.Join(homeDir, ".kodelet", "todos", fmt.Sprintf("%s.json", s.sessionID))
	return todoFilePath, nil
}

// SetTodoFilePath sets the path to the todo file
func (s *BasicState) SetTodoFilePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.todoFilePath = path
}

// SetFileLastAccessed sets the last access time for a file
func (s *BasicState) SetFileLastAccessed(path string, lastAccessed time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccessed[path] = lastAccessed
	return nil
}

// FileLastAccess returns a map of file paths to their last access times
func (s *BasicState) FileLastAccess() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastAccessed
}

// SetFileLastAccess sets the file last access map
func (s *BasicState) SetFileLastAccess(fileLastAccess map[string]time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAccessed = fileLastAccess
}

// GetFileLastAccessed gets the last access time for a file
func (s *BasicState) GetFileLastAccessed(path string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lastAccessed, ok := s.lastAccessed[path]
	if !ok {
		return time.Time{}, errors.Errorf("file %s has not been read yet", path)
	}
	return lastAccessed, nil
}

// ClearFileLastAccessed clears the last access time for a file
func (s *BasicState) ClearFileLastAccessed(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lastAccessed, path)
	return nil
}

// LockFile acquires an exclusive lock for the given file path.
// This ensures atomic read-modify-write operations when editing files.
func (s *BasicState) LockFile(path string) {
	s.fileLocksMu.Lock()
	lock, ok := s.fileLocks[path]
	if !ok {
		lock = &sync.Mutex{}
		s.fileLocks[path] = lock
	}
	s.fileLocksMu.Unlock()
	lock.Lock()
}

// UnlockFile releases the lock for the given file path.
func (s *BasicState) UnlockFile(path string) {
	s.fileLocksMu.Lock()
	lock, ok := s.fileLocks[path]
	s.fileLocksMu.Unlock()
	if ok {
		lock.Unlock()
	}
}

// BasicTools returns the list of basic tools
func (s *BasicState) BasicTools() []tooltypes.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tools
}

// MCPTools returns the list of MCP tools
func (s *BasicState) MCPTools() []tooltypes.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mcpTools
}

// Tools returns all available tools
func (s *BasicState) Tools() []tooltypes.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]tooltypes.Tool, 0, len(s.tools)+len(s.mcpTools)+len(s.customTools))
	tools = append(tools, s.tools...)
	tools = append(tools, s.mcpTools...)
	tools = append(tools, s.customTools...)
	return tools
}

// AddBackgroundProcess adds a background process to the state
func (s *BasicState) AddBackgroundProcess(process tooltypes.BackgroundProcess) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backgroundProcesses = append(s.backgroundProcesses, process)
	return nil
}

// GetBackgroundProcesses returns all background processes
func (s *BasicState) GetBackgroundProcesses() []tooltypes.BackgroundProcess {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Return a copy to avoid race conditions
	processes := make([]tooltypes.BackgroundProcess, len(s.backgroundProcesses))
	copy(processes, s.backgroundProcesses)
	return processes
}

// RemoveBackgroundProcess removes a background process by PID
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

// GetLLMConfig returns the LLM configuration
func (s *BasicState) GetLLMConfig() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.llmConfig
}

func (s *BasicState) configureTools() {
	for i, tool := range s.tools {
		switch tool.Name() {
		case "bash":
			s.tools[i] = NewBashTool(s.llmConfig.AllowedCommands)
		case "web_fetch":
			s.tools[i] = NewWebFetchTool(s.llmConfig.AllowedDomainsFile)
		}
	}
}

// DiscoverContexts discovers and returns context information for the current state
func (s *BasicState) DiscoverContexts() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	contexts := make(map[string]string)

	// 1. Add working directory context (uses configured patterns)
	if ctx := s.loadContextFromPatterns(s.contextDiscovery.workingDir); ctx != nil {
		contexts[ctx.Path] = ctx.Content
	}

	// 2. Add home directory context (~/.kodelet/)
	if ctx := s.loadContextFromPatterns(s.contextDiscovery.homeDir); ctx != nil {
		contexts[ctx.Path] = ctx.Content
	}

	// 3. Add access-based contexts (discovers context files in subdirectories of accessed files)
	for _, ctx := range s.discoverAccessBasedContexts() {
		contexts[ctx.Path] = ctx.Content
	}

	return contexts
}

// discover contexts in the working directory hierarchy for files that have been accessed
// only considers accessed files within the working directory
// e.g. for /workdir/foo/bar/baz/code.py with /workdir as the working directory:
// /workdir/foo/bar/baz/AGENTS.md, /workdir/foo/bar/AGENTS.md, /workdir/AGENTS.md will be discovered if they exist
func (s *BasicState) discoverAccessBasedContexts() []contextInfo {
	contexts := []contextInfo{}
	visited := make(map[string]bool)

	for path := range s.lastAccessed {
		dir := filepath.Dir(path)
		// Only process directories within the working directory
		if strings.HasPrefix(dir, s.contextDiscovery.workingDir) {
			ctxs := s.findContextsForPath(dir, visited)
			contexts = append(contexts, ctxs...)
		}
	}

	return contexts
}

// findContextsForPath searches up the directory tree from the given file path to find context files
func (s *BasicState) findContextsForPath(dir string, visited map[string]bool) []contextInfo {
	result := []contextInfo{}

	for !visited[dir] && dir != filepath.Dir(dir) && dir != s.contextDiscovery.workingDir {
		visited[dir] = true

		if info := s.loadContextFromPatterns(dir); info != nil {
			result = append(result, *info)
		}

		dir = filepath.Dir(dir)
	}

	return result
}

func (s *BasicState) loadContextFromPatterns(path string) *contextInfo {
	if path == "" {
		return nil
	}

	for _, pattern := range s.contextDiscovery.contextPatterns {
		if info := s.loadContextFile(filepath.Join(path, pattern)); info != nil {
			return info
		}
	}
	return nil
}

func (s *BasicState) loadContextFile(path string) *contextInfo {
	stat, err := os.Stat(path)
	if err != nil {
		return nil
	}

	// Check cache - only use if file hasn't been modified
	if cached, ok := s.contextCache[path]; ok {
		if cached.LastModified.Equal(stat.ModTime()) {
			return cached
		}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	info := &contextInfo{
		Content:      string(content),
		Path:         path,
		LastModified: stat.ModTime(),
	}

	s.contextCache[path] = info
	return info
}
