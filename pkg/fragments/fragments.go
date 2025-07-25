package fragments

import (
	"bytes"
	"context"
	"fmt"
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

// Metadata represents YAML frontmatter in fragment files
type Metadata struct {
	Name        string `yaml:"name,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Fragment represents a fragment with its metadata and content
type Fragment struct {
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
	fragmentDirs []string
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
			"./receipts", // Repo-specific (higher precedence)
			filepath.Join(homeDir, ".kodelet/receipts"), // User home directory
		}
		return nil
	}
}

// NewFragmentProcessor creates a new fragment processor with optional configuration
func NewFragmentProcessor(opts ...Option) (*Processor, error) {
	// Start with empty processor
	fp := &Processor{}

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

// findFragmentFile searches for a fragment file in the configured directories
func (fp *Processor) findFragmentFile(fragmentName string) (string, error) {
	// Try both .md and no extension
	possibleNames := []string{
		fragmentName + ".md",
		fragmentName,
	}

	for _, dir := range fp.fragmentDirs {
		for _, name := range possibleNames {
			fullPath := filepath.Join(dir, name)
			if _, err := os.Stat(fullPath); err == nil {
				return fullPath, nil
			}
		}
	}

	return "", errors.Errorf("fragment '%s' not found in directories: %v", fragmentName, fp.fragmentDirs)
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
	}

	bodyContent := fp.extractBodyContent(content)

	return metadata, bodyContent, nil
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

func (fp *Processor) LoadFragment(ctx context.Context, config *Config) (string, error) {
	logger.G(ctx).WithField("fragment", config.FragmentName).Debug("Loading fragment")

	fragmentPath, err := fp.findFragmentFile(config.FragmentName)
	if err != nil {
		return "", err
	}

	logger.G(ctx).WithField("path", fragmentPath).Debug("Found fragment file")

	content, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read fragment file '%s'", fragmentPath)
	}

	_, bodyContent, err := fp.parseFrontmatter(string(content))
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse frontmatter in fragment '%s'", fragmentPath)
	}

	processed, err := fp.processTemplate(ctx, bodyContent, config.Arguments)
	if err != nil {
		return "", errors.Wrapf(err, "failed to process fragment template '%s'", fragmentPath)
	}

	return processed, nil
}

func (fp *Processor) LoadFragmentWithMetadata(ctx context.Context, config *Config) (*Fragment, error) {
	logger.G(ctx).WithField("fragment", config.FragmentName).Debug("Loading fragment with metadata")

	fragmentPath, err := fp.findFragmentFile(config.FragmentName)
	if err != nil {
		return nil, err
	}

	logger.G(ctx).WithField("path", fragmentPath).Debug("Found fragment file")

	content, err := os.ReadFile(fragmentPath)
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

	return &Fragment{
		Metadata: metadata,
		Content:  processed,
		Path:     fragmentPath,
	}, nil
}

func (fp *Processor) GetFragmentMetadata(fragmentName string) (*Fragment, error) {
	fragmentPath, err := fp.findFragmentFile(fragmentName)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(fragmentPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read fragment file '%s'", fragmentPath)
	}

	metadata, bodyContent, err := fp.parseFrontmatter(string(content))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse frontmatter in fragment '%s'", fragmentPath)
	}

	return &Fragment{
		Metadata: metadata,
		Content:  bodyContent,
		Path:     fragmentPath,
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
			fullCmd := append([]string{command}, cmdArgs...)
			logger.G(ctx).WithFields(map[string]interface{}{
				"command": command,
				"args":    cmdArgs,
			}).WithError(err).Warn("Bash command failed")
			return fmt.Sprintf("[ERROR executing command '%s': %v]", strings.Join(fullCmd, " "), err)
		}

		// Remove trailing newlines for cleaner substitution
		return strings.TrimRight(string(output), "\n\r")
	}
}

// ListFragments returns a list of available fragments
func (fp *Processor) ListFragments() ([]string, error) {
	var fragments []string
	seen := make(map[string]bool)

	for _, dir := range fp.fragmentDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory might not exist, continue
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			// Remove .md extension if present
			name = strings.TrimSuffix(name, ".md")

			// Only add if not already seen (precedence: repo > home)
			if !seen[name] {
				fragments = append(fragments, name)
				seen[name] = true
			}
		}
	}

	return fragments, nil
}

func (fp *Processor) ListFragmentsWithMetadata() ([]*Fragment, error) {
	var fragments []*Fragment
	seen := make(map[string]bool)

	for _, dir := range fp.fragmentDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			fragmentName := strings.TrimSuffix(name, ".md")

			if !seen[fragmentName] {
				fragmentPath := filepath.Join(dir, name)

				content, err := os.ReadFile(fragmentPath)
				if err != nil {
					continue
				}

				metadata, bodyContent, err := fp.parseFrontmatter(string(content))
				if err != nil {
					continue
				}

				if metadata.Name == "" {
					metadata.Name = fragmentName
				}

				fragments = append(fragments, &Fragment{
					Metadata: metadata,
					Content:  bodyContent,
					Path:     fragmentPath,
				})
				seen[fragmentName] = true
			}
		}
	}

	return fragments, nil
}
