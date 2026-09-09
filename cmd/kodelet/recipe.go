package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type RecipeListConfig struct {
	ShowPath   bool
	JSONOutput bool
}

func NewRecipeListConfig() *RecipeListConfig {
	return &RecipeListConfig{
		ShowPath:   false,
		JSONOutput: false,
	}
}

type RecipeShowConfig struct {
	Arguments map[string]string
}

func NewRecipeShowConfig() *RecipeShowConfig {
	return &RecipeShowConfig{
		Arguments: make(map[string]string),
	}
}

type RecipeOutputFormat int

const (
	RecipeTableFormat RecipeOutputFormat = iota
	RecipeJSONFormat
)

type RecipeListOutput struct {
	Recipes []RecipeOutput
	Format  RecipeOutputFormat
}

type RecipeOutput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path,omitempty"`
}

func NewRecipeListOutput(fragmentsWithMetadata []*fragments.Fragment, format RecipeOutputFormat, showPath bool) *RecipeListOutput {
	output := &RecipeListOutput{
		Recipes: make([]RecipeOutput, 0, len(fragmentsWithMetadata)),
		Format:  format,
	}

	for _, fragment := range fragmentsWithMetadata {
		name := fragment.Metadata.Name
		if name == "" {
			name = fragment.ID
		}

		recipe := RecipeOutput{
			ID:          fragment.ID,
			Name:        name,
			Description: fragment.Metadata.Description,
		}

		if showPath || format == RecipeJSONFormat {
			recipe.Path = fragment.Path
		}

		output.Recipes = append(output.Recipes, recipe)
	}

	return output
}

func (o *RecipeListOutput) Render(w io.Writer) error {
	if o.Format == RecipeJSONFormat {
		return o.renderJSON(w)
	}
	return o.renderTable(w)
}

func (o *RecipeListOutput) renderJSON(w io.Writer) error {
	type jsonOutput struct {
		Recipes []RecipeOutput `json:"recipes"`
	}

	output := jsonOutput{
		Recipes: o.Recipes,
	}

	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return errors.Wrap(err, "error generating JSON output")
	}

	_, err = fmt.Fprintln(w, string(jsonData))
	return err
}

func (o *RecipeListOutput) renderTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if o.hasPath() {
		fmt.Fprintln(tw, "ID\tName\tDescription\tPath")
		fmt.Fprintln(tw, "----\t----\t-----------\t----")
	} else {
		fmt.Fprintln(tw, "ID\tName\tDescription")
		fmt.Fprintln(tw, "----\t----\t-----------")
	}

	for _, recipe := range o.Recipes {
		if o.hasPath() {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				recipe.ID,
				recipe.Name,
				recipe.Description,
				recipe.Path,
			)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\n",
				recipe.ID,
				recipe.Name,
				recipe.Description,
			)
		}
	}

	return tw.Flush()
}

func (o *RecipeListOutput) hasPath() bool {
	for _, recipe := range o.Recipes {
		if recipe.Path != "" {
			return true
		}
	}
	return false
}

var recipeCmd = &cobra.Command{
	Use:               "recipe",
	Short:             "Inspect recipes in a workspace",
	Long:              "List and render recipes in the selected runner's workspace. With the same-machine built-in runner, the workspace defaults to your current directory.",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

var recipeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available recipes",
	Long:  "List recipes in the selected workspace. Starts extensions on the runner to discover dynamic recipes.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		config := NewRecipeListConfig()
		config.ShowPath, _ = cmd.Flags().GetBool("show-path")
		config.JSONOutput, _ = cmd.Flags().GetBool("json")

		result, err := inspectCommandWorkspace(cmd, protocol.WorkspaceInspectParams{Operation: "recipe.list"})
		if err != nil {
			return err
		}
		format := RecipeTableFormat
		if config.JSONOutput {
			format = RecipeJSONFormat
		}
		return NewRecipeListOutput(result.Recipes, format, config.ShowPath).Render(cmd.OutOrStdout())
	},
}

var recipeShowCmd = &cobra.Command{
	Use:   "show <recipe>",
	Short: "Show recipe content with metadata",
	Long:  "Render a recipe in the selected workspace. Template functions may execute commands on the runner.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		config := NewRecipeShowConfig()

		// Parse arguments in format key=value
		argStrings, _ := cmd.Flags().GetStringSlice("arg")
		for _, arg := range argStrings {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) == 2 {
				config.Arguments[parts[0]] = parts[1]
			}
		}

		result, err := inspectCommandWorkspace(cmd, protocol.WorkspaceInspectParams{Operation: "recipe.show", Name: args[0], Arguments: config.Arguments})
		if err != nil {
			return err
		}
		if result.Recipe == nil {
			return errors.New("the runner returned no recipe content")
		}
		return renderRecipeShow(cmd.OutOrStdout(), result.Recipe)
	},
}

func init() {
	addWorkspaceInspectionFlags(recipeCmd)
	recipeCmd.AddCommand(recipeListCmd, recipeShowCmd)
	recipeListCmd.Flags().Bool("show-path", false, "Show recipe file paths on the runner")
	recipeListCmd.Flags().Bool("json", false, "Output in JSON format")
	recipeShowCmd.Flags().StringSliceP("arg", "a", nil, "Recipe argument in key=value format (repeatable)")
}

func renderRecipeShow(w io.Writer, fragment *fragments.Fragment) error {
	if fragment.Metadata.Name != "" || fragment.Metadata.Description != "" {
		fmt.Fprintln(w, "Recipe Metadata")

		if fragment.Metadata.Name != "" {
			fmt.Fprintf(w, "Name: %s\n", fragment.Metadata.Name)
		}

		if fragment.Metadata.Description != "" {
			fmt.Fprintf(w, "Description: %s\n", fragment.Metadata.Description)
		}

		fmt.Fprintf(w, "Path: %s\n", fragment.Path)

		if len(fragment.Metadata.Arguments) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "Arguments")
			for key, argMeta := range fragment.Metadata.Arguments {
				if argMeta.Description != "" {
					if argMeta.Default != "" {
						fmt.Fprintf(w, "  %s: %s (default: %s)\n", key, argMeta.Description, argMeta.Default)
					} else {
						fmt.Fprintf(w, "  %s: %s\n", key, argMeta.Description)
					}
				} else if argMeta.Default != "" {
					fmt.Fprintf(w, "  %s: (default: %s)\n", key, argMeta.Default)
				} else {
					fmt.Fprintf(w, "  %s\n", key)
				}
			}
		}

		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "Recipe Content")
	_, err := fmt.Fprint(w, fragment.Content)

	return err
}
