package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// RecipeListConfig holds configuration for the recipe list command
type RecipeListConfig struct {
	ShowPath   bool
	JSONOutput bool
}

// NewRecipeListConfig creates a new RecipeListConfig with default values
func NewRecipeListConfig() *RecipeListConfig {
	return &RecipeListConfig{
		ShowPath:   false,
		JSONOutput: false,
	}
}

// RecipeShowConfig holds configuration for the recipe show command
type RecipeShowConfig struct {
	Arguments map[string]string
}

// NewRecipeShowConfig creates a new RecipeShowConfig with default values
func NewRecipeShowConfig() *RecipeShowConfig {
	return &RecipeShowConfig{
		Arguments: make(map[string]string),
	}
}

// RecipeOutputFormat defines the format of the output
type RecipeOutputFormat int

const (
	RecipeTableFormat RecipeOutputFormat = iota
	RecipeJSONFormat
)

// RecipeListOutput represents the output for recipe list
type RecipeListOutput struct {
	Recipes []RecipeOutput
	Format  RecipeOutputFormat
}

// RecipeOutput represents a single recipe for output
type RecipeOutput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path,omitempty"`
}

// NewRecipeListOutput creates a new RecipeListOutput
func NewRecipeListOutput(fragmentsWithMetadata []*fragments.Fragment, format RecipeOutputFormat, showPath bool) *RecipeListOutput {
	output := &RecipeListOutput{
		Recipes: make([]RecipeOutput, 0, len(fragmentsWithMetadata)),
		Format:  format,
	}

	for _, fragment := range fragmentsWithMetadata {
		id := strings.TrimSuffix(filepath.Base(fragment.Path), ".md")

		name := fragment.Metadata.Name
		if name == "" {
			name = id
		}

		recipe := RecipeOutput{
			ID:          id,
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

// Render formats and renders the recipe list to the specified writer
func (o *RecipeListOutput) Render(w io.Writer) error {
	if o.Format == RecipeJSONFormat {
		return o.renderJSON(w)
	}
	return o.renderTable(w)
}

// renderJSON renders the output in JSON format
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

// renderTable renders the output in table format
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

// hasPath checks if any recipe has a path to display
func (o *RecipeListOutput) hasPath() bool {
	for _, recipe := range o.Recipes {
		if recipe.Path != "" {
			return true
		}
	}
	return false
}

var recipeCmd = &cobra.Command{
	Use:   "recipe",
	Short: "Manage recipes/fragments",
	Long:  `Manage recipes/fragments with metadata support`,
}

var recipeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available recipes",
	Long:  `List all available recipes with their metadata including ID, name and description`,
	RunE: func(cmd *cobra.Command, args []string) error {
		config := NewRecipeListConfig()
		config.ShowPath, _ = cmd.Flags().GetBool("show-path")
		config.JSONOutput, _ = cmd.Flags().GetBool("json")

		return runRecipeList(cmd.Context(), config)
	},
}

var recipeShowCmd = &cobra.Command{
	Use:   "show <recipe>",
	Short: "Show recipe content with metadata",
	Long:  `Show the rendered content of a recipe along with its metadata`,
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

		return runRecipeShow(cmd.Context(), args[0], config)
	},
}

func init() {
	recipeCmd.AddCommand(recipeListCmd)
	recipeCmd.AddCommand(recipeShowCmd)

	recipeListCmd.Flags().Bool("show-path", false, "Show the file path for each recipe")
	recipeListCmd.Flags().Bool("json", false, "Output in JSON format")

	recipeShowCmd.Flags().StringSliceP("arg", "a", []string{}, "Template arguments in format key=value (can be specified multiple times)")
}

func runRecipeList(_ context.Context, config *RecipeListConfig) error {
	processor, err := fragments.NewFragmentProcessor()
	if err != nil {
		return errors.Wrap(err, "failed to create fragment processor")
	}

	fragmentsWithMetadata, err := processor.ListFragmentsWithMetadata()
	if err != nil {
		return errors.Wrap(err, "failed to list fragments")
	}

	if len(fragmentsWithMetadata) == 0 {
		presenter.Info("No recipes found")
		return nil
	}

	format := RecipeTableFormat
	if config.JSONOutput {
		format = RecipeJSONFormat
	}

	output := NewRecipeListOutput(fragmentsWithMetadata, format, config.ShowPath)
	if err := output.Render(os.Stdout); err != nil {
		return errors.Wrap(err, "failed to render recipe list")
	}

	return nil
}

func runRecipeShow(ctx context.Context, recipeName string, config *RecipeShowConfig) error {
	processor, err := fragments.NewFragmentProcessor()
	if err != nil {
		return errors.Wrap(err, "failed to create fragment processor")
	}

	fragmentConfig := &fragments.Config{
		FragmentName: recipeName,
		Arguments:    config.Arguments,
	}

	fragmentWithMetadata, err := processor.LoadFragmentWithMetadata(ctx, fragmentConfig)
	if err != nil {
		return errors.Wrapf(err, "failed to load recipe '%s'", recipeName)
	}

	if fragmentWithMetadata.Metadata.Name != "" || fragmentWithMetadata.Metadata.Description != "" {
		presenter.Section("Recipe Metadata")

		if fragmentWithMetadata.Metadata.Name != "" {
			fmt.Printf("Name: %s\n", fragmentWithMetadata.Metadata.Name)
		}

		if fragmentWithMetadata.Metadata.Description != "" {
			fmt.Printf("Description: %s\n", fragmentWithMetadata.Metadata.Description)
		}

		fmt.Printf("Path: %s\n", fragmentWithMetadata.Path)
		fmt.Println()
	}

	presenter.Section("Recipe Content")
	fmt.Print(fragmentWithMetadata.Content)

	return nil
}
