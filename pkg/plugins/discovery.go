package plugins

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

const (
	skillFileName = "SKILL.md"
	pluginsSubdir = "plugins"
	skillsSubdir  = "skills"
	recipesSubdir = "recipes"
	kodeletDir    = ".kodelet"
)

// Discovery handles plugin discovery from configured directories
type Discovery struct {
	baseDir string // ".kodelet" or absolute path for repo-local
	homeDir string
}

// DiscoveryOption configures a Discovery instance
type DiscoveryOption func(*Discovery) error

// WithBaseDir sets a custom base directory (for testing)
func WithBaseDir(dir string) DiscoveryOption {
	return func(d *Discovery) error {
		d.baseDir = dir
		return nil
	}
}

// WithHomeDir sets a custom home directory (for testing)
func WithHomeDir(dir string) DiscoveryOption {
	return func(d *Discovery) error {
		d.homeDir = dir
		return nil
	}
}

// NewDiscovery creates a new plugin discovery instance
func NewDiscovery(opts ...DiscoveryOption) (*Discovery, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user home directory")
	}

	d := &Discovery{
		baseDir: kodeletDir,
		homeDir: homeDir,
	}

	for _, opt := range opts {
		if err := opt(d); err != nil {
			return nil, err
		}
	}

	return d, nil
}

// SkillDirs returns the skill discovery directories in precedence order
func (d *Discovery) SkillDirs() []string {
	dirs := []string{
		filepath.Join(d.baseDir, skillsSubdir),
	}

	dirs = append(dirs, d.pluginSkillDirs(d.baseDir)...)

	dirs = append(dirs, filepath.Join(d.homeDir, kodeletDir, skillsSubdir))

	dirs = append(dirs, d.pluginSkillDirs(filepath.Join(d.homeDir, kodeletDir))...)

	return dirs
}

// RecipeDirs returns the recipe discovery directories in precedence order
func (d *Discovery) RecipeDirs() []string {
	dirs := []string{
		filepath.Join(d.baseDir, recipesSubdir),
	}

	dirs = append(dirs, d.pluginRecipeDirs(d.baseDir)...)

	dirs = append(dirs, filepath.Join(d.homeDir, kodeletDir, recipesSubdir))

	dirs = append(dirs, d.pluginRecipeDirs(filepath.Join(d.homeDir, kodeletDir))...)

	return dirs
}

// pluginSkillDirs returns skill directories from all plugins under baseDir
// Plugin directories use "org@repo" naming format
func (d *Discovery) pluginSkillDirs(baseDir string) []string {
	pluginsDir := filepath.Join(baseDir, pluginsSubdir)
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}

	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(pluginsDir, entry.Name(), skillsSubdir)
		if _, err := os.Stat(skillDir); err == nil {
			dirs = append(dirs, skillDir)
		}
	}
	return dirs
}

// pluginRecipeDirs returns recipe directories from all plugins under baseDir
// Plugin directories use "org@repo" naming format
func (d *Discovery) pluginRecipeDirs(baseDir string) []string {
	pluginsDir := filepath.Join(baseDir, pluginsSubdir)
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}

	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		recipeDir := filepath.Join(pluginsDir, entry.Name(), recipesSubdir)
		if _, err := os.Stat(recipeDir); err == nil {
			dirs = append(dirs, recipeDir)
		}
	}
	return dirs
}

// DiscoverAll discovers all plugins (skills and recipes) with proper naming
func (d *Discovery) DiscoverAll() ([]Plugin, error) {
	var plugins []Plugin
	seen := make(map[string]bool)

	skills, _ := d.discoverSkillsFromDir(filepath.Join(d.baseDir, skillsSubdir), "")
	for _, s := range skills {
		if !seen[s.Name()] {
			plugins = append(plugins, s)
			seen[s.Name()] = true
		}
	}

	recipes, _ := d.discoverRecipesFromDir(filepath.Join(d.baseDir, recipesSubdir), "")
	for _, r := range recipes {
		if !seen[r.Name()] {
			plugins = append(plugins, r)
			seen[r.Name()] = true
		}
	}

	d.discoverFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), &plugins, seen)

	skills, _ = d.discoverSkillsFromDir(filepath.Join(d.homeDir, kodeletDir, skillsSubdir), "")
	for _, s := range skills {
		if !seen[s.Name()] {
			plugins = append(plugins, s)
			seen[s.Name()] = true
		}
	}

	recipes, _ = d.discoverRecipesFromDir(filepath.Join(d.homeDir, kodeletDir, recipesSubdir), "")
	for _, r := range recipes {
		if !seen[r.Name()] {
			plugins = append(plugins, r)
			seen[r.Name()] = true
		}
	}

	d.discoverFromPluginsDir(filepath.Join(d.homeDir, kodeletDir, pluginsSubdir), &plugins, seen)

	return plugins, nil
}

