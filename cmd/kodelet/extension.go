package main

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

type ExtensionOutput = protocol.ExtensionInfo

type ExtensionListOutput struct {
	Extensions []ExtensionOutput
	Format     OutputFormat
}

var extensionCmd = &cobra.Command{
	Use:               "extension",
	Short:             "Inspect extensions in a workspace",
	Long:              "View extension files in the selected runner's workspace without starting extension processes.",
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE:              func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

var extensionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered extensions",
	Long:  "List extension files in the selected workspace without starting extension processes.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		result, err := inspectCommandWorkspace(cmd, protocol.WorkspaceInspectParams{Operation: "extension.list"})
		if err != nil {
			return err
		}
		format := TableFormat
		if jsonOutput {
			format = JSONFormat
		}
		found := result.Extensions
		if found == nil {
			found = []ExtensionOutput{}
		}
		return (&ExtensionListOutput{Extensions: found, Format: format}).Render(cmd.OutOrStdout())
	},
}

var extensionInspectCmd = &cobra.Command{
	Use:   "inspect <extension>",
	Short: "Inspect a discovered extension",
	Long:  "Inspect extension file metadata in the selected workspace without starting extension processes.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOutput, _ := cmd.Flags().GetBool("json")
		result, err := inspectCommandWorkspace(cmd, protocol.WorkspaceInspectParams{Operation: "extension.inspect", Name: args[0]})
		if err != nil {
			return err
		}
		if result.Extension == nil {
			return errors.New("the runner returned no extension details")
		}
		if jsonOutput {
			return renderExtensionInspectJSON(cmd.OutOrStdout(), *result.Extension)
		}
		return renderExtensionInspectTable(cmd.OutOrStdout(), *result.Extension)
	},
}

func init() {
	addWorkspaceInspectionFlags(extensionCmd)
	extensionCmd.AddCommand(extensionListCmd, extensionInspectCmd)
	for _, cmd := range []*cobra.Command{extensionListCmd, extensionInspectCmd} {
		cmd.Flags().Bool("json", false, "Output in JSON format")
	}
	rootCmd.AddCommand(extensionCmd)
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
