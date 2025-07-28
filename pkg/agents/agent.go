package agents

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"

	"github.com/jingkaihe/kodelet/pkg/logger"
)

// AgentMetadata represents the YAML frontmatter configuration for an agent
type AgentMetadata struct {
	Name             string                `yaml:"name"`
	Description      string                `yaml:"description"`
	Provider         string                `yaml:"provider"`                   // required: anthropic, openai
	Model            string                `yaml:"model"`                      // required: model name
	ReasoningEffort  string                `yaml:"reasoning_effort,omitempty"` // OpenAI specific: low, medium, high
	MaxTokens        int                   `yaml:"max_tokens"`                 // required: maximum tokens
	AllowedTools     []string              `yaml:"allowed_tools,omitempty"`    // tools this agent can use
	AllowedCommands  []string              `yaml:"allowed_commands,omitempty"` // bash commands this agent can execute
	ThinkingBudget   int                   `yaml:"thinking_budget,omitempty"`  // Anthropic specific thinking budget
	OpenAIConfig     *OpenAIAgentConfig    `yaml:"openai,omitempty"`           // OpenAI-compatible provider config
}

// OpenAIAgentConfig holds OpenAI-compatible provider configuration for agents
type OpenAIAgentConfig struct {
	BaseURL      string `yaml:"base_url,omitempty"`       // Custom API base URL
	APIKeyEnvVar string `yaml:"api_key_env_var,omitempty"` // Environment variable name for API key
}

// Agent represents a loaded agent with its metadata, system prompt, and file path
type Agent struct {
	Metadata     AgentMetadata
	SystemPrompt string
	Path         string
}

// AgentProcessor handles loading and processing of agent definitions from disk
type AgentProcessor struct {
	agentDirs []string
}

// AgentProcessorOption configures an AgentProcessor
type AgentProcessorOption func(*AgentProcessor) error

// WithAgentDirs sets custom agent directories
func WithAgentDirs(dirs ...string) AgentProcessorOption {
	return func(ap *AgentProcessor) error {
		if len(dirs) == 0 {
			return errors.New("at least one agent directory must be specified")
		}
		ap.agentDirs = dirs
		return nil
	}
}

// WithDefaultDirs sets the default agent directories (./agents, ~/.kodelet/agents)
func WithDefaultDirs() AgentProcessorOption {
	return func(ap *AgentProcessor) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return errors.Wrap(err, "failed to get user home directory")
		}
		ap.agentDirs = []string{
			"./agents",                                    // Repository-specific (higher precedence)
			filepath.Join(homeDir, ".kodelet", "agents"), // User home directory
		}
		return nil
	}
}

// NewAgentProcessor creates a new agent processor with optional configuration
func NewAgentProcessor(opts ...AgentProcessorOption) (*AgentProcessor, error) {
	ap := &AgentProcessor{}

	// If no options provided, use defaults
	if len(opts) == 0 {
		if err := WithDefaultDirs()(ap); err != nil {
			return nil, errors.Wrap(err, "failed to apply default agent directories")
		}
		return ap, nil
	}

	// Apply provided options
	for _, opt := range opts {
		if err := opt(ap); err != nil {
			return nil, errors.Wrap(err, "failed to apply agent processor option")
		}
	}

	// If no directories were set after applying options, apply defaults
	if len(ap.agentDirs) == 0 {
		if err := WithDefaultDirs()(ap); err != nil {
			return nil, errors.Wrap(err, "failed to apply default agent directories")
		}
	}

	return ap, nil
}

// findAgentFile searches for an agent file in the configured directories
func (ap *AgentProcessor) findAgentFile(agentName string) (string, error) {
	// Try both .md and no extension
	possibleNames := []string{
		agentName + ".md",
		agentName,
	}

	for _, dir := range ap.agentDirs {
		for _, name := range possibleNames {
			fullPath := filepath.Join(dir, name)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}
	}

	return "", errors.Errorf("agent '%s' not found in directories: %v", agentName, ap.agentDirs)
}

// parseFrontmatter extracts YAML frontmatter and body content from agent markdown file
func (ap *AgentProcessor) parseFrontmatter(content string) (AgentMetadata, string, error) {
	var metadata AgentMetadata

	md := goldmark.New(
		goldmark.WithExtensions(
			meta.Meta,
		),
	)

	source := []byte(content)
	var buf bytes.Buffer
	pctx := parser.NewContext()

	if err := md.Convert(source, &buf, parser.WithContext(pctx)); err != nil {
		return metadata, content, errors.Wrap(err, "failed to convert markdown")
	}

	metaData := meta.Get(pctx)
	if metaData != nil {
		// Parse metadata fields
		if name, ok := metaData["name"].(string); ok {
			metadata.Name = name
		}
		if description, ok := metaData["description"].(string); ok {
			metadata.Description = description
		}
		if provider, ok := metaData["provider"].(string); ok {
			metadata.Provider = provider
		}
		if model, ok := metaData["model"].(string); ok {
			metadata.Model = model
		}
		if reasoningEffort, ok := metaData["reasoning_effort"].(string); ok {
			metadata.ReasoningEffort = reasoningEffort
		}
		if maxTokens, ok := metaData["max_tokens"].(int); ok {
			metadata.MaxTokens = maxTokens
		}
		if thinkingBudget, ok := metaData["thinking_budget"].(int); ok {
			metadata.ThinkingBudget = thinkingBudget
		}

		// Parse allowed_tools and allowed_commands
		if allowedTools := metaData["allowed_tools"]; allowedTools != nil {
			metadata.AllowedTools = ap.parseStringArrayField(allowedTools)
		}
		if allowedCommands := metaData["allowed_commands"]; allowedCommands != nil {
			metadata.AllowedCommands = ap.parseStringArrayField(allowedCommands)
		}

		// Parse OpenAI config
		if openaiConfig := metaData["openai"]; openaiConfig != nil {
			if configMap, ok := openaiConfig.(map[interface{}]interface{}); ok {
				oaiConfig := &OpenAIAgentConfig{}
				if baseURL, ok := configMap["base_url"].(string); ok {
					oaiConfig.BaseURL = baseURL
				}
				if apiKeyEnvVar, ok := configMap["api_key_env_var"].(string); ok {
					oaiConfig.APIKeyEnvVar = apiKeyEnvVar
				}
				metadata.OpenAIConfig = oaiConfig
			}
		}
	}

	bodyContent := ap.extractBodyContent(content)
	return metadata, bodyContent, nil
}

