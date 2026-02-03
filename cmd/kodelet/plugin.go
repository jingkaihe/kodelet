package main

import (
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

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage kodelet plugins (skills and recipes)",
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

Examples:
  kodelet plugin add user/repo              # Install all plugins from repo
  kodelet plugin add user/repo1 user/repo2  # Install from multiple repos
  kodelet plugin add user/repo@v1.0.0       # Install from specific tag
  kodelet plugin add user/repo -g           # Install globally
  kodelet plugin add user/repo --force      # Overwrite existing plugins
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		global, _ := cmd.Flags().GetBool("global")
		force, _ := cmd.Flags().GetBool("force")

		installer, err := plugins.NewInstaller(
			plugins.WithGlobal(global),
			plugins.WithForce(force),
		)
		if err != nil {
			return err
		}

		for _, arg := range args {
			repo, ref := parseRepoRef(arg)
			presenter.Info(fmt.Sprintf("Installing plugins from %s...", repo))

			result, err := installer.Install(cmd.Context(), repo, ref)
			if err != nil {
				return errors.Wrapf(err, "failed to install from %s", repo)
			}

			if len(result.Skills) > 0 {
				presenter.Success(fmt.Sprintf("Installed skills: %s", strings.Join(result.Skills, ", ")))
			}
			if len(result.Recipes) > 0 {
				presenter.Success(fmt.Sprintf("Installed recipes: %s", strings.Join(result.Recipes, ", ")))
			}

			location := "local (.kodelet/plugins/)"
			if global {
				location = "global (~/.kodelet/plugins/)"
			}
			presenter.Info(fmt.Sprintf("Plugin '%s' installed to %s", result.PluginName, location))
		}

		return nil
	},
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed plugins",
	Long: `List all installed plugins with their skills and recipes.

Shows both local (.kodelet/plugins/) and global (~/.kodelet/plugins/) plugins.

Examples:
  kodelet plugin list                       # List all plugins
`,
	RunE: func(_ *cobra.Command, _ []string) error {
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
			presenter.Info("No plugins installed")
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tLOCATION\tSKILLS\tRECIPES")
		fmt.Fprintln(tw, "----\t--------\t------\t-------")

		allPlugins := make([]struct {
			plugin   plugins.InstalledPlugin
			location string
		}, 0)

		for _, p := range localPlugins {
			allPlugins = append(allPlugins, struct {
				plugin   plugins.InstalledPlugin
				location string
			}{p, "local"})
		}

		for _, p := range globalPlugins {
			allPlugins = append(allPlugins, struct {
				plugin   plugins.InstalledPlugin
				location string
			}{p, "global"})
		}

		sort.Slice(allPlugins, func(i, j int) bool {
			return allPlugins[i].plugin.Name < allPlugins[j].plugin.Name
		})

		for _, entry := range allPlugins {
			p := entry.plugin
			skillCount := len(p.Skills)
			recipeCount := len(p.Recipes)

			skillsStr := fmt.Sprintf("%d", skillCount)
			if skillCount > 0 && skillCount <= 3 {
				skillsStr = strings.Join(p.Skills, ", ")
			}

			recipesStr := fmt.Sprintf("%d", recipeCount)
			if recipeCount > 0 && recipeCount <= 3 {
				recipesStr = strings.Join(p.Recipes, ", ")
			}

			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, entry.location, skillsStr, recipesStr)
		}
		tw.Flush()

		return nil
	},
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

	pluginRemoveCmd.Flags().BoolP("global", "g", false, "Remove from global directory")

	pluginCmd.AddCommand(pluginAddCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)

	rootCmd.AddCommand(pluginCmd)
}
