package sysprompt

import (
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"reflect"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/jingkaihe/kodelet/pkg/logger"
	"github.com/pkg/errors"
)

// Renderer provides prompt template rendering capabilities
type Renderer struct {
	templates *template.Template
	parseErr  error
}

var defaultRenderer = NewRenderer(TemplateFS)

// NewRenderer creates a new template renderer
func NewRenderer(fs fs.FS) *Renderer {
	renderer := &Renderer{}
	renderer.templates, renderer.parseErr = parseTemplates(fs, nil)
	return renderer
}

// NewRendererWithTemplateOverride creates a renderer with custom template overrides.
// Overrides are keyed by template path (e.g., templates/system.tmpl).
func NewRendererWithTemplateOverride(fs fs.FS, overrides map[string]string) *Renderer {
	renderer := &Renderer{}
	renderer.templates, renderer.parseErr = parseTemplates(fs, overrides)
	return renderer
}

// RenderPrompt renders a named template with the provided context
func (r *Renderer) RenderPrompt(name string, ctx *PromptContext) (string, error) {
	if r.parseErr != nil {
		return "", errors.Wrap(r.parseErr, "failed to initialize templates")
	}

	if r.templates.Lookup(name) == nil {
		return "", errors.Errorf("template %s not found", name)
	}

	var buf strings.Builder
	if err := r.templates.ExecuteTemplate(&buf, name, ctx); err != nil {
		return "", errors.Wrapf(err, "failed to execute template %s", name)
	}

	return buf.String(), nil
}

func parseTemplates(templateFS fs.FS, overrides map[string]string) (*template.Template, error) {
	templatePaths, err := collectTemplatePaths(templateFS, "templates")
	if err != nil {
		return nil, errors.Wrap(err, "failed to collect template paths")
	}

	templates := template.New("templates")
	var selfRef *template.Template
	templates = templates.Funcs(template.FuncMap{
		"include": func(templateName string, data any) (string, error) {
			var buf strings.Builder
			err := selfRef.ExecuteTemplate(&buf, templateName, data)
			return buf.String(), err
		},
		"hasContextEntries": func(ctx *PromptContext) bool {
			if ctx == nil {
				return false
			}
			return ctx.hasContextEntries()
		},
		"contextEntries": func(ctx *PromptContext) []contextEntry {
			if ctx == nil {
				return nil
			}
			return ctx.contextEntries()
		},
		"hasMCPServers": func(ctx *PromptContext) bool {
			if ctx == nil {
				return false
			}
			return ctx.hasMCPServers()
		},
		"mcpServersCSV": func(ctx *PromptContext) string {
			if ctx == nil {
				return ""
			}
			return ctx.mcpServersCSV()
		},
		"bash":    createBashFunc(context.Background()),
		"default": createDefaultFunc(),
	})
	selfRef = templates

	for _, path := range templatePaths {
		content := ""
		if override, ok := overrides[path]; ok {
			content = override
		} else {
			bytes, err := fs.ReadFile(templateFS, path)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to read template file %s", path)
			}
			content = string(bytes)
		}

		_, err := templates.New(path).Parse(content)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse template %s", path)
		}
	}

	for path, content := range overrides {
		if slices.Contains(templatePaths, path) {
			continue
		}

		_, err := templates.New(path).Parse(content)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to parse override template %s", path)
		}
	}

	return templates, nil
}

func createBashFunc(ctx context.Context) func(...string) string {
	return func(args ...string) string {
		if len(args) == 0 {
			return "[ERROR: bash function requires at least one argument]"
		}

		command := args[0]
		cmdArgs := args[1:]

		logger.G(ctx).WithFields(map[string]any{
			"command": command,
			"args":    cmdArgs,
		}).Debug("executing sysprompt bash command")

		cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx, command, cmdArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.G(ctx).WithFields(map[string]any{
				"command": command,
				"args":    cmdArgs,
			}).WithError(err).Warn("sysprompt bash command failed")
		}

		return strings.TrimRight(string(output), "\n\r")
	}
}

func createDefaultFunc() func(any, string) string {
	return func(value any, defaultValue string) string {
		if value == nil {
			return defaultValue
		}

		v := reflect.ValueOf(value)
		if v.Kind() == reflect.Map {
			if v.IsNil() || v.Len() == 0 {
				return defaultValue
			}
		}

		strValue := fmt.Sprint(value)
		if strValue == "" || strValue == "<no value>" {
			return defaultValue
		}

		return strValue
	}
}

func collectTemplatePaths(templateFS fs.FS, dir string) ([]string, error) {
	if _, err := fs.Stat(templateFS, dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var paths []string
	err := fs.WalkDir(templateFS, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return nil
		}

		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}

// RenderSystemPrompt renders the system prompt
func (r *Renderer) RenderSystemPrompt(ctx *PromptContext) (string, error) {
	return r.RenderPrompt(SystemTemplate, ctx)
}