// parseStringArrayField handles both []interface{} (YAML array) and string (comma-separated) formats
func (ap *AgentProcessor) parseStringArrayField(field interface{}) []string {
	switch v := field.(type) {
	case []interface{}:
		var result []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, strings.TrimSpace(str))
			}
		}
		return result
	case string:
		if v == "" {
			return []string{}
		}
		var result []string
		for _, item := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	default:
		return []string{}
	}
}

// extractBodyContent extracts the markdown body content after YAML frontmatter
func (ap *AgentProcessor) extractBodyContent(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}

	lines := strings.Split(content, "\n")
	var frontmatterEnd = -1

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}

	if frontmatterEnd == -1 {
		return content
	}

	contentLines := lines[frontmatterEnd+1:]
	return strings.Join(contentLines, "\n")
}

// LoadAgent loads a single agent by name
func (ap *AgentProcessor) LoadAgent(ctx context.Context, agentName string) (*Agent, error) {
	logger.G(ctx).WithField("agent", agentName).Debug("Loading agent")

	agentPath, err := ap.findAgentFile(agentName)
	if err != nil {
		return nil, err
	}

	logger.G(ctx).WithField("path", agentPath).Debug("Found agent file")

	content, err := os.ReadFile(agentPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read agent file '%s'", agentPath)
	}

	metadata, systemPrompt, err := ap.parseFrontmatter(string(content))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse frontmatter in agent '%s'", agentPath)
	}

	// Validate and apply defaults for required fields
	if metadata.Name == "" {
		metadata.Name = agentName
	}
	
	// Apply defaults for provider, model, and max_tokens if not specified
	if metadata.Provider == "" {
		metadata.Provider = "anthropic" // Default to Anthropic
	}
	if metadata.Model == "" {
		// Set default model based on provider
		switch metadata.Provider {
		case "anthropic":
			metadata.Model = "claude-3-5-sonnet-20241022"
		case "openai":
			metadata.Model = "gpt-4"
		default:
			return nil, errors.Errorf("agent '%s' has unsupported provider '%s' and no model specified", agentName, metadata.Provider)
		}
	}
	if metadata.MaxTokens == 0 {
		metadata.MaxTokens = 4096 // Default max tokens
	}

	return &Agent{
		Metadata:     metadata,
		SystemPrompt: systemPrompt,
		Path:         agentPath,
	}, nil
}

// ListAgents returns all available agents from the configured directories
func (ap *AgentProcessor) ListAgents(ctx context.Context) ([]*Agent, error) {
	var agents []*Agent
	seen := make(map[string]bool)

	for _, dir := range ap.agentDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory might not exist, continue
			logger.G(ctx).WithField("dir", dir).Debug("Agent directory not found, skipping")
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			agentName := strings.TrimSuffix(name, ".md")

			// Only process if not already seen (precedence: repo > home)
			if !seen[agentName] {
				agent, err := ap.LoadAgent(ctx, agentName)
				if err != nil {
					logger.G(ctx).WithField("agent", agentName).WithError(err).Warn("Failed to load agent, skipping")
					continue
				}

				agents = append(agents, agent)
				seen[agentName] = true
			}
		}
	}

	logger.G(ctx).WithField("count", len(agents)).Info("Loaded agents")
	return agents, nil
}

// ValidateAgent validates that an agent has all required fields and configurations
func (ap *AgentProcessor) ValidateAgent(agent *Agent) error {
	if agent.Metadata.Name == "" {
		return errors.New("agent name is required")
	}
	if agent.Metadata.Provider == "" {
		return errors.New("agent provider is required")
	}
	if agent.Metadata.Model == "" {
		return errors.New("agent model is required")
	}
	if agent.Metadata.MaxTokens <= 0 {
		return errors.New("agent max_tokens must be greater than 0")
	}
	if strings.TrimSpace(agent.SystemPrompt) == "" {
		return errors.New("agent system prompt cannot be empty")
	}

	// Validate provider-specific fields
	switch agent.Metadata.Provider {
	case "openai":
		if agent.Metadata.ReasoningEffort != "" {
			validEfforts := []string{"low", "medium", "high"}
			found := false
			for _, effort := range validEfforts {
				if agent.Metadata.ReasoningEffort == effort {
					found = true
					break
				}
			}
			if !found {
				return errors.Errorf("invalid reasoning_effort '%s', must be one of: %v",
					agent.Metadata.ReasoningEffort, validEfforts)
			}
		}
	case "anthropic":
		// Anthropic-specific validations can be added here
	default:
		return errors.Errorf("unsupported provider '%s'", agent.Metadata.Provider)
	}

	return nil
}