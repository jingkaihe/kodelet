package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/presenter"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ExtensionListConfig struct {
	JSONOutput bool
}

type ExtensionInspectConfig struct {
	JSONOutput bool
}

type ExtensionOutput struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Path      string `json:"path"`
	Directory string `json:"directory"`
	PluginRef string `json:"plugin_ref,omitempty"`
}

type ExtensionListOutput struct {
	Extensions []ExtensionOutput
	Format     OutputFormat
}

var extensionCmd = &cobra.Command{
	Use:   "extension",
	Short: "Manage extensions",
	Long:  "Discover and inspect Kodelet extensions loaded from standalone and plugin extension directories.",
}

var extensionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered extensions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		return runExtensionList(cmd.Context(), ExtensionListConfig{JSONOutput: jsonOutput})
	},
}

var extensionInspectCmd = &cobra.Command{
	Use:   "inspect <extension>",
	Short: "Inspect a discovered extension",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		return runExtensionInspect(cmd.Context(), args[0], ExtensionInspectConfig{JSONOutput: jsonOutput})
	},
}

func init() {
	extensionListCmd.Flags().Bool("json", false, "Output in JSON format")
	extensionInspectCmd.Flags().Bool("json", false, "Output in JSON format")
	extensionCmd.AddCommand(extensionListCmd)
	extensionCmd.AddCommand(extensionInspectCmd)
	rootCmd.AddCommand(extensionCmd)
}

func runExtensionList(_ context.Context, config ExtensionListConfig) error {
	discovered, err := discoverConfiguredExtensions()
	if err != nil {
		return err
	}

	format := TableFormat
	if config.JSONOutput {
		format = JSONFormat
	}
	if len(discovered) == 0 && format != JSONFormat {
		presenter.Info("No extensions found")
		return nil
	}

	output := NewExtensionListOutput(discovered, format)
	return output.Render(os.Stdout)
}

func runExtensionInspect(_ context.Context, query string, config ExtensionInspectConfig) error {
	discovered, err := discoverConfiguredExtensions()
	if err != nil {
		return err
	}

	for _, ext := range discovered {
		if extensionMatchesQuery(ext, query) {
			output := extensionOutput(ext)
			if config.JSONOutput {
				return renderExtensionInspectJSON(os.Stdout, output)
			}
			return renderExtensionInspectTable(os.Stdout, output)
		}
	}
	return errors.Errorf("extension not found: %s", query)
}

func discoverConfiguredExtensions() ([]extensions.Extension, error) {
	discovery, err := extensions.NewDiscovery(extensions.WithConfig(extensions.LoadConfigFromViper()))
	if err != nil {
		return nil, err
	}
	return discovery.Discover()
}

func NewExtensionListOutput(discovered []extensions.Extension, format OutputFormat) *ExtensionListOutput {
	output := &ExtensionListOutput{Extensions: make([]ExtensionOutput, 0, len(discovered)), Format: format}
	for _, ext := range discovered {
		output.Extensions = append(output.Extensions, extensionOutput(ext))
	}
	return output
}

func extensionOutput(ext extensions.Extension) ExtensionOutput {
	return ExtensionOutput{
		ID:        ext.ID,
		Name:      ext.Name,
		Source:    string(ext.Kind),
		Path:      ext.ExecPath,
		Directory: ext.Dir,
		PluginRef: ext.PluginRef,
	}
}

func extensionMatchesQuery(ext extensions.Extension, query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}
	return ext.ID == query || ext.Name == query || ext.PluginRef == query || ext.ExecPath == query || ext.Dir == query
}

func (o *ExtensionListOutput) Render(w io.Writer) error {
	if o.Format == JSONFormat {
		return o.renderJSON(w)
	}
	return o.renderTable(w)
}

func (o *ExtensionListOutput) renderJSON(w io.Writer) error {
	jsonData, err := json.MarshalIndent(map[string]any{"extensions": o.Extensions}, "", "  ")
	if err != nil {
		return errors.Wrap(err, "error generating JSON output")
	}
	_, err = fmt.Fprintln(w, string(jsonData))
	return err
}

func (o *ExtensionListOutput) renderTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tName\tSource\tPath")
	fmt.Fprintln(tw, "----\t----\t------\t----")
	for _, ext := range o.Extensions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", ext.ID, ext.Name, ext.Source, ext.Path)
	}
	return tw.Flush()
}

func renderExtensionInspectJSON(w io.Writer, output ExtensionOutput) error {
	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return errors.Wrap(err, "error generating JSON output")
	}
	_, err = fmt.Fprintln(w, string(jsonData))
	return err
}

func renderExtensionInspectTable(w io.Writer, output ExtensionOutput) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "ID:\t%s\n", output.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", output.Name)
	fmt.Fprintf(tw, "Source:\t%s\n", output.Source)
	fmt.Fprintf(tw, "Path:\t%s\n", output.Path)
	fmt.Fprintf(tw, "Directory:\t%s\n", output.Directory)
	if output.PluginRef != "" {
		fmt.Fprintf(tw, "Plugin ref:\t%s\n", output.PluginRef)
	}
	return tw.Flush()
}
