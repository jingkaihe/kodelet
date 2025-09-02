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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/jingkaihe/kodelet/pkg/logger"
	tooltypes "github.com/jingkaihe/kodelet/pkg/types/tools"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/attribute"
)

var (
	_ tooltypes.Tool = &CustomTool{}
)

// CustomToolDescription represents the JSON structure returned by tool's description command
type CustomToolDescription struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
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
}

// CustomToolManager manages discovery and registration of custom tools
type CustomToolManager struct {
	tools     map[string]*CustomTool
	globalDir string
	localDir  string
	config    CustomToolConfig
	mu        sync.RWMutex
}

// NewCustomToolManager creates a new custom tool manager
func NewCustomToolManager() (*CustomToolManager, error) {
	config := loadCustomToolConfig()

	globalDir := expandHomePath(config.GlobalDir)
	localDir := config.LocalDir

	manager := &CustomToolManager{
		tools:     make(map[string]*CustomTool),
		globalDir: globalDir,
		localDir:  localDir,
		config:    config,
	}

	return manager, nil
}

// loadCustomToolConfig loads custom tool configuration from Viper
func loadCustomToolConfig() CustomToolConfig {
	config := CustomToolConfig{
		Enabled:       true,
		GlobalDir:     "~/.kodelet/tools",
		LocalDir:      "./kodelet-tools",
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

// DiscoverTools scans directories and discovers available custom tools
func (m *CustomToolManager) DiscoverTools(ctx context.Context) error {
	if !m.config.Enabled {
		logger.G(ctx).Debug("custom tools disabled")
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.tools = make(map[string]*CustomTool)

	if err := m.discoverToolsInDir(ctx, m.globalDir, false); err != nil {
		logger.G(ctx).WithError(err).WithField("dir", m.globalDir).Warn("failed to discover global custom tools")
	}

	// Local tools override global ones with same name
	if err := m.discoverToolsInDir(ctx, m.localDir, true); err != nil {
		logger.G(ctx).WithError(err).WithField("dir", m.localDir).Debug("failed to discover local custom tools")
	}

	logger.G(ctx).WithField("count", len(m.tools)).Debug("discovered custom tools")
	return nil
}

// discoverToolsInDir discovers tools in a specific directory
func (m *CustomToolManager) discoverToolsInDir(ctx context.Context, dir string, override bool) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.Wrap(err, "failed to read directory")
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		execPath := filepath.Join(dir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.Mode()&0111 == 0 {
			continue
		}

		tool, err := m.validateTool(ctx, execPath)
		if err != nil {
			logger.G(ctx).WithError(err).WithField("path", execPath).Debug("failed to validate tool")
			continue
		}

		if !customToolWhiteListed(tool.name, m.config.ToolWhiteList) {
			logger.G(ctx).WithField("name", tool.name).Debug("skipping tool, not in whitelist")
			continue
		}

		// Check if tool already exists from global dir
		if _, exists := m.tools[tool.name]; exists && !override {
			logger.G(ctx).WithField("name", tool.name).Debug("skipping global tool, local version exists")
			continue
		} else if exists {
			logger.G(ctx).WithField("name", tool.name).Debug("overriding global tool with local version")
		}

		m.tools[tool.name] = tool
		logger.G(ctx).WithField("name", tool.name).WithField("path", execPath).Debug("registered custom tool")
	}

	return nil
}

// validateTool validates a tool by calling its description command
func (m *CustomToolManager) validateTool(ctx context.Context, execPath string) (*CustomTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, execPath, "description")

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

	tool := &CustomTool{
		execPath:    execPath,
		name:        desc.Name,
		description: desc.Description,
		schema:      &schema,
		timeout:     m.config.Timeout,
		maxOutput:   m.config.MaxOutputSize,
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

	name = strings.TrimPrefix(name, "custom_tool_")
	tool, exists := m.tools[name]
	return tool, exists
}

// CustomTool interface implementation

func (t *CustomTool) Name() string {
	return fmt.Sprintf("custom_tool_%s", t.name)
}

func (t *CustomTool) Description() string {
	return t.description
}

func (t *CustomTool) GenerateSchema() *jsonschema.Schema {
	return t.schema
}

func (t *CustomTool) TracingKVs(parameters string) ([]attribute.KeyValue, error) {
	return []attribute.KeyValue{
		attribute.String("tool.type", "custom"),
		attribute.String("tool.name", t.name),
		attribute.String("tool.exec_path", t.execPath),
	}, nil
}

func (t *CustomTool) ValidateInput(state tooltypes.State, parameters string) error {
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(parameters), &input); err != nil {
		return errors.Wrap(err, "invalid JSON input")
	}

	return nil
}

func (t *CustomTool) Execute(ctx context.Context, state tooltypes.State, parameters string) tooltypes.ToolResult {
	startTime := time.Now()

	execCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, t.execPath, "run")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return &CustomToolResult{
			toolName: t.Name(),
			err:      errors.Wrap(err, "failed to create stdin pipe").Error(),
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
			executionTime: executionTime,
			err:           errorMsg,
		}
	}

	outputStr := string(output)
	if len(outputStr) > t.maxOutput {
		outputStr = outputStr[:t.maxOutput] + "\n\n[TRUNCATED - Output exceeded 100KB limit]"
	}

	// Check if output is a JSON error response
	var jsonError map[string]interface{}
	if err := json.Unmarshal([]byte(outputStr), &jsonError); err == nil {
		if errMsg, ok := jsonError["error"].(string); ok {
			return &CustomToolResult{
				toolName:      t.Name(),
				executionTime: executionTime,
				err:           errMsg,
			}
		}
	}

	return &CustomToolResult{
		toolName:      t.Name(),
		executionTime: executionTime,
		result:        outputStr,
	}
}

type CustomToolResult struct {
	toolName      string
	executionTime time.Duration
	result        string
	err           string
}

func (r *CustomToolResult) GetResult() string {
	return r.result
}

func (r *CustomToolResult) GetError() string {
	return r.err
}

func (r *CustomToolResult) IsError() bool {
	return r.err != ""
}

func (r *CustomToolResult) AssistantFacing() string {
	return tooltypes.StringifyToolResult(r.result, r.err)
}

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
		}
	}

	return result
}

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