// DiscoverSkills discovers all skills with proper naming and precedence
func (d *Discovery) DiscoverSkills() (map[string]Plugin, error) {
	skills := make(map[string]Plugin)

	standaloneSkills, _ := d.discoverSkillsFromDir(filepath.Join(d.baseDir, skillsSubdir), "")
	for _, s := range standaloneSkills {
		if _, exists := skills[s.Name()]; !exists {
			skills[s.Name()] = s
		}
	}

	d.discoverSkillsFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), skills)

	globalSkills, _ := d.discoverSkillsFromDir(filepath.Join(d.homeDir, kodeletDir, skillsSubdir), "")
	for _, s := range globalSkills {
		if _, exists := skills[s.Name()]; !exists {
			skills[s.Name()] = s
		}
	}

	d.discoverSkillsFromPluginsDir(filepath.Join(d.homeDir, kodeletDir, pluginsSubdir), skills)

	return skills, nil
}

// DiscoverRecipes discovers all recipes with proper naming and precedence
func (d *Discovery) DiscoverRecipes() (map[string]Plugin, error) {
	recipes := make(map[string]Plugin)

	standaloneRecipes, _ := d.discoverRecipesFromDir(filepath.Join(d.baseDir, recipesSubdir), "")
	for _, r := range standaloneRecipes {
		if _, exists := recipes[r.Name()]; !exists {
			recipes[r.Name()] = r
		}
	}

	d.discoverRecipesFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), recipes)

	globalRecipes, _ := d.discoverRecipesFromDir(filepath.Join(d.homeDir, kodeletDir, recipesSubdir), "")
	for _, r := range globalRecipes {
		if _, exists := recipes[r.Name()]; !exists {
			recipes[r.Name()] = r
		}
	}

	d.discoverRecipesFromPluginsDir(filepath.Join(d.homeDir, kodeletDir, pluginsSubdir), recipes)

	return recipes, nil
}

// discoverFromPluginsDir discovers skills/recipes from all plugins under a plugins directory
// Plugin directories use "org@repo" naming format, skills/recipes are prefixed with "org/repo/"
func (d *Discovery) discoverFromPluginsDir(pluginsDir string, plugins *[]Plugin, seen map[string]bool) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		// Convert "org@repo" directory name to "org/repo/" prefix for skill/recipe names
		prefix := pluginNameToPrefix(pluginName)

		skillsDir := filepath.Join(pluginsDir, pluginName, skillsSubdir)
		skills, _ := d.discoverSkillsFromDir(skillsDir, prefix)
		for _, s := range skills {
			if !seen[s.Name()] {
				*plugins = append(*plugins, s)
				seen[s.Name()] = true
			}
		}

		recipesDir := filepath.Join(pluginsDir, pluginName, recipesSubdir)
		recipes, _ := d.discoverRecipesFromDir(recipesDir, prefix)
		for _, r := range recipes {
			if !seen[r.Name()] {
				*plugins = append(*plugins, r)
				seen[r.Name()] = true
			}
		}
	}
}

// discoverSkillsFromPluginsDir discovers skills from all plugins under a plugins directory
// Plugin directories use "org@repo" naming format
func (d *Discovery) discoverSkillsFromPluginsDir(pluginsDir string, skills map[string]Plugin) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		prefix := pluginNameToPrefix(pluginName)

		skillsDir := filepath.Join(pluginsDir, pluginName, skillsSubdir)
		pluginSkills, _ := d.discoverSkillsFromDir(skillsDir, prefix)
		for _, s := range pluginSkills {
			if _, exists := skills[s.Name()]; !exists {
				skills[s.Name()] = s
			}
		}
	}
}

// discoverRecipesFromPluginsDir discovers recipes from all plugins under a plugins directory
// Plugin directories use "org@repo" naming format
func (d *Discovery) discoverRecipesFromPluginsDir(pluginsDir string, recipes map[string]Plugin) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		prefix := pluginNameToPrefix(pluginName)

		recipesDir := filepath.Join(pluginsDir, pluginName, recipesSubdir)
		pluginRecipes, _ := d.discoverRecipesFromDir(recipesDir, prefix)
		for _, r := range pluginRecipes {
			if _, exists := recipes[r.Name()]; !exists {
				recipes[r.Name()] = r
			}
		}
	}
}

// discoverSkillsFromDir discovers skills from a directory with optional name prefix
func (d *Discovery) discoverSkillsFromDir(dir, prefix string) ([]Plugin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var skills []Plugin
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())

		info, err := os.Stat(entryPath)
		if err != nil || !info.IsDir() {
			continue
		}

		skillPath := filepath.Join(entryPath, skillFileName)
		skill, err := d.loadSkillFromPath(skillPath, prefix)
		if err != nil {
			continue
		}

		skill.directory = entryPath
		skills = append(skills, skill)
	}

	return skills, nil
}

