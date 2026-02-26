package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/plugins"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

const (
	// maxDescriptionDisplayLength is the maximum length for descriptions in table output
	maxDescriptionDisplayLength = 60
	// truncatedDescriptionLength is the length to truncate to (leaving room for "...")
	truncatedDescriptionLength = 57
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage kodelet plugins (skills, recipes, hooks, tools)",
	Long:  `Install, list, and remove kodelet plugins from GitHub repositories.`,
	Run: func(cmd *cobra.Command, _ []string) {
		cmd.Help()
	},
}

var pluginAddCmd = &cobra.Command{
	Use:   "add <repo>[@ref]...",
	Short: "Install plugins from GitHub repositories",
	Long: `Install plugins from one or more GitHub repositories.

The repository should contain:
	- skills/<name>/SKILL.md for skills
	- recipes/<name>.md for recipes
	- hooks/<name> (executable) for hooks
	- tools/<name> (executable) for plugin tools

Examples:
  kodelet plugin add user/repo              # Install all plugins from repo
  kodelet plugin add user/repo1 user/repo2  # Install from multiple repos
  kodelet plugin add user/repo@v1.0.0       # Install from specific tag
  kodelet plugin add user/repo -g           # Install globally
  kodelet plugin add user/repo --force      # Overwrite existing plugins
  kodelet plugin add repo1 repo2 --continue-on-error  # Continue even if one fails
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		global, _ := cmd.Flags().GetBool("global")
		force, _ := cmd.Flags().GetBool("force")
		continueOnError, _ := cmd.Flags().GetBool("continue-on-error")

		installer, err := plugins.NewInstaller(
			plugins.WithGlobal(global),
			plugins.WithForce(force),
		)
		if err != nil {
			return err
		}

		var installErrors []error
		for _, arg := range args {
			// Check for context cancellation between iterations
			select {
			case <-cmd.Context().Done():
				return cmd.Context().Err()
			default:
			}

			repo, ref := parseRepoRef(arg)
			presenter.Info(fmt.Sprintf("Installing plugins from %s...", repo))

			result, err := installer.Install(cmd.Context(), repo, ref)
			if err != nil {
				if continueOnError {
					presenter.Error(err, fmt.Sprintf("Failed to install from %s", repo))
					installErrors = append(installErrors, errors.Wrapf(err, "failed to install from %s", repo))
					continue
				}
				return errors.Wrapf(err, "failed to install from %s", repo)
			}

			if len(result.Skills) > 0 {
				presenter.Success(fmt.Sprintf("Installed skills: %s", strings.Join(result.Skills, ", ")))
			}
			if len(result.Recipes) > 0 {
				presenter.Success(fmt.Sprintf("Installed recipes: %s", strings.Join(result.Recipes, ", ")))
			}
			if len(result.Hooks) > 0 {
				presenter.Success(fmt.Sprintf("Installed hooks: %s", strings.Join(result.Hooks, ", ")))
			}
			if len(result.Tools) > 0 {
				presenter.Success(fmt.Sprintf("Installed tools: %s", strings.Join(result.Tools, ", ")))
			}

			location := "local (.kodelet/plugins/)"
			if global {
				location = "global (~/.kodelet/plugins/)"
			}
			presenter.Info(fmt.Sprintf("Plugin '%s' installed to %s", result.PluginName, location))
		}

		if len(installErrors) > 0 {
			return errors.Errorf("%d plugin(s) failed to install", len(installErrors))
		}

		return nil
	},
}

// PluginListOutput represents the JSON output structure for plugin list
type PluginListOutput struct {
	Plugins []PluginInfo `json:"plugins"`
}

// PluginInfo represents a single plugin in the JSON output
type PluginInfo struct {
	Name     string       `json:"name"`
	Location string       `json:"location"`
	Path     string       `json:"path,omitempty"`
	Skills   []SkillInfo  `json:"skills"`
	Recipes  []RecipeInfo `json:"recipes"`
	Hooks    []HookInfo   `json:"hooks"`
	Tools    []ToolInfo   `json:"tools"`
}

// SkillInfo represents a skill in the JSON output
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// RecipeInfo represents a recipe in the JSON output
type RecipeInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// HookInfo represents a hook in the JSON output
type HookInfo struct {
	Name string `json:"name"`
}

// ToolInfo represents a tool in the JSON output
type ToolInfo struct {
	Name string `json:"name"`
}

// pluginEntry associates a plugin with its location (local or global)
type pluginEntry struct {
	plugin   plugins.InstalledPlugin
	location string
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed plugins",
	Long: `List all installed plugins with their skills and recipes.

Shows both local (.kodelet/plugins/) and global (~/.kodelet/plugins/) plugins.

Examples:
  kodelet plugin list                       # List all plugins
  kodelet plugin list --json                # Output as JSON with descriptions
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")

		discovery, err := plugins.NewDiscovery()
		if err != nil {
			return err
		}

		localPlugins, err := discovery.ListInstalledPlugins(false)
		if err != nil {
			return errors.Wrap(err, "failed to list local plugins")
		}

		globalPlugins, err := discovery.ListInstalledPlugins(true)
		if err != nil {
			return errors.Wrap(err, "failed to list global plugins")
		}

		if len(localPlugins) == 0 && len(globalPlugins) == 0 {
			if jsonOutput {
				output := PluginListOutput{Plugins: []PluginInfo{}}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(output)
			}
			presenter.Info("No plugins installed")
			return nil
		}

		allPlugins := make([]pluginEntry, 0)

		for _, p := range localPlugins {
			allPlugins = append(allPlugins, pluginEntry{p, "local"})
		}

		for _, p := range globalPlugins {
			allPlugins = append(allPlugins, pluginEntry{p, "global"})
		}

		sort.Slice(allPlugins, func(i, j int) bool {
			return allPlugins[i].plugin.Name < allPlugins[j].plugin.Name
		})

		if jsonOutput {
			return outputPluginsJSON(discovery, allPlugins)
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tLOCATION\tSKILLS\tRECIPES\tHOOKS\tTOOLS")
		fmt.Fprintln(tw, "----\t--------\t------\t-------\t-----\t-----")

		for _, entry := range allPlugins {
			p := entry.plugin
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\n", p.Name, entry.location, len(p.Skills), len(p.Recipes), len(p.Hooks), len(p.Tools))
		}
		tw.Flush()

		return nil
	},
}

