package fragments

import (
	"bytes"
	"context"
	"embed"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

//go:embed recipes
var embedFS embed.FS

// Metadata represents YAML frontmatter in fragment files
type Metadata struct {
	Name            string   `yaml:"name,omitempty"`
	Description     string   `yaml:"description,omitempty"`
	AllowedTools    []string `yaml:"allowed_tools,omitempty"`
	AllowedCommands []string `yaml:"allowed_commands,omitempty"`
}

// Fragment represents a fragment with its metadata and content
type Fragment struct {
	ID       string
	Metadata Metadata
	Content  string
	Path     string
}

// Config holds configuration for fragment processing
type Config struct {
	FragmentName string
	Arguments    map[string]string
}

// Processor handles fragment loading and rendering
type Processor struct {
	fragmentDirs     []string
	builtinRecipesFS fs.FS
}

// Option is a function that configures a FragmentProcessor
type Option func(*Processor) error

// WithFragmentDirs sets custom fragment directories
func WithFragmentDirs(dirs ...string) Option {
	return func(fp *Processor) error {
		if len(dirs) == 0 {
			return errors.New("at least one fragment directory must be specified")
		}
		fp.fragmentDirs = dirs
		return nil
	}
}

// WithAdditionalDirs adds additional fragment directories to the current ones
// If no directories are currently set, it starts with defaults first
// If dirs is empty, this is a no-op
func WithAdditionalDirs(dirs ...string) Option {
	return func(fp *Processor) error {
		// If no directories provided, this is a no-op
		if len(dirs) == 0 {
			return nil
		}

		// If no directories are set yet, start with defaults
		if len(fp.fragmentDirs) == 0 {
			if err := WithDefaultDirs()(fp); err != nil {
				return errors.Wrap(err, "failed to initialize with default directories")
			}
		}

		fp.fragmentDirs = append(fp.fragmentDirs, dirs...)
		return nil
	}
}

// WithDefaultDirs resets to default fragment directories
func WithDefaultDirs() Option {
	return func(fp *Processor) error {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return errors.Wrap(err, "failed to get user home directory")
		}
		fp.fragmentDirs = []string{
			"./recipes", // Repo-specific (higher precedence)
			filepath.Join(homeDir, ".kodelet/recipes"), // User home directory
		}
		return nil
	}
}

// NewFragmentProcessor creates a new fragment processor with optional configuration
func NewFragmentProcessor(opts ...Option) (*Processor, error) {
	// Start with empty processor
	fp := &Processor{}

	// Create embedded recipes filesystem once
	builtinRecipesFS, err := fs.Sub(embedFS, "recipes")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create built-in recipes filesystem")
	}
	fp.builtinRecipesFS = builtinRecipesFS

	// If no options provided, use defaults
	if len(opts) == 0 {
		if err := WithDefaultDirs()(fp); err != nil {
			return nil, errors.Wrap(err, "failed to apply default fragment directories")
		}
		return fp, nil
	}

	// Apply provided options
	for _, opt := range opts {
		if err := opt(fp); err != nil {
			return nil, errors.Wrap(err, "failed to apply fragment processor option")
		}
	}

	// If no directories were set after applying options, apply defaults
	if len(fp.fragmentDirs) == 0 {
		if err := WithDefaultDirs()(fp); err != nil {
			return nil, errors.Wrap(err, "failed to apply default fragment directories")
		}
	}

	return fp, nil
}

// readEmbedded reads an embedded recipe file
func (fp *Processor) readEmbedded(name string) ([]byte, error) {
	return fs.ReadFile(fp.builtinRecipesFS, name)
}

// readFragmentContent reads fragment content from either file system or embedded resources
func (fp *Processor) readFragmentContent(path string) ([]byte, error) {
	if strings.HasPrefix(path, "embed:") {
		name := strings.TrimPrefix(path, "embed:")
		return fp.readEmbedded(name)
	}
	return os.ReadFile(path)
}

