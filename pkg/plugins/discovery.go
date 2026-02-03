package plugins

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
)

const (
	skillFileName = "SKILL.md"
	pluginsSubdir = "plugins"
	skillsSubdir  = "skills"
	recipesSubdir = "recipes"
	hooksSubdir   = "hooks"
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

// HookDirs returns the hook discovery directories with prefix info in precedence order.
// This is used by the hooks package for plugin-based hook discovery.
func (d *Discovery) HookDirs() []PluginDirConfig {
	var dirs []PluginDirConfig

	// 1. Repo-local standalone (highest precedence)
	dirs = append(dirs, PluginDirConfig{
		Dir:    filepath.Join(d.baseDir, hooksSubdir),
		Prefix: "",
	})

	// 2. Repo-local plugins
	dirs = append(dirs, d.pluginHookDirs(d.baseDir)...)

	// 3. Global standalone
	dirs = append(dirs, PluginDirConfig{
		Dir:    filepath.Join(d.homeDir, kodeletDir, hooksSubdir),
		Prefix: "",
	})

	// 4. Global plugins (lowest precedence)
	dirs = append(dirs, d.pluginHookDirs(filepath.Join(d.homeDir, kodeletDir))...)

	return dirs
}

// pluginHookDirs returns hook directories from all plugins under baseDir
// Plugin directories use "org@repo" naming format
func (d *Discovery) pluginHookDirs(baseDir string) []PluginDirConfig {
	pluginsDir := filepath.Join(baseDir, pluginsSubdir)
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}

	var dirs []PluginDirConfig
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hookDir := filepath.Join(pluginsDir, entry.Name(), hooksSubdir)
		if _, err := os.Stat(hookDir); err == nil {
			dirs = append(dirs, PluginDirConfig{
				Dir:    hookDir,
				Prefix: pluginNameToPrefix(entry.Name()),
			})
		}
	}
	return dirs
}

// DiscoverAll discovers all plugins (skills and recipes) with proper naming
func (d *Discovery) DiscoverAll() ([]Plugin, error) {
	var plugins []Plugin
	seen := make(map[string]bool)

	skillsDir := filepath.Join(d.baseDir, skillsSubdir)
	skills, err := d.discoverSkillsFromDir(skillsDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", skillsDir).Debug("failed to discover skills")
	}
	for _, s := range skills {
		if !seen[s.Name()] {
			plugins = append(plugins, s)
			seen[s.Name()] = true
		}
	}

	recipesDir := filepath.Join(d.baseDir, recipesSubdir)
	recipes, err := d.discoverRecipesFromDir(recipesDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", recipesDir).Debug("failed to discover recipes")
	}
	for _, r := range recipes {
		if !seen[r.Name()] {
			plugins = append(plugins, r)
			seen[r.Name()] = true
		}
	}

	d.discoverFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), &plugins, seen)

	globalSkillsDir := filepath.Join(d.homeDir, kodeletDir, skillsSubdir)
	skills, err = d.discoverSkillsFromDir(globalSkillsDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", globalSkillsDir).Debug("failed to discover global skills")
	}
	for _, s := range skills {
		if !seen[s.Name()] {
			plugins = append(plugins, s)
			seen[s.Name()] = true
		}
	}

	globalRecipesDir := filepath.Join(d.homeDir, kodeletDir, recipesSubdir)
	recipes, err = d.discoverRecipesFromDir(globalRecipesDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", globalRecipesDir).Debug("failed to discover global recipes")
	}
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

	skillsDir := filepath.Join(d.baseDir, skillsSubdir)
	standaloneSkills, err := d.discoverSkillsFromDir(skillsDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", skillsDir).Debug("failed to discover standalone skills")
	}
	for _, s := range standaloneSkills {
		if _, exists := skills[s.Name()]; !exists {
			skills[s.Name()] = s
		}
	}

	d.discoverSkillsFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), skills)

	globalSkillsDir := filepath.Join(d.homeDir, kodeletDir, skillsSubdir)
	globalSkills, err := d.discoverSkillsFromDir(globalSkillsDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", globalSkillsDir).Debug("failed to discover global skills")
	}
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

	recipesDir := filepath.Join(d.baseDir, recipesSubdir)
	standaloneRecipes, err := d.discoverRecipesFromDir(recipesDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", recipesDir).Debug("failed to discover standalone recipes")
	}
	for _, r := range standaloneRecipes {
		if _, exists := recipes[r.Name()]; !exists {
			recipes[r.Name()] = r
		}
	}

	d.discoverRecipesFromPluginsDir(filepath.Join(d.baseDir, pluginsSubdir), recipes)

	globalRecipesDir := filepath.Join(d.homeDir, kodeletDir, recipesSubdir)
	globalRecipes, err := d.discoverRecipesFromDir(globalRecipesDir, "")
	if err != nil {
		logrus.WithError(err).WithField("dir", globalRecipesDir).Debug("failed to discover global recipes")
	}
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
		logrus.WithError(err).WithField("dir", pluginsDir).Debug("failed to read plugins directory")
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
		skills, err := d.discoverSkillsFromDir(skillsDir, prefix)
		if err != nil {
			logrus.WithError(err).WithField("dir", skillsDir).Debug("failed to discover plugin skills")
		}
		for _, s := range skills {
			if !seen[s.Name()] {
				*plugins = append(*plugins, s)
				seen[s.Name()] = true
			}
		}

		recipesDir := filepath.Join(pluginsDir, pluginName, recipesSubdir)
		recipes, err := d.discoverRecipesFromDir(recipesDir, prefix)
		if err != nil {
			logrus.WithError(err).WithField("dir", recipesDir).Debug("failed to discover plugin recipes")
		}
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
		logrus.WithError(err).WithField("dir", pluginsDir).Debug("failed to read plugins directory")
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		prefix := pluginNameToPrefix(pluginName)

		skillsDir := filepath.Join(pluginsDir, pluginName, skillsSubdir)
		pluginSkills, err := d.discoverSkillsFromDir(skillsDir, prefix)
		if err != nil {
			logrus.WithError(err).WithField("dir", skillsDir).Debug("failed to discover plugin skills")
		}
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
		logrus.WithError(err).WithField("dir", pluginsDir).Debug("failed to read plugins directory")
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginName := entry.Name()
		prefix := pluginNameToPrefix(pluginName)

		recipesDir := filepath.Join(pluginsDir, pluginName, recipesSubdir)
		pluginRecipes, err := d.discoverRecipesFromDir(recipesDir, prefix)
		if err != nil {
			logrus.WithError(err).WithField("dir", recipesDir).Debug("failed to discover plugin recipes")
		}
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
		hooksDir := filepath.Join(pluginPath, hooksSubdir)

		hasSkills := false
		hasRecipes := false
		hasHooks := false
		if _, err := os.Stat(skillsDir); err == nil {
			hasSkills = true
		}
		if _, err := os.Stat(recipesDir); err == nil {
			hasRecipes = true
		}
		if _, err := os.Stat(hooksDir); err == nil {
			hasHooks = true
		}

		if !hasSkills && !hasRecipes && !hasHooks {
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

		if hasHooks {
			if hookEntries, err := os.ReadDir(hooksDir); err == nil {
				for _, hookEntry := range hookEntries {
					if hookEntry.IsDir() {
						continue
					}
					info, err := hookEntry.Info()
					if err != nil {
						continue
					}
					if info.Mode()&0o111 != 0 {
						plugin.Hooks = append(plugin.Hooks, hookEntry.Name())
					}
				}
			}
		}

		plugins = append(plugins, plugin)
	}

	return plugins, nil
}
