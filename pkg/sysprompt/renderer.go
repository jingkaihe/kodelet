package sysprompt

import (
	"io/fs"
	"strings"
	"text/template"

	"github.com/pkg/errors"
)

// Renderer provides prompt template rendering capabilities
type Renderer struct {
	templateFS fs.FS
	cache      map[string]*template.Template
}

// NewRenderer creates a new template renderer
func NewRenderer(fs fs.FS) *Renderer {
	return &Renderer{
		templateFS: fs,
		cache:      make(map[string]*template.Template),
	}
}

// RenderPrompt renders a named template with the provided context
func (r *Renderer) RenderPrompt(name string, ctx *PromptContext) (string, error) {
	tmpl, err := r.getTemplate(name)
	if err != nil {
		return "", errors.Wrapf(err, "failed to get template %s", name)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", errors.Wrapf(err, "failed to execute template %s", name)
	}

	return buf.String(), nil
}

// getTemplate loads a template from the FS or returns it from cache
func (r *Renderer) getTemplate(name string) (*template.Template, error) {
	if tmpl, ok := r.cache[name]; ok {
		return tmpl, nil
	}

	baseName := name
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		baseName = name[idx+1:]
	}

	tmpl := template.New(baseName)

	// We'll need to create a self-reference for the include function
	var selfRef *template.Template

	tmpl = tmpl.Funcs(template.FuncMap{
		"include": func(tplName string, data any) (string, error) {
			var buf strings.Builder
			err := selfRef.ExecuteTemplate(&buf, tplName, data)
			return buf.String(), err
		},
	})

	// Set the self reference after template creation
	selfRef = tmpl

	content, err := fs.ReadFile(r.templateFS, name)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read template file %s", name)
	}

	tmpl, err = tmpl.Parse(string(content))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse template %s", name)
	}

	err = fs.WalkDir(r.templateFS, "templates/components", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		component, err := fs.ReadFile(r.templateFS, path)
		if err != nil {
			return err
		}

		_, err = tmpl.New(path).Parse(string(component))
		return err
	})

	if err != nil {
		return nil, errors.Wrap(err, "failed to load component templates")
	}

	r.cache[name] = tmpl
	return tmpl, nil
}

// RenderSystemPrompt renders the system prompt
func (r *Renderer) RenderSystemPrompt(ctx *PromptContext) (string, error) {
	prompt, err := r.RenderPrompt(SystemTemplate, ctx)
	if err != nil {
		return "", err
	}

	prompt += ctx.FormatSystemInfo()
	prompt += ctx.FormatContexts()

	return prompt, nil
}

// RenderSubagentPrompt renders the subagent prompt
func (r *Renderer) RenderSubagentPrompt(ctx *PromptContext) (string, error) {
	prompt, err := r.RenderPrompt(SubagentTemplate, ctx)
	if err != nil {
		return "", err
	}

	prompt += ctx.FormatSystemInfo()
	prompt += ctx.FormatContexts()

	return prompt, nil
}
