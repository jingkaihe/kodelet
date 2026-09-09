package protocol

import (
	"strings"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/pkg/errors"
)

// WorkspaceInspectParams describes one inspection on the runner, without a model turn.
type WorkspaceInspectParams struct {
	CWD                string            `json:"cwd,omitempty"`
	Profile            string            `json:"profile,omitempty"`
	EnvironmentProfile string            `json:"environmentProfile,omitempty"`
	Operation          string            `json:"operation"`
	Name               string            `json:"name,omitempty"`
	Arguments          map[string]string `json:"arguments,omitempty"`
}

func (p WorkspaceInspectParams) Validate() error {
	switch p.Operation {
	case "recipe.list", "extension.list":
		if p.Name != "" {
			return errors.New("list does not accept a name")
		}
	case "recipe.show", "extension.inspect":
		if strings.TrimSpace(p.Name) == "" {
			return errors.New("a recipe or extension name is required")
		}
	default:
		return errors.New("unsupported workspace inspection")
	}
	if len(p.Arguments) > 0 && p.Operation != "recipe.show" {
		return errors.New("arguments are only supported for recipe show")
	}
	return nil
}

type ExtensionInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Path      string `json:"path"`
	Directory string `json:"directory"`
	PluginRef string `json:"plugin_ref,omitempty"`
}

type WorkspaceInspectResult struct {
	CWD                string                `json:"cwd"`
	EnvironmentProfile string                `json:"environmentProfile,omitempty"`
	Recipes            []*fragments.Fragment `json:"recipes,omitempty"`
	Recipe             *fragments.Fragment   `json:"recipe,omitempty"`
	Extensions         []ExtensionInfo       `json:"extensions,omitempty"`
	Extension          *ExtensionInfo        `json:"extension,omitempty"`
}
