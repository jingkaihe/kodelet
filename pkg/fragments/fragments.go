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
)

// FragmentConfig holds configuration for fragment processing
type FragmentConfig struct {
	FragmentName string
	Arguments    map[string]string
}

// FragmentProcessor handles fragment loading and rendering
type FragmentProcessor struct {
	fragmentDirs []string
}

// FragmentProcessorOption is a function that configures a FragmentProcessor
type FragmentProcessorOption func(*FragmentProcessor) error

// WithFragmentDirs sets custom fragment directories
func WithFragmentDirs(dirs ...string) FragmentProcessorOption {
	return func(fp *FragmentProcessor) error {
		if len(dirs) == 0 {
			return errors.New("at least one fragment directory must be specified")
		}
		fp.fragmentDirs = dirs
		return nil
	}
}

// WithAdditionalFragmentDirs adds additional fragment directories to the current ones
// If no directories are currently set, it starts with defaults first
// If dirs is empty, this is a no-op
func WithAdditionalFragmentDirs(dirs ...string) FragmentProcessorOption {
	return func(fp *FragmentProcessor) error {
		// If no directories provided, this is a no-op
		if len(dirs) == 0 {
			return nil
		}
		
		// If no directories are set yet, start with defaults
		if len(fp.fragmentDirs) == 0 {
			if err := WithDefaultFragmentDirs()(fp); err != nil {
				return errors.Wrap(err, "failed to initialize with default directories")
			}
		}
		
		fp.fragmentDirs = append(fp.fragmentDirs, dirs...)
		return nil
	}
}

// WithDefaultFragmentDirs resets to default fragment directories
func WithDefaultFragmentDirs() FragmentProcessorOption {
	return func(fp *FragmentProcessor) error {
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
func NewFragmentProcessor(opts ...FragmentProcessorOption) (*FragmentProcessor, error) {
	// Start with empty processor
	fp := &FragmentProcessor{}
	
	// If no options provided, use defaults
	if len(opts) == 0 {
		if err := WithDefaultFragmentDirs()(fp); err != nil {
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
		if err := WithDefaultFragmentDirs()(fp); err != nil {
			return nil, errors.Wrap(err, "failed to apply default fragment directories")
		}
	}
	
	return fp, nil
}

// findFragmentFile searches for a fragment file in the configured directories
func (fp *FragmentProcessor) findFragmentFile(fragmentName string) (string, error) {
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

// LoadFragment loads and processes a fragment with the given arguments
func (fp *FragmentProcessor) LoadFragment(ctx context.Context, config *FragmentConfig) (string, error) {
	logger.G(ctx).WithField("fragment", config.FragmentName).Debug("Loading fragment")

	// Find the fragment file
	fragmentPath, err := fp.findFragmentFile(config.FragmentName)
	if err != nil {
		return "", err
	}

	logger.G(ctx).WithField("path", fragmentPath).Debug("Found fragment file")

	// Read the fragment content
	content, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", errors.Wrapf(err, "failed to read fragment file '%s'", fragmentPath)
	}

	// Process the template
	processed, err := fp.processTemplate(ctx, string(content), config.Arguments)
	if err != nil {
		return "", errors.Wrapf(err, "failed to process fragment template '%s'", fragmentPath)
	}

	return processed, nil
}

// processTemplate processes a template string with variable substitution and bash command execution using FuncMap
func (fp *FragmentProcessor) processTemplate(ctx context.Context, templateContent string, args map[string]string) (string, error) {
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
func (fp *FragmentProcessor) createBashFunc(ctx context.Context) func(...string) string {
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
func (fp *FragmentProcessor) ListFragments() ([]string, error) {
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
