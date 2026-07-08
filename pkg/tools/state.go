package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/skills"
	llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/spf13/viper"
)

var _ tooltypes.State = &BasicState{}

type contextInfo struct {
	Content      string
	Path         string
	LastModified time.Time
}

// BasicState implements the State interface with basic functionality
type BasicState struct {
	mu             sync.RWMutex
	workingDir     string
	tools          []tooltypes.Tool
	mcpTools       []tooltypes.Tool
	extensionTools []tooltypes.Tool
	llmConfig      llmtypes.Config

	// Context discovery fields
	contextCache     map[string]*contextInfo
	contextDiscovery *ContextDiscovery

	// Per-file locking for atomic file operations
	fileLocks   map[string]*sync.Mutex
	fileLocksMu sync.Mutex
}

func hasExplicitAllowedTools(config llmtypes.Config) bool {
	return len(config.AllowedTools) > 0
}

func allowedToolNameSet(config llmtypes.Config) map[string]struct{} {
	if len(config.AllowedTools) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(config.AllowedTools))
	for _, name := range config.AllowedTools {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		allowed[trimmed] = struct{}{}
	}

	return allowed
}

func filterDiscoveredToolsByAllowed(config llmtypes.Config, tools []tooltypes.Tool) []tooltypes.Tool {
	if len(tools) == 0 || !hasExplicitAllowedTools(config) {
		return tools
	}

	allowed := allowedToolNameSet(config)
	if len(allowed) == 0 {
		return nil
	}

	filtered := make([]tooltypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if _, ok := allowed[tool.Name()]; ok {
			filtered = append(filtered, tool)
		}
	}

	return filtered
}

func filterDuplicateTools(tools []tooltypes.Tool, reserved map[string]struct{}) []tooltypes.Tool {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]tooltypes.Tool, 0, len(tools))
	for _, tool := range tools {
		name := tool.Name()
		if _, exists := reserved[name]; exists {
			continue
		}
		reserved[name] = struct{}{}
		filtered = append(filtered, tool)
	}
	return filtered
}

func skillsEnabledForConfig(config llmtypes.Config) bool {
	if config.IsSubAgent {
		return false
	}
	if config.Skills != nil && !config.Skills.Enabled {
		return false
	}
	if viper.GetBool("no_skills") {
		return false
	}
	return true
}

func filterOutSkill(tools []tooltypes.Tool) []tooltypes.Tool {
	filtered := make([]tooltypes.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Name() != "skill" {
			filtered = append(filtered, tool)
		}
	}
	return filtered
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
	workingDir = osutil.CanonicalizePath(workingDir)

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
		workingDir:   workingDir,
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
		allowedTools = enforceToolMode(allowedTools, state.llmConfig.ToolMode, defaultMainTools)
		state.tools = GetMainToolsWithOptions(ctx, allowedTools, state.llmConfig.EnableFSSearchTools)
		state.tools = enforceToolModeOnResolvedTools(state.tools, allowedTools, state.llmConfig.ToolMode)
	}
	state.configureTools()

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
		allowedTools = enforceToolMode(allowedTools, s.llmConfig.ToolMode, defaultSubAgentTools)
		s.tools = GetSubAgentToolsWithOptions(ctx, allowedTools, s.llmConfig.EnableFSSearchTools)
		s.tools = enforceToolModeOnResolvedTools(s.tools, allowedTools, s.llmConfig.ToolMode)
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
		allowedTools = enforceToolMode(allowedTools, s.llmConfig.ToolMode, defaultMainTools)
		s.tools = GetMainToolsWithOptions(ctx, allowedTools, s.llmConfig.EnableFSSearchTools)
		s.tools = enforceToolModeOnResolvedTools(s.tools, allowedTools, s.llmConfig.ToolMode)
		if s.llmConfig.DisableSubagent {
			s.tools = filterOutSubagent(s.tools)
		}
		if !skillsEnabledForConfig(s.llmConfig) {
			s.tools = filterOutSkill(s.tools)
		}
		s.configureTools()
		return nil
	}
}

// WithMCPTools returns an option that configures MCP tools
func WithMCPTools(mcpManager *MCPManager) BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		if noToolsConfigured(s.llmConfig) || mcpManager == nil {
			return nil
		}
		tools, err := mcpManager.ListMCPTools(ctx)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if len(filterDiscoveredToolsByAllowed(s.llmConfig, []tooltypes.Tool{&tool})) == 0 {
				continue
			}
			s.mcpTools = append(s.mcpTools, &tool)
		}
		return nil
	}
}

// WithExtraMCPTools returns an option that adds extra MCP tools
func WithExtraMCPTools(tools []tooltypes.Tool) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		if noToolsConfigured(s.llmConfig) {
			return nil
		}
		s.mcpTools = append(s.mcpTools, filterDiscoveredToolsByAllowed(s.llmConfig, tools)...)
		return nil
	}
}

// WithExtensionTools returns an option that configures extension-provided tools.
func WithExtensionTools(extensionTools []tooltypes.Tool) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		if noToolsConfigured(s.llmConfig) || len(extensionTools) == 0 {
			return nil
		}
		s.extensionTools = append(s.extensionTools, filterDiscoveredToolsByAllowed(s.llmConfig, extensionTools)...)
		return nil
	}
}

// WithLLMConfig returns an option that sets the LLM configuration
func WithLLMConfig(config llmtypes.Config) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		s.llmConfig = config
		if strings.TrimSpace(config.WorkingDirectory) != "" {
			s.workingDir = osutil.CanonicalizePath(config.WorkingDirectory)
			if s.contextDiscovery != nil {
				s.contextDiscovery.workingDir = s.workingDir
			}
		}
		return nil
	}
}

