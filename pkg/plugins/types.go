// Package plugins provides a unified plugin system for managing both skills
// (model-invoked capabilities) and recipes (user-invoked templates). It handles
// discovery, installation, and removal of plugins from GitHub repositories.
package plugins

// PluginType represents the type of plugin
type PluginType string

// Plugin types
const (
	PluginTypeSkill  PluginType = "skill"
	PluginTypeRecipe PluginType = "recipe"
)

// Plugin represents a discoverable plugin (skill or recipe)
type Plugin interface {
	Name() string
	Description() string
	Directory() string
	Type() PluginType
}

// PluginMetadata contains common metadata for all plugins
type PluginMetadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// SkillPlugin represents a discovered skill plugin
type SkillPlugin struct {
	name        string
	description string
	directory   string
}

// NewSkillPlugin creates a new skill plugin
func NewSkillPlugin(name, description, directory string) *SkillPlugin {
	return &SkillPlugin{
		name:        name,
		description: description,
		directory:   directory,
	}
}

// Name returns the skill name
func (s *SkillPlugin) Name() string { return s.name }

// Description returns the skill description
func (s *SkillPlugin) Description() string { return s.description }

// Directory returns the skill directory path
func (s *SkillPlugin) Directory() string { return s.directory }

// Type returns the plugin type (skill)
func (s *SkillPlugin) Type() PluginType { return PluginTypeSkill }

// RecipePlugin represents a discovered recipe plugin
type RecipePlugin struct {
	name        string
	description string
	directory   string
}

// NewRecipePlugin creates a new recipe plugin
func NewRecipePlugin(name, description, directory string) *RecipePlugin {
	return &RecipePlugin{
		name:        name,
		description: description,
		directory:   directory,
	}
}

// Name returns the recipe name
func (r *RecipePlugin) Name() string { return r.name }

// Description returns the recipe description
func (r *RecipePlugin) Description() string { return r.description }

// Directory returns the recipe directory path
func (r *RecipePlugin) Directory() string { return r.directory }

// Type returns the plugin type (recipe)
func (r *RecipePlugin) Type() PluginType { return PluginTypeRecipe }

// InstalledPlugin represents a plugin package that may contain multiple skills and recipes
type InstalledPlugin struct {
	Name    string   // Plugin package name (e.g., "my-plugin-repo")
	Path    string   // Full path to the plugin directory
	Skills  []string // List of skill names contained in this plugin
	Recipes []string // List of recipe names contained in this plugin
}

// PluginDirConfig represents a plugin directory with its prefix for discovery.
// Used by skills and fragments packages for plugin-based discovery.
type PluginDirConfig struct {
	Dir    string // Directory path containing skills or recipes
	Prefix string // Prefix to prepend to discovered item names (e.g., "org/repo/")
}