// discoverRecipesFromDir discovers recipes from a directory with optional name prefix
func (d *Discovery) discoverRecipesFromDir(dir, prefix string) ([]Plugin, error) {
	var recipes []Plugin

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}

		recipeName := strings.TrimSuffix(relPath, ".md")
		recipeName = filepath.ToSlash(recipeName)
		if prefix != "" {
			recipeName = prefix + recipeName
		}

		recipe, err := d.loadRecipeFromPath(path, recipeName)
		if err != nil {
			return nil
		}

		recipes = append(recipes, recipe)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return recipes, nil
}

// loadSkillFromPath loads a skill from a SKILL.md file path
func (d *Discovery) loadSkillFromPath(path, prefix string) (*SkillPlugin, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read skill file")
	}

	md := goldmark.New(
		goldmark.WithExtensions(meta.Meta),
	)

	var buf bytes.Buffer
	pctx := parser.NewContext()

	if err := md.Convert(content, &buf, parser.WithContext(pctx)); err != nil {
		return nil, errors.Wrap(err, "failed to parse markdown")
	}

	metaData := meta.Get(pctx)
	if metaData == nil {
		return nil, errors.New("missing frontmatter")
	}

	name, _ := metaData["name"].(string)
	description, _ := metaData["description"].(string)

	if name == "" {
		return nil, errors.New("skill name is required in frontmatter")
	}
	if description == "" {
		return nil, errors.New("skill description is required in frontmatter")
	}

	if prefix != "" {
		name = prefix + name
	}

	return &SkillPlugin{
		name:        name,
		description: description,
	}, nil
}

// loadRecipeFromPath loads a recipe from a markdown file path
func (d *Discovery) loadRecipeFromPath(path, name string) (*RecipePlugin, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read recipe file")
	}

	md := goldmark.New(
		goldmark.WithExtensions(meta.Meta),
	)

	var buf bytes.Buffer
	pctx := parser.NewContext()

	if err := md.Convert(content, &buf, parser.WithContext(pctx)); err != nil {
		return nil, errors.Wrap(err, "failed to parse markdown")
	}

	metaData := meta.Get(pctx)

	description := ""
	if metaData != nil {
		if desc, ok := metaData["description"].(string); ok {
			description = desc
		}
	}

	return &RecipePlugin{
		name:        name,
		description: description,
		directory:   filepath.Dir(path),
	}, nil
}

// ListInstalledPlugins returns all installed plugin packages from the specified location
// Plugin directories use "org@repo" naming format
func (d *Discovery) ListInstalledPlugins(global bool) ([]InstalledPlugin, error) {
	var baseDir string
	if global {
		baseDir = filepath.Join(d.homeDir, kodeletDir)
	} else {
		baseDir = d.baseDir
	}

	pluginsDir := filepath.Join(baseDir, pluginsSubdir)
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var plugins []InstalledPlugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginPath := filepath.Join(pluginsDir, entry.Name())
		skillsDir := filepath.Join(pluginPath, skillsSubdir)
		recipesDir := filepath.Join(pluginPath, recipesSubdir)

		hasSkills := false
		hasRecipes := false
		if _, err := os.Stat(skillsDir); err == nil {
			hasSkills = true
		}
		if _, err := os.Stat(recipesDir); err == nil {
			hasRecipes = true
		}

		if !hasSkills && !hasRecipes {
			continue
		}

		plugin := InstalledPlugin{
			Name: entry.Name(),
			Path: pluginPath,
		}

		if hasSkills {
			if skillEntries, err := os.ReadDir(skillsDir); err == nil {
				for _, skillEntry := range skillEntries {
					if !skillEntry.IsDir() {
						continue
					}
					skillPath := filepath.Join(skillsDir, skillEntry.Name(), skillFileName)
					if _, err := os.Stat(skillPath); err == nil {
						plugin.Skills = append(plugin.Skills, skillEntry.Name())
					}
				}
			}
		}

		if hasRecipes {
			_ = filepath.WalkDir(recipesDir, func(rpath string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if !strings.HasSuffix(d.Name(), ".md") {
					return nil
				}
				relRecipePath, err := filepath.Rel(recipesDir, rpath)
				if err != nil {
					return nil
				}
				recipeName := strings.TrimSuffix(relRecipePath, ".md")
				recipeName = filepath.ToSlash(recipeName)
				plugin.Recipes = append(plugin.Recipes, recipeName)
				return nil
			})
		}

		plugins = append(plugins, plugin)
	}

	return plugins, nil
}