// WithWorkingDirectory returns an option that sets the explicit working directory.
func WithWorkingDirectory(workingDir string) BasicStateOption {
	return func(_ context.Context, s *BasicState) error {
		workingDir = strings.TrimSpace(workingDir)
		if workingDir == "" {
			return nil
		}
		s.workingDir = osutil.CanonicalizePath(workingDir)
		if s.contextDiscovery != nil {
			s.contextDiscovery.workingDir = s.workingDir
		}
		if s.llmConfig.WorkingDirectory == "" {
			s.llmConfig.WorkingDirectory = s.workingDir
		}
		return nil
	}
}

// WithSkillTool returns an option that configures the skill tool with discovered skills
func WithSkillTool() BasicStateOption {
	return func(ctx context.Context, s *BasicState) error {
		if noToolsConfigured(s.llmConfig) {
			return nil
		}
		if !skillsEnabledForConfig(s.llmConfig) {
			s.tools = filterOutSkill(s.tools)
			return nil
		}
		if hasExplicitAllowedTools(s.llmConfig) {
			allowed := allowedToolNameSet(s.llmConfig)
			if _, ok := allowed["skill"]; !ok {
				s.tools = filterOutSkill(s.tools)
				return nil
			}
		}
		discoveredSkills := discoverSkills(ctx, s.llmConfig)
		if len(discoveredSkills) == 0 {
			s.tools = filterOutSkill(s.tools)
			return nil
		}
		skillTool := NewSkillToolWithOptions(discoveredSkills, len(discoveredSkills) > 0, s.llmConfig.ToolMode, s.llmConfig.EnableFSSearchTools)
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
	if llmConfig.IsSubAgent {
		return nil
	}

	// Check if skills are disabled via config
	if llmConfig.Skills != nil && !llmConfig.Skills.Enabled {
		return nil
	}

	if viper.GetBool("no_skills") {
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
		if noToolsConfigured(s.llmConfig) || s.llmConfig.DisableSubagent {
			s.tools = filterOutSubagent(s.tools)
			return nil
		}
		if hasExplicitAllowedTools(s.llmConfig) {
			allowed := allowedToolNameSet(s.llmConfig)
			if _, ok := allowed["subagent"]; !ok {
				s.tools = filterOutSubagent(s.tools)
				return nil
			}
		}
		discoveredWorkflows := discoverWorkflows(ctx)
		subagentTool := NewSubAgentTool(discoveredWorkflows, len(discoveredWorkflows) > 0, s.llmConfig.EnableFSSearchTools)
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

func enforceToolMode(allowedTools []string, toolMode llmtypes.ToolMode, defaultTools []string) []string {
	if !toolMode.IsPatchMode() {
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
		if tool == "file_read" || tool == "file_write" || tool == "file_edit" {
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

func enforceToolModeOnResolvedTools(tools []tooltypes.Tool, allowedTools []string, toolMode llmtypes.ToolMode) []tooltypes.Tool {
	if len(allowedTools) == 1 && allowedTools[0] == NoToolsMarker {
		return tools
	}

	if !toolMode.IsPatchMode() {
		filteredTools := make([]tooltypes.Tool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "apply_patch" {
				continue
			}
			filteredTools = append(filteredTools, tool)
		}
		return filteredTools
	}

	filteredTools := make([]tooltypes.Tool, 0, len(tools))
	hasApplyPatch := false
	for _, tool := range tools {
		switch tool.Name() {
		case "file_read", "file_write", "file_edit":
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

func noToolsConfigured(config llmtypes.Config) bool {
	return len(config.AllowedTools) == 1 && config.AllowedTools[0] == NoToolsMarker
}

// LockFile acquires an exclusive lock for the given file path.
// This ensures atomic read-modify-write operations when editing files.
func (s *BasicState) LockFile(path string) {
	path = osutil.CanonicalizePath(path)
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
	path = osutil.CanonicalizePath(path)
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

// ExtensionTools returns the list of extension-provided tools.
func (s *BasicState) ExtensionTools() []tooltypes.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.extensionTools
}

// Tools returns all available tools
func (s *BasicState) Tools() []tooltypes.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]tooltypes.Tool, 0, len(s.tools)+len(s.mcpTools)+len(s.extensionTools))
	reserved := map[string]struct{}{}
	tools = append(tools, filterDuplicateTools(s.tools, reserved)...)
	tools = append(tools, filterDuplicateTools(s.mcpTools, reserved)...)
	tools = append(tools, filterDuplicateTools(s.extensionTools, reserved)...)
	return tools
}

// GetLLMConfig returns the LLM configuration
func (s *BasicState) GetLLMConfig() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.llmConfig
}

// WorkingDirectory returns the state working directory.
func (s *BasicState) WorkingDirectory() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workingDir
}

func (s *BasicState) configureTools() {
	s.tools = s.configureToolSlice(s.tools)
	s.mcpTools = s.configureToolSlice(s.mcpTools)
}

func (s *BasicState) configureToolSlice(tools []tooltypes.Tool) []tooltypes.Tool {
	for i, tool := range tools {
		switch tool.Name() {
		case "bash":
			tools[i] = NewBashToolWithTimeout(s.llmConfig.AllowedCommands, s.llmConfig.EnableFSSearchTools, s.llmConfig.BashTimeout())
		case "web_fetch":
			tools[i] = NewWebFetchTool(s.llmConfig.AllowedDomainsFile)
		case "view_image":
			tools[i] = NewViewImageTool(s.llmConfig.Model, s.llmConfig.Provider)
		}
	}
	return tools
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

	return contexts
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
