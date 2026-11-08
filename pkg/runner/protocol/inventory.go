package protocol

import llmtypes "github.com/jingkaihe/kodelet/pkg/types/llm"

// Inventory scopes say where an item was installed.
const (
	InventoryScopeRepo    = "repo"
	InventoryScopeGlobal  = "global"
	InventoryScopePath    = "path"
	InventoryScopeSession = "session"
)

// Inventory extension statuses.
const (
	InventoryStatusReady  = "ready"
	InventoryStatusFailed = "failed"
)

// WorkspaceInventoryParams selects the workspace and profiles whose tools a run would load.
type WorkspaceInventoryParams struct {
	// Profile selects an embedded runner's trusted daemon environment projection.
	Profile            string                     `json:"profile,omitempty"`
	CWD                string                     `json:"cwd,omitempty"`
	EnvironmentProfile string                     `json:"environmentProfile,omitempty"`
	Options            *llmtypes.ExecutionOptions `json:"options,omitempty"`
}

// Validate accepts only environment restrictions, like command discovery.
func (p WorkspaceInventoryParams) Validate() error {
	return WorkspaceDiscoverParams{Options: p.Options}.Validate()
}

// InventoryTool is a tool and a one-line summary of what it does. Enabled is
// false when configuration or the active profile excludes the tool.
type InventoryTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// InventoryExtension is a discovered extension and what it registered.
type InventoryExtension struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Version   string          `json:"version,omitempty"`
	Scope     string          `json:"scope"`
	Path      string          `json:"path"`
	Status    string          `json:"status"`
	Error     string          `json:"error,omitempty"`
	Tools     []InventoryTool `json:"tools"`
	Commands  []string        `json:"commands,omitempty"`
	Shortcuts []string        `json:"shortcuts,omitempty"`
	Hooks     []string        `json:"hooks,omitempty"`
}

// InventorySkill is a discovered skill. Enabled is false when configuration excludes it.
type InventorySkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
}

// InventoryPlugin groups the extensions and skills one installed plugin provides.
type InventoryPlugin struct {
	Name       string               `json:"name"`
	Scope      string               `json:"scope"`
	Path       string               `json:"path"`
	Extensions []InventoryExtension `json:"extensions"`
	Skills     []InventorySkill     `json:"skills"`
}

// WorkspaceInventoryResult lists the built-in tools, plugins, extensions and
// skills a run in the workspace would load. Extensions and Skills hold only
// items installed outside a plugin.
type WorkspaceInventoryResult struct {
	CWD                string               `json:"cwd"`
	EnvironmentProfile string               `json:"environmentProfile,omitempty"`
	BuiltinTools       []InventoryTool      `json:"builtinTools"`
	Plugins            []InventoryPlugin    `json:"plugins"`
	Extensions         []InventoryExtension `json:"extensions"`
	Skills             []InventorySkill     `json:"skills"`
}