func (fp *Processor) findFragmentFile(fragmentName string) (string, error) {
	fragmentName = filepath.ToSlash(fragmentName)
	
	possibleNames := []string{
		fragmentName + ".md",
		fragmentName,
	}

	for _, dir := range fp.fragmentDirs {
		for _, name := range possibleNames {
			fullPath := filepath.Join(dir, filepath.FromSlash(name))
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}
	}

	for _, name := range possibleNames {
		if _, err := fs.Stat(fp.builtinRecipesFS, name); err == nil {
			return "embed:" + name, nil
		}
	}

	return "", errors.Errorf("fragment '%s' not found in directories: %v or built-in recipes", fragmentName, fp.fragmentDirs)
}

func (fp *Processor) parseFrontmatter(content string) (Metadata, string, error) {
	var metadata Metadata

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
		if name, ok := metaData["name"].(string); ok {
			metadata.Name = name
		}
		if description, ok := metaData["description"].(string); ok {
			metadata.Description = description
		}

		// Parse allowed_tools (support both string array and comma-separated string)
		if allowedTools := metaData["allowed_tools"]; allowedTools != nil {
			metadata.AllowedTools = fp.parseStringArrayField(allowedTools)
		}

		// Parse allowed_commands (support both string array and comma-separated string)
		if allowedCommands := metaData["allowed_commands"]; allowedCommands != nil {
			metadata.AllowedCommands = fp.parseStringArrayField(allowedCommands)
		}
	}

	bodyContent := fp.extractBodyContent(content)

	return metadata, bodyContent, nil
}

// parseStringArrayField handles both []interface{} (YAML array) and string (comma-separated) formats
func (fp *Processor) parseStringArrayField(field interface{}) []string {
	switch v := field.(type) {
	case []interface{}:
		// YAML array format: ["tool1", "tool2"]
		var result []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, strings.TrimSpace(str))
			}
		}
		return result
	case string:
		// Comma-separated string format: "tool1,tool2,tool3"
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

func (fp *Processor) extractBodyContent(content string) string {
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

func (fp *Processor) LoadFragment(ctx context.Context, config *Config) (*Fragment, error) {
	logger.G(ctx).WithField("fragment", config.FragmentName).Debug("Loading fragment with metadata")

	fragmentPath, err := fp.findFragmentFile(config.FragmentName)
	if err != nil {
		return nil, err
	}

	logger.G(ctx).WithField("path", fragmentPath).Debug("Found fragment file")

	content, err := fp.readFragmentContent(fragmentPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read fragment file '%s'", fragmentPath)
	}

	metadata, bodyContent, err := fp.parseFrontmatter(string(content))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse frontmatter in fragment '%s'", fragmentPath)
	}

	processed, err := fp.processTemplate(ctx, bodyContent, config.Arguments)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to process fragment template '%s'", fragmentPath)
	}

	displayPath := fragmentPath
	if strings.HasPrefix(fragmentPath, "embed:") {
		displayPath = "builtin:" + strings.TrimPrefix(fragmentPath, "embed:")
	}

	return &Fragment{
		Metadata: metadata,
		Content:  processed,
		Path:     displayPath,
	}, nil
}

func (fp *Processor) GetFragmentMetadata(fragmentName string) (*Fragment, error) {
	fragmentPath, err := fp.findFragmentFile(fragmentName)
	if err != nil {
		return nil, err
	}

	content, err := fp.readFragmentContent(fragmentPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read fragment file '%s'", fragmentPath)
	}

	metadata, bodyContent, err := fp.parseFrontmatter(string(content))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse frontmatter in fragment '%s'", fragmentPath)
	}

	displayPath := fragmentPath
	if strings.HasPrefix(fragmentPath, "embed:") {
		displayPath = "builtin:" + strings.TrimPrefix(fragmentPath, "embed:")
	}

	return &Fragment{
		Metadata: metadata,
		Content:  bodyContent,
		Path:     displayPath,
	}, nil
}