func outputPluginsJSON(discovery *plugins.Discovery, allPlugins []pluginEntry) error {
	skills, err := discovery.DiscoverSkills()
	if err != nil {
		return errors.Wrap(err, "failed to discover skills")
	}

	recipes, err := discovery.DiscoverRecipes()
	if err != nil {
		return errors.Wrap(err, "failed to discover recipes")
	}

	output := PluginListOutput{Plugins: make([]PluginInfo, 0, len(allPlugins))}

	for _, entry := range allPlugins {
		p := entry.plugin
		pluginPrefix := plugins.PluginNameToUserFacing(p.Name) + "/"

		info := PluginInfo{
			Name:     plugins.PluginNameToUserFacing(p.Name),
			Location: entry.location,
			Path:     p.Path,
			Skills:   make([]SkillInfo, 0, len(p.Skills)),
			Recipes:  make([]RecipeInfo, 0, len(p.Recipes)),
			Hooks:    make([]HookInfo, 0, len(p.Hooks)),
			Tools:    make([]ToolInfo, 0, len(p.Tools)),
		}

		for _, skillName := range p.Skills {
			fullName := pluginPrefix + skillName
			skillInfo := SkillInfo{Name: skillName}
			if skill, ok := skills[fullName]; ok {
				skillInfo.Description = skill.Description()
			}
			info.Skills = append(info.Skills, skillInfo)
		}

		for _, recipeName := range p.Recipes {
			fullName := pluginPrefix + recipeName
			recipeInfo := RecipeInfo{Name: recipeName}
			if recipe, ok := recipes[fullName]; ok {
				recipeInfo.Description = recipe.Description()
			}
			info.Recipes = append(info.Recipes, recipeInfo)
		}

		for _, hookName := range p.Hooks {
			info.Hooks = append(info.Hooks, HookInfo{Name: hookName})
		}

		for _, toolName := range p.Tools {
			info.Tools = append(info.Tools, ToolInfo{Name: toolName})
		}

		output.Plugins = append(output.Plugins, info)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

var pluginShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show details of a specific plugin",
	Long: `Show detailed information about an installed plugin including its skills and recipes.

Examples:
  kodelet plugin show user/repo            # Show plugin details
  kodelet plugin show user/repo --json     # Output as JSON
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		name := args[0]

		discovery, err := plugins.NewDiscovery()
		if err != nil {
			return err
		}

		// Search in both local and global
		var found *plugins.InstalledPlugin
		var location string

		localPlugins, err := discovery.ListInstalledPlugins(false)
		if err != nil {
			return errors.Wrap(err, "failed to list local plugins")
		}

		globalPlugins, err := discovery.ListInstalledPlugins(true)
		if err != nil {
			return errors.Wrap(err, "failed to list global plugins")
		}

		// Normalize input name (support both org/repo and org@repo)
		searchName := name
		if strings.Contains(name, "/") {
			searchName = strings.Replace(name, "/", "@", 1)
		}

		for i := range localPlugins {
			if localPlugins[i].Name == searchName {
				found = &localPlugins[i]
				location = "local"
				break
			}
		}

		if found == nil {
			for i := range globalPlugins {
				if globalPlugins[i].Name == searchName {
					found = &globalPlugins[i]
					location = "global"
					break
				}
			}
		}

		if found == nil {
			return errors.Errorf("plugin '%s' not found", name)
		}

		if jsonOutput {
			return outputPluginShowJSON(discovery, found, location)
		}

		return outputPluginShowTable(discovery, found, location)
	},
}

func outputPluginShowTable(discovery *plugins.Discovery, p *plugins.InstalledPlugin, location string) error {
	skills, err := discovery.DiscoverSkills()
	if err != nil {
		return errors.Wrap(err, "failed to discover skills")
	}

	recipes, err := discovery.DiscoverRecipes()
	if err != nil {
		return errors.Wrap(err, "failed to discover recipes")
	}

	pluginPrefix := plugins.PluginNameToUserFacing(p.Name) + "/"

	fmt.Printf("Name:     %s\n", plugins.PluginNameToUserFacing(p.Name))
	fmt.Printf("Location: %s\n", location)
	fmt.Printf("Path:     %s\n", p.Path)
	fmt.Println()

	if len(p.Skills) > 0 {
		fmt.Printf("Skills (%d):\n", len(p.Skills))
		for _, skillName := range p.Skills {
			fullName := pluginPrefix + skillName
			desc := ""
			if skill, ok := skills[fullName]; ok {
				desc = skill.Description()
			}
			if desc != "" {
				// Truncate long descriptions
				if len(desc) > maxDescriptionDisplayLength {
					desc = desc[:truncatedDescriptionLength] + "..."
				}
				fmt.Printf("  • %s - %s\n", skillName, desc)
			} else {
				fmt.Printf("  • %s\n", skillName)
			}
		}
		fmt.Println()
	}

	if len(p.Recipes) > 0 {
		fmt.Printf("Recipes (%d):\n", len(p.Recipes))
		for _, recipeName := range p.Recipes {
			fullName := pluginPrefix + recipeName
			desc := ""
			if recipe, ok := recipes[fullName]; ok {
				desc = recipe.Description()
			}
			if desc != "" {
				if len(desc) > maxDescriptionDisplayLength {
					desc = desc[:truncatedDescriptionLength] + "..."
				}
				fmt.Printf("  • %s - %s\n", recipeName, desc)
			} else {
				fmt.Printf("  • %s\n", recipeName)
			}
		}
		fmt.Println()
	}

	if len(p.Hooks) > 0 {
		fmt.Printf("Hooks (%d):\n", len(p.Hooks))
		for _, hookName := range p.Hooks {
			fmt.Printf("  • %s\n", hookName)
		}
		fmt.Println()
	}

	if len(p.Tools) > 0 {
		fmt.Printf("Tools (%d):\n", len(p.Tools))
		for _, toolName := range p.Tools {
			fmt.Printf("  • %s\n", toolName)
		}
	}

	return nil
}

func outputPluginShowJSON(discovery *plugins.Discovery, p *plugins.InstalledPlugin, location string) error {
	skills, err := discovery.DiscoverSkills()
	if err != nil {
		return errors.Wrap(err, "failed to discover skills")
	}

	recipes, err := discovery.DiscoverRecipes()
	if err != nil {
		return errors.Wrap(err, "failed to discover recipes")
	}

	pluginPrefix := plugins.PluginNameToUserFacing(p.Name) + "/"

	info := PluginInfo{
		Name:     plugins.PluginNameToUserFacing(p.Name),
		Location: location,
		Path:     p.Path,
		Skills:   make([]SkillInfo, 0, len(p.Skills)),
		Recipes:  make([]RecipeInfo, 0, len(p.Recipes)),
		Hooks:    make([]HookInfo, 0, len(p.Hooks)),
		Tools:    make([]ToolInfo, 0, len(p.Tools)),
	}

	for _, skillName := range p.Skills {
		fullName := pluginPrefix + skillName
		skillInfo := SkillInfo{Name: skillName}
		if skill, ok := skills[fullName]; ok {
			skillInfo.Description = skill.Description()
		}
		info.Skills = append(info.Skills, skillInfo)
	}

	for _, recipeName := range p.Recipes {
		fullName := pluginPrefix + recipeName
		recipeInfo := RecipeInfo{Name: recipeName}
		if recipe, ok := recipes[fullName]; ok {
			recipeInfo.Description = recipe.Description()
		}
		info.Recipes = append(info.Recipes, recipeInfo)
	}

	for _, hookName := range p.Hooks {
		info.Hooks = append(info.Hooks, HookInfo{Name: hookName})
	}

	for _, toolName := range p.Tools {
		info.Tools = append(info.Tools, ToolInfo{Name: toolName})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(info)
}

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>...",
	Short: "Remove one or more plugins",
	Long: `Remove one or more installed plugins by name.

Examples:
  kodelet plugin remove my-plugin          # Remove a single plugin
  kodelet plugin remove plugin1 plugin2    # Remove multiple plugins
  kodelet plugin remove my-plugin -g       # Remove from global directory
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		global, _ := cmd.Flags().GetBool("global")

		remover, err := plugins.NewRemover(plugins.WithGlobal(global))
		if err != nil {
			return err
		}

		var removed []string
		for _, name := range args {
			if err := remover.Remove(name); err != nil {
				return errors.Wrapf(err, "failed to remove %s", name)
			}
			removed = append(removed, name)
		}

		presenter.Success(fmt.Sprintf("Removed plugins: %s", strings.Join(removed, ", ")))
		return nil
	},
}

func parseRepoRef(arg string) (repo, ref string) {
	if idx := strings.LastIndex(arg, "@"); idx != -1 {
		return arg[:idx], arg[idx+1:]
	}
	return arg, ""
}

func init() {
	pluginAddCmd.Flags().BoolP("global", "g", false, "Install to global directory (~/.kodelet/)")
	pluginAddCmd.Flags().Bool("force", false, "Overwrite existing plugins")
	pluginAddCmd.Flags().Bool("continue-on-error", false, "Continue installing other plugins if one fails")

	pluginListCmd.Flags().Bool("json", false, "Output as JSON with skill/recipe descriptions")

	pluginShowCmd.Flags().Bool("json", false, "Output as JSON with full descriptions")

	pluginRemoveCmd.Flags().BoolP("global", "g", false, "Remove from global directory")

	pluginCmd.AddCommand(pluginAddCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginShowCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)

	rootCmd.AddCommand(pluginCmd)
}
