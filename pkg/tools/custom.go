package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/jingkaihe/kodelet/pkg/osutil"
	"github.com/jingkaihe/kodelet/pkg/plugins"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
)

var _ tooltypes.Tool = &CustomTool{}

const (
	customToolAliasPrefix  = "custom_tool_"
	pluginToolAliasPrefix  = "plugin_tool_"
	customToolSourceLocal  = "local"
	customToolSourceGlobal = "global"
	customToolSourcePlugin = "plugin"
)

// CustomToolDescription represents the JSON structure returned by tool's description command
type CustomToolDescription struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// CustomToolConfig represents the configuration for custom tools
type CustomToolConfig struct {
	Enabled       bool          `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	GlobalDir     string        `mapstructure:"global_dir" json:"global_dir" yaml:"global_dir"`
	LocalDir      string        `mapstructure:"local_dir" json:"local_dir" yaml:"local_dir"`
	Timeout       time.Duration `mapstructure:"timeout" json:"timeout" yaml:"timeout"`
	MaxOutputSize int           `mapstructure:"max_output_size" json:"max_output_size" yaml:"max_output_size"`
	ToolWhiteList []string      `mapstructure:"tool_white_list" json:"tool_white_list" yaml:"tool_white_list"`
}

// CustomTool represents a custom executable tool
type CustomTool struct {
	execPath    string
	name        string
	description string
	schema      *jsonschema.Schema
	timeout     time.Duration
	maxOutput   int
	source      string
	canonical   string
}

type customToolNameAlias struct {
	Canonical string
	Aliases   []string
}

type customToolDiscoveryDir struct {
	Path      string
	Prefix    string
	Source    string
	Overwrite bool
}

// CustomToolManager manages discovery and registration of custom tools
type CustomToolManager struct {
	tools     map[string]*CustomTool
	toolsByID map[string]*CustomTool
	nameIndex map[string]string
	rawNames  map[string]string
	globalDir string
	localDir  string
	config    CustomToolConfig
	mu        sync.RWMutex
}

// NewCustomToolManager creates a new custom tool manager
func NewCustomToolManager() (*CustomToolManager, error) {
	config := LoadCustomToolConfig()

	globalDir := expandHomePath(config.GlobalDir)
	localDir := config.LocalDir

	manager := &CustomToolManager{
		tools:     make(map[string]*CustomTool),
		toolsByID: make(map[string]*CustomTool),
		nameIndex: make(map[string]string),
		rawNames:  make(map[string]string),
		globalDir: globalDir,
		localDir:  localDir,
		config:    config,
	}

	return manager, nil
}

// LoadCustomToolConfig loads custom tool configuration from Viper
// This is exported so it can be used by other packages (e.g., for injecting into fragments)
func LoadCustomToolConfig() CustomToolConfig {
	config := CustomToolConfig{
		Enabled:       true,
		GlobalDir:     "~/.kodelet/tools",
		LocalDir:      "./.kodelet/tools",
		Timeout:       30 * time.Second,
		MaxOutputSize: 102400, // 100KB
	}

	if viper.IsSet("custom_tools") {
		if err := viper.UnmarshalKey("custom_tools", &config); err != nil {
			logger.G(context.Background()).WithError(err).Warn("failed to load custom tools config, using defaults")
		}
	}

	return config
}

// expandHomePath expands ~ to the user's home directory
func expandHomePath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

func customToolWhiteListed(toolName string, whiteList []string) bool {
	return len(whiteList) == 0 || slices.Contains(whiteList, toolName)
}

func pluginPrefixToCanonical(prefix string) string {
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return ""
	}
	return strings.Replace(trimmed, "@", "/", 1)
}

func sanitizeAliasSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "x"
	}
	var b strings.Builder
	for _, r := range segment {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "x"
	}
	return out
}

func sanitizeToolAliasName(name string) string {
	segments := strings.Split(name, "/")
	for i, segment := range segments {
		segments[i] = sanitizeAliasSegment(segment)
	}
	result := strings.Join(segments, "_")
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	result = strings.Trim(result, "_")
	if result == "" {
		return "tool"
	}
	return result
}

func pluginToolAliasName(canonical string) string {
	return pluginToolAliasPrefix + sanitizeToolAliasName(canonical)
}

func localToolAliasName(baseName string) string {
	return customToolAliasPrefix + sanitizeToolAliasName(baseName)
}

func normalizeToolIdentifier(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, customToolAliasPrefix)
	trimmed = strings.TrimPrefix(trimmed, pluginToolAliasPrefix)
	trimmed = strings.Replace(trimmed, "@", "/", 1)
	return strings.TrimSpace(trimmed)
}

func canonicalIDForTool(baseName, pluginPrefix string) string {
	pluginName := pluginPrefixToCanonical(pluginPrefix)
	if pluginName == "" {
		return baseName
	}
	return pluginName + "/" + baseName
}

func aliasesForTool(baseName, pluginPrefix string) customToolNameAlias {
	canonical := canonicalIDForTool(baseName, pluginPrefix)
	if canonical == baseName {
		sanitizedBase := sanitizeToolAliasName(baseName)
		return customToolNameAlias{
			Canonical: canonical,
			Aliases: []string{
				localToolAliasName(baseName),
				baseName,
				sanitizedBase,
				customToolAliasPrefix + sanitizedBase,
				pluginToolAliasPrefix + sanitizedBase,
			},
		}
	}

	pluginDirName := strings.Replace(canonical, "/", "@", 1)
	sanitizedCanonical := sanitizeToolAliasName(canonical)
	return customToolNameAlias{
		Canonical: canonical,
		Aliases: []string{
			pluginToolAliasName(canonical),
			localToolAliasName(canonical),
			canonical,
			pluginDirName,
			sanitizedCanonical,
			localToolAliasName(sanitizedCanonical),
			pluginToolAliasName(sanitizedCanonical),
		},
	}
}

// DiscoverTools scans directories and discovers available custom tools
func (m *CustomToolManager) DiscoverTools(ctx context.Context) error {
	if !m.config.Enabled {
		logger.G(ctx).Debug("custom tools disabled")
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools = make(map[string]*CustomTool)
	m.toolsByID = make(map[string]*CustomTool)
	m.nameIndex = make(map[string]string)
	m.rawNames = make(map[string]string)

	for _, dirCfg := range m.discoveryDirs() {
		if err := m.discoverToolsInDir(ctx, dirCfg); err != nil {
			logger.G(ctx).WithError(err).WithField("dir", dirCfg.Path).Debug("failed to discover custom tools")
		}
	}

	m.syncPrimaryTools()

	logger.G(ctx).WithField("count", len(m.tools)).Debug("discovered custom tools")
	return nil
}

func (m *CustomToolManager) discoveryDirs() []customToolDiscoveryDir {
	var dirs []customToolDiscoveryDir

	addDir := func(path, prefix, source string, overwrite bool) {
		if strings.TrimSpace(path) == "" {
			return
		}
		dirs = append(dirs, customToolDiscoveryDir{
			Path:      path,
			Prefix:    prefix,
			Source:    source,
			Overwrite: overwrite,
		})
	}

	addDir(m.localDir, "", customToolSourceLocal, false)

	for _, pluginDir := range plugins.ScanPluginSubdirs("./.kodelet/plugins", "tools") {
		addDir(pluginDir.Dir, pluginDir.Prefix, customToolSourcePlugin, false)
	}

	addDir(m.globalDir, "", customToolSourceGlobal, false)

	homeDir, err := os.UserHomeDir()
	if err == nil {
		globalPluginDir := filepath.Join(homeDir, ".kodelet", "plugins")
		for _, pluginDir := range plugins.ScanPluginSubdirs(globalPluginDir, "tools") {
			addDir(pluginDir.Dir, pluginDir.Prefix, customToolSourcePlugin, false)
		}
	}

	unique := make([]customToolDiscoveryDir, 0, len(dirs))
	seen := make(map[string]bool)
	for _, dir := range dirs {
		key := dir.Path + "|" + dir.Prefix + "|" + dir.Source
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, dir)
	}

	return unique
}

// discoverToolsInDir discovers tools in a specific directory
func (m *CustomToolManager) discoverToolsInDir(ctx context.Context, dirCfg customToolDiscoveryDir) error {
	if _, err := os.Stat(dirCfg.Path); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(dirCfg.Path)
	if err != nil {
		return errors.Wrap(err, "failed to read directory")
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		execPath := filepath.Join(dirCfg.Path, entry.Name())

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.Mode()&0o111 == 0 {
			continue
		}

		tool, err := m.validateTool(ctx, execPath)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("path", execPath).Debug("failed to validate tool")
			continue
		}

		names := aliasesForTool(tool.name, dirCfg.Prefix)
		if !customToolWhiteListed(names.Canonical, m.config.ToolWhiteList) && !customToolWhiteListed(tool.name, m.config.ToolWhiteList) {
			logger.G(ctx).WithField("name", tool.name).Debug("skipping tool, not in whitelist")
			continue
		}

		if existing, exists := m.toolsByID[names.Canonical]; exists && !dirCfg.Overwrite {
			logger.G(ctx).WithFields(map[string]any{
				"name":     names.Canonical,
				"existing": existing.execPath,
				"skipped":  execPath,
			}).Debug("skipping tool due to precedence")
			continue
		}

		tool.source = dirCfg.Source
		tool.canonical = names.Canonical
		m.toolsByID[names.Canonical] = tool
		for _, alias := range names.Aliases {
			m.nameIndex[normalizeToolIdentifier(alias)] = names.Canonical
			m.rawNames[alias] = names.Canonical
		}
		logger.G(ctx).WithFields(map[string]any{
			"name":      names.Canonical,
			"aliases":   names.Aliases,
			"path":      execPath,
			"source":    dirCfg.Source,
			"overwrite": dirCfg.Overwrite,
		}).Debug("registered custom tool")
	}

	return nil
}

func (m *CustomToolManager) syncPrimaryTools() {
	m.tools = make(map[string]*CustomTool)
	for _, tool := range m.toolsByID {
		m.tools[tool.canonical] = tool
	}
}

// validateTool validates a tool by calling its description command
func (m *CustomToolManager) validateTool(ctx context.Context, execPath string) (*CustomTool, error) {
	timeout := m.config.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, execPath, "description")
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.Wrapf(err, "failed to run description command: %s", stderr.String())
	}

	var desc CustomToolDescription
	if err := json.Unmarshal(stdout.Bytes(), &desc); err != nil {
		return nil, errors.Wrap(err, "failed to parse tool description")
	}

	if desc.Name == "" {
		return nil, errors.New("tool name is required")
	}
	if desc.Description == "" {
		return nil, errors.New("tool description is required")
	}

	schemaBytes, err := json.Marshal(desc.InputSchema)
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal input schema")
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return nil, errors.Wrap(err, "failed to parse input schema")
	}

	maxOutput := m.config.MaxOutputSize
	if maxOutput == 0 {
		maxOutput = 102400
	}

	tool := &CustomTool{
		execPath:    execPath,
		name:        desc.Name,
		description: desc.Description,
		schema:      &schema,
		timeout:     timeout,
		maxOutput:   maxOutput,
	}

	return tool, nil
}

// ListTools returns all discovered custom tools
func (m *CustomToolManager) ListTools() []tooltypes.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tools := make([]tooltypes.Tool, 0, len(m.tools))
	for _, tool := range m.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetTool returns a specific tool by name
func (m *CustomToolManager) GetTool(name string) (*CustomTool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if tool, ok := m.toolsByID[name]; ok {
		return tool, true
	}

	if canonical, ok := m.rawNames[name]; ok {
		tool, exists := m.toolsByID[canonical]
		return tool, exists
	}

	normalized := normalizeToolIdentifier(name)
	canonical, ok := m.nameIndex[normalized]
	if !ok {
		canonical = normalized
	}
	tool, exists := m.toolsByID[canonical]
	return tool, exists
}

// CustomTool interface implementation

// Name returns the name of the tool
func (t *CustomTool) Name() string {
	canonical := t.canonical
	if canonical == "" {
		canonical = t.name
	}
	if strings.Contains(canonical, "/") {
		return pluginToolAliasName(canonical)
	}
	return localToolAliasName(canonical)
}

// Description returns the description of the tool
func (t *CustomTool) Description() string {
	canonical := t.canonical
	if canonical == "" {
		canonical = t.name
	}
	if strings.Contains(canonical, "/") {
		return fmt.Sprintf("[Plugin Tool: %s] %s", canonical, t.description)
	}
	return t.description
}

// GenerateSchema generates the JSON schema for the tool's input parameters
func (t *CustomTool) GenerateSchema() *jsonschema.Schema {
	return t.schema
}

// TracingKVs returns tracing key-value pairs for observability
func (t *CustomTool) TracingKVs(_ string) ([]attribute.KeyValue, error) {
	canonical := t.canonical
	if canonical == "" {
		canonical = t.name
	}
	return []attribute.KeyValue{
		attribute.String("tool.type", "custom"),
		attribute.String("tool.name", t.name),
		attribute.String("tool.canonical", canonical),
		attribute.String("tool.source", t.source),
		attribute.String("tool.exec_path", t.execPath),
	}, nil
}

// ValidateInput validates the input parameters for the tool
func (t *CustomTool) ValidateInput(_ tooltypes.State, parameters string) error {
	var input map[string]any
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid JSON input")
	}

	return nil
}

// Execute runs the custom tool and returns the result
func (t *CustomTool) Execute(ctx context.Context, _ tooltypes.State, parameters string) tooltypes.ToolResult {
	startTime := time.Now()

	execCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, t.execPath, "run")
	osutil.SetProcessGroup(cmd)
	osutil.SetProcessGroupKill(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return &CustomToolResult{
			toolName:      t.Name(),
			canonicalName: t.canonical,
			source:        t.source,
			err:           errors.Wrap(err, "failed to create stdin pipe").Error(),
		}
	}

	// Write input to stdin and start command
	go func() {
		defer stdin.Close()
		io.WriteString(stdin, parameters)
	}()

	output, err := cmd.CombinedOutput()
	executionTime := time.Since(startTime)

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return &CustomToolResult{
				toolName:      t.Name(),
				canonicalName: t.canonical,
				source:        t.source,
				executionTime: executionTime,
				err:           fmt.Sprintf("tool execution timed out after %v", t.timeout),
			}
		}

		errorMsg := string(output)
		if errorMsg == "" {
			errorMsg = err.Error()
		}

		return &CustomToolResult{
			toolName:      t.Name(),
			canonicalName: t.canonical,
			source:        t.source,
			executionTime: executionTime,
			err:           errorMsg,
		}
	}

	outputStr := string(output)
	if len(outputStr) > t.maxOutput {
		outputStr = outputStr[:t.maxOutput] + "\n\n[TRUNCATED - Output exceeded 100KB limit]"
	}

	// Check if output is a JSON error response
	var jsonError map[string]any
	if err := json.Unmarshal([]byte(outputStr), &jsonError); err == nil {
		if errMsg, ok := jsonError["error"].(string); ok {
			return &CustomToolResult{
				toolName:      t.Name(),
				canonicalName: t.canonical,
				source:        t.source,
				executionTime: executionTime,
				err:           errMsg,
			}
		}
	}

	return &CustomToolResult{
		toolName:      t.Name(),
		canonicalName: t.canonical,
		source:        t.source,
		executionTime: executionTime,
		result:        outputStr,
	}
}

// CustomToolResult represents the result of a custom tool execution
type CustomToolResult struct {
	toolName      string
	canonicalName string
	source        string
	executionTime time.Duration
	result        string
	err           string
}

// GetResult returns the tool output
func (r *CustomToolResult) GetResult() string {
	return r.result
}

// GetError returns the error message
func (r *CustomToolResult) GetError() string {
	return r.err
}

// IsError returns true if the result contains an error
func (r *CustomToolResult) IsError() bool {
	return r.err != ""
}

// AssistantFacing returns the string representation for the AI assistant
func (r *CustomToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

// StructuredData returns structured metadata about the custom tool execution
func (r *CustomToolResult) StructuredData() tooltypes.StructuredToolResult {
	result := tooltypes.StructuredToolResult{
		ToolName:  r.toolName,
		Success:   !r.IsError(),
		Timestamp: time.Now(),
	}

	if r.IsError() {
		result.Error = r.GetError()
	} else {
		result.Metadata = &tooltypes.CustomToolMetadata{
			ExecutionTime: r.executionTime,
			Output:        r.result,
			CanonicalName: r.canonicalName,
			Source:        r.source,
		}
	}

	return result
}

// CreateCustomToolManagerFromViper creates a custom tool manager from Viper configuration
func CreateCustomToolManagerFromViper(ctx context.Context) (*CustomToolManager, error) {
	manager, err := NewCustomToolManager()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create custom tool manager")
	}

	if err := manager.DiscoverTools(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to discover custom tools")
	}

	return manager, nil
}