// processTemplate processes a template string with variable substitution and bash command execution using FuncMap
func (fp *Processor) processTemplate(ctx context.Context, templateContent string, args map[string]string) (string, error) {
	// Create template with custom FuncMap for bash command execution
	tmpl, err := template.New("fragment").Funcs(template.FuncMap{
		"bash": fp.createBashFunc(ctx),
	}).Parse(templateContent)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse template")
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, args)
	if err != nil {
		return "", errors.Wrap(err, "failed to execute template")
	}

	return buf.String(), nil
}

// createBashFunc returns a function that can be used in templates to execute bash commands
func (fp *Processor) createBashFunc(ctx context.Context) func(...string) string {
	return func(args ...string) string {
		if len(args) == 0 {
			return "[ERROR: bash function requires at least one argument]"
		}

		// First argument is the command, rest are arguments
		command := args[0]
		cmdArgs := args[1:]

		logger.G(ctx).WithFields(map[string]interface{}{
			"command": command,
			"args":    cmdArgs,
		}).Debug("Executing bash command")

		// Execute the command with timeout
		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx, command, cmdArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.G(ctx).WithFields(map[string]interface{}{
				"command": command,
				"args":    cmdArgs,
			}).WithError(err).Warn("Bash command failed")

			return strings.TrimRight(string(output), "\n\r")
		}

		// Remove trailing newlines for cleaner substitution
		return strings.TrimRight(string(output), "\n\r")
	}
}

func (fp *Processor) processFragmentEntry(name, path string, content []byte, seen map[string]bool) *Fragment {
	fragmentName := strings.TrimSuffix(name, ".md")
	fragmentName = filepath.ToSlash(fragmentName)

	if seen[fragmentName] {
		return nil
	}

	metadata, bodyContent, err := fp.parseFrontmatter(string(content))
	if err != nil {
		return nil
	}

	if metadata.Name == "" {
		metadata.Name = filepath.Base(fragmentName)
	}

	seen[fragmentName] = true
	return &Fragment{
		ID:       fragmentName,
		Metadata: metadata,
		Content:  bodyContent,
		Path:     path,
	}
}

func (fp *Processor) processFragmentsFromFS(fragmentsFS fs.FS, pathConstructor func(string) string, fragments *[]*Fragment, seen map[string]bool) {
	fp.walkFragmentsDir(fragmentsFS, ".", pathConstructor, fragments, seen)
}

func (fp *Processor) walkFragmentsDir(fragmentsFS fs.FS, dir string, pathConstructor func(string) string, fragments *[]*Fragment, seen map[string]bool) {
	entries, err := fs.ReadDir(fragmentsFS, dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		
		if entry.IsDir() {
			fp.walkFragmentsDir(fragmentsFS, entryPath, pathConstructor, fragments, seen)
			continue
		}

		content, err := fs.ReadFile(fragmentsFS, entryPath)
		if err != nil {
			continue
		}

		displayPath := pathConstructor(entryPath)
		if fragment := fp.processFragmentEntry(entryPath, displayPath, content, seen); fragment != nil {
			*fragments = append(*fragments, fragment)
		}
	}
}

func (fp *Processor) ListFragmentsWithMetadata() ([]*Fragment, error) {
	var fragments []*Fragment
	seen := make(map[string]bool)

	for _, dir := range fp.fragmentDirs {
		dirFS := os.DirFS(dir)
		fp.processFragmentsFromFS(dirFS, func(name string) string {
			return filepath.Join(dir, name)
		}, &fragments, seen)
	}

	fp.processFragmentsFromFS(fp.builtinRecipesFS, func(name string) string {
		fragmentName := strings.TrimSuffix(name, ".md")
		return "builtin:" + fragmentName + ".md"
	}, &fragments, seen)

	return fragments, nil
}
