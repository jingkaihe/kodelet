package sysprompt

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
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

// TemplateMetadata holds parsed frontmatter from custom templates
type TemplateMetadata struct {
	Name        string
	Description string
	Defaults    map[string]string
}

// CustomPromptRenderer handles rendering of custom system prompt templates
type CustomPromptRenderer struct {
	fragmentDirs []string
}

// NewCustomPromptRenderer creates a new custom prompt renderer
func NewCustomPromptRenderer(fragmentDirs []string) *CustomPromptRenderer {
	return &CustomPromptRenderer{fragmentDirs: fragmentDirs}
}

// GetFragmentDirs returns the default fragment directories for recipes
func GetFragmentDirs() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return []string{"./recipes"}
	}
	return []string{
		"./recipes",
		filepath.Join(homeDir, ".kodelet/recipes"),
	}
}

// RenderCustomPrompt renders a custom system prompt template
func (r *CustomPromptRenderer) RenderCustomPrompt(ctx context.Context, templatePath, recipeName string, args map[string]string, promptCtx *PromptContext) (string, error) {
	var templateContent string
	var defaults map[string]string

	if templatePath != "" {
		content, meta, err := r.loadTemplateFile(templatePath)
		if err != nil {
			return "", errors.Wrap(err, "failed to load custom system prompt template")
		}
		templateContent = content
		defaults = meta.Defaults
	} else if recipeName != "" {
		content, meta, err := r.loadFromRecipe(recipeName)
		if err != nil {
			return "", errors.Wrap(err, "failed to load recipe as system prompt")
		}
		templateContent = content
		defaults = meta.Defaults
	} else {
		return "", errors.New("either template path or recipe name must be specified")
	}

	mergedArgs := make(map[string]string)
	for k, v := range defaults {
		mergedArgs[k] = v
	}
	for k, v := range args {
		mergedArgs[k] = v
	}

	promptCtx.CustomArgs = mergedArgs

	tmpl, err := template.New("custom-sysprompt").Funcs(template.FuncMap{
		"bash":    r.createBashFunc(ctx),
		"default": r.createDefaultFunc(),
		"env":     os.Getenv,
	}).Parse(templateContent)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse custom system prompt template")
	}

	data := r.buildTemplateData(promptCtx)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", errors.Wrap(err, "failed to execute custom system prompt template")
	}

	return buf.String(), nil
}

// buildTemplateData combines PromptContext fields with CustomArgs into a flat map
func (r *CustomPromptRenderer) buildTemplateData(ctx *PromptContext) map[string]interface{} {
	data := map[string]interface{}{
		"Date":             ctx.Date,
		"WorkingDirectory": ctx.WorkingDirectory,
		"Platform":         ctx.Platform,
		"OSVersion":        ctx.OSVersion,
		"IsGitRepo":        ctx.IsGitRepo,
	}

	for k, v := range ctx.CustomArgs {
		data[k] = v
	}

	return data
}

// createBashFunc creates the bash template function
func (r *CustomPromptRenderer) createBashFunc(ctx context.Context) func(...string) string {
	return func(args ...string) string {
		if len(args) == 0 {
			return "[ERROR: bash function requires at least one argument]"
		}

		command := args[0]
		cmdArgs := args[1:]

		logger.G(ctx).WithFields(map[string]interface{}{
			"command": command,
			"args":    cmdArgs,
		}).Debug("Executing bash command in custom prompt")

		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		execCmd := exec.CommandContext(cmdCtx, command, cmdArgs...)
		output, err := execCmd.CombinedOutput()
		if err != nil {
			logger.G(ctx).WithFields(map[string]interface{}{
				"command": command,
				"args":    cmdArgs,
			}).WithError(err).Warn("Bash command failed in custom prompt")
			return strings.TrimSpace(string(output))
		}
		return strings.TrimSpace(string(output))
	}
}

// createDefaultFunc creates the default template function
func (r *CustomPromptRenderer) createDefaultFunc() func(interface{}, string) string {
	return func(value interface{}, defaultValue string) string {
		if value == nil {
			return defaultValue
		}

		strValue := fmt.Sprint(value)
		if strValue == "" || strValue == "<no value>" {
			return defaultValue
		}

		return strValue
	}
}

// loadTemplateFile loads and parses a template file with optional frontmatter
func (r *CustomPromptRenderer) loadTemplateFile(path string) (string, *TemplateMetadata, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to read template file")
	}

	return r.parseTemplateContent(string(content))
}

// loadFromRecipe loads a fragment/recipe and extracts its content for use as system prompt
func (r *CustomPromptRenderer) loadFromRecipe(name string) (string, *TemplateMetadata, error) {
	for _, dir := range r.fragmentDirs {
		path := filepath.Join(dir, name+".md")
		if _, err := os.Stat(path); err == nil {
			return r.loadTemplateFile(path)
		}
		// Try without .md extension
		path = filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return r.loadTemplateFile(path)
		}
	}
	return "", nil, errors.Errorf("recipe '%s' not found in directories: %v", name, r.fragmentDirs)
}

// parseTemplateContent parses template content with optional YAML frontmatter
func (r *CustomPromptRenderer) parseTemplateContent(content string) (string, *TemplateMetadata, error) {
	md := goldmark.New(
		goldmark.WithExtensions(meta.Meta),
	)

	var buf bytes.Buffer
	pctx := parser.NewContext()

	if err := md.Convert([]byte(content), &buf, parser.WithContext(pctx)); err != nil {
		return "", nil, errors.Wrap(err, "failed to parse markdown")
	}

	metadata := &TemplateMetadata{}
	if metaData := meta.Get(pctx); metaData != nil {
		if name, ok := metaData["name"].(string); ok {
			metadata.Name = name
		}
		if description, ok := metaData["description"].(string); ok {
			metadata.Description = description
		}
		if defaults, ok := metaData["defaults"].(map[interface{}]interface{}); ok {
			metadata.Defaults = make(map[string]string)
			for k, v := range defaults {
				if keyStr, ok := k.(string); ok {
					if valStr, ok := v.(string); ok {
						metadata.Defaults[keyStr] = valStr
					}
				}
			}
		}
	}

	bodyContent := extractBodyContent(content)

	return bodyContent, metadata, nil
}

// extractBodyContent removes YAML frontmatter and returns the body
func extractBodyContent(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}

	lines := strings.Split(content, "\n")
	frontmatterEnd := -1

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}

	if frontmatterEnd == -1 {
		return content
	}

	return strings.TrimSpace(strings.Join(lines[frontmatterEnd+1:], "\n"))
}
